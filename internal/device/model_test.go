package device

import (
	"testing"
	"time"

	"furnace.local/iot/internal/protocol"
)

func TestCommandRetriesNeverRepeatPhysicalAction(t *testing.T) {
	now := time.Now()
	m := New("f1", "door", "door")
	c := protocol.DeviceCommand{CommandID: "one", DeviceID: "door", FurnaceID: "f1", TargetBootID: m.State.BootID, Action: "OPEN", IssuedAt: now, ExpiresAt: now.Add(time.Second)}
	if err := m.Command(c, now); err != nil {
		t.Fatal(err)
	}
	if err := m.Command(c, now); err != nil {
		t.Fatal(err)
	}
	m.Tick(now.Add(600 * time.Millisecond))
	if !m.State.Bool("doorOpen") || m.ExecutionCount != 1 {
		t.Fatalf("retry duplicated/lost action: %+v", m)
	}
	if err := m.Command(c, now.Add(700*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if m.ExecutionCount != 1 {
		t.Fatal("completed command executed twice")
	}
}

func TestExpiredAndPreviousBootCommandsAreRejected(t *testing.T) {
	now := time.Now()
	m := New("f1", "door", "door")
	c := protocol.DeviceCommand{CommandID: "one", DeviceID: "door", FurnaceID: "f1", TargetBootID: m.State.BootID, Action: "OPEN", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(-time.Second)}
	if m.Command(c, now) == nil {
		t.Fatal("expired command accepted")
	}
	c.ExpiresAt = now.Add(time.Second)
	c.TargetBootID = "old-boot"
	if m.Command(c, now) == nil {
		t.Fatal("old boot command accepted")
	}
	if m.ExecutionCount != 0 {
		t.Fatal("invalid command moved actuator")
	}
}

func TestCommandsCannotCrossDeviceOrFurnace(t *testing.T) {
	now := time.Now()
	m := New("f2", "door-2", "door")
	c := protocol.DeviceCommand{CommandID: "one", DeviceID: "door-1", FurnaceID: "f1", TargetBootID: m.State.BootID, Action: "OPEN", IssuedAt: now, ExpiresAt: now.Add(time.Second)}
	if m.Command(c, now) == nil {
		t.Fatal("cross furnace command accepted")
	}
}

func TestJammedDoorDoesNotReportCompletion(t *testing.T) {
	now := time.Now()
	m := New("f1", "door", "door")
	m.Jammed = true
	c := protocol.DeviceCommand{CommandID: "one", DeviceID: "door", FurnaceID: "f1", TargetBootID: m.State.BootID, Action: "OPEN", IssuedAt: now, ExpiresAt: now.Add(time.Second)}
	if err := m.Command(c, now); err != nil {
		t.Fatal(err)
	}
	m.Tick(now.Add(time.Second))
	if m.State.ExecutionStatus == "COMPLETED" || m.State.Bool("doorOpen") {
		t.Fatal("jammed actuator fabricated completion")
	}
}
