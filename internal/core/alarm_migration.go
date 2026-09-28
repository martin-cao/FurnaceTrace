package core

import (
	"context"
	"encoding/json"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"time"
)

func ruleLeaf(field, op string, value any) protocol.RuleCondition {
	return protocol.RuleCondition{Type: "condition", Field: field, Operator: op, Value: encoded(value)}
}
func ruleGroup(logic string, children ...protocol.RuleCondition) protocol.RuleCondition {
	return protocol.RuleCondition{Type: "group", Logic: logic, Children: children}
}

// Defaults are inserted once as ordinary editable rules. Deleted defaults are never recreated.
func (e *Engine) seedAlarmRules(ctx context.Context) error {
	tx, err := e.store.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var migrated, hasLegacy bool
	if err = tx.QueryRow(ctx, "SELECT legacy_alarms_migrated FROM core.catalog_revision WHERE id=1 FOR UPDATE").Scan(&migrated); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, "SELECT to_regclass('core.temperature_alarm_settings') IS NOT NULL").Scan(&hasLegacy); err != nil {
		return err
	}
	type seed struct {
		key, category, name string
		condition           protocol.RuleCondition
		clear               *protocol.RuleCondition
		enabled             bool
	}
	ids := map[string]map[string]string{}
	inputs := map[string]protocol.AlarmRuleInput{}
	inserted := false
	for _, f := range e.cfg {
		defaults := []seed{
			{key: "scan", category: "scan", name: "扫码失败", condition: ruleLeaf("scanFailed", "eq", true), enabled: true},
			{key: "mes-reject", category: "mes", name: "MES 拒绝入炉", condition: ruleLeaf("mesRejected", "eq", true), enabled: true},
			{key: "mes-offline", category: "mes", name: "MES 通信失败", condition: ruleLeaf("mesUnavailable", "eq", true), enabled: true},
			{key: "door-timeout", category: "workflow", name: "开关门超时", condition: ruleLeaf("doorTimeout", "eq", true), enabled: true},
			{key: "entry-timeout", category: "workflow", name: "入炉到位超时", condition: ruleLeaf("entryTimeout", "eq", true), enabled: true},
			{key: "reset-timeout", category: "workflow", name: "设备复位超时", condition: ruleLeaf("resetTimeout", "eq", true), enabled: true},
			{key: "workflow", category: "workflow", name: "入炉执行异常", condition: ruleLeaf("workflowFault", "eq", true), enabled: true},
			{key: "camera", category: "device", name: "摄像头离线", condition: ruleLeaf("cameraOnline", "eq", false), enabled: true},
		}
		devices := []protocol.RuleCondition{ruleLeaf("sensorOnline", "eq", false), ruleLeaf("doorOnline", "eq", false), ruleLeaf("lampOnline", "eq", false)}
		if f.Temperature != "" {
			devices = append(devices, ruleLeaf("temperatureOnline", "eq", false))
			old := struct {
				UpperLimitC    float64 `json:"upperLimitC"`
				RecoveryDeltaC float64 `json:"recoveryDeltaC"`
				Enabled        *bool   `json:"enabled"`
			}{UpperLimitC: 800, RecoveryDeltaC: 5}
			if hasLegacy && !migrated {
				var raw []byte
				readErr := tx.QueryRow(ctx, "SELECT settings FROM core.temperature_alarm_settings WHERE furnace_id=$1", f.ID).Scan(&raw)
				if readErr != nil && readErr != pgx.ErrNoRows {
					return readErr
				}
				if len(raw) > 0 {
					if err = json.Unmarshal(raw, &old); err != nil {
						return err
					}
				}
			}
			clear := ruleLeaf("temperatureC", "lte", old.UpperLimitC-old.RecoveryDeltaC)
			defaults = append(defaults, seed{key: "temperature", category: "temperature", name: "炉温超限", condition: ruleLeaf("temperatureC", "gt", old.UpperLimitC), clear: &clear, enabled: old.Enabled == nil || *old.Enabled})
		}
		defaults = append(defaults, seed{key: "devices", category: "device", name: "设备通信异常", condition: ruleGroup("OR", devices...), enabled: true})
		ids[f.ID] = map[string]string{}
		for _, d := range defaults {
			yes := d.enabled
			in := protocol.AlarmRuleInput{Category: d.category, Name: f.Name + " · " + d.name, FurnaceID: f.ID, Enabled: &yes, Condition: d.condition, ClearCondition: d.clear, Channels: &protocol.RuleChannels{Telegram: true, Lamp: true}, RepeatSeconds: 0, Recoveries: true}
			id := protocol.ID()
			tag, err := tx.Exec(ctx, "INSERT INTO core.alarm_rules(id,furnace_id,doc,seed_key) VALUES($1,$2,$3,$4) ON CONFLICT(seed_key) DO NOTHING", id, f.ID, encoded(in), f.ID+":"+d.key)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				if err = tx.QueryRow(ctx, "SELECT id FROM core.alarm_rules WHERE seed_key=$1", f.ID+":"+d.key).Scan(&id); err != nil {
					return err
				}
			} else {
				inserted = true
			}
			ids[f.ID][d.key] = id
			inputs[id] = in
			if _, err = tx.Exec(ctx, "INSERT INTO core.alarm_rule_states(rule_id) VALUES($1) ON CONFLICT DO NOTHING", id); err != nil {
				return err
			}
		}
	}
	if !migrated {
		rows, err := tx.Query(ctx, "SELECT doc FROM core.alarms WHERE active AND COALESCE(doc->>'ruleId','')='' ORDER BY raised_at")
		if err != nil {
			return err
		}
		legacy := []protocol.Alarm{}
		for rows.Next() {
			var raw []byte
			var a protocol.Alarm
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			if err = json.Unmarshal(raw, &a); err != nil {
				rows.Close()
				return err
			}
			legacy = append(legacy, a)
		}
		rows.Close()
		if rows.Err() != nil {
			return rows.Err()
		}
		attached := map[string]bool{}
		mapping := map[string]string{"TEMPERATURE_HIGH": "temperature", "CAMERA_OFFLINE": "camera", "SCAN_TIMEOUT": "scan", "AMBIGUOUS": "scan", "INVALID_BARCODE": "scan", "MES_REJECTED": "mes-reject", "BASKET_OCCUPIED": "mes-reject", "MES_UNAVAILABLE": "mes-offline", "OPEN_TIMEOUT": "door-timeout", "CLOSE_TIMEOUT": "door-timeout", "ENTRY_TIMEOUT": "entry-timeout", "RESET_TIMEOUT": "reset-timeout", "RECOVERY_REQUIRED": "workflow", "DEVICE_RESTARTED": "workflow"}
		for _, a := range legacy {
			key := mapping[a.Code]
			if key == "" {
				key = "devices"
				if a.CycleID != "" {
					key = "workflow"
				}
			}
			id := ids[a.FurnaceID][key]
			if id == "" {
				continue
			}
			in := inputs[id]
			now := time.Now().UTC()
			if attached[id] || !*in.Enabled {
				a.Resolution = "rule_migrated"
				a.RecoveredAt = &now
				if _, err = tx.Exec(ctx, "UPDATE core.alarms SET active=false,doc=$2 WHERE id=$1", a.ID, encoded(a)); err != nil {
					return err
				}
				continue
			}
			a.RuleID = id
			a.RuleVersion = 1
			a.Category = in.Category
			lamp := true
			a.Lamp = &lamp
			if _, err = tx.Exec(ctx, "UPDATE core.alarms SET doc=$2 WHERE id=$1", a.ID, encoded(a)); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "UPDATE core.alarm_rule_states SET active_alarm_id=$2,notified=true WHERE rule_id=$1", id, a.ID); err != nil {
				return err
			}
			attached[id] = true
		}
		if hasLegacy {
			if _, err = tx.Exec(ctx, "ALTER TABLE core.temperature_alarm_settings RENAME TO temperature_alarm_settings_legacy"); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "UPDATE core.catalog_revision SET legacy_alarms_migrated=true WHERE id=1"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO core.audit(id,operator,action,target,reason) VALUES($1,'system','MIGRATE_ALARM_RULES','alarm-rules','旧报警迁入统一规则；历史记录保留，合并不代表现场故障消除')", protocol.ID()); err != nil {
			return err
		}
	}
	if inserted || !migrated {
		if _, err = bumpRules(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
