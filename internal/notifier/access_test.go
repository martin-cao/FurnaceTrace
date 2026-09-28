package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAccountSuspensionRejectsStaleEnable(t *testing.T) {
	address := os.Getenv("TEST_DATABASE_URL")
	if address == "" {
		t.Skip("requires isolated TEST_DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(address)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("use isolated _test database")
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
	s := Service{db: db}
	user, other := protocol.ID(), protocol.ID()
	for _, id := range []string{user, other} {
		if _, err = db.Exec(ctx, "INSERT INTO notify.jobs(id,idempotency_key,request,user_id) VALUES($1,$1,'{}',$1)", id); err != nil {
			t.Fatal(err)
		}
	}
	sync := func(enabled bool, version int64) int {
		b, _ := json.Marshal(protocol.AccountAccess{UserID: user, Enabled: enabled, Version: version})
		w := httptest.NewRecorder()
		s.syncAccess(w, httptest.NewRequest("PUT", "/internal/v1/account-access", bytes.NewReader(b)))
		return w.Code
	}
	if sync(false, 2) != 204 || sync(true, 1) != 204 {
		t.Fatal("access sync failed")
	}
	var enabled bool
	db.QueryRow(ctx, "SELECT enabled FROM notify.account_access WHERE user_id=$1", user).Scan(&enabled)
	if enabled {
		t.Fatal("old update resumed account")
	}
	var status string
	db.QueryRow(ctx, "SELECT status FROM notify.jobs WHERE id=$1", user).Scan(&status)
	if status != "cancelled" {
		t.Fatal("pending notification survived suspension")
	}
	db.QueryRow(ctx, "SELECT status FROM notify.jobs WHERE id=$1", other).Scan(&status)
	if status != "pending" {
		t.Fatal("other user affected")
	}
	if sync(true, 2) != 409 {
		t.Fatal("same version allowed conflicting state")
	}
	if sync(true, 3) != 204 {
		t.Fatal("new enable failed")
	}
	db.QueryRow(ctx, "SELECT status FROM notify.jobs WHERE id=$1", user).Scan(&status)
	if status != "cancelled" {
		t.Fatal("reactivation replayed cancelled notifications")
	}
}
