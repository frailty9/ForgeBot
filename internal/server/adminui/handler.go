// Package adminui serves the embedded Admin Web UI without allowing it to
// intercept ForgeBot's JSON API routes.
package adminui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist
var assets embed.FS

// Handler returns a handler rooted at /admin/. Unknown extensionless paths
// fall back to index.html so the client-side History API router can operate.
func Handler() http.Handler {
	static, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requestPath == "." || requestPath == "" {
			requestPath = "index.html"
		}
		file, openErr := static.Open(requestPath)
		if openErr == nil {
			_ = file.Close()
			serveFile(w, r, static, requestPath)
			return
		}
		if path.Ext(requestPath) != "" {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, static, "index.html")
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, static fs.FS, name string) {
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; font-src 'self'; connect-src 'self'")
	data, err := fs.ReadFile(static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
}
