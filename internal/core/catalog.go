package core

import (
	"context"
	"errors"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"image"
	"image/png"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

var batchNumber = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)

const batchColumns = `t.id,t.batch_no,t.material_name,t.process_spec,t.quality_status,t.enabled,t.notes,t.version,t.created_at,t.updated_at,
 EXISTS(SELECT 1 FROM core.baskets b JOIN core.cycles c ON c.basket_no=b.basket_no WHERE b.batch_id=t.id AND c.status NOT IN ('COMPLETED','ABORTED'))`
const basketColumns = `b.basket_no,b.batch_id,t.batch_no,t.material_name,t.quality_status,t.enabled,b.quantity,b.enabled,b.notes,b.version,b.created_at,b.updated_at,
 EXISTS(SELECT 1 FROM core.cycles c WHERE c.basket_no=b.basket_no AND c.status NOT IN ('COMPLETED','ABORTED')),
 EXISTS(SELECT 1 FROM core.occupancy o WHERE o.basket_no=b.basket_no)`

type rowScanner interface{ Scan(...any) error }

func readBatch(row rowScanner) (protocol.Batch, error) {
	var b protocol.Batch
	err := row.Scan(&b.ID, &b.BatchNo, &b.MaterialName, &b.ProcessSpec, &b.QualityStatus, &b.Enabled, &b.Notes, &b.Version, &b.CreatedAt, &b.UpdatedAt, &b.InUse)
	return b, err
}
func readBasket(row rowScanner) (protocol.Basket, error) {
	var b protocol.Basket
	err := row.Scan(&b.BasketNo, &b.BatchID, &b.BatchNo, &b.MaterialName, &b.QualityStatus, &b.BatchEnabled, &b.Quantity, &b.Enabled, &b.Notes, &b.Version, &b.CreatedAt, &b.UpdatedAt, &b.InUse, &b.Occupied)
	return b, err
}
func bumpCatalog(ctx context.Context, tx pgx.Tx) (int64, error) {
	var v int64
	err := tx.QueryRow(ctx, "UPDATE core.catalog_revision SET version=version+1 WHERE id=1 RETURNING version").Scan(&v)
	return v, err
}
func (e *Engine) loadCatalog(ctx context.Context) error {
	return e.store.db.QueryRow(ctx, "SELECT version FROM core.catalog_revision WHERE id=1").Scan(&e.catalogVersion)
}
func (e *Engine) catalogRevision() int64 { e.mu.Lock(); defer e.mu.Unlock(); return e.catalogVersion }
func catalogueError(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return fail("NUMBER_EXISTS", "编号已存在；历史已删除编号也不能重复使用")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fault{404, "CATALOG_NOT_FOUND", "台账记录不存在或已删除"}
	}
	return err
}
func (e *Engine) catalogWrite(w http.ResponseWriter, r *http.Request, body any, action string, fn func(pgx.Tx) (string, error)) {
	e.idempotent(w, r, body, func(tx pgx.Tx) (string, afterCommit, error) {
		id, err := fn(tx)
		if err != nil {
			return "", nil, catalogueError(err)
		}
		if err = accountAudit(r, tx, action, id, "维护生产台账"); err != nil {
			return "", nil, err
		}
		version, err := bumpCatalog(r.Context(), tx)
		return id, func() { e.catalogVersion = version }, err
	})
}
func validBatch(in protocol.BatchInput) bool {
	return batchNumber.MatchString(in.BatchNo) && strings.TrimSpace(in.MaterialName) != "" && utf8.RuneCountInString(in.MaterialName) <= 100 && utf8.RuneCountInString(in.ProcessSpec) <= 200 && utf8.RuneCountInString(in.Notes) <= 500 && in.Enabled != nil && (in.QualityStatus == "pending" || in.QualityStatus == "passed" || in.QualityStatus == "failed")
}
func validBasket(in protocol.BasketInput) bool {
	id, err := basket(in.BasketNo)
	return err == nil && id == in.BasketNo && in.BatchID != "" && len(in.BatchID) <= 64 && in.Quantity >= 1 && in.Quantity <= 100000 && in.Enabled != nil && utf8.RuneCountInString(in.Notes) <= 500
}
func existingBatch(ctx context.Context, tx pgx.Tx, id string) error {
	var yes bool
	err := tx.QueryRow(ctx, "SELECT true FROM core.batches WHERE id=$1 AND deleted_at IS NULL FOR SHARE", id).Scan(&yes)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault{400, "BATCH_NOT_FOUND", "请选择未删除的批次"}
	}
	return err
}
func mutableBatch(ctx context.Context, tx pgx.Tx, id string, version int64) (bool, error) {
	var current int64
	var deleted, inUse bool
	err := tx.QueryRow(ctx, `SELECT t.version,t.deleted_at IS NOT NULL,EXISTS(SELECT 1 FROM core.baskets b JOIN core.cycles c ON c.basket_no=b.basket_no WHERE b.batch_id=t.id AND c.status NOT IN ('COMPLETED','ABORTED')) FROM core.batches t WHERE t.id=$1 FOR UPDATE`, id).Scan(&current, &deleted, &inUse)
	if err != nil {
		return false, err
	}
	if deleted {
		return true, nil
	}
	if current != version {
		return false, fail("STALE_VERSION", "批次已被其他操作修改，请刷新后重新编辑")
	}
	if inUse {
		return false, fail("BATCH_IN_USE", "批次关联正在处理的入炉周期，暂不能修改")
	}
	return false, nil
}
func mutableBasket(ctx context.Context, tx pgx.Tx, id string, version int64) (bool, error) {
	var current int64
	var deleted, inUse, occupied bool
	err := tx.QueryRow(ctx, `SELECT b.version,b.deleted_at IS NOT NULL,EXISTS(SELECT 1 FROM core.cycles c WHERE c.basket_no=b.basket_no AND c.status NOT IN ('COMPLETED','ABORTED')),EXISTS(SELECT 1 FROM core.occupancy o WHERE o.basket_no=b.basket_no) FROM core.baskets b WHERE b.basket_no=$1 FOR UPDATE`, id).Scan(&current, &deleted, &inUse, &occupied)
	if err != nil {
		return false, err
	}
	if deleted {
		return true, nil
	}
	if current != version {
		return false, fail("STALE_VERSION", "料筐已被其他操作修改，请刷新后重新编辑")
	}
	if inUse || occupied {
		return false, fail("BASKET_IN_USE", "料筐正在入炉或已有占用，暂不能修改或删除")
	}
	return false, nil
}
func (e *Engine) catalogRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/batches", e.listBatches)
	m.HandleFunc("GET /api/v1/baskets", e.listBaskets)
	m.HandleFunc("GET /api/v1/batches/{batchId}", func(w http.ResponseWriter, r *http.Request) {
		b, err := readBatch(e.store.db.QueryRow(r.Context(), "SELECT "+batchColumns+" FROM core.batches t WHERE t.id=$1 AND t.deleted_at IS NULL", r.PathValue("batchId")))
		if err != nil {
			writeError(w, catalogueError(err))
			return
		}
		platform.JSON(w, 200, b)
	})
	m.HandleFunc("GET /api/v1/baskets/{basketNo}", func(w http.ResponseWriter, r *http.Request) {
		b, err := readBasket(e.store.db.QueryRow(r.Context(), "SELECT "+basketColumns+" FROM core.baskets b JOIN core.batches t ON t.id=b.batch_id WHERE b.basket_no=$1 AND b.deleted_at IS NULL", r.PathValue("basketNo")))
		if err != nil {
			writeError(w, catalogueError(err))
			return
		}
		platform.JSON(w, 200, b)
	})
	m.HandleFunc("GET /api/v1/baskets/{basketNo}/qrcode", e.basketQR)
	m.HandleFunc("POST /api/v1/batches", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.BatchInput
		if platform.Read(r, &in) != nil || !validBatch(in) {
			platform.Error(w, 400, "INVALID_BATCH", "请填写有效批次编号、物料、质检状态和启用状态")
			return
		}
		e.catalogWrite(w, r, in, "CREATE_BATCH", func(tx pgx.Tx) (string, error) {
			id := protocol.ID()
			_, err := tx.Exec(r.Context(), "INSERT INTO core.batches(id,batch_no,material_name,process_spec,quality_status,enabled,notes) VALUES($1,$2,$3,$4,$5,$6,$7)", id, in.BatchNo, strings.TrimSpace(in.MaterialName), in.ProcessSpec, in.QualityStatus, *in.Enabled, in.Notes)
			return id, err
		})
	})
	m.HandleFunc("PUT /api/v1/batches/{batchId}", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.BatchUpdate
		if platform.Read(r, &in) != nil || !validBatch(in.BatchInput) || in.Version < 1 {
			platform.Error(w, 400, "INVALID_BATCH", "批次内容或版本无效")
			return
		}
		id := r.PathValue("batchId")
		e.catalogWrite(w, r, in, "UPDATE_BATCH", func(tx pgx.Tx) (string, error) {
			deleted, err := mutableBatch(r.Context(), tx, id, in.Version)
			if err != nil {
				return id, err
			}
			if deleted {
				return id, pgx.ErrNoRows
			}
			_, err = tx.Exec(r.Context(), "UPDATE core.batches SET batch_no=$2,material_name=$3,process_spec=$4,quality_status=$5,enabled=$6,notes=$7,version=version+1,updated_at=now() WHERE id=$1", id, in.BatchNo, strings.TrimSpace(in.MaterialName), in.ProcessSpec, in.QualityStatus, *in.Enabled, in.Notes)
			return id, err
		})
	})
	m.HandleFunc("DELETE /api/v1/batches/{batchId}", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.CatalogDelete
		if platform.Read(r, &in) != nil || in.Version < 1 {
			platform.Error(w, 400, "INVALID_VERSION", "需要当前记录版本")
			return
		}
		id := r.PathValue("batchId")
		e.catalogWrite(w, r, in, "DELETE_BATCH", func(tx pgx.Tx) (string, error) {
			deleted, err := mutableBatch(r.Context(), tx, id, in.Version)
			if err != nil || deleted {
				return id, err
			}
			var linked bool
			if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM core.baskets WHERE batch_id=$1 AND deleted_at IS NULL)", id).Scan(&linked); err != nil {
				return id, err
			}
			if linked {
				return id, fail("BATCH_HAS_BASKETS", "请先处理该批次关联的料筐")
			}
			_, err = tx.Exec(r.Context(), "UPDATE core.batches SET deleted_at=now(),updated_at=now(),version=version+1 WHERE id=$1", id)
			return id, err
		})
	})
	m.HandleFunc("POST /api/v1/baskets", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.BasketInput
		if platform.Read(r, &in) != nil || !validBasket(in) {
			platform.Error(w, 400, "INVALID_BASKET", "料筐编号须完整符合编号规则（默认 7 位字母数字），并选择批次、数量与启用状态")
			return
		}
		e.catalogWrite(w, r, in, "CREATE_BASKET", func(tx pgx.Tx) (string, error) {
			if err := existingBatch(r.Context(), tx, in.BatchID); err != nil {
				return "", err
			}
			_, err := tx.Exec(r.Context(), "INSERT INTO core.baskets(basket_no,batch_id,quantity,enabled,notes) VALUES($1,$2,$3,$4,$5)", in.BasketNo, in.BatchID, in.Quantity, *in.Enabled, in.Notes)
			return in.BasketNo, err
		})
	})
	m.HandleFunc("PUT /api/v1/baskets/{basketNo}", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.BasketUpdate
		id := r.PathValue("basketNo")
		if platform.Read(r, &in) != nil || !validBasket(in.BasketInput) || in.Version < 1 || in.BasketNo != id {
			platform.Error(w, 400, "INVALID_BASKET", "料筐内容或版本无效；编号创建后不能更改")
			return
		}
		e.catalogWrite(w, r, in, "UPDATE_BASKET", func(tx pgx.Tx) (string, error) {
			deleted, err := mutableBasket(r.Context(), tx, id, in.Version)
			if err != nil {
				return id, err
			}
			if deleted {
				return id, pgx.ErrNoRows
			}
			if err = existingBatch(r.Context(), tx, in.BatchID); err != nil {
				return id, err
			}
			_, err = tx.Exec(r.Context(), "UPDATE core.baskets SET batch_id=$2,quantity=$3,enabled=$4,notes=$5,version=version+1,updated_at=now() WHERE basket_no=$1", id, in.BatchID, in.Quantity, *in.Enabled, in.Notes)
			return id, err
		})
	})
	m.HandleFunc("DELETE /api/v1/baskets/{basketNo}", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.CatalogDelete
		if platform.Read(r, &in) != nil || in.Version < 1 {
			platform.Error(w, 400, "INVALID_VERSION", "需要当前记录版本")
			return
		}
		id := r.PathValue("basketNo")
		e.catalogWrite(w, r, in, "DELETE_BASKET", func(tx pgx.Tx) (string, error) {
			deleted, err := mutableBasket(r.Context(), tx, id, in.Version)
			if err != nil || deleted {
				return id, err
			}
			_, err = tx.Exec(r.Context(), "UPDATE core.baskets SET deleted_at=now(),updated_at=now(),version=version+1 WHERE basket_no=$1", id)
			return id, err
		})
	})
}
func (e *Engine) listBatches(w http.ResponseWriter, r *http.Request) {
	page, size, err := pageParams(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if err != nil || utf8.RuneCountInString(q) > 80 {
		platform.Error(w, 400, "INVALID_QUERY", "查询条件无效")
		return
	}
	q = "%" + q + "%"
	out := protocol.Page[protocol.Batch]{Items: []protocol.Batch{}, Pagination: protocol.Pagination{Page: page, PageSize: size}}
	where := "t.deleted_at IS NULL AND (t.batch_no ILIKE $1 OR t.material_name ILIKE $1)"
	if err = e.store.db.QueryRow(r.Context(), "SELECT count(*) FROM core.batches t WHERE "+where, q).Scan(&out.Pagination.Total); err != nil {
		writeError(w, err)
		return
	}
	rows, err := e.store.db.Query(r.Context(), "SELECT "+batchColumns+" FROM core.batches t WHERE "+where+" ORDER BY t.created_at DESC,t.id LIMIT $2 OFFSET $3", q, size, (page-1)*size)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		b, err := readBatch(rows)
		if err != nil {
			writeError(w, err)
			return
		}
		out.Items = append(out.Items, b)
	}
	if rows.Err() != nil {
		writeError(w, rows.Err())
		return
	}
	platform.JSON(w, 200, out)
}
func (e *Engine) listBaskets(w http.ResponseWriter, r *http.Request) {
	page, size, err := pageParams(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	batchID := r.URL.Query().Get("batchId")
	if err != nil || utf8.RuneCountInString(q) > 80 || len(batchID) > 64 {
		platform.Error(w, 400, "INVALID_QUERY", "查询条件无效")
		return
	}
	q = "%" + q + "%"
	out := protocol.Page[protocol.Basket]{Items: []protocol.Basket{}, Pagination: protocol.Pagination{Page: page, PageSize: size}}
	from := " FROM core.baskets b JOIN core.batches t ON t.id=b.batch_id WHERE b.deleted_at IS NULL AND (b.basket_no ILIKE $1 OR t.batch_no ILIKE $1 OR t.material_name ILIKE $1) AND ($2='' OR b.batch_id=$2)"
	if err = e.store.db.QueryRow(r.Context(), "SELECT count(*)"+from, q, batchID).Scan(&out.Pagination.Total); err != nil {
		writeError(w, err)
		return
	}
	rows, err := e.store.db.Query(r.Context(), "SELECT "+basketColumns+from+" ORDER BY b.created_at DESC,b.basket_no LIMIT $3 OFFSET $4", q, batchID, size, (page-1)*size)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		b, err := readBasket(rows)
		if err != nil {
			writeError(w, err)
			return
		}
		out.Items = append(out.Items, b)
	}
	if rows.Err() != nil {
		writeError(w, rows.Err())
		return
	}
	platform.JSON(w, 200, out)
}
func (e *Engine) basketQR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("basketNo")
	var exists bool
	if err := e.store.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM core.baskets WHERE basket_no=$1 AND deleted_at IS NULL)", id).Scan(&exists); err != nil {
		writeError(w, err)
		return
	}
	if !exists {
		platform.Error(w, 404, "BASKET_NOT_FOUND", "料筐不存在或已删除")
		return
	}
	matrix, err := qrcode.NewQRCodeWriter().Encode(id, gozxing.BarcodeFormat_QR_CODE, 384, 384, nil)
	if err != nil {
		platform.Error(w, 400, "INVALID_QR", "二维码生成失败")
		return
	}
	img := image.NewGray(image.Rect(0, 0, 384, 384))
	for y := 0; y < 384; y++ {
		for x := 0; x < 384; x++ {
			if !matrix.Get(x, y) {
				img.Pix[y*img.Stride+x] = 255
			}
		}
	}
	w.Header().Set("Content-Type", "image/png")
	_ = png.Encode(w, img)
}
