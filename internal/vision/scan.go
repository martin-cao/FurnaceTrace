package vision

import (
	"context"
	"encoding/json"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/multi/qrcode"
	"image"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

type Stability struct {
	last  string
	count int
}

func (s *Stability) Observe(codes []string) string {
	if len(codes) != 1 {
		s.last = ""
		s.count = 0
		return ""
	}
	if codes[0] != s.last {
		s.last = codes[0]
		s.count = 1
		return ""
	}
	s.count++
	if s.count >= 2 {
		return s.last
	}
	return ""
}

type Scanner struct {
	mu          sync.Mutex
	state       protocol.CameraState
	session     *protocol.ScanSession
	started     time.Time
	stable      Stability
	ambiguous   bool
	pending     *protocol.ScanResult
	lastSession string
	continuous  Continuous
	detection   *protocol.BarcodeDetection
}

// Latch a visible code; short decode misses must not repeatedly start admission.
type Continuous struct {
	stable  Stability
	last    string
	missing int
}

func (c *Continuous) Observe(codes []string) string {
	if len(codes) == 0 {
		c.missing++
		if c.missing >= 3 {
			c.last = ""
		}
	} else {
		c.missing = 0
	}
	code := c.stable.Observe(codes)
	if code == "" || code == c.last {
		return ""
	}
	c.last = code
	return code
}

func (s *Scanner) result(outcome, raw string, now time.Time) {
	v := s.session
	s.pending = &protocol.ScanResult{SessionID: v.SessionID, CycleID: v.CycleID, FurnaceID: v.FurnaceID, CameraID: v.CameraID, Outcome: outcome, RawCode: raw, ObservedAt: now}
	s.lastSession = v.SessionID
	s.session = nil
	s.state.SessionID = ""
}
func (s *Scanner) frame(img *image.Gray, received time.Time) {
	s.mu.Lock()
	s.state.LastFrameAt = &received
	s.state.Online = true
	s.mu.Unlock()
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return
	}
	results, _ := qrcode.NewQRCodeMultiReader().DecodeMultiple(bitmap, nil)
	codes := make([]string, 0, len(results))
	for _, r := range results {
		codes = append(codes, r.GetText())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		if code := s.continuous.Observe(codes); code != "" {
			s.detection = &protocol.BarcodeDetection{DetectionID: protocol.ID(), FurnaceID: platform.Env("FURNACE_ID", "f1"), CameraID: s.state.CameraID, RawCode: code, ObservedAt: received}
		}
		return
	}
	if !received.After(s.started) {
		return
	}
	if len(codes) > 1 {
		s.ambiguous = true
	}
	if code := s.stable.Observe(codes); code != "" {
		s.result("DECODED", code, received)
	}
}
func (s *Scanner) stream(ctx context.Context) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp", "-timeout", "3000000", "-i", platform.MediaRTSPURL("rtsp://mediamtx:8554/f1"), "-an", "-vf", "fps=5,scale=1280:720", "-pix_fmt", "gray", "-f", "rawvideo", "pipe:1")
		pipe, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err == nil {
			for {
				pixels := make([]byte, 1280*720)
				if _, err = io.ReadFull(pipe, pixels); err != nil {
					break
				}
				s.frame(&image.Gray{Pix: pixels, Stride: 1280, Rect: image.Rect(0, 0, 1280, 720)}, time.Now().UTC())
			}
			_ = cmd.Wait()
		}
		s.mu.Lock()
		s.state.Online = false
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func Run(ctx context.Context) error {
	s := &Scanner{state: protocol.CameraState{CameraID: platform.Env("CAMERA_ID", "f1-camera")}}
	go s.stream(ctx)
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.mu.Lock()
				s.state.Online = s.state.LastFrameAt != nil && now.Sub(*s.state.LastFrameAt) < 3*time.Second
				if s.session != nil && now.After(s.session.ExpiresAt) {
					outcome := "SCAN_TIMEOUT"
					if !s.state.Online {
						outcome = "CAMERA_OFFLINE"
					} else if s.ambiguous {
						outcome = "AMBIGUOUS"
					}
					s.result(outcome, "", now.UTC())
				}
				pending := s.pending
				detection := s.detection
				s.mu.Unlock()
				if detection != nil {
					err := platform.Request(ctx, "POST", platform.Env("CORE_URL", "http://core:8080")+"/internal/v1/barcode-detections", detection.DetectionID, detection, nil)
					if err == nil || time.Since(detection.ObservedAt) > 10*time.Second {
						s.mu.Lock()
						if s.detection == detection {
							s.detection = nil
						}
						s.mu.Unlock()
					}
				}
				if pending != nil {
					err := platform.Request(ctx, "POST", platform.Env("CORE_URL", "http://core:8080")+"/internal/v1/scan-results", pending.SessionID, pending, nil)
					if err == nil {
						s.mu.Lock()
						if s.pending == pending {
							s.pending = nil
						}
						s.mu.Unlock()
					}
				}
			}
		}
	}()
	mux := platform.Mux("vision", nil)
	mux.HandleFunc("GET /internal/v1/camera", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		platform.JSON(w, 200, s.state)
	})
	mux.HandleFunc("POST /internal/v1/scan-sessions", func(w http.ResponseWriter, r *http.Request) {
		var v protocol.ScanSession
		if platform.Read(r, &v) != nil || v.SessionID == "" || v.CameraID != s.state.CameraID || v.FurnaceID != platform.Env("FURNACE_ID", "f1") {
			platform.Error(w, 400, "INVALID_SESSION", "扫码会话格式错误")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.lastSession == v.SessionID {
			platform.Accepted(w, v.SessionID)
			return
		}
		if s.session != nil {
			old, _ := json.Marshal(s.session)
			next, _ := json.Marshal(v)
			if string(old) == string(next) {
				platform.Accepted(w, v.SessionID)
				return
			}
			platform.Error(w, 409, "SCANNER_BUSY", "已有扫码会话")
			return
		}
		if !v.ExpiresAt.After(time.Now()) {
			platform.Error(w, 400, "SESSION_EXPIRED", "扫码会话已过期")
			return
		}
		s.session = &v
		s.started = time.Now()
		s.stable = Stability{}
		s.ambiguous = false
		s.state.SessionID = v.SessionID
		platform.Accepted(w, v.SessionID)
	})
	return platform.Serve(ctx, "vision", mux)
}
