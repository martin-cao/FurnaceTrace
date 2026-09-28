package notifier

import (
	"context"
	"encoding/json"
	"furnace.local/iot/internal/protocol"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTelegramRateLimitThenAccepted(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]string
		if r.URL.Path != "/botdemo/sendMessage" || json.NewDecoder(r.Body).Decode(&body) != nil || body["chat_id"] != "99" || body["text"] != "入炉报警" {
			t.Error("incorrect Telegram request")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()
	s := Service{client: server.Client(), base: server.URL, token: "demo"}
	n := protocol.NotificationRequest{Text: "入炉报警"}
	first := s.send(context.Background(), n, 0, 99)
	if first.status != "pending" || time.Until(first.next) < time.Second {
		t.Fatalf("429 did not schedule retry: %+v", first)
	}
	second := s.send(context.Background(), n, 1, 99)
	if second.status != "sent" || second.sent == nil {
		t.Fatalf("successful response not recorded: %+v", second)
	}
}

func TestLostTelegramResponseRemainsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = c.Close()
		}
	}))
	defer server.Close()
	s := Service{client: server.Client(), base: server.URL, token: "demo"}
	result := s.send(context.Background(), protocol.NotificationRequest{Text: "demo"}, 0, 99)
	if result.status != "unknown" {
		t.Fatalf("lost response was treated as safe to retry: %+v", result)
	}
}
