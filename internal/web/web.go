// Package web serves the browser UI.
//
// The files are embedded in the exe. When the exe runs from the repo's bin
// folder, the UI is served from internal/web/static on disk instead, so
// editing the UI only needs a browser reload (no rebuild).
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed static
var static embed.FS

// Handler serves the UI.
func Handler() http.Handler {
	if dir := devDir(); dir != "" {
		h := http.FileServer(http.Dir(dir))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			h.ServeHTTP(w, r)
		})
	}
	sub, _ := fs.Sub(static, "static")
	return http.FileServer(http.FS(sub))
}

func devDir() string {
	if d := os.Getenv("SPANWALL_WEB_DIR"); d != "" {
		return d
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	d := filepath.Join(filepath.Dir(exe), "..", "internal", "web", "static")
	if _, err := os.Stat(filepath.Join(d, "index.html")); err == nil {
		return d
	}
	return ""
}
