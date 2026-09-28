package notifier

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed schema.sql
var schema string

type Service struct {
	db                 *pgxpool.Pool
	client, pollClient *http.Client
	base, token        string
	furnaces           []string
	metaMu             sync.Mutex
	botUsername        string
	botOnline          bool
	pairLimits         platform.Limiter
}

func encode(v any) []byte { b, _ := json.Marshal(v); return b }
func (s *Service) defaults() protocol.NotificationPreferences {
	return protocol.NotificationPreferences{Enabled: true, Recoveries: true, FurnaceIDs: append([]string{}, s.furnaces...), Categories: []string{"scan", "mes", "device", "workflow", "temperature", "rules"}}
}
func allows(p protocol.NotificationPreferences, n protocol.NotificationRequest) bool {
	return p.Enabled && slices.Contains(p.FurnaceIDs, n.FurnaceID) && slices.Contains(p.Categories, n.Category) && (n.Kind != "RECOVERY" || p.Recoveries)
}
func (s *Service) validPreferences(p protocol.NotificationPreferences) bool {
	if p.FurnaceIDs == nil || p.Categories == nil || len(p.FurnaceIDs) > 100 || len(p.Categories) > 6 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range p.FurnaceIDs {
		if !slices.Contains(s.furnaces, id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, kind := range p.Categories {
		if !slices.Contains([]string{"scan", "mes", "device", "workflow", "temperature", "rules"}, kind) || seen[kind] {
			return false
		}
		seen[kind] = true
	}
	return true
}
func (s *Service) work(ctx context.Context) {
	if s.token == "" {
		return
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id, user, binding string
	var chat int64
	var b []byte
	var attempts int
	err = tx.QueryRow(ctx, "SELECT id,request,attempts,user_id,chat_id,binding_id FROM notify.jobs WHERE status='pending' AND user_id IS NOT NULL AND next_attempt_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1").Scan(&id, &b, &attempts, &user, &chat, &binding)
	if err != nil {
		return
	}
	var n protocol.NotificationRequest
	if json.Unmarshal(b, &n) != nil {
		return
	}
	var bindingExists bool
	if tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM notify.bindings WHERE user_id=$1 AND binding_id=$2 AND chat_id=$3)", user, binding, chat).Scan(&bindingExists) != nil {
		return
	}
	p := s.defaults()
	var prefs []byte
	if tx.QueryRow(ctx, "SELECT doc FROM notify.preferences WHERE user_id=$1", user).Scan(&prefs) == nil {
		if json.Unmarshal(prefs, &p) != nil {
			return
		}
	}
	var suspended bool
	if tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM notify.account_access WHERE user_id=$1 AND NOT enabled)", user).Scan(&suspended) != nil {
		return
	}
	var closed bool
	if n.Kind == "REMINDER" && tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM notify.closed_rule_alarms WHERE alarm_id=$1)", n.AlarmID).Scan(&closed) != nil {
		return
	}
	if closed || suspended || !bindingExists || !allows(p, n) {
		_, err = tx.Exec(ctx, "UPDATE notify.jobs SET status='cancelled',last_error='绑定或个人通知偏好已变更' WHERE id=$1", id)
		if err == nil {
			_ = tx.Commit(ctx)
		}
		return
	}
	_, err = tx.Exec(ctx, "UPDATE notify.jobs SET status='unknown',attempts=attempts+1,last_error='发送进行中；进程中断时结果不确定' WHERE id=$1", id)
	if err != nil || tx.Commit(ctx) != nil {
		return
	}
	result := s.send(ctx, n, attempts, chat)
	_, _ = s.db.Exec(ctx, "UPDATE notify.jobs SET status=$2,last_error=$3,sent_at=$4,next_attempt_at=$5 WHERE id=$1", id, result.status, result.message, result.sent, result.next)
}
func (s *Service) enqueue(w http.ResponseWriter, r *http.Request) {
	var input protocol.NotificationRequest
	key := r.Header.Get("Idempotency-Key")
	if platform.Read(r, &input) != nil || len(key) < 8 || len(key) > 128 || input.ID == "" || input.AlarmID == "" || strings.TrimSpace(input.Text) == "" || len([]rune(input.Text)) > 4000 || (input.Kind != "ALARM" && input.Kind != "RECOVERY" && input.Kind != "REMINDER") {
		platform.Error(w, 400, "INVALID_NOTIFICATION", "通知事件格式无效")
		return
	}
	if input.Category == "" {
		input.Category = "workflow"
	}
	if !slices.Contains([]string{"scan", "mes", "device", "workflow", "temperature", "rules"}, input.Category) {
		platform.Error(w, 400, "INVALID_CATEGORY", "通知类别无效")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知存储不可用")
		return
	}
	defer tx.Rollback(r.Context())
	result, err := tx.Exec(r.Context(), "INSERT INTO notify.events(id,idempotency_key,request) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", input.ID, key, encode(input))
	if err != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知存储不可用")
		return
	}
	if result.RowsAffected() == 0 {
		var same bool
		if tx.QueryRow(r.Context(), "SELECT request=$3::jsonb FROM notify.events WHERE id=$1 OR idempotency_key=$2", input.ID, key, encode(input)).Scan(&same) != nil || !same {
			platform.Error(w, 409, "IDEMPOTENCY_CONFLICT", "事件编号已用于其他内容")
			return
		}
		platform.Accepted(w, input.ID)
		return
	}
	if input.Kind == "REMINDER" {
		var closed bool
		if tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM notify.closed_rule_alarms WHERE alarm_id=$1)", input.AlarmID).Scan(&closed) != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "提醒状态不可用")
			return
		}
		if closed {
			if tx.Commit(r.Context()) != nil {
				platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知提交失败")
				return
			}
			platform.Accepted(w, input.ID)
			return
		}
	}
	rows, err := tx.Query(r.Context(), "SELECT b.user_id,b.binding_id,b.chat_id,p.doc FROM notify.bindings b JOIN notify.preferences p ON p.user_id=b.user_id WHERE b.created_at <= $1 AND NOT EXISTS(SELECT 1 FROM notify.account_access a WHERE a.user_id=b.user_id AND NOT a.enabled)", input.CreatedAt)
	if err != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "接收者读取失败")
		return
	}
	type recipient struct {
		user, binding string
		chat          int64
		prefs         protocol.NotificationPreferences
	}
	recipients := []recipient{}
	for rows.Next() {
		var rec recipient
		var data []byte
		if rows.Scan(&rec.user, &rec.binding, &rec.chat, &data) != nil {
			rows.Close()
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "接收者读取失败")
			return
		}
		if json.Unmarshal(data, &rec.prefs) == nil && allows(rec.prefs, input) {
			recipients = append(recipients, rec)
		}
	}
	rows.Close()
	for _, rec := range recipients {
		if input.Kind == "REMINDER" || input.Kind == "RECOVERY" {
			if _, err = tx.Exec(r.Context(), "UPDATE notify.jobs SET status='cancelled',last_error='由较新的提醒或恢复消息替代' WHERE user_id=$1 AND status='pending' AND request->>'alarmId'=$2 AND request->>'kind'='REMINDER'", rec.user, input.AlarmID); err != nil {
				platform.Error(w, 503, "STORAGE_UNAVAILABLE", "提醒合并失败")
				return
			}
		}
		delivery := input
		delivery.ID = protocol.ID()
		if _, err = tx.Exec(r.Context(), "INSERT INTO notify.jobs(id,idempotency_key,request,user_id,chat_id,binding_id,event_id) VALUES($1,$2,$3,$4,$5,$6,$7)", delivery.ID, input.ID+":"+rec.user, encode(delivery), rec.user, rec.chat, rec.binding, input.ID); err != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "投递创建失败")
			return
		}
	}
	if tx.Commit(r.Context()) != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知提交失败")
		return
	}
	platform.Accepted(w, input.ID)
}
func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("userId")
	p, size := 1, 20
	var err error
	if v := r.URL.Query().Get("page"); v != "" {
		p, err = strconv.Atoi(v)
	}
	if err == nil {
		if v := r.URL.Query().Get("pageSize"); v != "" {
			size, err = strconv.Atoi(v)
		}
	}
	if user == "" || err != nil || p < 1 || size < 1 || size > 100 {
		platform.Error(w, 400, "INVALID_QUERY", "需要用户编号和有效分页参数")
		return
	}
	var total int
	if s.db.QueryRow(r.Context(), "SELECT count(*) FROM notify.jobs WHERE user_id=$1", user).Scan(&total) != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知存储不可用")
		return
	}
	rows, err := s.db.Query(r.Context(), "SELECT request,status,attempts,last_error,sent_at,next_attempt_at FROM notify.jobs WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3", user, size, (p-1)*size)
	if err != nil {
		platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知存储不可用")
		return
	}
	defer rows.Close()
	items := []protocol.Notification{}
	for rows.Next() {
		var b []byte
		var n protocol.Notification
		if rows.Scan(&b, &n.Status, &n.Attempts, &n.LastError, &n.SentAt, &n.NextAttemptAt) != nil {
			platform.Error(w, 503, "STORAGE_UNAVAILABLE", "通知读取失败")
			return
		}
		if json.Unmarshal(b, &n.NotificationRequest) == nil {
			items = append(items, n)
		}
	}
	platform.JSON(w, 200, protocol.Page[protocol.Notification]{Items: items, Pagination: protocol.Pagination{Page: p, PageSize: size, Total: total}})
}
func Run(ctx context.Context) error {
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.Exec(ctx, schema); err != nil {
		return err
	}
	lock, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	var acquired bool
	if err = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(7614863)").Scan(&acquired); err != nil || !acquired {
		return errors.New("another notifier owns this bot consumer")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy := os.Getenv("TELEGRAM_PROXY"); proxy != "" {
		u, x := url.Parse(proxy)
		if x != nil || u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("invalid TELEGRAM_PROXY")
		}
		transport.Proxy = http.ProxyURL(u)
	}
	cfg, err := platform.Config()
	if err != nil {
		return err
	}
	ids := []string{}
	for _, f := range cfg {
		ids = append(ids, f.ID)
	}
	s := &Service{db: db, client: &http.Client{Timeout: 8 * time.Second, Transport: transport}, pollClient: &http.Client{Timeout: 35 * time.Second, Transport: transport}, base: platform.Env("TELEGRAM_API_BASE", "https://api.telegram.org"), token: os.Getenv("TELEGRAM_TOKEN"), furnaces: ids}
	go s.pollBot(ctx)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.work(ctx)
			}
		}
	}()
	m := platform.Mux("notifier", func() bool {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return db.Ping(c) == nil
	})
	m.HandleFunc("PUT /internal/v1/account-access", s.syncAccess)
	m.HandleFunc("POST /internal/v1/notifications/{alarmId}/cancel-reminders", s.cancelReminders)
	m.HandleFunc("POST /internal/v1/notifications", s.enqueue)
	m.HandleFunc("GET /internal/v1/notifications", s.list)
	s.pairRoutes(m)
	return platform.Serve(ctx, "notifier", m)
}
