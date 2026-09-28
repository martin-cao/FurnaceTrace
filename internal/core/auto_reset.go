package core

import (
	"context"
	"furnace.local/iot/internal/protocol"
	"time"
)

func prepareReset(rec record, now time.Time) record {
	if rec.AlarmReason == "" {
		rec.AlarmReason = rec.Cycle.Reason
	}
	rec.Cycle.Status = "RESETTING"
	rec.Cycle.UpdatedAt = now
	rec.Cycle.Reason = "正在自动关门并复位"
	rec.ResetAborts = true
	rec.Deadline = now.Add(15 * time.Second)
	rec.CommandID = "" // Wait for fresh door state and old command expiry before issuing CLOSE/RESET.
	return rec
}

// Called with e.mu held. The existing reset state machine waits for real device feedback.
func (e *Engine) autoReset(ctx context.Context, rec record) error {
	rec = prepareReset(rec, time.Now().UTC())
	tx, err := e.store.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = saveRecord(ctx, tx, rec); err != nil {
		return err
	}
	if err = event(ctx, tx, rec.Cycle.ID, "AUTO_RESET", "流程异常后自动关门复位，原原因: "+rec.AlarmReason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO core.audit(id,operator,action,target,reason) VALUES($1,'system','AUTO_RESET',$2,$3)", protocol.ID(), rec.Cycle.ID, rec.AlarmReason); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	e.active[rec.Cycle.FurnaceID] = rec
	return nil
}
