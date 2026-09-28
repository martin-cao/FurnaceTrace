package core

import (
	"context"
	"encoding/json"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"log/slog"
	"time"
)

func (e *Engine) dispatch(ctx context.Context) {
	rows, err := e.store.db.Query(ctx, "SELECT id,doc FROM core.commands WHERE status='PENDING' AND next_attempt_at<=now() AND expires_at>now() ORDER BY next_attempt_at LIMIT 10")
	if err != nil {
		return
	}
	var commands []protocol.DeviceCommand
	for rows.Next() {
		var id string
		var b []byte
		if rows.Scan(&id, &b) == nil {
			var c protocol.DeviceCommand
			if json.Unmarshal(b, &c) == nil {
				commands = append(commands, c)
			}
		}
	}
	rows.Close()
	for _, c := range commands {
		// Recheck durable state immediately before any side effect; blocked cycles cancel dispatch.
		var valid bool
		if e.store.db.QueryRow(ctx, "SELECT status='PENDING' AND expires_at>now() FROM core.commands WHERE id=$1", c.CommandID).Scan(&valid) != nil || !valid {
			continue
		}
		_ = platform.Request(ctx, "POST", platform.Env("SCADA_URL", "http://scada-adapter:8080")+"/internal/v1/scada/commands", "", c, nil)
		_, _ = e.store.db.Exec(ctx, "UPDATE core.commands SET attempts=attempts+1,next_attempt_at=now()+interval '1 second' WHERE id=$1", c.CommandID)
	}
	_, _ = e.store.db.Exec(ctx, "UPDATE core.commands SET status='EXPIRED' WHERE expires_at<=now() AND status IN ('PENDING','ACCEPTED')")
	rows, err = e.store.db.Query(ctx, "SELECT id,kind,payload FROM core.outbox WHERE NOT delivered AND next_attempt_at<=now() ORDER BY next_attempt_at LIMIT 10")
	if err != nil {
		return
	}
	type job struct {
		id, kind string
		payload  []byte
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.kind, &j.payload) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		var err error
		switch j.kind {
		case "account-access":
			err = platform.Request(ctx, "PUT", platform.Env("NOTIFIER_URL", "http://notifier:8080")+"/internal/v1/account-access", "", json.RawMessage(j.payload), nil)
		case "cancel-reminders":
			var in struct {
				AlarmID string `json:"alarmId"`
			}
			_ = json.Unmarshal(j.payload, &in)
			err = platform.Request(ctx, "POST", platform.Env("NOTIFIER_URL", "http://notifier:8080")+"/internal/v1/notifications/"+in.AlarmID+"/cancel-reminders", "", nil, nil)
		case "notification":
			err = platform.Request(ctx, "POST", platform.Env("NOTIFIER_URL", "http://notifier:8080")+"/internal/v1/notifications", j.id, json.RawMessage(j.payload), nil)
		case "scan-session":
			var s protocol.ScanSession
			_ = json.Unmarshal(j.payload, &s)
			f, ok := e.config(s.FurnaceID)
			if !ok || !s.ExpiresAt.After(time.Now()) {
				err = nil
			} else {
				err = platform.Request(ctx, "POST", f.VisionURL+"/internal/v1/scan-sessions", "", s, nil)
			}
		}
		if err == nil {
			_, _ = e.store.db.Exec(ctx, "UPDATE core.outbox SET delivered=true WHERE id=$1", j.id)
		} else {
			_, _ = e.store.db.Exec(ctx, "UPDATE core.outbox SET attempts=attempts+1,next_attempt_at=now()+interval '2 seconds' WHERE id=$1", j.id)
		}
	}
}
func (e *Engine) syncLamp(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, f := range e.cfg {
		v := e.device(f.Lamp)
		if !v.Online || v.State == nil {
			continue
		}
		var active bool
		if e.store.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.alarms WHERE furnace_id=$1 AND active AND COALESCE((doc->>'lamp')::boolean,true))", f.ID).Scan(&active) != nil {
			continue
		}
		if v.State.Bool("lampOn") == active {
			continue
		}
		var pending bool
		if e.store.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.commands WHERE device_id=$1 AND status IN ('PENDING','ACCEPTED'))", f.Lamp).Scan(&pending) != nil || pending {
			continue
		}
		action := "ALARM_OFF"
		if active {
			action = "ALARM_ON"
		}
		now := time.Now().UTC()
		c := protocol.DeviceCommand{CommandID: protocol.ID(), DeviceID: f.Lamp, FurnaceID: f.ID, TargetBootID: v.State.BootID, Action: action, IssuedAt: now, ExpiresAt: now.Add(10 * time.Second)}
		_, _ = e.store.db.Exec(ctx, "INSERT INTO core.commands(id,cycle_id,device_id,status,expires_at,doc) VALUES($1,'',$2,'PENDING',$3,$4)", c.CommandID, c.DeviceID, c.ExpiresAt, encoded(c))
	}
}
func Run(ctx context.Context) error {
	cfg, err := platform.Config()
	if err != nil {
		return err
	}
	store, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer store.close()
	active, err := store.active(ctx)
	if err != nil {
		return err
	}
	e := &Engine{store: store, cfg: cfg, active: active, devices: map[string]protocol.DeviceView{}, deviceReceived: map[string]time.Time{}, cameras: map[string]protocol.CameraState{}, armed: map[string]bool{}, validating: map[string]bool{}, startup: time.Now()}
	for _, f := range cfg {
		e.cameras[f.ID] = protocol.CameraState{CameraID: f.Camera}
		for kind, id := range f.DeviceIDs() {
			e.devices[id] = protocol.DeviceView{DeviceID: id, FurnaceID: f.ID, Kind: kind, Reason: "STARTING"}
		}
	}
	if err = e.bootstrapAdmin(ctx); err != nil {
		return err
	}
	if err = e.loadAlarmRules(ctx); err != nil {
		return err
	}
	if err = e.loadCatalog(ctx); err != nil {
		return err
	}
	if err = e.loadCameraSettings(ctx); err != nil {
		return err
	}
	for _, r := range active {
		if err = e.transition(ctx, r, "BLOCKED", "RECOVERY_REQUIRED", ""); err != nil {
			return err
		}
	}
	go e.cameraSettingsLoop(ctx)
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.tick(ctx)
			}
		}
	}()
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.dispatch(ctx)
				e.syncLamp(ctx)
			}
		}
	}()
	for _, f := range cfg {
		go func(f platform.FurnaceConfig) {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				var state protocol.CameraState
				err := platform.Request(ctx, "GET", f.VisionURL+"/internal/v1/camera", "", nil, &state)
				e.mu.Lock()
				if err == nil && state.CameraID == f.Camera {
					e.cameras[f.ID] = state
				} else {
					state = e.cameras[f.ID]
					state.Online = false
					e.cameras[f.ID] = state
				}
				e.mu.Unlock()
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}(f)
	}
	mux := e.routes()
	e.authRoutes(mux)
	e.accountSettingsRoutes(mux)
	e.catalogRoutes(mux)
	e.alarmRuleRoutes(mux)
	e.cameraSettingsRoutes(mux)
	e.personalRoutes(mux)
	attachUI(mux)
	slog.Info("workflow initialized", "furnaces", len(cfg), "unfinished", len(active))
	go e.serveSCADA(ctx)
	return platform.Serve(ctx, "core", e.authenticated(mux, false))
}
