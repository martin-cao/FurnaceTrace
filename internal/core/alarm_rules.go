package core

import (
	"context"
	"encoding/json"
	"fmt"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type ruleRuntime struct {
	Rule     protocol.AlarmRule
	Alarm    *protocol.Alarm
	Notified bool
	Next     *time.Time
}

func (r ruleRuntime) view() protocol.AlarmRule {
	v := r.Rule
	v.Active = r.Alarm != nil
	v.NextReminderAt = r.Next
	return v
}
func bumpRules(ctx context.Context, tx pgx.Tx) (int64, error) {
	var v int64
	err := tx.QueryRow(ctx, "UPDATE core.catalog_revision SET rules_version=rules_version+1 WHERE id=1 RETURNING rules_version").Scan(&v)
	return v, err
}
func (e *Engine) rulesRevision() int64 { e.mu.Lock(); defer e.mu.Unlock(); return e.rulesVersion }
func (e *Engine) validRule(in protocol.AlarmRuleInput) bool {
	if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 80 || in.Enabled == nil || in.Channels == nil || in.RepeatSeconds != 0 && (in.RepeatSeconds < 10 || in.RepeatSeconds > 86400) {
		return false
	}
	if in.Category != "" && in.Category != "rules" && in.Category != "temperature" && in.Category != "scan" && in.Category != "mes" && in.Category != "device" && in.Category != "workflow" {
		return false
	}
	if _, ok := e.config(in.FurnaceID); !ok {
		return false
	}
	count := 0
	if !validCondition(in.Condition, 1, &count) {
		return false
	}
	count = 0
	return in.ClearCondition == nil || validCondition(*in.ClearCondition, 1, &count)
}
func (e *Engine) loadAlarmRules(ctx context.Context) error {
	if err := e.seedAlarmRules(ctx); err != nil {
		return err
	}
	e.rules = map[string]*ruleRuntime{}
	if err := e.store.db.QueryRow(ctx, "SELECT rules_version FROM core.catalog_revision WHERE id=1").Scan(&e.rulesVersion); err != nil {
		return err
	}
	rows, err := e.store.db.Query(ctx, `SELECT r.id,r.doc,r.version,r.created_at,r.updated_at,COALESCE(s.notified,false),s.next_reminder_at,a.doc
 FROM core.alarm_rules r LEFT JOIN core.alarm_rule_states s ON s.rule_id=r.id LEFT JOIN core.alarms a ON a.id=s.active_alarm_id AND a.active WHERE r.deleted_at IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		rt := &ruleRuntime{}
		var raw, alarm []byte
		if err = rows.Scan(&rt.Rule.ID, &raw, &rt.Rule.Version, &rt.Rule.CreatedAt, &rt.Rule.UpdatedAt, &rt.Notified, &rt.Next, &alarm); err != nil {
			return err
		}
		if json.Unmarshal(raw, &rt.Rule.AlarmRuleInput) != nil || !e.validRule(rt.Rule.AlarmRuleInput) {
			return fmt.Errorf("invalid saved alarm rule %s", rt.Rule.ID)
		}
		if len(alarm) > 0 {
			rt.Alarm = &protocol.Alarm{}
			if err = json.Unmarshal(alarm, rt.Alarm); err != nil {
				return err
			}
		} else {
			rt.Next = nil
			rt.Notified = false
		}
		e.rules[rt.Rule.ID] = rt
	}
	return rows.Err()
}
func refreshRuleAlarm(ctx context.Context, tx pgx.Tx, rt *ruleRuntime) error {
	if rt.Alarm == nil {
		return nil
	}
	var raw []byte
	if err := tx.QueryRow(ctx, "SELECT doc FROM core.alarms WHERE id=$1 AND active FOR UPDATE", rt.Alarm.ID).Scan(&raw); err != nil {
		return err
	}
	var a protocol.Alarm
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	rt.Alarm = &a
	return nil
}
func saveRuleState(ctx context.Context, tx pgx.Tx, rt ruleRuntime) error {
	id := ""
	if rt.Alarm != nil {
		id = rt.Alarm.ID
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.alarm_rule_states(rule_id,active_alarm_id,notified,next_reminder_at) VALUES($1,$2,$3,$4)
 ON CONFLICT(rule_id) DO UPDATE SET active_alarm_id=excluded.active_alarm_id,notified=excluded.notified,next_reminder_at=excluded.next_reminder_at`, rt.Rule.ID, id, rt.Notified, rt.Next)
	return err
}
func closeRuleAlarm(ctx context.Context, tx pgx.Tx, rt *ruleRuntime, reason string, now time.Time) error {
	if rt.Alarm == nil {
		return nil
	}
	if err := enqueue(ctx, tx, protocol.ID(), "cancel-reminders", map[string]string{"alarmId": rt.Alarm.ID}); err != nil {
		return err
	}
	rt.Alarm.RecoveredAt = &now
	rt.Alarm.Resolution = reason
	if _, err := tx.Exec(ctx, "UPDATE core.alarms SET active=false,doc=$2 WHERE id=$1", rt.Alarm.ID, encoded(rt.Alarm)); err != nil {
		return err
	}
	if rt.Notified && rt.Rule.Channels.Telegram && rt.Rule.Recoveries {
		if err := notify(ctx, tx, *rt.Alarm, "RECOVERY"); err != nil {
			return err
		}
	}
	rt.Alarm = nil
	rt.Notified = false
	rt.Next = nil
	return nil
}
func (e *Engine) ruleEvent(ctx context.Context, original *ruleRuntime, action, message string, now time.Time) error {
	rt := *original
	tx, err := e.store.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = refreshRuleAlarm(ctx, tx, &rt); err != nil {
		return err
	}
	switch action {
	case "raise":
		lamp := rt.Rule.Channels.Lamp
		a := protocol.Alarm{ID: protocol.ID(), FurnaceID: rt.Rule.FurnaceID, Code: "RULE_" + rt.Rule.ID, Message: rt.Rule.Name + "：" + message, RaisedAt: now, Category: rt.Rule.Category, RuleID: rt.Rule.ID, RuleVersion: rt.Rule.Version, Lamp: &lamp}
		if rec, ok := e.active[a.FurnaceID]; ok && usesWorkflowCondition(rt.Rule.Condition) {
			a.CycleID = rec.Cycle.ID
			a.Message += "；料筐：" + rec.Cycle.BasketNo
			if rec.Cycle.MES != nil {
				a.Message += "；" + rec.Cycle.MES.Message
			}
		}
		if _, err = tx.Exec(ctx, "INSERT INTO core.alarms(id,furnace_id,cycle_id,code,raised_at,doc) VALUES($1,$2,$3,$4,$5,$6)", a.ID, a.FurnaceID, a.CycleID, a.Code, now, encoded(a)); err != nil {
			return err
		}
		rt.Alarm = &a
		rt.Notified = false
		rt.Next = nil
		if rt.Rule.Channels.Telegram {
			if err = notify(ctx, tx, a, "ALARM"); err != nil {
				return err
			}
			rt.Notified = true
		}
	case "recover":
		if err = closeRuleAlarm(ctx, tx, &rt, "condition_cleared", now); err != nil {
			return err
		}
	case "first", "reminder":
		kind := "REMINDER"
		if action == "first" {
			kind = "ALARM"
		}
		if err = notify(ctx, tx, *rt.Alarm, kind); err != nil {
			return err
		}
		rt.Notified = true
	}
	if rt.Alarm != nil && rt.Rule.Channels.Telegram && rt.Notified && rt.Rule.RepeatSeconds > 0 {
		next := now.Add(time.Duration(rt.Rule.RepeatSeconds) * time.Second)
		rt.Next = &next
	}
	if err = saveRuleState(ctx, tx, rt); err != nil {
		return err
	}
	version, err := bumpRules(ctx, tx)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	e.rules[rt.Rule.ID] = &rt
	e.rulesVersion = version
	return nil
}
func (e *Engine) monitorRules(ctx context.Context, f platform.FurnaceConfig, now time.Time) {
	values := e.ruleValues(f)
	for _, rt := range e.rules {
		if rt.Rule.FurnaceID != f.ID || !*rt.Rule.Enabled {
			continue
		}
		trigger := evalCondition(rt.Rule.Condition, values)
		if rt.Alarm == nil {
			if trigger == yes {
				_ = e.ruleEvent(ctx, rt, "raise", describeCondition(rt.Rule.Condition, values), now)
			}
			continue
		}
		clear := trigger == no
		if rt.Rule.ClearCondition != nil {
			clear = trigger != yes && evalCondition(*rt.Rule.ClearCondition, values) == yes
		}
		if clear {
			_ = e.ruleEvent(ctx, rt, "recover", "", now)
			continue
		}
		// Unknown readings neither clear an alarm nor generate claims that a condition recovered.
		if !rt.Rule.Channels.Telegram {
			continue
		}
		if !rt.Notified {
			_ = e.ruleEvent(ctx, rt, "first", "", now)
		} else if rt.Rule.RepeatSeconds > 0 && (rt.Next == nil || !now.Before(*rt.Next)) {
			_ = e.ruleEvent(ctx, rt, "reminder", "", now)
		}
	}
}
func (e *Engine) alarmRuleRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/alarm-rules", func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		list := []protocol.AlarmRule{}
		for _, rt := range e.rules {
			list = append(list, rt.view())
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].CreatedAt.Equal(list[j].CreatedAt) {
				return list[i].ID < list[j].ID
			}
			return list[i].CreatedAt.After(list[j].CreatedAt)
		})
		platform.JSON(w, 200, list)
	})
	m.HandleFunc("POST /api/v1/alarm-rules", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.AlarmRuleInput
		if platform.Read(r, &in) != nil || !e.validRule(in) {
			platform.Error(w, 400, "INVALID_RULE", "请填写有效名称、炉子、条件和报警方式；最多 4 层/16 个条件，重复间隔为 0 或 10–86400 秒")
			return
		}
		if in.Category == "" {
			in.Category = "rules"
		}
		e.idempotent(w, r, in, func(tx pgx.Tx) (string, afterCommit, error) {
			if len(e.rules) >= 100 {
				return "", nil, fail("RULE_LIMIT", "最多保留 100 条自定义规则")
			}
			now := time.Now().UTC()
			rt := &ruleRuntime{Rule: protocol.AlarmRule{AlarmRuleInput: in, ID: protocol.ID(), Version: 1, CreatedAt: now, UpdatedAt: now}}
			if _, err := tx.Exec(r.Context(), "INSERT INTO core.alarm_rules(id,furnace_id,doc,created_at,updated_at) VALUES($1,$2,$3,$4,$4)", rt.Rule.ID, in.FurnaceID, encoded(in), now); err != nil {
				return "", nil, err
			}
			if err := saveRuleState(r.Context(), tx, *rt); err != nil {
				return "", nil, err
			}
			if err := accountAudit(r, tx, "CREATE_ALARM_RULE", rt.Rule.ID, in.Name); err != nil {
				return "", nil, err
			}
			version, err := bumpRules(r.Context(), tx)
			return rt.Rule.ID, func() { e.rules[rt.Rule.ID] = rt; e.rulesVersion = version }, err
		})
	})
	m.HandleFunc("PUT /api/v1/alarm-rules/{ruleId}", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.AlarmRuleUpdate
		if platform.Read(r, &in) != nil || in.Version < 1 || !e.validRule(in.AlarmRuleInput) {
			platform.Error(w, 400, "INVALID_RULE", "规则内容、版本或重复间隔无效")
			return
		}
		if in.Category == "" {
			in.Category = "rules"
		}
		e.idempotent(w, r, in, func(tx pgx.Tx) (string, afterCommit, error) {
			old, ok := e.rules[r.PathValue("ruleId")]
			if !ok {
				return "", nil, fault{404, "RULE_NOT_FOUND", "规则不存在"}
			}
			if old.Rule.Version != in.Version {
				return "", nil, fail("STALE_VERSION", "规则已被修改，请刷新后重新编辑")
			}
			if old.Rule.FurnaceID != in.FurnaceID {
				return "", nil, fault{400, "RULE_FURNACE_IMMUTABLE", "所属炉子不能更改，请新建规则"}
			}
			rt := *old
			rt.Rule.AlarmRuleInput = in.AlarmRuleInput
			rt.Rule.Version++
			rt.Rule.UpdatedAt = time.Now().UTC()
			if err := refreshRuleAlarm(r.Context(), tx, &rt); err != nil {
				return "", nil, err
			}
			if !*in.Enabled {
				if err := closeRuleAlarm(r.Context(), tx, &rt, "rule_disabled", rt.Rule.UpdatedAt); err != nil {
					return "", nil, err
				}
			}
			rt.Next = nil
			if rt.Alarm != nil {
				lamp := in.Channels.Lamp
				rt.Alarm.Lamp = &lamp
				rt.Alarm.Category = in.Category
				if _, err := tx.Exec(r.Context(), "UPDATE core.alarms SET doc=$2 WHERE id=$1", rt.Alarm.ID, encoded(rt.Alarm)); err != nil {
					return "", nil, err
				}
				if in.Channels.Telegram && rt.Notified && in.RepeatSeconds > 0 {
					next := rt.Rule.UpdatedAt.Add(time.Duration(in.RepeatSeconds) * time.Second)
					rt.Next = &next
				}
			}
			if _, err := tx.Exec(r.Context(), "UPDATE core.alarm_rules SET doc=$2,version=$3,updated_at=$4 WHERE id=$1", rt.Rule.ID, encoded(in.AlarmRuleInput), rt.Rule.Version, rt.Rule.UpdatedAt); err != nil {
				return "", nil, err
			}
			if err := saveRuleState(r.Context(), tx, rt); err != nil {
				return "", nil, err
			}
			if err := accountAudit(r, tx, "UPDATE_ALARM_RULE", rt.Rule.ID, in.Name); err != nil {
				return "", nil, err
			}
			version, err := bumpRules(r.Context(), tx)
			return rt.Rule.ID, func() { e.rules[rt.Rule.ID] = &rt; e.rulesVersion = version }, err
		})
	})
	m.HandleFunc("DELETE /api/v1/alarm-rules/{ruleId}", func(w http.ResponseWriter, r *http.Request) {
		var in protocol.CatalogDelete
		if platform.Read(r, &in) != nil || in.Version < 1 {
			platform.Error(w, 400, "INVALID_VERSION", "需要当前规则版本")
			return
		}
		e.idempotent(w, r, in, func(tx pgx.Tx) (string, afterCommit, error) {
			id := r.PathValue("ruleId")
			old, ok := e.rules[id]
			if !ok {
				return "", nil, fault{404, "RULE_NOT_FOUND", "规则不存在或已删除"}
			}
			if old.Rule.Version != in.Version {
				return "", nil, fail("STALE_VERSION", "规则已被修改，请刷新")
			}
			rt := *old
			if err := refreshRuleAlarm(r.Context(), tx, &rt); err != nil {
				return "", nil, err
			}
			if err := closeRuleAlarm(r.Context(), tx, &rt, "rule_deleted", time.Now().UTC()); err != nil {
				return "", nil, err
			}
			if _, err := tx.Exec(r.Context(), "UPDATE core.alarm_rules SET deleted_at=now(),updated_at=now(),version=version+1 WHERE id=$1", id); err != nil {
				return "", nil, err
			}
			if err := saveRuleState(r.Context(), tx, rt); err != nil {
				return "", nil, err
			}
			if err := accountAudit(r, tx, "DELETE_ALARM_RULE", id, old.Rule.Name); err != nil {
				return "", nil, err
			}
			version, err := bumpRules(r.Context(), tx)
			return id, func() { delete(e.rules, id); e.rulesVersion = version }, err
		})
	})
}
