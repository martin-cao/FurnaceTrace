package device

import (
	"context"
	"encoding/json"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

func Run(ctx context.Context) error {
	id := platform.Env("DEVICE_ID", "f1-door")
	kind := platform.Env("DEVICE_KIND", "door")
	model := New(platform.Env("FURNACE_ID", "f1"), id, kind)
	var mu sync.Mutex
	topic := "iot/v1/devices/" + id
	opts := mqtt.NewClientOptions().AddBroker(platform.Env("MQTT_URL", "tcp://mosquitto:1883")).SetClientID(id + "-" + model.State.BootID).SetAutoReconnect(true).SetConnectRetry(true).SetCleanSession(true).SetConnectTimeout(2 * time.Second)
	opts.SetWill(topic+"/availability", "offline", 1, true)
	var client mqtt.Client
	publish := func() {
		mu.Lock()
		if model.Muted {
			mu.Unlock()
			return
		}
		model.State.Seq++
		model.State.ObservedAt = time.Now().UTC()
		b, _ := json.Marshal(model.State)
		mu.Unlock()
		if client != nil && client.IsConnectionOpen() {
			client.Publish(topic+"/state", 1, true, b)
		}
	}
	opts.OnConnect = func(c mqtt.Client) {
		c.Publish(topic+"/availability", 1, true, "online")
		c.Subscribe(topic+"/command", 1, func(_ mqtt.Client, msg mqtt.Message) {
			// A command delivered as retained is never eligible, even if its TTL is still valid.
			if msg.Retained() {
				slog.Warn("retained command rejected", "deviceId", id)
				return
			}
			var cmd protocol.DeviceCommand
			if json.Unmarshal(msg.Payload(), &cmd) != nil {
				return
			}
			mu.Lock()
			err := model.Command(cmd, time.Now())
			mu.Unlock()
			if err != nil {
				slog.Warn("command rejected", "deviceId", id, "commandId", cmd.CommandID, "reason", err.Error())
			}
			publish()
		})
	}
	client = mqtt.NewClient(opts)
	client.Connect()
	defer client.Disconnect(100)
	go func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		n := 0
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				mu.Lock()
				before := model.State.ExecutionStatus
				model.Tick(now)
				changed := before != model.State.ExecutionStatus
				mu.Unlock()
				n++
				if changed || n%10 == 0 {
					publish()
				}
			}
		}
	}()
	mux := platform.Mux("device-sim", client.IsConnectionOpen)
	mux.HandleFunc("GET /internal/v1/sim/state", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		platform.JSON(w, 200, model.State)
	})
	mux.HandleFunc("PATCH /internal/v1/sim/state", func(w http.ResponseWriter, r *http.Request) {
		var input protocol.SimulationInput
		if platform.Read(r, &input) != nil {
			platform.Error(w, 400, "INVALID_INPUT", "模拟输入格式错误")
			return
		}
		mu.Lock()
		err := model.Inject(input)
		mu.Unlock()
		if err != nil {
			platform.Error(w, 400, "INVALID_INPUT", err.Error())
			return
		}
		publish()
		w.WriteHeader(204)
	})
	return platform.Serve(ctx, "device-sim", mux)
}
