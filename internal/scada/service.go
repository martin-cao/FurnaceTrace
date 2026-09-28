package scada

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type item struct {
	view       protocol.DeviceView
	lastChange time.Time
}
type Adapter struct {
	mu     sync.Mutex
	items  map[string]*item
	order  []string
	fuxa   string
	client *http.Client
}

func (a *Adapter) fuxaRequest(ctx context.Context, method, path string, body, output any) error {
	var data io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		data = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, a.fuxa+path, data)
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("FUXA_API_KEY"); key != "" {
		r.Header.Set("x-api-key", key)
	}
	res, err := a.client.Do(r)
	if err != nil {
		return errors.New("FUXA unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("FUXA rejected request")
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(output)
	}
	return nil
}
func (a *Adapter) snapshot() []protocol.DeviceView {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]protocol.DeviceView, 0, len(a.order))
	for _, id := range a.order {
		out = append(out, a.items[id].view)
	}
	return out
}
func (a *Adapter) poll(ctx context.Context) {
	ids := make([]string, 0, len(a.order))
	for _, id := range a.order {
		ids = append(ids, id+"-state")
	}
	b, _ := json.Marshal(ids)
	var values []struct {
		ID    string          `json:"id"`
		Value json.RawMessage `json:"value"`
	}
	err := a.fuxaRequest(ctx, "GET", "/api/getTagValue?ids="+url.QueryEscape(string(b)), nil, &values)
	now := time.Now().UTC()
	a.mu.Lock()
	for _, v := range values {
		var raw string
		if json.Unmarshal(v.Value, &raw) != nil {
			continue
		}
		var state protocol.DeviceState
		if json.Unmarshal([]byte(raw), &state) != nil {
			continue
		}
		i, ok := a.items[state.DeviceID]
		if !ok || v.ID != state.DeviceID+"-state" || i.view.FurnaceID != state.FurnaceID || i.view.Kind != state.Kind || state.BootID == "" || state.Seq < 1 || state.ObservedAt.After(now.Add(time.Second)) {
			continue
		}
		old := i.view.State
		if old == nil || old.BootID != state.BootID || state.Seq > old.Seq {
			i.view.State = &state
			i.lastChange = now
		}
	}
	for _, i := range a.items {
		i.view.Online = err == nil && i.view.State != nil && now.Sub(i.lastChange) < 3*time.Second && now.Sub(i.view.State.ObservedAt) < 3*time.Second
		i.view.Reason = ""
		if !i.view.Online {
			i.view.Reason = "DEVICE_OFFLINE"
			if err != nil {
				i.view.Reason = "SCADA_OFFLINE"
			}
		}
	}
	a.mu.Unlock()
	// Retrying the latest snapshot is safe: core deduplicates source sequence numbers.
	_ = platform.Request(ctx, "POST", platform.Env("CORE_URL", "http://core:8080")+"/internal/v1/device-states", "", a.snapshot(), nil)
}
func Run(ctx context.Context) error {
	cfg, err := platform.Config()
	if err != nil {
		return err
	}
	a := &Adapter{items: map[string]*item{}, fuxa: platform.Env("FUXA_URL", "http://fuxa:1881"), client: &http.Client{Timeout: 1500 * time.Millisecond}}
	for _, f := range cfg {
		for kind, id := range f.DeviceIDs() {
			a.items[id] = &item{view: protocol.DeviceView{DeviceID: id, FurnaceID: f.ID, Kind: kind, Reason: "STARTING"}}
			a.order = append(a.order, id)
		}
	}
	go func() {
		// FUXA v1.3.4 permits 1000 API requests per 5 minutes by default.
		// Two batched reads per second leave capacity for command writes.
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			a.poll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	mux := platform.Mux("scada-adapter", nil)
	mux.HandleFunc("GET /internal/v1/scada/devices", func(w http.ResponseWriter, r *http.Request) { platform.JSON(w, 200, a.snapshot()) })
	mux.HandleFunc("POST /internal/v1/scada/commands", func(w http.ResponseWriter, r *http.Request) {
		var c protocol.DeviceCommand
		if platform.Read(r, &c) != nil || c.CommandID == "" || !c.ExpiresAt.After(time.Now()) {
			platform.Error(w, 400, "INVALID_COMMAND", "命令无效或已经过期")
			return
		}
		a.mu.Lock()
		i := a.items[c.DeviceID]
		valid := i != nil && i.view.Online && i.view.FurnaceID == c.FurnaceID && i.view.State != nil && i.view.State.BootID == c.TargetBootID
		a.mu.Unlock()
		if !valid {
			platform.Error(w, 409, "DEVICE_UNAVAILABLE", "设备离线或启动编号不匹配")
			return
		}
		data, _ := json.Marshal(c)
		if a.fuxaRequest(r.Context(), "POST", "/api/setTagValue", map[string]any{"tags": []any{map[string]any{"id": c.DeviceID + "-command", "value": string(data)}}}, nil) != nil {
			platform.Error(w, 503, "SCADA_UNAVAILABLE", "SCADA 命令写入失败")
			return
		}
		platform.Accepted(w, c.CommandID)
	})
	return platform.Serve(ctx, "scada-adapter", mux)
}
