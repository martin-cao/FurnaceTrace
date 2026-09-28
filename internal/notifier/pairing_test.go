package notifier

import (
	"bytes"
	"context"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPairingExpirySingleUseAndPersonalRouting(t *testing.T) {
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
	if _, err = db.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS notify"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
	s := Service{db: db, furnaces: []string{"f1", "f2"}}
	a, b := protocol.ID(), protocol.ID()
	putCode := func(chat int64, code string, expires time.Time) {
		t.Helper()
		if _, err := db.Exec(ctx, "INSERT INTO notify.pair_codes(chat_id,code_hash,update_id,display_name,expires_at) VALUES($1,$2,$1,'test-chat',$3) ON CONFLICT(chat_id) DO UPDATE SET code_hash=excluded.code_hash,expires_at=excluded.expires_at,used_by=NULL,binding_id=NULL", chat, codeDigest(code), expires); err != nil {
			t.Fatal(err)
		}
	}
	putCode(991, "EXPR1234", time.Now().Add(-time.Minute))
	if _, err = s.pair(ctx, a, "EXPR1234"); err == nil {
		t.Fatal("expired pair code accepted")
	}
	putCode(992, "PAIR2345", time.Now().Add(time.Minute))
	if _, err = s.pair(ctx, a, "pair-2345"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pair(ctx, b, "PAIR2345"); err == nil {
		t.Fatal("another account consumed used code")
	}
	if _, err = s.pair(ctx, a, "PAIR2345"); err != nil {
		t.Fatal("same binding retry failed")
	}
	putCode(993, "PAIR6789", time.Now().Add(time.Minute))
	if _, err = s.pair(ctx, b, "PAIR6789"); err != nil {
		t.Fatal(err)
	}
	pA := protocol.NotificationPreferences{Enabled: true, FurnaceIDs: []string{"f1"}, Categories: []string{"device"}, Recoveries: true}
	pB := protocol.NotificationPreferences{Enabled: true, FurnaceIDs: []string{"f2"}, Categories: []string{"device"}, Recoveries: false}
	for user, p := range map[string]protocol.NotificationPreferences{a: pA, b: pB} {
		if _, err = db.Exec(ctx, "UPDATE notify.preferences SET doc=$2 WHERE user_id=$1", user, encode(p)); err != nil {
			t.Fatal(err)
		}
	}
	n := protocol.NotificationRequest{ID: protocol.ID(), AlarmID: protocol.ID(), FurnaceID: "f1", Category: "device", Kind: "ALARM", Text: "private routing test", CreatedAt: time.Now().UTC()}
	r := httptest.NewRequest("POST", "/internal/v1/notifications", bytes.NewReader(encode(n)))
	r.Header.Set("Idempotency-Key", n.ID)
	w := httptest.NewRecorder()
	s.enqueue(w, r)
	if w.Code != 202 {
		t.Fatalf("enqueue: %d %s", w.Code, w.Body.String())
	}
	var countA, countB int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM notify.jobs WHERE user_id=$1", a).Scan(&countA)
	_ = db.QueryRow(ctx, "SELECT count(*) FROM notify.jobs WHERE user_id=$1", b).Scan(&countB)
	if countA != 1 || countB != 0 {
		t.Fatalf("preferences not isolated: A=%d B=%d", countA, countB)
	}
	if err = s.unpair(ctx, a); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = db.QueryRow(ctx, "SELECT status FROM notify.jobs WHERE user_id=$1", a).Scan(&status)
	if status != "cancelled" {
		t.Fatal("unbind left a pending delivery")
	}
	if _, err = s.pair(ctx, a, "PAIR2345"); err == nil {
		t.Fatal("unlinked code became reusable")
	}
}

func TestPreferenceChangeCancelsOnlyOwnPending(t *testing.T) {
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
	if _, err = db.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS notify"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
	s := Service{db: db, furnaces: []string{"f1"}}
	user, other := protocol.ID(), protocol.ID()
	for _, u := range []string{user, other} {
		n := protocol.NotificationRequest{ID: protocol.ID(), AlarmID: protocol.ID(), FurnaceID: "f1", Category: "device", Kind: "ALARM", Text: "test", CreatedAt: time.Now()}
		if _, err = db.Exec(ctx, "INSERT INTO notify.jobs(id,idempotency_key,request,user_id,chat_id,binding_id) VALUES($1,$1,$2,$3,1,'test-binding')", n.ID, encode(n), u); err != nil {
			t.Fatal(err)
		}
	}
	p := s.defaults()
	p.Enabled = false
	m := http.NewServeMux()
	s.pairRoutes(m)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("PUT", "/internal/v1/users/"+user+"/notification-preferences", bytes.NewReader(encode(p))))
	if w.Code != 200 {
		t.Fatalf("save preferences: %d %s", w.Code, w.Body.String())
	}
	for u, want := range map[string]string{user: "cancelled", other: "pending"} {
		var got string
		if err = db.QueryRow(ctx, "SELECT status FROM notify.jobs WHERE user_id=$1", u).Scan(&got); err != nil || got != want {
			t.Fatalf("user preference crossed boundary: %s %v", got, err)
		}
	}
}
