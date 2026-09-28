// Package protocol contains the wire types specified in api/openapi.yaml.
package protocol

import (
	"crypto/rand"
	"fmt"
	"time"
)

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type DeviceState struct {
	DeviceID        string         `json:"deviceId"`
	FurnaceID       string         `json:"furnaceId"`
	Kind            string         `json:"kind"`
	BootID          string         `json:"bootId"`
	Seq             int64          `json:"seq"`
	ObservedAt      time.Time      `json:"observedAt"`
	Values          map[string]any `json:"values"`
	CommandID       string         `json:"commandId,omitempty"`
	ExecutionStatus string         `json:"executionStatus,omitempty"`
}

func (s DeviceState) Bool(k string) bool { v, _ := s.Values[k].(bool); return v }

type DeviceView struct {
	DeviceID  string       `json:"deviceId"`
	FurnaceID string       `json:"furnaceId"`
	Kind      string       `json:"kind"`
	Online    bool         `json:"online"`
	Reason    string       `json:"reason"`
	State     *DeviceState `json:"state"`
}

type DeviceCommand struct {
	TemperatureC *float64  `json:"temperatureC,omitempty"`
	CommandID    string    `json:"commandId"`
	CycleID      string    `json:"cycleId"`
	DeviceID     string    `json:"deviceId"`
	FurnaceID    string    `json:"furnaceId"`
	TargetBootID string    `json:"targetBootId"`
	Action       string    `json:"action"`
	IssuedAt     time.Time `json:"issuedAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}
type CommandRecord struct {
	Command  DeviceCommand `json:"command"`
	Status   string        `json:"status"`
	Attempts int           `json:"attempts"`
}
type Cycle struct {
	MES         *MESResult `json:"mes,omitempty"`
	ID          string     `json:"id"`
	FurnaceID   string     `json:"furnaceId"`
	BasketNo    string     `json:"basketNo"`
	RawCode     string     `json:"rawCode"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	CompletedAt *time.Time `json:"completedAt"`
}
type CycleEvent struct {
	ID      string    `json:"id"`
	CycleID string    `json:"cycleId"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}
type CycleDetail struct {
	Cycle    Cycle           `json:"cycle"`
	Events   []CycleEvent    `json:"events"`
	Commands []CommandRecord `json:"commands"`
}
type CameraState struct {
	CameraID    string     `json:"cameraId"`
	Online      bool       `json:"online"`
	SessionID   string     `json:"sessionId"`
	LastFrameAt *time.Time `json:"lastFrameAt"`
}
type Furnace struct {
	FurnaceID  string       `json:"furnaceId"`
	Name       string       `json:"name"`
	Ready      bool         `json:"ready"`
	Phase      string       `json:"phase"`
	Cycle      *Cycle       `json:"cycle"`
	Devices    []DeviceView `json:"devices"`
	Camera     CameraState  `json:"camera"`
	PreviewURL string       `json:"previewUrl"`
}
type ScanSession struct {
	SessionID string    `json:"sessionId"`
	CycleID   string    `json:"cycleId"`
	FurnaceID string    `json:"furnaceId"`
	CameraID  string    `json:"cameraId"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type BarcodeDetection struct {
	DetectionID string    `json:"detectionId"`
	FurnaceID   string    `json:"furnaceId"`
	CameraID    string    `json:"cameraId"`
	RawCode     string    `json:"rawCode"`
	ObservedAt  time.Time `json:"observedAt"`
}
type ScanResult struct {
	SessionID  string    `json:"sessionId"`
	CycleID    string    `json:"cycleId"`
	FurnaceID  string    `json:"furnaceId"`
	CameraID   string    `json:"cameraId"`
	Outcome    string    `json:"outcome"`
	RawCode    string    `json:"rawCode"`
	ObservedAt time.Time `json:"observedAt"`
}
type ManualScan struct {
	CycleID  string `json:"cycleId"`
	RawCode  string `json:"rawCode"`
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
}
type OperatorAction struct {
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
}
type Alarm struct {
	Category       string     `json:"category,omitempty"`
	RuleID         string     `json:"ruleId,omitempty"`
	RuleVersion    int64      `json:"ruleVersion,omitempty"`
	Resolution     string     `json:"resolution,omitempty"`
	Lamp           *bool      `json:"lamp,omitempty"`
	ID             string     `json:"id"`
	FurnaceID      string     `json:"furnaceId"`
	CycleID        string     `json:"cycleId"`
	Code           string     `json:"code"`
	Message        string     `json:"message"`
	RaisedAt       time.Time  `json:"raisedAt"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt"`
	RecoveredAt    *time.Time `json:"recoveredAt"`
	Operator       string     `json:"operator"`
}
type NotificationRequest struct {
	ID        string    `json:"id"`
	AlarmID   string    `json:"alarmId"`
	FurnaceID string    `json:"furnaceId"`
	Kind      string    `json:"kind"`
	Category  string    `json:"category,omitempty"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}
type Notification struct {
	NotificationRequest
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"lastError"`
	SentAt        *time.Time `json:"sentAt"`
	NextAttemptAt time.Time  `json:"nextAttemptAt"`
}
type MESResult struct {
	Reason   string             `json:"reason"`
	Snapshot *AdmissionSnapshot `json:"snapshot,omitempty"`
	BasketNo string             `json:"basketNo"`
	Result   int                `json:"result"`
	Message  string             `json:"message"`
}
type Pagination struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
	Total    int `json:"total"`
}
type Page[T any] struct {
	Items      []T        `json:"items"`
	Pagination Pagination `json:"pagination"`
}
type StreamSnapshot struct {
	RulesVersion     int64     `json:"rulesVersion"`
	CatalogVersion   int64     `json:"catalogVersion"`
	At               time.Time `json:"at"`
	Furnaces         []Furnace `json:"furnaces"`
	ActiveAlarmCount int       `json:"activeAlarmCount"`
}
type SimulationInput struct {
	Present     *bool `json:"present,omitempty"`
	InFurnace   *bool `json:"inFurnace,omitempty"`
	Jammed      *bool `json:"jammed,omitempty"`
	Muted       *bool `json:"muted,omitempty"`
	ForceClosed *bool `json:"forceClosed,omitempty"`
}
type CameraScene struct {
	Codes  []string `json:"codes"`
	Paused bool     `json:"paused"`
}
