package protocol

import (
	"encoding/json"
	"time"
)

type RuleCondition struct {
	Type     string          `json:"type"`
	Field    string          `json:"field,omitempty"`
	Operator string          `json:"operator,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Logic    string          `json:"logic,omitempty"`
	Children []RuleCondition `json:"children,omitempty"`
}
type RuleChannels struct {
	Telegram bool `json:"telegram"`
	Lamp     bool `json:"lamp"`
}
type AlarmRuleInput struct {
	Category       string         `json:"category"`
	Name           string         `json:"name"`
	FurnaceID      string         `json:"furnaceId"`
	Enabled        *bool          `json:"enabled"`
	Condition      RuleCondition  `json:"condition"`
	ClearCondition *RuleCondition `json:"clearCondition,omitempty"`
	Channels       *RuleChannels  `json:"channels"`
	RepeatSeconds  int            `json:"repeatSeconds"`
	Recoveries     bool           `json:"recoveries"`
}
type AlarmRuleUpdate struct {
	AlarmRuleInput
	Version int64 `json:"version"`
}
type AlarmRule struct {
	AlarmRuleInput
	ID             string     `json:"id"`
	Version        int64      `json:"version"`
	Active         bool       `json:"active"`
	NextReminderAt *time.Time `json:"nextReminderAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}
