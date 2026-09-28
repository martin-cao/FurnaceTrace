package notifier

import (
	"furnace.local/iot/internal/platform"
	"net/http"
)

func (s *Service) cancelReminders(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("alarmId")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知存储不可用")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), "INSERT INTO notify.closed_rule_alarms(alarm_id) VALUES($1) ON CONFLICT DO NOTHING", id)
	if err == nil {
		_, err = tx.Exec(r.Context(), "UPDATE notify.jobs SET status='cancelled',last_error='规则报警已结束，取消重复提醒' WHERE status='pending' AND request->>'alarmId'=$1 AND request->>'kind'='REMINDER'", id)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "取消提醒失败")
		return
	}
	w.WriteHeader(204)
}
