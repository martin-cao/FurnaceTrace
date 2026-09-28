package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"furnace.local/iot/internal/protocol"
	"io"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"sync/atomic"
	"time"
)

type delivery struct {
	status, message string
	sent            *time.Time
	next            time.Time
}

func (s *Service) send(ctx context.Context, n protocol.NotificationRequest, attempts int, chatID int64) delivery {
	body, _ := json.Marshal(map[string]string{"chat_id": strconv.FormatInt(chatID, 10), "text": n.Text})
	var wrote atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(i httptrace.WroteRequestInfo) {
		if i.Err == nil {
			wrote.Store(true)
		}
	}}
	r, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "POST", s.base+"/bot"+s.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return delivery{status: "failed", message: "Telegram 接口配置无效", next: time.Now()}
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(r)
	status, last := "pending", "Telegram 暂不可达"
	delay := time.Duration(min(300, 1<<min(attempts, 8))) * time.Second
	var sent *time.Time
	if err != nil {
		if wrote.Load() {
			status = "unknown"
			last = "请求已发送，但没有收到确认；暂停自动重发"
		}
	} else {
		defer res.Body.Close()
		var result struct {
			OK         bool `json:"ok"`
			ErrorCode  int  `json:"error_code"`
			Parameters struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result)
		switch {
		case res.StatusCode >= 200 && res.StatusCode < 300 && decodeErr == nil && result.OK:
			status = "sent"
			last = ""
			now := time.Now().UTC()
			sent = &now
		case res.StatusCode == 429 || result.ErrorCode == 429:
			delay = time.Duration(max(1, result.Parameters.RetryAfter)) * time.Second
			last = "Telegram 限流，等待重试"
		case res.StatusCode >= 400 && res.StatusCode < 500:
			status = "failed"
			last = "Telegram 拒绝请求，请检查凭据和 Chat ID"
		case decodeErr != nil:
			status = "unknown"
			last = "Telegram 响应无法解析，发送结果不确定"
		default:
			last = "Telegram 服务暂不可用"
		}
	}
	return delivery{status: status, message: last, sent: sent, next: time.Now().Add(delay)}
}
