// Package scan handles tag_scanned events: event, entry, room, router. It
// also implements model.ScanLog and model.PlayService.
//
// Owner: track A (docs/PLAN.md section 10).
package scan

import (
	"context"
	"log/slog"

	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// Deps are the collaborators of a Handler.
type Deps struct {
	HA       ha.Client
	Entries  model.EntryLookup
	Settings model.SettingsSource
	Rooms    *rooms.Resolver
	Router   *player.Router
	Log      model.ScanLog
	Logger   *slog.Logger
}

// Handler handles scans and test plays.
type Handler struct {
	deps Deps
}

var _ model.PlayService = (*Handler)(nil)

// New returns a Handler.
func New(d Deps) *Handler { return &Handler{deps: d} }

// Run subscribes to tag_scanned and handles events until ctx is done.
func (h *Handler) Run(ctx context.Context) error {
	_ = h.deps
	panic("TODO: track A")
}

func (h *Handler) PlayEntry(ctx context.Context, entry model.Entry, areaID string) (string, error) {
	panic("TODO: track A")
}

func (h *Handler) Areas(ctx context.Context) ([]model.AreaInfo, error) { panic("TODO: track A") }

// Ring is an in-memory model.ScanLog that keeps the last N scans.
type Ring struct {
	size int
}

var _ model.ScanLog = (*Ring)(nil)

// NewRing returns a Ring of the given size (50 in production).
func NewRing(size int) *Ring { return &Ring{size: size} }

func (r *Ring) Add(s model.Scan) {
	_ = r.size
	panic("TODO: track A")
}

func (r *Ring) Recent() []model.Scan { panic("TODO: track A") }
