package platform

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func Env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func Context() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func Accepted(w http.ResponseWriter, id string) {
	JSON(w, 202, map[string]string{"id": id, "status": "accepted"})
}
func Read(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func Internal(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			token := os.Getenv("SERVICE_TOKEN")
			if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
				Error(w, 401, "UNAUTHORIZED", "服务认证失败")
				return
			}
		}
		// Public mutation APIs are same-origin in this local operator console.
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					Error(w, 403, "ORIGIN_DENIED", "请求来源不匹配")
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}
func Mux(service string, ready func() bool) *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, 200, map[string]string{"status": "ok", "service": service})
	})
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready != nil && !ready() {
			Error(w, 503, "NOT_READY", "服务尚未就绪")
			return
		}
		JSON(w, 200, map[string]string{"status": "ok", "service": service})
	})
	return m
}
func Serve(ctx context.Context, service string, h http.Handler) error {
	if os.Getenv("SERVICE_TOKEN") == "" {
		return errors.New("SERVICE_TOKEN must be configured")
	}
	s := &http.Server{Addr: Env("HTTP_ADDR", ":8080"), Handler: Internal(h), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(stop)
	}()
	slog.Info("service listening", "service", service, "address", s.Addr)
	err := s.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

var Client = &http.Client{Timeout: 2 * time.Second}

type HTTPError struct{ Status int }

func (e HTTPError) Error() string { return fmt.Sprintf("upstream HTTP %d", e.Status) }
func Request(ctx context.Context, method, address, key string, body, output any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, address, rd)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+os.Getenv("SERVICE_TOKEN"))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	resp, err := Client.Do(r)
	if err != nil {
		return errors.New("upstream unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return HTTPError{resp.StatusCode}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(output)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}
