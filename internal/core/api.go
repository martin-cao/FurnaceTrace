package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type fault struct {
	status        int
	code, message string
}

func (f fault) Error() string         { return f.message }
func fail(code, message string) error { return fault{409, code, message} }
func writeError(w http.ResponseWriter, err error) {
	var f fault
	if errors.As(err, &f) {
		platform.Error(w, f.status, f.code, f.message)
	} else {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "持久数据暂不可用")
	}
}

type afterCommit func()

func (e *Engine) idempotent(w http.ResponseWriter, r *http.Request, body any, fn func(pgx.Tx) (string, afterCommit, error)) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		platform.Error(w, 400, "INVALID_IDEMPOTENCY_KEY", "需要 8–128 字符的 Idempotency-Key")
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ctx := r.Context()
	tx, err := e.store.db.Begin(ctx)
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	scope := requestUser(r).ID + " " + r.Method + " " + r.URL.Path
	result, err := tx.Exec(ctx, "INSERT INTO core.idempotency(scope,key,body) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", scope, key, encoded(body))
	if err != nil {
		writeError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		var equal bool
		var response []byte
		err = tx.QueryRow(ctx, "SELECT body=$3::jsonb,response FROM core.idempotency WHERE scope=$1 AND key=$2", scope, key, encoded(body)).Scan(&equal, &response)
		if err != nil {
			writeError(w, err)
			return
		}
		if !equal {
			writeError(w, fail("IDEMPOTENCY_CONFLICT", "同一幂等键不能用于不同内容"))
			return
		}
		platform.JSON(w, 202, json.RawMessage(response))
		return
	}
	id, apply, err := fn(tx)
	if err != nil {
		writeError(w, err)
		return
	}
	response := map[string]string{"id": id, "status": "accepted"}
	if _, err = tx.Exec(ctx, "UPDATE core.idempotency SET response=$3 WHERE scope=$1 AND key=$2", scope, key, encoded(response)); err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if apply != nil {
		apply()
	}
	platform.JSON(w, 202, response)
}
func (e *Engine) scan(ctx context.Context, tx pgx.Tx, input protocol.ScanResult, manual bool, operator, reason string) (string, afterCommit, error) {
	r, ok := e.active[input.FurnaceID]
	if !ok || r.Cycle.ID != input.CycleID || (!manual && r.SessionID != input.SessionID) {
		return input.SessionID, nil, nil
	}
	allowed := r.Cycle.Status == "SCANNING" || manual && r.Cycle.Status == "NEEDS_INPUT"
	if !allowed {
		if err := event(ctx, tx, r.Cycle.ID, "SCAN_IGNORED", "流程执行期间忽略重复扫码"); err != nil {
			return "", nil, err
		}
		return r.Cycle.ID, nil, nil
	}
	f, _ := e.config(input.FurnaceID)
	if !manual && input.CameraID != f.Camera {
		return "", nil, fail("CAMERA_MISMATCH", "摄像头与炉子不匹配")
	}
	if manual && !e.equipmentReady(f) {
		return "", nil, fail("DEVICE_UNAVAILABLE", "设备离线，不能补录放行")
	}
	if input.Outcome == "DECODED" {
		id, err := basket(input.RawCode)
		if err != nil {
			r.Cycle.Status = "NEEDS_INPUT"
			r.Cycle.Reason = "INVALID_BARCODE"
		} else {
			r.Cycle.BasketNo = id
			r.Cycle.RawCode = input.RawCode
			r.Cycle.Status = "VALIDATING"
			r.Cycle.Reason = ""
			r.NextValidation = time.Now()
			r.ValidationAttempts = 0
		}
	} else {
		r.Cycle.Status = "NEEDS_INPUT"
		r.Cycle.Reason = input.Outcome
	}
	r.AlarmReason = r.Cycle.Reason
	r.Cycle.UpdatedAt = time.Now().UTC()
	if err := saveRecord(ctx, tx, r); err != nil {
		return "", nil, err
	}
	if err := event(ctx, tx, r.Cycle.ID, r.Cycle.Status, "扫码结果: "+input.Outcome); err != nil {
		return "", nil, err
	}
	if manual {
		if _, err := tx.Exec(ctx, "INSERT INTO core.audit(id,operator,action,target,reason) VALUES($1,$2,'MANUAL_SCAN',$3,$4)", protocol.ID(), operator, r.Cycle.ID, reason); err != nil {
			return "", nil, err
		}
		if err := event(ctx, tx, r.Cycle.ID, "MANUAL_SCAN", operator+": "+reason); err != nil {
			return "", nil, err
		}

	}
	revision := e.catalogVersion
	if r.Cycle.Status == "VALIDATING" {
		var err error
		revision, err = bumpCatalog(ctx, tx)
		if err != nil {
			return "", nil, err
		}
	}
	return r.Cycle.ID, func() { e.active[f.ID] = r; e.catalogVersion = revision }, nil
}
func (e *Engine) snapshots() []protocol.Furnace {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]protocol.Furnace, 0, len(e.cfg))
	for _, f := range e.cfg {
		v := protocol.Furnace{FurnaceID: f.ID, Name: f.Name, Ready: e.ready(f), Phase: "IDLE", Camera: e.cameras[f.ID], PreviewURL: f.PreviewURL, Devices: []protocol.DeviceView{}}
		if rec, ok := e.active[f.ID]; ok {
			c := rec.Cycle
			v.Cycle = &c
			v.Phase = c.Status
		}
		for _, id := range []string{f.Sensor, f.Door, f.Lamp} {
			v.Devices = append(v.Devices, e.device(id))
		}
		if f.Temperature != "" {
			v.Devices = append(v.Devices, e.device(f.Temperature))
		}
		result = append(result, v)
	}
	return result
}
func pageParams(r *http.Request) (int, int, error) {
	p, s := 1, 20
	var err error
	if v := r.URL.Query().Get("page"); v != "" {
		p, err = strconv.Atoi(v)
		if err != nil {
			return 0, 0, err
		}
	}
	if v := r.URL.Query().Get("pageSize"); v != "" {
		s, err = strconv.Atoi(v)
		if err != nil {
			return 0, 0, err
		}
	}
	if p < 1 || s < 1 || s > 100 {
		return 0, 0, fmt.Errorf("invalid pagination")
	}
	return p, s, nil
}
func (e *Engine) routes() *http.ServeMux {
	m := platform.Mux("core", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return e.store.db.Ping(ctx) == nil
	})
	e.detectionRoutes(m)
	m.HandleFunc("GET /api/v1/furnaces", func(w http.ResponseWriter, r *http.Request) { platform.JSON(w, 200, e.snapshots()) })
	m.HandleFunc("GET /api/v1/furnaces/{furnaceId}", func(w http.ResponseWriter, r *http.Request) {
		for _, f := range e.snapshots() {
			if f.FurnaceID == r.PathValue("furnaceId") {
				platform.JSON(w, 200, f)
				return
			}
		}
		platform.Error(w, 404, "NOT_FOUND", "炉子不存在")
	})
	m.HandleFunc("POST /internal/v1/device-states", func(w http.ResponseWriter, r *http.Request) {
		var views []protocol.DeviceView
		if platform.Read(r, &views) != nil || len(views) == 0 {
			platform.Error(w, 400, "INVALID_STATE", "设备状态格式错误")
			return
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, v := range views {
			f, ok := e.config(v.FurnaceID)
			if !ok {
				continue
			}
			expected := map[string]string{}
			for kind, id := range f.DeviceIDs() {
				expected[id] = kind
			}
			if expected[v.DeviceID] != v.Kind {
				continue
			}
			if v.State != nil && (v.State.DeviceID != v.DeviceID || v.State.FurnaceID != v.FurnaceID || v.State.Kind != v.Kind) {
				continue
			}
			old := e.devices[v.DeviceID]
			if old.State != nil && v.State != nil && old.State.BootID == v.State.BootID && v.State.Seq <= old.State.Seq {
				v.State = old.State
			}
			if v.Kind != "temperature" && old.State != nil && v.State != nil && old.State.BootID != v.State.BootID {
				if rec, active := e.active[f.ID]; active && rec.Cycle.Status != "BLOCKED" && rec.Cycle.Status != "NEEDS_INPUT" {
					if err := e.transition(r.Context(), rec, "BLOCKED", "DEVICE_RESTARTED", ""); err != nil {
						writeError(w, err)
						return
					}
				}
			}
			e.devices[v.DeviceID] = v
			e.deviceReceived[v.DeviceID] = time.Now()
			if v.Online && v.State != nil && v.State.CommandID != "" && (v.State.ExecutionStatus == "ACCEPTED" || v.State.ExecutionStatus == "COMPLETED") {
				_, _ = e.store.db.Exec(r.Context(), "UPDATE core.commands SET status=$2 WHERE id=$1 AND device_id=$3 AND doc->>'targetBootId'=$4 AND status IN ('PENDING','ACCEPTED')", v.State.CommandID, v.State.ExecutionStatus, v.DeviceID, v.State.BootID)
			}
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /internal/v1/scan-results", func(w http.ResponseWriter, r *http.Request) {
		var input protocol.ScanResult
		if platform.Read(r, &input) != nil || input.SessionID == "" {
			platform.Error(w, 400, "INVALID_SCAN", "扫码结果格式错误")
			return
		}
		valid := map[string]bool{"DECODED": true, "SCAN_TIMEOUT": true, "CAMERA_OFFLINE": true, "AMBIGUOUS": true}
		if !valid[input.Outcome] || len(input.RawCode) > 2048 {
			platform.Error(w, 400, "INVALID_SCAN", "扫码结果无效")
			return
		}
		e.idempotent(w, r, input, func(tx pgx.Tx) (string, afterCommit, error) { return e.scan(r.Context(), tx, input, false, "", "") })
	})
	m.HandleFunc("POST /api/v1/furnaces/{furnaceId}/manual-scans", func(w http.ResponseWriter, r *http.Request) {
		var input protocol.ManualScan
		if platform.Read(r, &input) != nil || strings.TrimSpace(input.Reason) == "" || len(input.RawCode) > 2048 || len(input.Reason) > 500 {
			platform.Error(w, 400, "INVALID_INPUT", "请填写料筐号、操作人和补录原因")
			return
		}
		input.Operator = requestUser(r).Username
		e.idempotent(w, r, input, func(tx pgx.Tx) (string, afterCommit, error) {
			f := r.PathValue("furnaceId")
			rec, ok := e.active[f]
			if !ok || rec.Cycle.ID != input.CycleID || rec.Cycle.Status != "NEEDS_INPUT" {
				return "", nil, fail("CYCLE_NOT_WAITING", "当前周期不在等待补录状态")
			}
			return e.scan(r.Context(), tx, protocol.ScanResult{CycleID: input.CycleID, FurnaceID: f, Outcome: "DECODED", RawCode: input.RawCode}, true, input.Operator, input.Reason)
		})
	})
	m.HandleFunc("POST /api/v1/furnaces/{furnaceId}/resets", e.resetHandler)
	m.HandleFunc("GET /api/v1/cycles", e.listCycles)
	m.HandleFunc("GET /api/v1/cycles/{cycleId}", e.getCycle)
	m.HandleFunc("GET /api/v1/alarms", e.listAlarms)
	m.HandleFunc("POST /api/v1/alarms/{alarmId}/acknowledgements", e.acknowledge)
	m.HandleFunc("GET /api/v1/notifications", e.personalNotifications)
	m.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		lastSessionCheck := time.Time{}
		for {
			if time.Since(lastSessionCheck) > 5*time.Second {
				if _, err := e.session(r.Context(), r); err != nil {
					return
				}
				lastSessionCheck = time.Now()
			}
			var count int
			if err := e.store.db.QueryRow(r.Context(), "SELECT count(*) FROM core.alarms WHERE active").Scan(&count); err != nil {
				return
			}
			payload := protocol.StreamSnapshot{At: time.Now().UTC(), Furnaces: e.snapshots(), ActiveAlarmCount: count, CatalogVersion: e.catalogRevision(), RulesVersion: e.rulesRevision()}
			if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", encoded(payload)); err != nil {
				return
			}
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
			}
		}
	})
	u, _ := url.Parse(platform.Env("MEDIA_URL", "http://mediamtx:8888"))
	proxy := httputil.NewSingleHostReverseProxy(u)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		if platform.Env("MEDIA_AUTH_ENABLED", "false") == "true" {
			r.SetBasicAuth("media", platform.Env("SERVICE_TOKEN", ""))
		}
	}
	m.Handle("GET /media/", http.StripPrefix("/media", proxy))
	return m
}
func (e *Engine) resetHandler(w http.ResponseWriter, r *http.Request) {
	var input protocol.OperatorAction
	if platform.Read(r, &input) != nil || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 500 {
		platform.Error(w, 400, "INVALID_INPUT", "请填写操作人和复位原因")
		return
	}
	input.Operator = requestUser(r).Username
	e.idempotent(w, r, input, func(tx pgx.Tx) (string, afterCommit, error) {
		f, ok := e.config(r.PathValue("furnaceId"))
		if !ok {
			return "", nil, fault{404, "NOT_FOUND", "炉子不存在"}
		}
		rec, ok := e.active[f.ID]
		if !ok || rec.Cycle.Status != "BLOCKED" && rec.Cycle.Status != "NEEDS_INPUT" {
			return "", nil, fail("RESET_NOT_ALLOWED", "当前没有需要复位的周期")
		}
		rec = prepareReset(rec, time.Now().UTC())
		if err := saveRecord(r.Context(), tx, rec); err != nil {
			return "", nil, err
		}
		if err := event(r.Context(), tx, rec.Cycle.ID, "MANUAL_RESET", input.Operator+": "+input.Reason); err != nil {
			return "", nil, err
		}
		if _, err := tx.Exec(r.Context(), "INSERT INTO core.audit(id,operator,action,target,reason) VALUES($1,$2,'RESET',$3,$4)", protocol.ID(), input.Operator, rec.Cycle.ID, input.Reason); err != nil {
			return "", nil, err
		}
		return rec.Cycle.ID, func() { e.active[f.ID] = rec }, nil
	})
}
