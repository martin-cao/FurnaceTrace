package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type identityKey struct{}
type identity struct {
	user    protocol.User
	digest  string
	expires time.Time
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
var loginLimits platform.Limiter

func digestToken(raw string) string { v := sha256.Sum256([]byte(raw)); return hex.EncodeToString(v[:]) }
func currentIdentity(r *http.Request) identity {
	v, _ := r.Context().Value(identityKey{}).(identity)
	return v
}
func requestUser(r *http.Request) protocol.User { return currentIdentity(r).user }
func validPassword(v string) bool               { return utf8.RuneCountInString(v) >= 8 && len(v) <= 72 }

func (e *Engine) bootstrapAdmin(ctx context.Context) error {
	var exists bool
	if err := e.store.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.users WHERE role='admin')").Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	name := strings.ToLower(platform.Env("ADMIN_USERNAME", "admin"))
	password := os.Getenv("ADMIN_PASSWORD")
	if !usernamePattern.MatchString(name) || !validPassword(password) {
		return errors.New("configure a valid ADMIN_USERNAME and ADMIN_PASSWORD before starting core")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	_, err = e.store.db.Exec(ctx, "INSERT INTO core.users(id,username,display_name,password_hash,role) VALUES($1,$2,'管理员',$3,'admin')", protocol.ID(), name, string(hash))
	return err
}
func (e *Engine) session(ctx context.Context, r *http.Request) (identity, error) {
	cookie, err := r.Cookie("iot_session")
	if err != nil || len(cookie.Value) != 64 {
		return identity{}, errors.New("no session")
	}
	id := identity{digest: digestToken(cookie.Value)}
	err = e.store.db.QueryRow(ctx, `SELECT u.id,u.username,u.display_name,u.role,s.expires_at FROM core.sessions s JOIN core.users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now() AND u.enabled`, id.digest).Scan(&id.user.ID, &id.user.Username, &id.user.DisplayName, &id.user.Role, &id.expires)
	return id, err
}
func (e *Engine) authenticated(h http.Handler, scada bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		protected := scada || strings.HasPrefix(r.URL.Path, "/api/v1/") || strings.HasPrefix(r.URL.Path, "/media/")
		if !scada && (r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/register") {
			protected = false
		}
		if !protected {
			h.ServeHTTP(w, r)
			return
		}
		id, err := e.session(r.Context(), r)
		if err != nil {
			platform.Error(w, 401, "LOGIN_REQUIRED", "请先登录")
			return
		}
		adminAction := scada || r.Method == "POST" && (strings.Contains(r.URL.Path, "/manual-scans") || strings.HasSuffix(r.URL.Path, "/resets") || strings.HasSuffix(r.URL.Path, "/acknowledgements"))
		adminAction = adminAction || strings.HasPrefix(r.URL.Path, "/api/v1/users") || strings.HasSuffix(r.URL.Path, "/camera-config")
		adminAction = adminAction || (r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE") && (strings.HasPrefix(r.URL.Path, "/api/v1/batches") || strings.HasPrefix(r.URL.Path, "/api/v1/baskets") || strings.HasPrefix(r.URL.Path, "/api/v1/alarm-rules"))
		if adminAction && id.user.Role != "admin" {
			platform.Error(w, 403, "ADMIN_REQUIRED", "此操作需要管理员权限")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
	})
}
func (e *Engine) newSession(ctx context.Context, tx pgx.Tx, user protocol.User) (string, protocol.SessionResponse, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", protocol.SessionResponse{}, err
	}
	raw := hex.EncodeToString(key)
	until := time.Now().UTC().Add(12 * time.Hour)
	_, err := tx.Exec(ctx, "INSERT INTO core.sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", digestToken(raw), user.ID, until)
	return raw, protocol.SessionResponse{User: user, ExpiresAt: until}, err
}
func setSessionCookie(w http.ResponseWriter, r *http.Request, raw string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "iot_session", Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || os.Getenv("COOKIE_SECURE") == "true", MaxAge: maxAge})
}
func rateAuth(w http.ResponseWriter, r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !loginLimits.Allow(ip, 12, time.Minute) {
		w.Header().Set("Retry-After", "60")
		platform.Error(w, 429, "TOO_MANY_ATTEMPTS", "尝试过于频繁，请稍后再试")
		return false
	}
	return true
}
func (e *Engine) authRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		if !rateAuth(w, r) {
			return
		}
		var in protocol.RegisterRequest
		if platform.Read(r, &in) != nil || !usernamePattern.MatchString(in.Username) || !validPassword(in.Password) || strings.TrimSpace(in.DisplayName) == "" || utf8.RuneCountInString(in.DisplayName) > 50 {
			platform.Error(w, 400, "INVALID_ACCOUNT", "用户名需为 3–32 位字母数字或下划线；密码至少 8 字符，最多 72 字节")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		if err != nil {
			writeError(w, err)
			return
		}
		user := protocol.User{ID: protocol.ID(), Username: strings.ToLower(in.Username), DisplayName: strings.TrimSpace(in.DisplayName), Role: "user"}
		tx, err := e.store.db.Begin(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback(r.Context())
		result, err := tx.Exec(r.Context(), "INSERT INTO core.users(id,username,display_name,password_hash,role) VALUES($1,$2,$3,$4,'user') ON CONFLICT(username) DO NOTHING", user.ID, user.Username, user.DisplayName, string(hash))
		if err != nil {
			writeError(w, err)
			return
		}
		if result.RowsAffected() == 0 {
			platform.Error(w, 409, "USERNAME_TAKEN", "用户名已被使用")
			return
		}
		raw, response, err := e.newSession(r.Context(), tx, user)
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			writeError(w, err)
			return
		}
		setSessionCookie(w, r, raw, 43200)
		platform.JSON(w, 201, response)
	})
	m.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !rateAuth(w, r) {
			return
		}
		var in protocol.LoginRequest
		if platform.Read(r, &in) != nil || !usernamePattern.MatchString(in.Username) || len(in.Password) > 72 {
			platform.Error(w, 400, "INVALID_LOGIN", "登录信息格式无效")
			return
		}
		var user protocol.User
		var hash string
		err := e.store.db.QueryRow(r.Context(), "SELECT id,username,display_name,role,password_hash FROM core.users WHERE username=$1 AND enabled", strings.ToLower(in.Username)).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &hash)
		if err != nil && err != pgx.ErrNoRows {
			writeError(w, err)
			return
		}
		if err == pgx.ErrNoRows {
			hash = "$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || user.ID == "" {
			platform.Error(w, 401, "INVALID_CREDENTIALS", "用户名或密码不正确")
			return
		}
		tx, err := e.store.db.Begin(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback(r.Context())
		var currentHash string
		if err = tx.QueryRow(r.Context(), "SELECT password_hash FROM core.users WHERE id=$1 AND enabled FOR UPDATE", user.ID).Scan(&currentHash); err != nil || currentHash != hash {
			platform.Error(w, 401, "INVALID_CREDENTIALS", "账户信息已变更，请重新登录")
			return
		}
		raw, response, err := e.newSession(r.Context(), tx, user)
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			writeError(w, err)
			return
		}
		setSessionCookie(w, r, raw, 43200)
		platform.JSON(w, 200, response)
	})
	m.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) { platform.JSON(w, 200, requestUser(r)) })
	m.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if _, err := e.store.db.Exec(r.Context(), "DELETE FROM core.sessions WHERE token_hash=$1", currentIdentity(r).digest); err != nil {
			writeError(w, err)
			return
		}
		setSessionCookie(w, r, "", -1)
		w.WriteHeader(204)
	})
}
func (e *Engine) serveSCADA(ctx context.Context) {
	u, _ := url.Parse(platform.Env("FUXA_URL", "http://fuxa:1881"))
	proxy := httputil.NewSingleHostReverseProxy(u)
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			proxy.ServeHTTP(w, r)
			return
		}
		connection, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			tick := time.NewTicker(5 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-connection.Done():
					return
				case <-tick.C:
					if id, err := e.session(connection, r); err != nil || id.user.Role != "admin" {
						cancel()
						return
					}
				}
			}
		}()
		proxy.ServeHTTP(w, r.WithContext(connection))
	})
	srv := &http.Server{Addr: platform.Env("SCADA_HTTP_ADDR", ":8081"), Handler: platform.Internal(e.authenticated(guarded, true)), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(stop)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "SCADA proxy could not start")
	}
}
