package core

import (
	"context"
	"fmt"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Lock the account while checking credentials so simultaneous changes cannot use a stale password.
func (e *Engine) credentialTx(r *http.Request, password string) (pgx.Tx, error) {
	tx, err := e.store.db.Begin(r.Context())
	if err != nil {
		return nil, err
	}
	var hash string
	err = tx.QueryRow(r.Context(), "SELECT password_hash FROM core.users WHERE id=$1 AND enabled FOR UPDATE", requestUser(r).ID).Scan(&hash)
	if err != nil {
		tx.Rollback(r.Context())
		return nil, err
	}
	if len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		tx.Rollback(r.Context())
		return nil, fault{403, "CURRENT_PASSWORD_INVALID", "当前密码不正确"}
	}
	return tx, nil
}
func accountAudit(r *http.Request, tx pgx.Tx, action, target, reason string) error {
	_, err := tx.Exec(r.Context(), "INSERT INTO core.audit(id,operator,action,target,reason) VALUES($1,$2,$3,$4,$5)", protocol.ID(), requestUser(r).Username, action, target, reason)
	return err
}
func (e *Engine) accountSettingsRoutes(m *http.ServeMux) {
	m.HandleFunc("PUT /api/v1/me/profile", func(w http.ResponseWriter, r *http.Request) {
		if !rateAuth(w, r) {
			return
		}
		var in protocol.ProfileUpdate
		if platform.Read(r, &in) != nil || !usernamePattern.MatchString(in.Username) || strings.TrimSpace(in.DisplayName) == "" || utf8.RuneCountInString(in.DisplayName) > 50 {
			platform.Error(w, 400, "INVALID_PROFILE", "用户名需为 3–32 位字母数字或下划线；显示名称为 1–50 字符")
			return
		}
		tx, err := e.credentialTx(r, in.CurrentPassword)
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback(r.Context())
		user := requestUser(r)
		user.Username = strings.ToLower(in.Username)
		user.DisplayName = strings.TrimSpace(in.DisplayName)
		_, err = tx.Exec(r.Context(), "UPDATE core.users SET username=$2,display_name=$3 WHERE id=$1", user.ID, user.Username, user.DisplayName)
		if pe, ok := err.(*pgconn.PgError); ok && pe.Code == "23505" {
			platform.Error(w, 409, "USERNAME_TAKEN", "用户名已被使用")
			return
		}
		if err == nil {
			err = accountAudit(r, tx, "UPDATE_PROFILE", user.ID, "修改用户名或显示名称")
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			writeError(w, err)
			return
		}
		platform.JSON(w, 200, user)
	})
	m.HandleFunc("PUT /api/v1/me/password", func(w http.ResponseWriter, r *http.Request) {
		if !rateAuth(w, r) {
			return
		}
		var in protocol.PasswordChange
		if platform.Read(r, &in) != nil || !validPassword(in.NewPassword) {
			platform.Error(w, 400, "INVALID_PASSWORD", "新密码至少 8 字符，最多 72 字节")
			return
		}
		tx, err := e.credentialTx(r, in.CurrentPassword)
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback(r.Context())
		hash, err := bcrypt.GenerateFromPassword([]byte(in.NewPassword), 12)
		if err == nil {
			_, err = tx.Exec(r.Context(), "UPDATE core.users SET password_hash=$2 WHERE id=$1", requestUser(r).ID, string(hash))
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), "DELETE FROM core.sessions WHERE user_id=$1", requestUser(r).ID)
		}
		if err == nil {
			err = accountAudit(r, tx, "CHANGE_PASSWORD", requestUser(r).ID, "修改密码并撤销全部会话")
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			writeError(w, err)
			return
		}
		setSessionCookie(w, r, "", -1)
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /api/v1/users", e.listUsers)
	m.HandleFunc("PUT /api/v1/users/{userId}/access", e.updateUserAccess)
}
func (e *Engine) listUsers(w http.ResponseWriter, r *http.Request) {
	page, size, err := pageParams(r)
	if err != nil {
		platform.Error(w, 400, "INVALID_PAGE", "分页参数无效")
		return
	}
	out := protocol.Page[protocol.ManagedUser]{Items: []protocol.ManagedUser{}, Pagination: protocol.Pagination{Page: page, PageSize: size}}
	if err := e.store.db.QueryRow(r.Context(), "SELECT count(*) FROM core.users").Scan(&out.Pagination.Total); err != nil {
		writeError(w, err)
		return
	}
	rows, err := e.store.db.Query(r.Context(), "SELECT id,username,display_name,role,enabled,created_at FROM core.users ORDER BY created_at,id LIMIT $1 OFFSET $2", size, (page-1)*size)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var u protocol.ManagedUser
		if err = rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Enabled, &u.CreatedAt); err != nil {
			writeError(w, err)
			return
		}
		out.Items = append(out.Items, u)
	}
	if rows.Err() != nil {
		writeError(w, rows.Err())
		return
	}
	platform.JSON(w, 200, out)
}
func (e *Engine) updateUserAccess(w http.ResponseWriter, r *http.Request) {
	var in protocol.UserAccessUpdate
	if platform.Read(r, &in) != nil || in.Enabled == nil || (in.Role != "admin" && in.Role != "user") {
		platform.Error(w, 400, "INVALID_ACCESS", "请选择角色和启用状态")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	writeAccessError := func(err error) {
		if ctx.Err() != nil {
			platform.Error(w, 503, "ACCESS_UPDATE_TIMEOUT", "权限修改超时，结果尚未确认；请刷新用户列表核实后再试")
		} else {
			writeError(w, err)
		}
	}
	tx, err := e.store.db.Begin(r.Context())
	if err != nil {
		writeAccessError(err)
		return
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = tx.Rollback(cleanup)
	}()
	// Service leases use single bigint keys (notifier owns 7614863).
	// Use a separate two-key transaction namespace and reject contention instead of waiting forever.
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(7614862, 1)").Scan(&locked); err != nil {
		writeAccessError(err)
		return
	}
	if !locked {
		platform.Error(w, 409, "ACCESS_UPDATE_BUSY", "另一项用户权限修改正在进行，请稍后重试")
		return
	}
	var actorOK bool
	if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM core.users WHERE id=$1 AND role='admin' AND enabled)", requestUser(r).ID).Scan(&actorOK); err != nil {
		writeAccessError(err)
		return
	}
	if !actorOK {
		platform.Error(w, 403, "ADMIN_REQUIRED", "管理员权限已变更，请重新登录")
		return
	}
	var u protocol.ManagedUser
	var version int64
	err = tx.QueryRow(r.Context(), "SELECT id,username,display_name,role,enabled,created_at,access_version FROM core.users WHERE id=$1 FOR UPDATE", r.PathValue("userId")).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Enabled, &u.CreatedAt, &version)
	if err == pgx.ErrNoRows {
		platform.Error(w, 404, "USER_NOT_FOUND", "用户不存在")
		return
	}
	if err != nil {
		writeAccessError(err)
		return
	}
	if in.Role == u.Role && *in.Enabled == u.Enabled {
		platform.JSON(w, 200, u)
		return
	}
	if u.ID == requestUser(r).ID {
		platform.Error(w, 409, "SELF_ACCESS_CHANGE", "不能修改自己的角色或启用状态")
		return
	}
	if u.Role == "admin" && u.Enabled && (in.Role != "admin" || !*in.Enabled) {
		var count int
		if err = tx.QueryRow(r.Context(), "SELECT count(*) FROM core.users WHERE role='admin' AND enabled").Scan(&count); err != nil {
			writeAccessError(err)
			return
		}
		if count <= 1 {
			platform.Error(w, 409, "LAST_ADMIN", "必须保留至少一个启用的管理员")
			return
		}
	}
	u.Role = in.Role
	u.Enabled = *in.Enabled
	version++
	_, err = tx.Exec(r.Context(), "UPDATE core.users SET role=$2,enabled=$3,access_version=$4 WHERE id=$1", u.ID, u.Role, u.Enabled, version)
	if err == nil {
		_, err = tx.Exec(r.Context(), "DELETE FROM core.sessions WHERE user_id=$1", u.ID)
	}
	if err == nil {
		err = enqueue(r.Context(), tx, protocol.ID(), "account-access", protocol.AccountAccess{UserID: u.ID, Enabled: u.Enabled, Version: version})
	}
	if err == nil {
		err = accountAudit(r, tx, "UPDATE_USER_ACCESS", u.ID, fmt.Sprintf("role=%s enabled=%t", u.Role, u.Enabled))
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeAccessError(err)
		return
	}
	platform.JSON(w, 200, u)
}
