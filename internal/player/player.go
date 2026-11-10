// Package player defines the Player interface and the Router that walks the
// player chain with fallback.
//
// Owner: track A (docs/PLAN.md section 10).
package player

import (
	"context"
	"errors"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// Query is the "Artist — Album" search fallback for unknown tag names.
type Query struct {
	Artist string
	Album  string
}

// Request is one playback request.
type Request struct {
	Media   catalog.Media
	Query   *Query // set for the search fallback; Media is empty then
	Area    rooms.Area
	Volume  int // percent
	Shuffle bool
	Repeat  string // off | one | all
}

// Player plays media on one kind of speaker.
type Player interface {
	Name() string // "sonos", "music_assistant"
	Supports(req Request) bool
	Play(ctx context.Context, req Request) error
}

// Searcher resolves a Query to media. The Music Assistant player implements it.
type Searcher interface {
	Search(ctx context.Context, area rooms.Area, q Query) (catalog.Media, error)
}

// ErrNoPlayer means no player in the chain supports the request.
var ErrNoPlayer = errors.New("no player supports the request")

// Router holds the registered players. The chain order (players.default) and
// the fallback flag come from settings on each call, so UI edits apply at once.
type Router struct {
	players  map[string]Player
	settings model.SettingsSource
	searcher Searcher // nil = Query requests fail unless a player supports them
}

// NewRouter registers players by Name.
func NewRouter(settings model.SettingsSource, searcher Searcher, players ...Player) *Router {
	r := &Router{players: make(map[string]Player, len(players)), settings: settings, searcher: searcher}
	for _, p := range players {
		r.players[p.Name()] = p
	}
	return r
}

// Play tries the chain in order and returns the name of the player that
// handled the request. override, if not empty, is tried alone.
func (r *Router) Play(ctx context.Context, req Request, override string) (string, error) {
	_, _ = r.settings, r.searcher
	panic("TODO: track A")
}
