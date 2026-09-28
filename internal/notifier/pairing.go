package notifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"
)

func normalizeCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}
func codeDigest(code string) string {
	sum := sha256.Sum256([]byte(normalizeCode(code)))
	return hex.EncodeToString(sum[:])
}
func (s *Service) state(ctx context.Context, user string) (protocol.TelegramState, error) {
	s.metaMu.Lock()
	state := protocol.TelegramState{BotUsername: s.botUsername, BotConfigured: s.token != "", BotOnline: s.botOnline, Preferences: s.defaults()}
	s.metaMu.Unlock()
	var raw []byte
	err := s.db.QueryRow(ctx, "SELECT doc FROM notify.preferences WHERE user_id=$1", user).Scan(&raw)
	if err != nil && err != pgx.ErrNoRows {
		return state, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &state.Preferences); err != nil {
			return state, err
		}
	}
	var binding protocol.TelegramBinding
	err = s.db.QueryRow(ctx, "SELECT display_name,created_at FROM notify.bindings WHERE user_id=$1", user).Scan(&binding.DisplayName, &binding.PairedAt)
	if err == nil {
		state.Binding = &binding
	} else if err != pgx.ErrNoRows {
		return state, err
	}
	return state, nil
}
func (s *Service) pair(ctx context.Context, user, raw string) (int64, error) {
	if len(normalizeCode(raw)) != 8 {
		return 0, errors.New("invalid code")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var chat int64
	var display string
	var expiry time.Time
	var used, binding *string
	err = tx.QueryRow(ctx, "SELECT chat_id,display_name,expires_at,used_by,binding_id FROM notify.pair_codes WHERE code_hash=$1 FOR UPDATE", codeDigest(raw)).Scan(&chat, &display, &expiry, &used, &binding)
	if err != nil {
		return 0, errors.New("invalid or expired code")
	}
	if used != nil {
		var same bool
		if *used == user && binding != nil {
			_ = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM notify.bindings WHERE user_id=$1 AND binding_id=$2)", user, *binding).Scan(&same)
		}
		if same {
			return chat, nil
		}
		return 0, errors.New("code already used")
	}
	if !expiry.After(time.Now()) {
		return 0, errors.New("expired code")
	}
	var occupied bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM notify.bindings WHERE user_id=$1 OR chat_id=$2)", user, chat).Scan(&occupied); err != nil {
		return 0, err
	}
	if occupied {
		return 0, errors.New("already bound")
	}
	id := protocol.ID()
	if _, err = tx.Exec(ctx, "INSERT INTO notify.bindings(user_id,binding_id,chat_id,display_name) VALUES($1,$2,$3,$4)", user, id, chat, display); err != nil {
		return 0, errors.New("already bound")
	}
	if _, err = tx.Exec(ctx, "INSERT INTO notify.preferences(user_id,doc) VALUES($1,$2) ON CONFLICT(user_id) DO NOTHING", user, encode(s.defaults())); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "UPDATE notify.pair_codes SET used_by=$2,binding_id=$3 WHERE chat_id=$1", chat, user, id); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return chat, nil
}
func (s *Service) unpair(ctx context.Context, user string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "UPDATE notify.jobs SET status='cancelled',last_error='用户已解除 Telegram 绑定' WHERE user_id=$1 AND status='pending'", user)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "DELETE FROM notify.pair_codes WHERE chat_id IN (SELECT chat_id FROM notify.bindings WHERE user_id=$1)", user)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "DELETE FROM notify.bindings WHERE user_id=$1", user)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) pairRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /internal/v1/users/{userId}/telegram", func(w http.ResponseWriter, r *http.Request) {
		state, err := s.state(r.Context(), r.PathValue("userId"))
		if err != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "绑定状态读取失败")
			return
		}
		platform.JSON(w, 200, state)
	})
	m.HandleFunc("POST /internal/v1/users/{userId}/telegram/pair", func(w http.ResponseWriter, r *http.Request) {
		user := r.PathValue("userId")
		if !s.pairLimits.Allow(user, 5, time.Minute) {
			platform.Error(w, 429, "TOO_MANY_ATTEMPTS", "配对尝试过于频繁")
			return
		}
		var in protocol.PairRequest
		if platform.Read(r, &in) != nil || len(in.Code) > 20 {
			platform.Error(w, 400, "INVALID_PAIR_CODE", "配对码格式无效")
			return
		}
		chat, err := s.pair(r.Context(), user, in.Code)
		if err != nil {
			platform.Error(w, 409, "PAIRING_REJECTED", "配对码无效、过期、已使用或账户已绑定")
			return
		}
		state, err := s.state(r.Context(), user)
		if err != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "配对成功，状态读取失败，请刷新")
			return
		}
		platform.JSON(w, 200, state)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			s.send(ctx, protocol.NotificationRequest{Text: "配对成功。现在可以在网页「个人通知」中选择炉子与通知类型。"}, 0, chat)
		}()
	})
	m.HandleFunc("DELETE /internal/v1/users/{userId}/telegram", func(w http.ResponseWriter, r *http.Request) {
		if s.unpair(r.Context(), r.PathValue("userId")) != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "解绑失败")
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("PUT /internal/v1/users/{userId}/notification-preferences", func(w http.ResponseWriter, r *http.Request) {
		var p protocol.NotificationPreferences
		if platform.Read(r, &p) != nil || !s.validPreferences(p) {
			platform.Error(w, 400, "INVALID_PREFERENCES", "炉子或通知类别无效")
			return
		}
		tx, err := s.db.Begin(r.Context())
		if err != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知设置保存失败")
			return
		}
		defer tx.Rollback(r.Context())
		if _, err = tx.Exec(r.Context(), "INSERT INTO notify.preferences(user_id,doc) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET doc=excluded.doc", r.PathValue("userId"), encode(p)); err != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知设置保存失败")
			return
		}
		_, err = tx.Exec(r.Context(), `UPDATE notify.jobs SET status='cancelled',last_error='个人通知偏好已变更' WHERE user_id=$1 AND status='pending'
 AND NOT ($2 AND request->>'furnaceId'=ANY($3::text[]) AND COALESCE(request->>'category','workflow')=ANY($4::text[]) AND (request->>'kind'<>'RECOVERY' OR $5))`, r.PathValue("userId"), p.Enabled, p.FurnaceIDs, p.Categories, p.Recoveries)
		if err != nil || tx.Commit(r.Context()) != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知设置保存失败")
			return
		}
		platform.JSON(w, 200, p)
	})
}
