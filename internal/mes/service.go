// Package mes validates admission against the persisted production catalogue.
package mes

import (
	"context"
	"errors"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"os"
	"time"
)

type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Lock is used by core before reserving a basket, closing the gap between the HTTP verdict and an edit.
func Lookup(ctx context.Context, q Querier, basketNo string, lock bool) (protocol.MESResult, error) {
	result := protocol.MESResult{BasketNo: basketNo, Result: 0, Reason: "NOT_FOUND", Message: "料筐不存在或已删除"}
	var s protocol.AdmissionSnapshot
	var batchDeleted bool
	query := `SELECT b.basket_no,b.version,b.enabled,b.quantity,t.id,t.batch_no,t.version,t.enabled,t.material_name,t.process_spec,t.quality_status,t.deleted_at IS NOT NULL
 FROM core.baskets b JOIN core.batches t ON t.id=b.batch_id WHERE b.basket_no=$1 AND b.deleted_at IS NULL`
	if lock {
		query += " FOR SHARE OF b,t"
	}
	err := q.QueryRow(ctx, query, basketNo).Scan(&s.BasketNo, &s.BasketVersion, &s.BasketEnabled, &s.Quantity, &s.BatchID, &s.BatchNo, &s.BatchVersion, &s.BatchEnabled, &s.MaterialName, &s.ProcessSpec, &s.QualityStatus, &batchDeleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	s.CheckedAt = time.Now().UTC()
	result.Snapshot = &s
	result.Result = 2
	switch {
	case !s.BasketEnabled:
		result.Reason = "BASKET_DISABLED"
		result.Message = "料筐未启用入炉"
	case batchDeleted || !s.BatchEnabled:
		result.Reason = "BATCH_DISABLED"
		result.Message = "关联批次已停用或删除"
	case s.QualityStatus == "pending":
		result.Reason = "QUALITY_PENDING"
		result.Message = "批次尚未通过质检"
	case s.QualityStatus != "passed":
		result.Reason = "QUALITY_FAILED"
		result.Message = "批次质检不合格，禁止入炉"
	default:
		result.Result = 1
		result.Reason = "ALLOWED"
		result.Message = "料筐存在，批次质检通过，允许入炉"
	}
	return result, nil
}
func Run(ctx context.Context) error {
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return errors.New("MES database configuration invalid")
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	mux := platform.Mux("mes", func() bool {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		var v int
		return db.QueryRow(ctx, "SELECT 1 FROM core.catalog_revision WHERE id=1").Scan(&v) == nil
	})
	mux.HandleFunc("GET /internal/v1/mes/baskets/{basketNo}", func(w http.ResponseWriter, r *http.Request) {
		result, err := Lookup(r.Context(), db, r.PathValue("basketNo"), false)
		if err != nil {
			platform.Error(w, 503, "MES_STORAGE_UNAVAILABLE", "MES 台账暂不可用")
			return
		}
		platform.JSON(w, 200, result)
	})
	return platform.Serve(ctx, "mes", mux)
}
