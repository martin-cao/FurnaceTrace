package core

import (
	"errors"
	"fmt"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"net/http"
	"strings"
)

func notificationCategory(code string) string {
	if strings.HasPrefix(code, "RULE_") {
		return "rules"
	}
	if code == "TEMPERATURE_HIGH" {
		return "temperature"
	}
	if strings.HasPrefix(code, "MES_") || code == "BASKET_OCCUPIED" {
		return "mes"
	}
	if code == "SCAN_TIMEOUT" || code == "AMBIGUOUS" || code == "INVALID_BARCODE" {
		return "scan"
	}
	if strings.HasSuffix(code, "OFFLINE") || code == "DEVICE_RESTARTED" {
		return "device"
	}
	return "workflow"
}
func personalError(w http.ResponseWriter, err error, pair bool) {
	var upstream platform.HTTPError
	if errors.As(err, &upstream) {
		if upstream.Status == 429 {
			w.Header().Set("Retry-After", "60")
			platform.Error(w, 429, "TOO_MANY_ATTEMPTS", "配对尝试过于频繁，请稍后再试")
			return
		}
		if upstream.Status == 400 || upstream.Status == 409 {
			msg := "通知设置无效"
			if pair {
				msg = "配对码无效、过期或账户已绑定；请先检查绑定状态，必要时重新 /start"
			}
			platform.Error(w, upstream.Status, "PAIRING_OR_PREFERENCES_INVALID", msg)
			return
		}
	}
	platform.Error(w, 503, "NOTIFIER_UNAVAILABLE", "通知服务暂不可用")
}
func (e *Engine) personalRoutes(m *http.ServeMux) {
	base := platform.Env("NOTIFIER_URL", "http://notifier:8080")
	path := func(r *http.Request) string { return base + "/internal/v1/users/" + requestUser(r).ID }
	m.HandleFunc("GET /api/v1/me/telegram", func(w http.ResponseWriter, r *http.Request) {
		var state protocol.TelegramState
		if err := platform.Request(r.Context(), "GET", path(r)+"/telegram", "", nil, &state); err != nil {
			personalError(w, err, false)
			return
		}
		platform.JSON(w, 200, state)
	})
	m.HandleFunc("POST /api/v1/me/telegram/pair", func(w http.ResponseWriter, r *http.Request) {
		var input protocol.PairRequest
		if platform.Read(r, &input) != nil || len(input.Code) > 20 {
			platform.Error(w, 400, "INVALID_PAIR_CODE", "配对码格式无效")
			return
		}
		var state protocol.TelegramState
		if err := platform.Request(r.Context(), "POST", path(r)+"/telegram/pair", "", input, &state); err != nil {
			personalError(w, err, true)
			return
		}
		platform.JSON(w, 200, state)
	})
	m.HandleFunc("DELETE /api/v1/me/telegram", func(w http.ResponseWriter, r *http.Request) {
		if err := platform.Request(r.Context(), "DELETE", path(r)+"/telegram", "", nil, nil); err != nil {
			personalError(w, err, false)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("PUT /api/v1/me/notification-preferences", func(w http.ResponseWriter, r *http.Request) {
		var input, output protocol.NotificationPreferences
		if platform.Read(r, &input) != nil {
			platform.Error(w, 400, "INVALID_PREFERENCES", "通知设置格式无效")
			return
		}
		if err := platform.Request(r.Context(), "PUT", path(r)+"/notification-preferences", "", input, &output); err != nil {
			personalError(w, err, false)
			return
		}
		platform.JSON(w, 200, output)
	})
}
func (e *Engine) personalNotifications(w http.ResponseWriter, r *http.Request) {
	p, size, err := pageParams(r)
	if err != nil {
		platform.Error(w, 400, "INVALID_PAGE", "分页参数无效")
		return
	}
	var page protocol.Page[protocol.Notification]
	address := platform.Env("NOTIFIER_URL", "http://notifier:8080") + fmt.Sprintf("/internal/v1/notifications?userId=%s&page=%d&pageSize=%d", requestUser(r).ID, p, size)
	if err = platform.Request(r.Context(), "GET", address, "", nil, &page); err != nil {
		personalError(w, err, false)
		return
	}
	platform.JSON(w, 200, page)
}
