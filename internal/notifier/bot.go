package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type botUpdate struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		Text string `json:"text"`
		From struct {
			ID        int64  `json:"id"`
			IsBot     bool   `json:"is_bot"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
		} `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
	} `json:"message"`
}

func pairCode(chat, update int64) string {
	mac := hmac.New(sha256.New, []byte(os.Getenv("SERVICE_TOKEN")))
	fmt.Fprintf(mac, "telegram-pair-v1:%d:%d", chat, update)
	// Deterministic per Telegram update, so retrying a lost reply returns the same code.
	return base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding).EncodeToString(mac.Sum(nil)[:5])
}
func (s *Service) botCall(ctx context.Context, client *http.Client, method string, in, out any) error {
	r, err := http.NewRequestWithContext(ctx, "POST", s.base+"/bot"+s.token+"/"+method, bytes.NewReader(encode(in)))
	if err != nil {
		return errors.New("invalid bot configuration")
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := client.Do(r)
	if err != nil {
		return errors.New("Telegram unavailable")
	}
	defer res.Body.Close()
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&envelope) != nil || !envelope.OK || res.StatusCode != 200 {
		return fmt.Errorf("Telegram HTTP %d", res.StatusCode)
	}
	return json.Unmarshal(envelope.Result, out)
}
func (s *Service) handleUpdate(ctx context.Context, u botUpdate) error {
	m := u.Message
	if m == nil || m.Chat.Type != "private" || m.From.IsBot || m.Chat.ID != m.From.ID {
		return nil
	}
	parts := strings.Fields(m.Text)
	if len(parts) == 0 {
		return nil
	}
	command := strings.Split(parts[0], "@")[0]
	if command != "/start" && command != "/status" && command != "/unlink" && command != "/help" {
		return nil
	}
	var user string
	err := s.db.QueryRow(ctx, "SELECT user_id FROM notify.bindings WHERE chat_id=$1", m.Chat.ID).Scan(&user)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	text := "请先发送 /start 获取配对码，再登录网页，在「个人通知」中完成绑定。"
	if user != "" {
		switch command {
		case "/unlink":
			if err = s.unpair(ctx, user); err != nil {
				return err
			}
			text = "已解除绑定，尚未发送的通知已取消。再次发送 /start 可以重新配对。"
		case "/status":
			state, x := s.state(ctx, user)
			if x != nil {
				return x
			}
			status := "已暂停"
			if state.Preferences.Enabled {
				status = "已启用"
			}
			text = "账户已绑定。\n通知：" + status + "\n订阅炉子：" + strings.Join(state.Preferences.FurnaceIDs, ", ") + "\n在网页「个人通知」中修改订阅。"
		default:
			text = "你已完成配对。\n在网页「个人通知」中设置通知偏好。\n/status 查看状态\n/unlink 解除绑定"
		}
	} else if command == "/start" {
		code := pairCode(m.Chat.ID, u.ID)
		display := m.From.FirstName
		if m.From.Username != "" {
			display = "@" + m.From.Username
		}
		var expiry time.Time
		err = s.db.QueryRow(ctx, `INSERT INTO notify.pair_codes(chat_id,code_hash,update_id,display_name,expires_at) VALUES($1,$2,$3,$4,now()+interval '10 minutes')
 ON CONFLICT(chat_id) DO UPDATE SET code_hash=excluded.code_hash,update_id=excluded.update_id,display_name=excluded.display_name,
 expires_at=CASE WHEN notify.pair_codes.update_id=excluded.update_id THEN notify.pair_codes.expires_at ELSE excluded.expires_at END,
 used_by=NULL,binding_id=NULL RETURNING expires_at`, m.Chat.ID, codeDigest(code), u.ID, display).Scan(&expiry)
		if err != nil {
			return err
		}
		if expiry.After(time.Now()) {
			text = "你的网页配对码：\n\n" + code[:4] + "-" + code[4:] + "\n\n10 分钟内有效，仅可使用一次。\n登录炉前工作台，在「个人通知」中填写此码。请勿把配对码交给他人。"
		} else {
			text = "此前的配对请求已过期，请重新发送 /start。"
		}
	}
	result := s.send(ctx, protocol.NotificationRequest{Text: text}, 0, m.Chat.ID)
	if result.status == "sent" || result.status == "failed" {
		return nil
	}
	return errors.New("bot reply not confirmed")
}
func (s *Service) pollBot(ctx context.Context) {
	if s.token == "" {
		return
	}
	var offset int64
	var stored string
	if s.db.QueryRow(ctx, "SELECT value FROM notify.bot_state WHERE name='offset'").Scan(&stored) == nil {
		offset, _ = strconv.ParseInt(stored, 10, 64)
	}
	for ctx.Err() == nil {
		s.metaMu.Lock()
		named := s.botUsername != ""
		s.metaMu.Unlock()
		var err error
		if !named {
			var bot struct {
				Username string `json:"username"`
			}
			err = s.botCall(ctx, s.client, "getMe", map[string]any{}, &bot)
			if err == nil {
				s.metaMu.Lock()
				s.botUsername = bot.Username
				s.metaMu.Unlock()
			}
		}
		var updates []botUpdate
		if err == nil {
			err = s.botCall(ctx, s.pollClient, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, &updates)
		}
		s.metaMu.Lock()
		s.botOnline = err == nil
		s.metaMu.Unlock()
		if err == nil {
			for _, u := range updates {
				if u.ID < offset {
					continue
				}
				if err = s.handleUpdate(ctx, u); err != nil {
					break
				}
				next := u.ID + 1
				if _, err = s.db.Exec(ctx, "INSERT INTO notify.bot_state(name,value) VALUES('offset',$1) ON CONFLICT(name) DO UPDATE SET value=excluded.value", strconv.FormatInt(next, 10)); err != nil {
					break
				}
				offset = next
			}
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}
