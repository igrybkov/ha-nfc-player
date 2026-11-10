// Package sonos plays Apple Music share links on native Sonos media players.
//
// Owner: track A (docs/PLAN.md section 10).
package sonos

import (
	"context"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/player"
)

// Player is the Sonos player.
type Player struct {
	ha    ha.Client
	apple catalog.Apple
}

var _ player.Player = (*Player)(nil)

// New returns a Sonos player. apple is used to queue artist songs.
func New(c ha.Client, apple catalog.Apple) *Player { return &Player{ha: c, apple: apple} }

func (p *Player) Name() string { return "sonos" }

func (p *Player) Supports(req player.Request) bool { panic("TODO: track A") }

func (p *Player) Play(ctx context.Context, req player.Request) error {
	_, _ = p.ha, p.apple
	panic("TODO: track A")
}
