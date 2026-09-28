package core

import (
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"
)

func (e *Engine) detectionRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /internal/v1/barcode-detections", func(w http.ResponseWriter, req *http.Request) {
		var input protocol.BarcodeDetection
		if platform.Read(req, &input) != nil || input.DetectionID == "" || strings.TrimSpace(input.RawCode) == "" || len(input.RawCode) > 2048 || input.ObservedAt.IsZero() {
			platform.Error(w, 400, "INVALID_DETECTION", "二维码识别结果无效")
			return
		}
		f, ok := e.config(input.FurnaceID)
		if !ok || input.CameraID != f.Camera {
			platform.Error(w, 400, "CAMERA_MISMATCH", "摄像头与炉子不匹配")
			return
		}
		e.idempotent(w, req, input, func(tx pgx.Tx) (string, afterCommit, error) {
			ctx := req.Context()
			now := time.Now().UTC()
			if now.Sub(input.ObservedAt) > 10*time.Second || input.ObservedAt.After(now.Add(time.Second)) {
				return input.DetectionID, nil, nil
			}
			if active, ok := e.active[f.ID]; ok {
				return active.Cycle.ID, nil, event(ctx, tx, active.Cycle.ID, "SCAN_IGNORED", "流程执行期间忽略持续扫码结果")
			}
			door := e.device(f.Door)
			if !e.ready(f) || door.State == nil || !door.State.Bool("doorClosed") {
				return "", nil, fail("DEVICE_UNAVAILABLE", "等待视频、设备在线且炉门关闭后处理扫码")
			}
			r := record{Cycle: protocol.Cycle{ID: protocol.ID(), FurnaceID: f.ID, RawCode: input.RawCode, Status: "VALIDATING", CreatedAt: now, UpdatedAt: now}, SessionID: input.DetectionID, NextValidation: now}
			id, err := basket(input.RawCode)
			if err != nil {
				r.Cycle.Status = "NEEDS_INPUT"
				r.Cycle.Reason = "INVALID_BARCODE"
				r.AlarmReason = "INVALID_BARCODE"
			} else {
				r.Cycle.BasketNo = id
			}
			if err := saveRecord(ctx, tx, r); err != nil {
				return "", nil, err
			}
			if err := event(ctx, tx, r.Cycle.ID, r.Cycle.Status, "持续扫码识别到二维码，视为料筐到位"); err != nil {
				return "", nil, err
			}
			revision, err := bumpCatalog(ctx, tx)
			if err != nil {
				return "", nil, err
			}
			return r.Cycle.ID, func() { e.active[f.ID] = r; e.catalogVersion = revision }, nil
		})
	})
}
