package main

import (
	"context"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/webui"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	ctx, cancel := platform.Context()
	defer cancel()
	u, err := url.Parse(platform.Env("CORE_URL", "http://core:8080"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		log.Fatal("invalid CORE_URL")
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		platform.Error(w, 502, "CORE_UNAVAILABLE", "工作台暂时无法连接，请稍后重试")
	}
	m := platform.Mux("qr-display", nil)
	forward := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		// Preserve the browser's Host and Cookie so core still checks origin and session.
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
	for _, route := range []string{
		"POST /api/v1/auth/login", "GET /api/v1/me",
		"GET /api/v1/baskets", "GET /api/v1/baskets/{basketNo}/qrcode",
	} {
		m.Handle(route, forward)
	}
	assets, err := fs.Sub(webui.Assets, "dist")
	if err != nil {
		log.Fatal(err)
	}
	files := http.FileServer(http.FS(assets))
	m.Handle("GET /assets/", files)
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		r.URL.Path = "/qr-display.html"
		files.ServeHTTP(w, r)
	})
	if err := platform.Serve(ctx, "qr-display", m); err != nil {
		log.Fatal(err)
	}
}
