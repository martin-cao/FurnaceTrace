package core

import (
	_ "embed"
	"furnace.local/iot/internal/webui"
	"io/fs"
	"net/http"
	"strings"
)

func attachUI(m *http.ServeMux) {
	assets, _ := fs.Sub(webui.Assets, "dist")
	server := http.FileServer(http.FS(assets))
	m.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/internal/") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(assets, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
			r.URL.Path = "/"
		}
		server.ServeHTTP(w, r)
	}))
}
