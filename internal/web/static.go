package web

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/web/ui"
)

// ingressPath matches the X-Ingress-Path header Supervisor sets, e.g.
// /api/hassio_ingress/<token>: one or more non-empty segments, with an
// optional trailing slash. validIngressPath also refuses "." and "..".
var ingressPath = regexp.MustCompile(`^/[A-Za-z0-9_.\-]+(/[A-Za-z0-9_.\-]+)*/?$`)

// validIngressPath reports whether p is safe to use as the <base> href.
// Anything else is ignored and the page falls back to "./".
func validIngressPath(p string) bool {
	if !ingressPath.MatchString(p) {
		return false
	}
	for _, seg := range strings.Split(strings.Trim(p, "/"), "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// contentSecurityPolicy goes on every UI response. The UI has no inline
// scripts; inline styles stay allowed for the style attributes the screens
// set. data: and blob: cover the favicon and image previews. HA shows the
// page in an iframe on its own origin, so frame-ancestors 'self' allows it.
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data: blob:; " +
	"style-src 'self' 'unsafe-inline'; frame-ancestors 'self'"

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
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			files.ServeHTTP(w, r)
			return
		}
		base := "./"
		if p := r.Header.Get("X-Ingress-Path"); p != "" && validIngressPath(p) {
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
