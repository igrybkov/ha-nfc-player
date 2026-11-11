// Package web is the HTTP server: JSON API, embedded UI and Ingress handling.
package web

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/pools"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
)

// TagLister lists and renames HA tags. ha.WSClient satisfies it.
type TagLister interface {
	Tags(ctx context.Context) ([]ha.Tag, error)
	RenameTag(ctx context.Context, tagID, name string) error
}

// Labels is the part of the label engine the server uses. Nil in Deps means
// the labels package itself; tests pass a fake.
type Labels interface {
	Render(w io.Writer, job labels.Job) error
	SheetMap(t labels.Template, job labels.Job) [][]labels.SlotState
	Layouts() []labels.Layout
	ValidateTemplate(t labels.Template) error
}

// Deps are the collaborators of the server.
type Deps struct {
	Registry    *registry.Registry
	Pools       *pools.Store
	Settings    *config.SettingsStore
	Templates   *config.TemplateStore
	Backgrounds *config.BackgroundStore
	Play        model.PlayService
	Scans       model.ScanLog
	Artwork     model.ArtworkSource
	HATags      TagLister
	ArtPicker   ArtPicker // nil = no art picker (routes answer 503)
	Labels      Labels    // nil = package labels
	Supervisor  bool      // accept only 172.30.32.2 and localhost
	Logger      *slog.Logger
}

// Server serves the API and UI.
type Server struct {
	deps Deps
	log  *slog.Logger
	lbl  Labels
}

// New returns a Server.
func New(d Deps) *Server {
	s := &Server{deps: d, log: d.Logger, lbl: d.Labels}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.lbl == nil {
		s.lbl = labelsPkg{}
	}
	return s
}

// needPicker answers 503 when no ArtPicker is wired.
func (s *Server) needPicker(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.deps.ArtPicker == nil {
			writeError(w, http.StatusServiceUnavailable, "art picker not available")
			return
		}
		h(w, r)
	}
}

// Timeouts for calls into other components.
const (
	haTimeout      = 10 * time.Second
	haNamesTimeout = 3 * time.Second // scans list: show recorded names rather than wait
	playTimeout    = 30 * time.Second
	artworkTimeout = 20 * time.Second
	printTimeout   = 90 * time.Second
)

// Handler returns the root handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/tags", s.listTags)
	mux.HandleFunc("POST /api/tags", s.createTag)
	mux.HandleFunc("PUT /api/tags/{id}", s.updateTag)
	mux.HandleFunc("DELETE /api/tags/{id}", s.deleteTag)
	mux.HandleFunc("POST /api/tags/{id}/play", s.playTag)
	mux.HandleFunc("GET /api/tags/{id}/artwork", s.tagArtwork)

	mux.HandleFunc("GET /api/pools", s.listPools)
	mux.HandleFunc("POST /api/pools", s.createPool)
	mux.HandleFunc("PUT /api/pools/{id}", s.updatePool)
	mux.HandleFunc("DELETE /api/pools/{id}", s.deletePool)
	mux.HandleFunc("GET /api/pools/{id}/artwork", s.poolArtwork)
	mux.HandleFunc("GET /api/pools/{id}/artwork/candidates", s.needPicker(s.poolArtCandidates))
	mux.HandleFunc("GET /api/tags/{id}/artwork/candidates", s.needPicker(s.tagArtCandidates))
	mux.HandleFunc("GET /api/artwork/preview", s.needPicker(s.artPreview))
	mux.HandleFunc("POST /api/play", s.play)
	mux.HandleFunc("POST /api/stop", s.stop)

	mux.HandleFunc("GET /api/ha/tags", s.haTags)
	mux.HandleFunc("PUT /api/ha/tags/{id}", s.renameHATag)
	mux.HandleFunc("GET /api/ha/areas", s.haAreas)
	mux.HandleFunc("GET /api/scans", s.scans)

	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	mux.HandleFunc("GET /api/templates", s.listTemplates)
	mux.HandleFunc("POST /api/templates", s.createTemplate)
	mux.HandleFunc("PUT /api/templates/{id}", s.updateTemplate)
	mux.HandleFunc("DELETE /api/templates/{id}", s.deleteTemplate)

	mux.HandleFunc("GET /api/backgrounds", s.listBackgrounds)
	mux.HandleFunc("POST /api/backgrounds", s.createBackground)
	mux.HandleFunc("PUT /api/backgrounds/{id}", s.updateBackground)
	mux.HandleFunc("DELETE /api/backgrounds/{id}", s.deleteBackground)

	mux.HandleFunc("GET /api/layouts", s.listLayouts)
	mux.HandleFunc("POST /api/print/preview", s.printPreview)
	mux.HandleFunc("POST /api/print/pdf", s.printPDF)
	mux.HandleFunc("PUT /api/print/offsets/{template}", s.saveOffset)
	mux.HandleFunc("PUT /api/print/default-background/{id}", s.setDefaultBackground)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	mux.Handle("/", s.uiHandler())

	return s.recoverPanics(s.allowClients(mux))
}

// supervisorIngress is the address of the Supervisor Ingress gateway.
var supervisorIngress = netip.MustParseAddr("172.30.32.2")

// allowClients rejects requests that do not come from the Ingress gateway or
// loopback when running under Supervisor. Only RemoteAddr counts: forwarded
// headers are set by the client and prove nothing. /healthz stays open for
// container health checks.
func (s *Server) allowClients(next http.Handler) http.Handler {
	if !s.deps.Supervisor {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || clientAllowed(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		s.log.Warn("web: rejected client", "remote", r.RemoteAddr, "path", r.URL.Path)
		writeError(w, http.StatusForbidden, "access only through Home Assistant Ingress")
	})
}

func clientAllowed(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || ip == supervisorIngress
}

// recoverPanics turns a panic in a handler into a 500 so one bad request
// does not take the process down.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("web: panic", "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// labelsPkg is the default Labels: the labels package.
type labelsPkg struct{}

func (labelsPkg) Render(w io.Writer, job labels.Job) error { return labels.Render(w, job) }
func (labelsPkg) SheetMap(t labels.Template, job labels.Job) [][]labels.SlotState {
	return labels.SheetMap(t, job)
}
func (labelsPkg) Layouts() []labels.Layout                 { return labels.Layouts() }
func (labelsPkg) ValidateTemplate(t labels.Template) error { return t.Validate() }
