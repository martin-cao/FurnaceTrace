package core

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func cameraCipher() (cipher.AEAD, error) {
	key, err := hex.DecodeString(os.Getenv("CAMERA_ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("camera encryption key unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func sealCamera(id, value string) ([]byte, error) {
	c, err := cameraCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.Seal(nonce, nonce, []byte(value), []byte(id)), nil
}
func openCamera(id string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	c, err := cameraCipher()
	if err != nil {
		return "", err
	}
	if len(data) < c.NonceSize() {
		return "", errors.New("invalid encrypted camera source")
	}
	b, err := c.Open(nil, data[:c.NonceSize()], data[c.NonceSize():], []byte(id))
	return string(b), err
}
func validCameraURL(raw, furnace string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || (u.Scheme != "rtsp" && u.Scheme != "rtsps") || u.Hostname() == "" || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\t") {
		return false
	}
	if (u.Hostname() == "mediamtx" || u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost") && u.Port() == "8554" && strings.Trim(u.Path, "/") == furnace {
		return false
	}
	return true
}
func (e *Engine) loadCameraSettings(ctx context.Context) error {
	e.cameraConfigs = map[string]protocol.CameraConfig{}
	e.cameraSwitchUntil = map[string]time.Time{}
	for _, f := range e.cfg {
		if _, err := e.store.db.Exec(ctx, "INSERT INTO core.camera_settings(furnace_id) VALUES($1) ON CONFLICT DO NOTHING", f.ID); err != nil {
			return err
		}
		e.cameraConfigs[f.ID] = protocol.CameraConfig{FurnaceID: f.ID, Mode: "simulated", Message: "正在读取视频配置"}
	}
	return nil
}
func (e *Engine) cameraConfig(ctx context.Context, id string) (protocol.CameraConfig, []byte, error) {
	out := protocol.CameraConfig{FurnaceID: id}
	var encrypted []byte
	err := e.store.db.QueryRow(ctx, "SELECT mode,encrypted_url,revision FROM core.camera_settings WHERE furnace_id=$1", id).Scan(&out.Mode, &encrypted, &out.Revision)
	if err != nil {
		return out, nil, err
	}
	out.HasSavedRTSP = len(encrypted) > 0
	e.mu.Lock()
	status := e.cameraConfigs[id]
	out.Applied = status.Revision == out.Revision && status.Applied
	out.Message = status.Message
	out.Online = e.cameras[id].Online && out.Applied && !time.Now().Before(e.cameraSwitchUntil[id])
	e.mu.Unlock()
	// This response is administrator-only and no-store; never log the decoded URL.
	out.RTSPURL, err = openCamera(id, encrypted)
	if err != nil {
		return out, nil, err
	}
	if out.Mode == "simulated" {
		out.SourceHost = "模拟摄像头"
	} else if u, parseErr := url.Parse(out.RTSPURL); parseErr == nil {
		out.SourceHost = u.Host
	}
	return out, encrypted, nil
}
func (e *Engine) cameraSettingsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/furnaces/{furnaceId}/camera-config", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("furnaceId")
		if _, ok := e.config(id); !ok {
			platform.Error(w, 404, "CAMERA_NOT_FOUND", "炉子不存在")
			return
		}
		out, _, err := e.cameraConfig(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		platform.JSON(w, 200, out)
	})
	m.HandleFunc("PUT /api/v1/furnaces/{furnaceId}/camera-config", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("furnaceId")
		if _, ok := e.config(id); !ok {
			platform.Error(w, 404, "CAMERA_NOT_FOUND", "炉子不存在")
			return
		}
		var in protocol.CameraConfigUpdate
		if platform.Read(r, &in) != nil || (in.Mode != "simulated" && in.Mode != "rtsp") || in.RTSPURL != nil && !validCameraURL(*in.RTSPURL, id) {
			platform.Error(w, 400, "INVALID_RTSP", "请输入有效 RTSP / RTSPS 地址，不能指向本炉自己的输出流")
			return
		}
		e.cameraMu.Lock()
		defer e.cameraMu.Unlock()
		old, encrypted, err := e.cameraConfig(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		same := old.Mode == in.Mode
		if in.RTSPURL != nil {
			previous, _ := openCamera(id, encrypted)
			same = same && previous == *in.RTSPURL
			encrypted, err = sealCamera(id, *in.RTSPURL)
			if err != nil {
				platform.Error(w, 503, "CAMERA_KEY_UNAVAILABLE", "摄像头凭据加密配置不可用，请联系管理员")
				return
			}
		}
		if in.Mode == "rtsp" && len(encrypted) == 0 {
			platform.Error(w, 400, "RTSP_REQUIRED", "首次使用 RTSP 时必须填写视频流地址")
			return
		}
		e.mu.Lock()
		if _, active := e.active[id]; active {
			e.mu.Unlock()
			platform.Error(w, 409, "CYCLE_ACTIVE", "请先完成或复位当前入炉周期，再切换视频源")
			return
		}
		if same {
			e.mu.Unlock()
			platform.JSON(w, 200, old)
			return
		}
		tx, err := e.store.db.Begin(r.Context())
		if err == nil {
			defer tx.Rollback(r.Context())
			_, err = tx.Exec(r.Context(), "UPDATE core.camera_settings SET mode=$2,encrypted_url=$3,revision=revision+1 WHERE furnace_id=$1", id, in.Mode, encrypted)
		}
		if err == nil {
			err = accountAudit(r, tx, "CONFIGURE_CAMERA", id, "视频源模式: "+in.Mode)
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err == nil {
			e.cameraConfigs[id] = protocol.CameraConfig{FurnaceID: id, Mode: in.Mode, Message: "已保存，正在应用视频配置"}
		}
		e.mu.Unlock()
		if err != nil {
			writeError(w, err)
			return
		}
		out, _, err := e.cameraConfig(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		platform.JSON(w, 200, out)
	})
}

// Never return upstream bodies: they may contain camera URLs or credentials.
func mediaConfig(ctx context.Context, method, path string, body, output any) error {
	var input io.Reader
	if body != nil {
		input = bytes.NewReader(encoded(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, platform.Env("MEDIA_API_URL", "http://mediamtx:9997")+path, input)
	if err != nil {
		return errors.New("media unavailable")
	}
	req.SetBasicAuth("controller", os.Getenv("SERVICE_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := platform.Client.Do(req)
	if err != nil {
		return errors.New("media unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("media configuration rejected")
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output)
	}
	return nil
}
func (e *Engine) reconcileCameras(ctx context.Context) {
	e.cameraMu.Lock()
	defer e.cameraMu.Unlock()
	for _, f := range e.cfg {
		out, data, err := e.cameraConfig(ctx, f.ID)
		if err != nil {
			continue
		}
		source := "rtsp://127.0.0.1:8554/" + f.ID + "-sim"
		if out.Mode == "rtsp" {
			source, err = openCamera(f.ID, data)
		}
		var current struct {
			Source    string `json:"source"`
			Transport string `json:"rtspTransport"`
		}
		if err == nil {
			err = mediaConfig(ctx, "GET", "/v3/config/paths/get/"+url.PathEscape(f.ID), nil, &current)
		}
		if err == nil && (current.Source != source || current.Transport != "tcp") {
			e.mu.Lock()
			_, active := e.active[f.ID]
			out.Applied = false
			out.Message = "正在切换视频源"
			e.cameraConfigs[f.ID] = out
			e.mu.Unlock()
			if active {
				continue
			}
			err = mediaConfig(ctx, "PATCH", "/v3/config/paths/patch/"+url.PathEscape(f.ID), map[string]any{"source": source, "rtspTransport": "tcp"}, nil)
			if err == nil {
				e.mu.Lock()
				e.cameraSwitchUntil[f.ID] = time.Now().Add(5 * time.Second)
				e.mu.Unlock()
			}
		}
		out.Applied = err == nil
		out.Message = "配置已应用，视频状态以实时连接为准"
		if err != nil {
			out.Message = "视频配置暂未应用，将自动重试；请检查媒体服务或加密配置"
		}
		e.mu.Lock()
		e.cameraConfigs[f.ID] = out
		e.mu.Unlock()
	}
}
func (e *Engine) cameraSettingsLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		e.reconcileCameras(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
