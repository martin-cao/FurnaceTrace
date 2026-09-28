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
	"time"
)

func settingsDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	address := os.Getenv("TEST_DATABASE_URL")
	if address == "" {
		t.Skip("requires isolated TEST_DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(address)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("use a dedicated _test database")
	}
	db, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err = db.Exec(context.Background(), "CREATE SCHEMA IF NOT EXISTS core"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(context.Background(), schema); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestSettingsAccountAccessAndPassword(t *testing.T) {
	db := settingsDB(t)
	ctx := context.Background()
	e := Engine{store: &Store{db: db}}
	mux := http.NewServeMux()
	e.authRoutes(mux)
	e.accountSettingsRoutes(mux)
	e.cameraSettingsRoutes(mux)
	h := platform.Internal(e.authenticated(mux, false))
	request := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	register := func() (protocol.User, *http.Cookie) {
		name := "settings_" + protocol.ID()[:8]
		w := request("POST", "/api/v1/auth/register", protocol.RegisterRequest{Username: name, DisplayName: "设置测试", Password: "temporary-pass-123"}, nil)
		if w.Code != 201 {
			t.Fatalf("registration=%d %s", w.Code, w.Body.String())
		}
		var s protocol.SessionResponse
		json.Unmarshal(w.Body.Bytes(), &s)
		return s.User, w.Result().Cookies()[0]
	}
	admin, ac := register()
	user, uc := register()
	if _, err := db.Exec(ctx, "UPDATE core.users SET role='admin' WHERE id=$1", admin.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/users", "/api/v1/furnaces/f1/camera-config"} {
		if w := request("GET", path, nil, uc); w.Code != 403 {
			t.Fatalf("user accessed %s: %d", path, w.Code)
		}
	}
	profile := protocol.ProfileUpdate{Username: user.Username + "_new", DisplayName: "新的名称", CurrentPassword: "wrong"}
	if w := request("PUT", "/api/v1/me/profile", profile, uc); w.Code != 403 {
		t.Fatal("wrong password accepted")
	}
	profile.CurrentPassword = "temporary-pass-123"
	w := request("PUT", "/api/v1/me/profile", profile, uc)
	if w.Code != 200 {
		t.Fatalf("profile=%d %s", w.Code, w.Body.String())
	}
	var updated protocol.User
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.ID != user.ID || updated.Username != profile.Username {
		t.Fatal("profile lost identity")
	}
	profile.Username = admin.Username
	if w = request("PUT", "/api/v1/me/profile", profile, uc); w.Code != 409 {
		t.Fatal("duplicate username accepted")
	}
	w = request("PUT", "/api/v1/me/password", protocol.PasswordChange{CurrentPassword: "temporary-pass-123", NewPassword: "replacement-pass-456"}, uc)
	if w.Code != 204 {
		t.Fatalf("password=%d", w.Code)
	}
	if w = request("GET", "/api/v1/me", nil, uc); w.Code != 401 {
		t.Fatal("old session survived password change")
	}
	w = request("POST", "/api/v1/auth/login", protocol.LoginRequest{Username: updated.Username, Password: "replacement-pass-456"}, nil)
	if w.Code != 200 {
		t.Fatal("new password failed")
	}
	uc = w.Result().Cookies()[0]
	yes, no := true, false
	if w = request("PUT", "/api/v1/users/"+admin.ID+"/access", protocol.UserAccessUpdate{Role: "user", Enabled: &yes}, ac); w.Code != 409 {
		t.Fatal("self demotion accepted")
	}
	if w = request("PUT", "/api/v1/users/"+user.ID+"/access", protocol.UserAccessUpdate{Role: "admin", Enabled: &yes}, uc); w.Code != 403 {
		t.Fatal("self escalation accepted")
	}
	if w = request("PUT", "/api/v1/users/"+user.ID+"/access", protocol.UserAccessUpdate{Role: "user", Enabled: &no}, ac); w.Code != 200 {
		t.Fatalf("disable=%d", w.Code)
	}
	if w = request("GET", "/api/v1/me", nil, uc); w.Code != 401 {
		t.Fatal("disabled session survived")
	}
	if w = request("POST", "/api/v1/auth/login", protocol.LoginRequest{Username: updated.Username, Password: "replacement-pass-456"}, nil); w.Code != 401 {
		t.Fatal("disabled account logged in")
	}
	var jobs int
	db.QueryRow(ctx, "SELECT count(*) FROM core.outbox WHERE kind='account-access' AND payload->>'userId'=$1", user.ID).Scan(&jobs)
	if jobs != 1 {
		t.Fatal("missing durable notification suspension")
	}
}
func TestCameraConfigPersistsAppliesAndGuardsCycle(t *testing.T) {
	db := settingsDB(t)
	ctx := context.Background()
	t.Setenv("CAMERA_ENCRYPTION_KEY", strings.Repeat("12", 32))
	t.Setenv("SERVICE_TOKEN", "media-test-token")
	source := "rtsp://127.0.0.1:8554/f1-sim"
	transport := "tcp"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "controller" || pass != "media-test-token" {
			w.WriteHeader(401)
			return
		}
		if r.Method == "PATCH" {
			var in struct {
				Source    string `json:"source"`
				Transport string `json:"rtspTransport"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			source = in.Source
			transport = in.Transport
		}
		json.NewEncoder(w).Encode(map[string]string{"source": source, "rtspTransport": transport})
	}))
	defer upstream.Close()
	t.Setenv("MEDIA_API_URL", upstream.URL)
	e := Engine{store: &Store{db: db}, cfg: []platform.FurnaceConfig{{ID: "f1"}}, active: map[string]record{}, cameras: map[string]protocol.CameraState{}}
	if err := e.loadCameraSettings(ctx); err != nil {
		t.Fatal(err)
	}
	m := http.NewServeMux()
	e.cameraSettingsRoutes(m)
	put := func(in protocol.CameraConfigUpdate) *httptest.ResponseRecorder {
		b, _ := json.Marshal(in)
		r := httptest.NewRequest("PUT", "/api/v1/furnaces/f1/camera-config", bytes.NewReader(b))
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, identity{user: protocol.User{ID: "camera-test", Username: "admin", Role: "admin"}}))
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w
	}
	address := "rtsp://camera:private-password@192.168.1.50:554/live?secret=value"
	w := put(protocol.CameraConfigUpdate{Mode: "rtsp", RTSPURL: &address})
	if w.Code != 200 {
		t.Fatalf("camera save=%d %s", w.Code, w.Body.String())
	}
	var visible protocol.CameraConfig
	if json.Unmarshal(w.Body.Bytes(), &visible) != nil || visible.RTSPURL != address {
		t.Fatal("administrator cannot read the saved RTSP address")
	}

	_, encrypted, err := e.cameraConfig(ctx, "f1")
	if err != nil || bytes.Contains(encrypted, []byte(address)) {
		t.Fatal("camera URL not encrypted")
	}
	e.reconcileCameras(ctx)
	if source != address || transport != "tcp" {
		t.Fatal("desired source did not reach MediaMTX")
	}
	before, _, _ := e.cameraConfig(ctx, "f1")
	put(protocol.CameraConfigUpdate{Mode: "rtsp"})
	after, _, _ := e.cameraConfig(ctx, "f1")
	if before.Revision != after.Revision {
		t.Fatal("same PUT changed revision")
	}
	e.active["f1"] = record{}
	if w = put(protocol.CameraConfigUpdate{Mode: "simulated"}); w.Code != 409 {
		t.Fatal("source changed during active cycle")
	}
	delete(e.active, "f1")
	// Simulate a MediaMTX restart losing its runtime patch, then reconcile from PostgreSQL.
	source = "rtsp://127.0.0.1:8554/f1-sim"
	e.reconcileCameras(ctx)
	if source != address {
		t.Fatal("persisted camera source not restored")
	}
	bad := "http://192.168.1.50/"
	if w = put(protocol.CameraConfigUpdate{Mode: "rtsp", RTSPURL: &bad}); w.Code != 400 {
		t.Fatal("non RTSP URL accepted")
	}
	if !e.cameraSwitchUntil["f1"].After(time.Now()) {
		t.Fatal("missing decoder reconnect grace")
	}
}
