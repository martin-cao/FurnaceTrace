package notifier

import (
	"bytes"
	"context"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRuleRemindersCoalesceAndStopAfterRecovery(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("requires isolated TEST_DATABASE_URL")
	}
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("use dedicated test DB")
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
	user, alarm := protocol.ID(), protocol.ID()
	if _, err = db.Exec(ctx, "INSERT INTO notify.bindings(user_id,binding_id,chat_id,display_name) VALUES($1,$2,123456789,'test')", user, protocol.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "INSERT INTO notify.preferences(user_id,doc) VALUES($1,$2)", user, encode(s.defaults())); err != nil {
		t.Fatal(err)
	}
	send := func() {
		t.Helper()
		n := protocol.NotificationRequest{ID: protocol.ID(), AlarmID: alarm, FurnaceID: "f1", Kind: "REMINDER", Category: "rules", Text: "持续提醒测试", CreatedAt: time.Now().UTC()}
		r := httptest.NewRequest("POST", "/internal/v1/notifications", bytes.NewReader(encode(n)))
		r.Header.Set("Idempotency-Key", n.ID)
		w := httptest.NewRecorder()
		s.enqueue(w, r)
		if w.Code != 202 {
			t.Fatalf("enqueue=%d %s", w.Code, w.Body.String())
		}
	}
	count := func(status string) int {
		var n int
		db.QueryRow(ctx, "SELECT count(*) FROM notify.jobs WHERE user_id=$1 AND status=$2", user, status).Scan(&n)
		return n
	}
	send()
	send()
	if count("pending") != 1 || count("cancelled") != 1 {
		t.Fatal("pending reminders accumulated")
	}
	r := httptest.NewRequest("POST", "/internal/v1/notifications/"+alarm+"/cancel-reminders", nil)
	r.SetPathValue("alarmId", alarm)
	w := httptest.NewRecorder()
	s.cancelReminders(w, r)
	if w.Code != 204 || count("pending") != 0 {
		t.Fatal("reminders survived recovery")
	}
	send()
	if count("pending") != 0 {
		t.Fatal("late reminder recreated a closed alarm delivery")
	}
}
