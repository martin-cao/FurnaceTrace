package device

import (
	"errors"
	"furnace.local/iot/internal/protocol"
	"math"
	"time"
)

type Model struct {
	State          protocol.DeviceState
	Jammed, Muted  bool
	ExecutionCount int
	seen           map[string]protocol.DeviceCommand
	pending        *protocol.DeviceCommand
	due            time.Time
}

func New(furnace, id, kind string) *Model {
	v := map[string]any{"executionCount": 0}
	switch kind {
	case "sensor":
		v["present"] = false
		v["inFurnace"] = false
	case "door":
		v["doorOpen"] = false
		v["doorClosed"] = true
	case "lamp":
		v["lampOn"] = false
	case "temperature":
		v["temperatureC"] = 25.0
	}
	return &Model{State: protocol.DeviceState{DeviceID: id, FurnaceID: furnace, Kind: kind, BootID: protocol.ID(), Seq: 1, ObservedAt: time.Now().UTC(), Values: v, ExecutionStatus: "IDLE"}, seen: map[string]protocol.DeviceCommand{}}
}
func (m *Model) Command(c protocol.DeviceCommand, now time.Time) error {
	if c.DeviceID != m.State.DeviceID || c.FurnaceID != m.State.FurnaceID || c.TargetBootID != m.State.BootID {
		return errors.New("command target mismatch")
	}
	if c.CommandID == "" || !c.ExpiresAt.After(now) || c.IssuedAt.After(now.Add(time.Second)) {
		return errors.New("expired or invalid command")
	}
	if old, ok := m.seen[c.CommandID]; ok {
		if !sameCommand(old, c) {
			return errors.New("command id reused with different content")
		}
		return nil
	}
	if m.pending != nil {
		return errors.New("device busy")
	}
	valid := c.Action == "RESET" || m.State.Kind == "door" && (c.Action == "OPEN" || c.Action == "CLOSE") || m.State.Kind == "lamp" && (c.Action == "ALARM_ON" || c.Action == "ALARM_OFF") || m.State.Kind == "temperature" && c.Action == "SET_TEMPERATURE"
	if !valid || m.State.Kind == "sensor" || m.State.Kind == "temperature" && c.Action != "SET_TEMPERATURE" {
		return errors.New("action not supported by device")
	}
	if c.Action == "SET_TEMPERATURE" && (c.TemperatureC == nil || math.IsNaN(*c.TemperatureC) || math.IsInf(*c.TemperatureC, 0)) {
		return errors.New("temperature must be finite")
	}
	m.seen[c.CommandID] = c
	m.State.CommandID = c.CommandID
	m.State.ExecutionStatus = "ACCEPTED"
	m.pending = &c
	m.due = now.Add(500 * time.Millisecond)
	m.ExecutionCount++
	m.State.Values["executionCount"] = m.ExecutionCount
	return nil
}
func (m *Model) Tick(now time.Time) {
	if m.pending == nil {
		return
	}
	if !m.pending.ExpiresAt.After(now) {
		m.State.ExecutionStatus = "REJECTED"
		m.pending = nil
		return
	}
	if now.Before(m.due) || m.Jammed {
		return
	}
	switch m.pending.Action {
	case "SET_TEMPERATURE":
		m.State.Values["temperatureC"] = *m.pending.TemperatureC
	case "OPEN":
		m.State.Values["doorOpen"] = true
		m.State.Values["doorClosed"] = false
	case "CLOSE":
		m.State.Values["doorOpen"] = false
		m.State.Values["doorClosed"] = true
	case "ALARM_ON":
		m.State.Values["lampOn"] = true
	case "ALARM_OFF", "RESET":
		if m.State.Kind == "lamp" {
			m.State.Values["lampOn"] = false
		}
	}
	m.State.ExecutionStatus = "COMPLETED"
	m.pending = nil
}
func (m *Model) Inject(in protocol.SimulationInput) error {
	if (in.Present != nil || in.InFurnace != nil) && m.State.Kind != "sensor" {
		return errors.New("not a position sensor")
	}
	if in.Present != nil {
		m.State.Values["present"] = *in.Present
	}
	if in.InFurnace != nil {
		m.State.Values["inFurnace"] = *in.InFurnace
	}
	if in.Jammed != nil {
		m.Jammed = *in.Jammed
	}
	if in.Muted != nil {
		m.Muted = *in.Muted
	}
	if in.ForceClosed != nil && *in.ForceClosed {
		if m.State.Kind != "door" {
			return errors.New("not a door")
		}
		m.pending = nil
		m.State.Values["doorOpen"] = false
		m.State.Values["doorClosed"] = true
		m.State.ExecutionStatus = "IDLE"
	}
	return nil
}

// Compare decoded payload values, not the optional temperature pointers.
func sameCommand(a, b protocol.DeviceCommand) bool {
	av, bv := a.TemperatureC, b.TemperatureC
	a.TemperatureC, b.TemperatureC = nil, nil
	return a == b && (av == nil && bv == nil || av != nil && bv != nil && *av == *bv)
}
