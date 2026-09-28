package core

import (
	"bytes"
	"context"
	"encoding/json"
	"furnace.local/iot/internal/protocol"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUserAccessDoesNotWaitForNotifierLease(t *testing.T) {
	c := newCatalogTest(t, "lock-f1")
	ctx := context.Background()
	m := http.NewServeMux()
	c.e.accountSettingsRoutes(m)
	c.e.cameraSettingsRoutes(m)
	h := c.e.authenticated(m, false)
	var target string
	if err := c.db.QueryRow(ctx, "SELECT user_id FROM core.sessions WHERE token_hash=$1", digestToken(c.user.Value)).Scan(&target); err != nil {
		t.Fatal(err)
	}
	reader := httptest.NewRequest("GET", "/api/v1/furnaces/lock-f1/camera-config", nil)
	reader.AddCookie(c.user)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, reader)
	if response.Code != 403 {
		t.Fatal("ordinary user could read camera credentials")
	}
	lease, err := c.db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = lease.Exec(ctx, "SELECT pg_advisory_unlock(7614863)"); lease.Release() }()
	if _, err = lease.Exec(ctx, "SELECT pg_advisory_lock(7614863)"); err != nil {
		t.Fatal(err)
	}
	change := func(role string) *httptest.ResponseRecorder {
		t.Helper()
		enabled := true
		b, _ := json.Marshal(protocol.UserAccessUpdate{Role: role, Enabled: &enabled})
		deadline, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		r := httptest.NewRequest("PUT", "/api/v1/users/"+target+"/access", bytes.NewReader(b)).WithContext(deadline)
		r.AddCookie(c.admin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := change("admin"); w.Code != 200 {
		t.Fatalf("notifier lease blocked promotion: %d %s", w.Code, w.Body.String())
	}
	tx, err := lease.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(7614862,1)"); err != nil {
		t.Fatal(err)
	}
	if w := change("user"); w.Code != 409 || !bytes.Contains(w.Body.Bytes(), []byte("ACCESS_UPDATE_BUSY")) {
		t.Fatalf("contended edit did not fail promptly: %d", w.Code)
	}
	var role string
	if err = c.db.QueryRow(ctx, "SELECT role FROM core.users WHERE id=$1", target).Scan(&role); err != nil || role != "admin" {
		t.Fatal("failed edit altered privileges")
	}
}
