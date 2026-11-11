// Package player defines the Player interface and the Router that walks the
// player chain with fallback.
package player

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// Query is a search: "Artist — Album" from an unknown tag name, or a bare
// name from a play command. An empty Artist searches by Album (the name)
// alone.
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
	Radio   bool   // Music Assistant radio mode; players that can't do it don't Support the request
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

// ErrUnknownPlayer means a player override names no registered player.
var ErrUnknownPlayer = errors.New("unknown player")

// ErrNoSearcher means a Query request reached a router without a Searcher.
var ErrNoSearcher = errors.New("no searcher for the search fallback")

// ErrNotFound means a search found nothing playable. Searchers wrap it.
var ErrNotFound = errors.New("not found")

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

// Play walks the chain and returns the name of the player that handled the
// request.
//
// The chain is override (if not empty) followed by settings players.default,
// without duplicates. Players that are not registered or don't Support the
// request are skipped. When a player fails and players.fallback is on, the
// next one is tried; otherwise the error is returned at once.
//
// A Query request is resolved through the Searcher, once, before it goes to
// a player that can't take a Query itself. Later players get the result.
func (r *Router) Play(ctx context.Context, req Request, override string) (string, error) {
	s := r.settings.Settings()
	if override != "" {
		if _, ok := r.players[override]; !ok {
			return "", fmt.Errorf("player override %q: %w", override, ErrUnknownPlayer)
		}
	}
	chain := chain(override, s.Players.Default)

	var (
		errs     []error
		resolved *Request // req with Query resolved to Media
	)
	for _, name := range chain {
		p, ok := r.players[name]
		if !ok {
			continue
		}
		cur := req
		if resolved != nil {
			cur = *resolved // search once, pass the result on
		} else if req.Query != nil && !p.Supports(req) {
			m, err := r.search(ctx, req)
			if err != nil {
				// The search fails the same way for every player.
				return "", err
			}
			resolved = &Request{}
			*resolved = req
			resolved.Media, resolved.Query = m, nil
			cur = *resolved
		}
		if !p.Supports(cur) {
			continue
		}
		err := p.Play(ctx, cur)
		if err == nil {
			return name, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
		if !s.Players.Fallback || ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return "", ErrNoPlayer
	}
	return "", errors.Join(errs...)
}

func (r *Router) search(ctx context.Context, req Request) (catalog.Media, error) {
	if r.searcher == nil {
		return catalog.Media{}, fmt.Errorf("search %q — %q: %w", req.Query.Artist, req.Query.Album, ErrNoSearcher)
	}
	m, err := r.searcher.Search(ctx, req.Area, *req.Query)
	if err != nil {
		return catalog.Media{}, fmt.Errorf("search %q — %q: %w", req.Query.Artist, req.Query.Album, err)
	}
	return m, nil
}

func chain(override string, defaults []string) []string {
	out := make([]string, 0, len(defaults)+1)
	if override != "" {
		out = append(out, override)
	}
	for _, n := range defaults {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}
