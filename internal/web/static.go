package web

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"regexp"

	"github.com/igrybkov/ha-nfc-player/internal/web/ui"
)

// ingressPath matches the X-Ingress-Path header Supervisor sets, e.g.
// /api/hassio_ingress/<token>. Anything else is ignored.
var ingressPath = regexp.MustCompile(`^/[A-Za-z0-9/_.\-]*$`)

// uiHandler serves the embedded UI. index.html gets a <base> tag with the
// Ingress path, so relative URLs resolve under Ingress even when the page URL
// lacks a trailing slash. Assets are revalidated on every load, so an app
// update is never hidden behind a stale cache.
func (s *Server) uiHandler() http.Handler {
	files := http.FileServerFS(ui.FS)
	index, err := fs.ReadFile(ui.FS, "index.html")
	if err != nil {
		panic("web: embedded index.html missing: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			files.ServeHTTP(w, r)
			return
		}
		base := "./"
		if p := r.Header.Get("X-Ingress-Path"); p != "" && ingressPath.MatchString(p) {
			base = p + "/"
			if p[len(p)-1] == '/' {
				base = p
			}
		}
		page := bytes.Replace(index, []byte("<!--BASE-->"),
			[]byte(`<base href="`+html.EscapeString(base)+`">`), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
}
