package core

import (
	"context"
	"fmt"
	"furnace.local/iot/internal/mes"
	"furnace.local/iot/internal/protocol"
)

// Called with the engine lock held. Persist the verdict, snapshot and unique reservation together.
func (e *Engine) acceptMES(ctx context.Context, r record, verdict protocol.MESResult) error {
	tx, err := e.store.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	changed := false
	if verdict.Result == 1 {
		current, err := mes.Lookup(ctx, tx, r.Cycle.BasketNo, true)
		if err != nil {
			return err
		}
		if current.Result != 1 {
			verdict = current
		} else if verdict.Snapshot == nil || current.Snapshot.BasketNo != verdict.Snapshot.BasketNo || current.Snapshot.BatchID != verdict.Snapshot.BatchID || current.Snapshot.BasketVersion != verdict.Snapshot.BasketVersion || current.Snapshot.BatchVersion != verdict.Snapshot.BatchVersion {
			verdict = current
			verdict.Result = 2
			verdict.Reason = "CATALOG_CHANGED"
			verdict.Message = "MES 校验后台账已变更，请核实后重新处理"
		} else {
			verdict = current
		}
	}
	if verdict.Result == 1 {
		result, err := tx.Exec(ctx, "INSERT INTO core.occupancy(basket_no,cycle_id) VALUES($1,$2) ON CONFLICT DO NOTHING", r.Cycle.BasketNo, r.Cycle.ID)
		if err != nil {
			return err
		}
		changed = result.RowsAffected() > 0
		if !changed {
			var owner string
			if err = tx.QueryRow(ctx, "SELECT cycle_id FROM core.occupancy WHERE basket_no=$1", r.Cycle.BasketNo).Scan(&owner); err != nil {
				return err
			}
			if owner != r.Cycle.ID {
				verdict.Result = 2
				verdict.Reason = "BASKET_OCCUPIED"
				verdict.Message = "该料筐已有入炉占用记录"
			}
		}
	}
	r.Cycle.MES = &verdict
	if err = saveRecord(ctx, tx, r); err != nil {
		return err
	}
	if err = event(ctx, tx, r.Cycle.ID, "MES_RESULT", fmt.Sprintf("%s: %s", verdict.Reason, verdict.Message)); err != nil {
		return err
	}
	revision := e.catalogVersion
	if changed {
		revision, err = bumpCatalog(ctx, tx)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	e.catalogVersion = revision
	e.active[r.Cycle.FurnaceID] = r
	if verdict.Result == 1 {
		return e.transition(ctx, r, "OPENING", "MES 台账校验通过", "OPEN")
	}
	code := "MES_REJECTED"
	if verdict.Reason == "BASKET_OCCUPIED" {
		code = verdict.Reason
	}
	return e.transition(ctx, r, "BLOCKED", code, "")
}
