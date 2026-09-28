package notifier

import (
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"net/http"
)

func (s *Service) syncAccess(w http.ResponseWriter, r *http.Request) {
	var in protocol.AccountAccess
	if platform.Read(r, &in) != nil || in.UserID == "" || in.Version < 1 {
		platform.Error(w, 400, "INVALID_ACCESS", "账号状态格式错误")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知存储不可用")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO notify.account_access(user_id,enabled,version) VALUES($1,$2,$3)
 ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled,version=excluded.version WHERE notify.account_access.version<excluded.version`, in.UserID, in.Enabled, in.Version)
	var enabled bool
	var version int64
	if err == nil {
		err = tx.QueryRow(r.Context(), "SELECT enabled,version FROM notify.account_access WHERE user_id=$1 FOR UPDATE", in.UserID).Scan(&enabled, &version)
	}
	if err == nil && version == in.Version && enabled != in.Enabled {
		platform.Error(w, 409, "VERSION_CONFLICT", "相同账号状态版本内容不一致")
		return
	}
	if err == nil && !enabled {
		_, err = tx.Exec(r.Context(), "UPDATE notify.jobs SET status='cancelled',last_error='账户已停用' WHERE user_id=$1 AND status='pending'", in.UserID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "账号状态保存失败")
		return
	}
	w.WriteHeader(204)
}
