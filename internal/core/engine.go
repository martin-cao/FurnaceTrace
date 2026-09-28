package core

import (
	"context"
	"encoding/json"
	"fmt"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	rules             map[string]*ruleRuntime
	rulesVersion      int64
	mu                sync.Mutex
	catalogVersion    int64
	cameraMu          sync.Mutex
	cameraConfigs     map[string]protocol.CameraConfig
	cameraSwitchUntil map[string]time.Time
	store             *Store
	cfg               []platform.FurnaceConfig
	active            map[string]record
	devices           map[string]protocol.DeviceView
	deviceReceived    map[string]time.Time
	cameras           map[string]protocol.CameraState
	armed             map[string]bool
	validating        map[string]bool
	startup           time.Time
}

func (e *Engine) config(id string) (platform.FurnaceConfig, bool) {
	for _, f := range e.cfg {
		if f.ID == id {
			return f, true
		}
	}
	return platform.FurnaceConfig{}, false
}
func (e *Engine) device(id string) protocol.DeviceView {
	v := e.devices[id]
	if time.Since(e.deviceReceived[id]) > 3*time.Second {
		v.Online = false
		v.Reason = "ADAPTER_OFFLINE"
	}
	return v
}
func (e *Engine) equipmentReady(f platform.FurnaceConfig) bool {
	return e.device(f.Sensor).Online && e.device(f.Door).Online && e.device(f.Lamp).Online
}
func (e *Engine) ready(f platform.FurnaceConfig) bool {
	config, configured := e.cameraConfigs[f.ID]
	return (!configured || config.Applied) && !time.Now().Before(e.cameraSwitchUntil[f.ID]) && e.equipmentReady(f) && e.cameras[f.ID].Online
}
func (e *Engine) transition(ctx context.Context, r record, stage, reason, action string) error {
	f, _ := e.config(r.Cycle.FurnaceID)
	now := time.Now().UTC()
	r.Cycle.Status = stage
	r.Cycle.Reason = reason
	if stage == "BLOCKED" || stage == "NEEDS_INPUT" {
		r.AlarmReason = reason
	}
	r.Cycle.UpdatedAt = now
	r.Deadline = now.Add(10 * time.Second)
	if stage == "WAITING_ENTRY" {
		r.Deadline = now.Add(30 * time.Second)
	}
	if stage == "COMPLETED" || stage == "ABORTED" {
		r.Cycle.CompletedAt = &now
	}
	tx, err := e.store.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if action != "" {
		v := e.device(f.Door)
		if !v.Online || v.State == nil {
			return fmt.Errorf("door is unavailable")
		}
		cmd := protocol.DeviceCommand{CommandID: protocol.ID(), CycleID: r.Cycle.ID, DeviceID: f.Door, FurnaceID: f.ID, TargetBootID: v.State.BootID, Action: action, IssuedAt: now, ExpiresAt: r.Deadline}
		r.CommandID = cmd.CommandID
		if _, err = tx.Exec(ctx, "INSERT INTO core.commands(id,cycle_id,device_id,status,expires_at,doc) VALUES($1,$2,$3,'PENDING',$4,$5)", cmd.CommandID, cmd.CycleID, cmd.DeviceID, cmd.ExpiresAt, encoded(cmd)); err != nil {
			return err
		}
	}
	if err = saveRecord(ctx, tx, r); err != nil {
		return err
	}
	if err = event(ctx, tx, r.Cycle.ID, stage, reason); err != nil {
		return err
	}
	if stage == "BLOCKED" || stage == "NEEDS_INPUT" {
		if _, err = tx.Exec(ctx, "UPDATE core.commands SET status='UNKNOWN' WHERE cycle_id=$1 AND status IN ('PENDING','ACCEPTED')", r.Cycle.ID); err != nil {
			return err
		}
	}
	revision := e.catalogVersion
	if stage == "COMPLETED" || stage == "ABORTED" {
		revision, err = bumpCatalog(ctx, tx)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	e.catalogVersion = revision
	if stage == "COMPLETED" || stage == "ABORTED" {
		delete(e.active, f.ID)
	} else {
		e.active[f.ID] = r
	}
	slog.Info("cycle transition", "furnaceId", f.ID, "cycleId", r.Cycle.ID, "status", stage, "reason", reason)
	return nil
}
func (e *Engine) start(ctx context.Context, f platform.FurnaceConfig) error {
	now := time.Now().UTC()
	r := record{Cycle: protocol.Cycle{ID: protocol.ID(), FurnaceID: f.ID, Status: "SCANNING", CreatedAt: now, UpdatedAt: now}, SessionID: protocol.ID(), Deadline: now.Add(5 * time.Second)}
	s := protocol.ScanSession{SessionID: r.SessionID, CycleID: r.Cycle.ID, FurnaceID: f.ID, CameraID: f.Camera, ExpiresAt: r.Deadline}
	tx, err := e.store.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = saveRecord(ctx, tx, r); err != nil {
		return err
	}
	if err = event(ctx, tx, r.Cycle.ID, "SCANNING", "料筐到位，开始扫码"); err != nil {
		return err
	}
	if err = enqueue(ctx, tx, s.SessionID, "scan-session", s); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	e.active[f.ID] = r
	e.armed[f.ID] = false
	return nil
}
func (e *Engine) tick(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for _, f := range e.cfg {
		v := e.device(f.Sensor)
		door := e.device(f.Door)
		e.monitorRules(ctx, f, now)
		r, exists := e.active[f.ID]
		if !exists {
			// A stable QR detection now starts admission; no entrance sensor trigger.
			continue
		}
		stage := r.Cycle.Status
		if stage == "BLOCKED" || stage == "NEEDS_INPUT" {
			// Start recovery immediately; failed recovery retries after the persisted backoff.
			if !r.ResetAborts || !now.Before(r.Deadline) {
				if err := e.autoReset(ctx, r); err != nil {
					slog.Error("auto reset", "furnaceId", f.ID, "error", err)
				}
			}
			continue
		}
		if r.ResetAborts && stage == "RESETTING" && r.CommandID == "" {
			if now.After(r.Deadline) {
				_ = e.transition(ctx, r, "BLOCKED", "RESET_TIMEOUT", "")
				continue
			}
			if !door.Online || door.State == nil {
				continue
			}
			var unresolved bool
			if err := e.store.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.commands WHERE cycle_id=$1 AND status IN ('PENDING','ACCEPTED','UNKNOWN') AND expires_at>now())", r.Cycle.ID).Scan(&unresolved); err != nil || unresolved {
				continue
			}
			if door.State.Bool("doorClosed") {
				_ = e.transition(ctx, r, "RESETTING", "炉门已关闭，正在复位", "RESET")
			} else {
				_ = e.transition(ctx, r, "CLOSING", "复位自动关门", "CLOSE")
			}
			continue
		}
		if !r.ResetAborts && !e.equipmentReady(f) || r.ResetAborts && (!door.Online || door.State == nil) {
			_ = e.transition(ctx, r, "BLOCKED", "DEVICE_OFFLINE", "")
			continue
		}
		switch stage {
		case "SCANNING":
			if now.After(r.Deadline.Add(time.Second)) {
				reason := "SCAN_TIMEOUT"
				if !e.cameras[f.ID].Online {
					reason = "CAMERA_OFFLINE"
				}
				_ = e.transition(ctx, r, "NEEDS_INPUT", reason, "")
			}
		case "VALIDATING":
			if !e.validating[f.ID] && !now.Before(r.NextValidation) {
				e.validating[f.ID] = true
				go e.validateMES(ctx, r)
			}
		case "OPENING", "CLOSING", "RESETTING":
			var b []byte
			err := e.store.db.QueryRow(ctx, "SELECT doc FROM core.commands WHERE id=$1", r.CommandID).Scan(&b)
			if err != nil {
				continue
			}
			var command protocol.DeviceCommand
			_ = json.Unmarshal(b, &command)
			if door.State.BootID != command.TargetBootID {
				_ = e.transition(ctx, r, "BLOCKED", "DEVICE_RESTARTED", "")
				continue
			}
			matched := door.State.CommandID == r.CommandID
			if matched && door.State.ExecutionStatus == "ACCEPTED" {
				_, _ = e.store.db.Exec(ctx, "UPDATE core.commands SET status='ACCEPTED' WHERE id=$1 AND status='PENDING'", r.CommandID)
			}
			physicalMatch := stage == "OPENING" && door.State.Bool("doorOpen") || stage != "OPENING" && door.State.Bool("doorClosed")
			if matched && door.State.ExecutionStatus == "COMPLETED" && physicalMatch {
				_, _ = e.store.db.Exec(ctx, "UPDATE core.commands SET status='COMPLETED' WHERE id=$1", r.CommandID)
				if stage == "OPENING" && door.State.Bool("doorOpen") {
					_ = e.transition(ctx, r, "WAITING_ENTRY", "炉门已打开", "")
				}
				if stage == "CLOSING" && door.State.Bool("doorClosed") {
					_ = e.transition(ctx, r, "RESETTING", "炉门已关闭，等待复位", "RESET")
				}
				if stage == "RESETTING" && door.State.Bool("doorClosed") {
					if r.ResetAborts {
						_ = e.transition(ctx, r, "ABORTED", "自动复位完成，异常周期已中止", "")
					} else {
						_ = e.transition(ctx, r, "COMPLETED", "入炉完成", "")
					}
				}
			} else if now.After(r.Deadline) {
				reason := map[string]string{"OPENING": "OPEN_TIMEOUT", "CLOSING": "CLOSE_TIMEOUT", "RESETTING": "RESET_TIMEOUT"}[stage]
				_ = e.transition(ctx, r, "BLOCKED", reason, "")
			}
		case "WAITING_ENTRY":
			if v.State.Bool("inFurnace") && !v.State.Bool("present") {
				_ = e.transition(ctx, r, "CLOSING", "料筐已进入炉内", "CLOSE")
			} else if now.After(r.Deadline) {
				_ = e.transition(ctx, r, "BLOCKED", "ENTRY_TIMEOUT", "")
			}
		}
	}
}

func basket(raw string) (string, error) {
	length, err := strconv.Atoi(platform.Env("BASKET_LENGTH", "7"))
	if err != nil || length < 1 || length > 64 {
		return "", fmt.Errorf("invalid basket length configuration")
	}
	pattern, err := regexp.Compile(platform.Env("BASKET_PATTERN", fmt.Sprintf(`^[a-zA-Z0-9]{%d}$`, length)))
	if err != nil {
		return "", fmt.Errorf("invalid basket pattern configuration")
	}
	raw = strings.TrimSpace(raw)
	if len(raw) < length {
		return "", fmt.Errorf("invalid basket")
	}
	id := raw[len(raw)-length:]
	if !pattern.MatchString(id) {
		return "", fmt.Errorf("invalid basket")
	}
	return id, nil
}
func (e *Engine) validateMES(ctx context.Context, r record) {
	var res protocol.MESResult
	err := platform.Request(ctx, "GET", platform.Env("MES_URL", "http://mes:8080")+"/internal/v1/mes/baskets/"+url.PathEscape(r.Cycle.BasketNo), "", nil, &res)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.validating[r.Cycle.FurnaceID] = false
	current, ok := e.active[r.Cycle.FurnaceID]
	if !ok || current.Cycle.ID != r.Cycle.ID || current.Cycle.Status != "VALIDATING" {
		return
	}
	r = current
	if err != nil || res.BasketNo != r.Cycle.BasketNo || res.Result < 0 || res.Result > 2 || res.Result == 1 && res.Snapshot == nil {
		r.ValidationAttempts++
		if r.ValidationAttempts >= 6 {
			_ = e.transition(ctx, r, "BLOCKED", "MES_UNAVAILABLE", "")
			return
		}
		r.NextValidation = time.Now().Add(time.Duration(1<<min(r.ValidationAttempts-1, 3)) * time.Second)
		tx, x := e.store.db.Begin(ctx)
		if x == nil {
			defer tx.Rollback(ctx)
			if saveRecord(ctx, tx, r) == nil && tx.Commit(ctx) == nil {
				e.active[r.Cycle.FurnaceID] = r
			}
		}
		return
	}
	_ = e.acceptMES(ctx, r, res)
}
