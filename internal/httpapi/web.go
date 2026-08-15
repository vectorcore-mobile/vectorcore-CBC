package httpapi

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"time"

	webui "github.com/vectorcore/cbc/web"
)

// uiHandler returns an http.Handler that serves the embedded React SPA under
// /ui/. Requests for paths that don't match a real file fall back to
// index.html so React Router's client-side routing works correctly - even
// for nested routes like /ui/alerts/{id}.
func uiHandler() http.Handler {
	sub, err := fs.Sub(webui.FS, "dist")
	if err != nil {
		// web/dist not present (UI not built) - placeholder page.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`<!doctype html><html><body style="font-family:monospace;background:#0d1117;color:#e6edf3;padding:2rem">
<h2>VectorCore CBC</h2><p>UI not built. Run <code>make ui</code> then rebuild the binary.</p>
<p><a href="/docs" style="color:#58a6ff">API Docs</a></p></body></html>`))
		})
	}

	fileServer := http.StripPrefix("/ui", http.FileServer(http.FS(sub)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/ui"), "/")
		if path == "" {
			serveIndex(w, r, sub)
			return
		}

		// Only hand real embedded files to the file server; everything else
		// (including nested SPA routes like alerts/{id}) falls back to
		// index.html directly. Note: this deliberately never asks the file
		// server for "index.html" itself - net/http's FileServer redirects
		// any request ending in that name to "./", which would turn a
		// nested route's fallback into a wrong, one-level-up redirect.
		if f, err := sub.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, sub)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(b))
}
