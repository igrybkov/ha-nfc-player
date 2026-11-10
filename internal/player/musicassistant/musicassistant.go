// Package musicassistant plays media through music_assistant.* services.
//
// Owner: track A (docs/PLAN.md section 10).
package musicassistant

import (
	"context"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// Player is the Music Assistant player.
type Player struct {
	ha       ha.Client
	settings model.SettingsSource
}

var (
	_ player.Player   = (*Player)(nil)
	_ player.Searcher = (*Player)(nil)
)

// New returns a Music Assistant player.
func New(c ha.Client, s model.SettingsSource) *Player { return &Player{ha: c, settings: s} }

func (p *Player) Name() string { return "music_assistant" }

func (p *Player) Supports(req player.Request) bool { panic("TODO: track A") }

func (p *Player) Play(ctx context.Context, req player.Request) error {
	_, _ = p.ha, p.settings
	panic("TODO: track A")
}

// Search runs music_assistant.search and returns the first apple_music:// hit.
func (p *Player) Search(ctx context.Context, area rooms.Area, q player.Query) (catalog.Media, error) {
	panic("TODO: track A")
}
