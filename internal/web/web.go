// Package web is the HTTP server: JSON API, embedded UI and Ingress handling.
//
// Owner: track C (docs/PLAN.md section 10).
package web

import (
	"log/slog"
	"net/http"

	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// Deps are the collaborators of the server.
type Deps struct {
	Registry    *registry.Registry
	Settings    *config.SettingsStore
	Templates   *store.File[[]labels.Template]   // user templates only
	Backgrounds *store.File[[]labels.Background] // user backgrounds only
	Play        model.PlayService
	Scans       model.ScanLog
	Artwork     model.ArtworkSource
	HA          ha.Client
	Supervisor  bool // accept only 172.30.32.2 and localhost
	Logger      *slog.Logger
}

// Server serves the API and UI.
type Server struct {
	deps Deps
}

// New returns a Server.
func New(d Deps) *Server { return &Server{deps: d} }

// Handler returns the root handler with all routes.
func (s *Server) Handler() http.Handler {
	_ = s.deps
	panic("TODO: track C")
}
