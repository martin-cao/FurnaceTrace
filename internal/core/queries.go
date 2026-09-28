package core

import (
	"encoding/json"
	"fmt"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"
)

func (e *Engine) listCycles(w http.ResponseWriter, r *http.Request) {
	p, size, err := pageParams(r)
	if err != nil {
		platform.Error(w, 400, "INVALID_PAGE", "分页参数无效")
		return
	}
	where := []string{"true"}
	args := []any{}
	for _, filter := range []struct{ key, col string }{{"furnaceId", "furnace_id"}, {"basketNo", "basket_no"}, {"status", "status"}} {
		if v := r.URL.Query().Get(filter.key); v != "" {
			args = append(args, v)
			where = append(where, fmt.Sprintf("%s=$%d", filter.col, len(args)))
		}
	}
	condition := strings.Join(where, " AND ")
	var total int
	if err = e.store.db.QueryRow(r.Context(), "SELECT count(*) FROM core.cycles WHERE "+condition, args...).Scan(&total); err != nil {
		writeError(w, err)
		return
	}
	args = append(args, size, (p-1)*size)
	rows, err := e.store.db.Query(r.Context(), fmt.Sprintf("SELECT doc->'cycle' FROM core.cycles WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d", condition, len(args)-1, len(args)), args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	items := []protocol.Cycle{}
	for rows.Next() {
		var b []byte
		var c protocol.Cycle
		if err = rows.Scan(&b); err != nil {
			break
		}
		if err = json.Unmarshal(b, &c); err != nil {
			break
		}
		items = append(items, c)
	}
	if err != nil || rows.Err() != nil {
		writeError(w, fmt.Errorf("read cycles"))
		return
	}
	platform.JSON(w, 200, protocol.Page[protocol.Cycle]{Items: items, Pagination: protocol.Pagination{Page: p, PageSize: size, Total: total}})
}
func (e *Engine) getCycle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("cycleId")
	var b []byte
	err := e.store.db.QueryRow(r.Context(), "SELECT doc->'cycle' FROM core.cycles WHERE id=$1", id).Scan(&b)
	if err == pgx.ErrNoRows {
		platform.Error(w, 404, "NOT_FOUND", "记录不存在")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	detail := protocol.CycleDetail{Events: []protocol.CycleEvent{}, Commands: []protocol.CommandRecord{}}
	if err = json.Unmarshal(b, &detail.Cycle); err != nil {
		writeError(w, err)
		return
	}
	rows, err := e.store.db.Query(r.Context(), "SELECT doc FROM core.events WHERE cycle_id=$1 ORDER BY at", id)
	if err != nil {
		writeError(w, err)
		return
	}
	for rows.Next() {
		var data []byte
		var ev protocol.CycleEvent
		if err = rows.Scan(&data); err != nil {
			break
		}
		if err = json.Unmarshal(data, &ev); err != nil {
			break
		}
		detail.Events = append(detail.Events, ev)
	}
	rows.Close()
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err = e.store.db.Query(r.Context(), "SELECT doc,status,attempts FROM core.commands WHERE cycle_id=$1 ORDER BY expires_at", id)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		var c protocol.CommandRecord
		if err = rows.Scan(&data, &c.Status, &c.Attempts); err != nil {
			break
		}
		if err = json.Unmarshal(data, &c.Command); err != nil {
			break
		}
		detail.Commands = append(detail.Commands, c)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	platform.JSON(w, 200, detail)
}
func (e *Engine) listAlarms(w http.ResponseWriter, r *http.Request) {
	p, size, err := pageParams(r)
	if err != nil {
		platform.Error(w, 400, "INVALID_PAGE", "分页参数无效")
		return
	}
	furnace := r.URL.Query().Get("furnaceId")
	active := r.URL.Query().Get("active")
	if active != "" && active != "true" && active != "false" {
		platform.Error(w, 400, "INVALID_FILTER", "active 必须为 true/false")
		return
	}
	cond := "($1='' OR furnace_id=$1) AND ($2='' OR active::text=$2)"
	var total int
	if err = e.store.db.QueryRow(r.Context(), "SELECT count(*) FROM core.alarms WHERE "+cond, furnace, active).Scan(&total); err != nil {
		writeError(w, err)
		return
	}
	rows, err := e.store.db.Query(r.Context(), "SELECT doc FROM core.alarms WHERE "+cond+" ORDER BY raised_at DESC LIMIT $3 OFFSET $4", furnace, active, size, (p-1)*size)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	items := []protocol.Alarm{}
	for rows.Next() {
		var b []byte
		var a protocol.Alarm
		if err = rows.Scan(&b); err != nil {
			break
		}
		if err = json.Unmarshal(b, &a); err != nil {
			break
		}
		items = append(items, a)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	platform.JSON(w, 200, protocol.Page[protocol.Alarm]{Items: items, Pagination: protocol.Pagination{Page: p, PageSize: size, Total: total}})
}
func (e *Engine) acknowledge(w http.ResponseWriter, r *http.Request) {
	var in protocol.OperatorAction
	if platform.Read(r, &in) != nil || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 {
		platform.Error(w, 400, "INVALID_INPUT", "请填写操作人和原因")
		return
	}
	in.Operator = requestUser(r).Username
	e.idempotent(w, r, in, func(tx pgx.Tx) (string, afterCommit, error) {
		id := r.PathValue("alarmId")
		var b []byte
		if err := tx.QueryRow(r.Context(), "SELECT doc FROM core.alarms WHERE id=$1 FOR UPDATE", id).Scan(&b); err != nil {
			if err == pgx.ErrNoRows {
				return "", nil, fault{404, "NOT_FOUND", "报警不存在"}
			}
			return "", nil, err
		}
		var a protocol.Alarm
		if err := json.Unmarshal(b, &a); err != nil {
			return "", nil, err
		}
		if a.AcknowledgedAt == nil {
			now := time.Now().UTC()
			a.AcknowledgedAt = &now
			a.Operator = in.Operator
			if _, err := tx.Exec(r.Context(), "UPDATE core.alarms SET doc=$2 WHERE id=$1", id, encoded(a)); err != nil {
				return "", nil, err
			}
			if _, err := tx.Exec(r.Context(), "INSERT INTO core.audit(id,operator,action,target,reason) VALUES($1,$2,'ACK_ALARM',$3,$4)", protocol.ID(), in.Operator, id, in.Reason); err != nil {
				return "", nil, err
			}
		}
		return id, nil, nil
	})
}
