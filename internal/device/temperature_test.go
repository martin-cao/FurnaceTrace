package device

import (
	"encoding/json"
	"furnace.local/iot/internal/protocol"
	"testing"
	"time"
)

func TestTemperatureCommandUsesFeedbackAndDeduplicates(t *testing.T) {
	m := New("f1", "f1-temperature", "temperature")
	now := time.Now().UTC().Round(0)
	value := 650.5
	c := protocol.DeviceCommand{CommandID: protocol.ID(), DeviceID: m.State.DeviceID, FurnaceID: "f1", TargetBootID: m.State.BootID, Action: "SET_TEMPERATURE", TemperatureC: &value, IssuedAt: now, ExpiresAt: now.Add(10 * time.Second)}
	if err := m.Command(c, now); err != nil {
		t.Fatal(err)
	}
	if m.State.Values["temperatureC"] != 25.0 {
		t.Fatal("accepted is not completed")
	}
	m.Tick(now.Add(time.Second))
	if m.State.Values["temperatureC"] != value || m.State.ExecutionStatus != "COMPLETED" {
		t.Fatal("missing temperature feedback")
	}
	b, _ := json.Marshal(c)
	var duplicate protocol.DeviceCommand
	_ = json.Unmarshal(b, &duplicate)
	if err := m.Command(duplicate, now); err != nil || m.ExecutionCount != 1 {
		t.Fatalf("duplicate executed or rejected: %v", err)
	}
	other := 900.0
	duplicate.TemperatureC = &other
	if m.Command(duplicate, now) == nil {
		t.Fatal("same id accepted different temperature")
	}
	c.CommandID = protocol.ID()
	c.TargetBootID = protocol.ID()
	if m.Command(c, now) == nil {
		t.Fatal("old boot accepted")
	}
	c.TargetBootID = m.State.BootID
	c.TemperatureC = nil
	if m.Command(c, now) == nil {
		t.Fatal("missing temperature accepted")
	}
}
