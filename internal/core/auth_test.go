package core

import (
	"bytes"
	"context"
	"encoding/json"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAccountSessionAndAdminBoundary(t *testing.T) {
	address := os.Getenv("TEST_DATABASE_URL")
	if address == "" {
		t.Skip("requires isolated TEST_DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(address)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("use a dedicated _test database")
	}
	ctx := context.Background()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS core"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
	e := Engine{store: &Store{db: db}}
	mux := http.NewServeMux()
	e.authRoutes(mux)
	mux.HandleFunc("POST /api/v1/furnaces/f1/resets", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := platform.Internal(e.authenticated(mux, false))
	request := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/v1/me", nil, nil); w.Code != 401 {
		t.Fatalf("anonymous access=%d", w.Code)
	}
	name := "user_" + strings.ReplaceAll(protocol.ID()[:8], "-", "")
	w := request("POST", "/api/v1/auth/register", protocol.RegisterRequest{Username: name, Password: "test-password-123", DisplayName: "测试用户"}, nil)
	if w.Code != 201 {
		t.Fatalf("registration: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("invalid session cookie")
	}
	var response protocol.SessionResponse
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.User.Role != "user" {
		t.Fatal("self-registration granted privileges")
	}
	if w = request("POST", "/api/v1/furnaces/f1/resets", nil, cookies[0]); w.Code != 403 {
		t.Fatal("normal user reached control endpoint")
	}
	if w = request("GET", "/api/v1/me", nil, cookies[0]); w.Code != 200 {
		t.Fatal("session did not authenticate")
	}
	if w = request("POST", "/api/v1/auth/logout", nil, cookies[0]); w.Code != 204 {
		t.Fatal("logout failed")
	}
	if w = request("GET", "/api/v1/me", nil, cookies[0]); w.Code != 401 {
		t.Fatal("revoked session remained valid")
	}
}
