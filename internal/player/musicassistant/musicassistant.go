// Package musicassistant plays media through music_assistant.* services.
//
// Owner: track A (docs/PLAN.md section 10).
package musicassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// SearchLimit is the per-type result limit of music_assistant.search, as in
// the old script.
const SearchLimit = 5

// ErrNoConfigEntry means no Music Assistant config entry is set or found.
var ErrNoConfigEntry = errors.New("music assistant: no config entry")

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

// Supports every Apple Music kind and Query, in an area with an MA entity.
func (p *Player) Supports(req player.Request) bool {
	if req.Area.MAEntity == "" {
		return false
	}
	return req.Query != nil || req.Media.URI() != ""
}

// Play resolves a Query if needed, then runs volume_set, play_media
// (enqueue replace, radio_mode when asked), shuffle_set and repeat_set on
// the MA entity. Volume goes first so the music never starts at the
// previous level.
func (p *Player) Play(ctx context.Context, req player.Request) error {
	if !p.Supports(req) {
		return fmt.Errorf("music assistant: unsupported request (kind %q, entity %q)", req.Media.Kind, req.Area.MAEntity)
	}
	media := req.Media
	if req.Query != nil {
		m, err := p.Search(ctx, req.Area, *req.Query)
		if err != nil {
			return err
		}
		media = m
	}
	repeat := req.Repeat
	if repeat == "" {
		repeat = "all"
	}
	target := &ha.Target{EntityID: []string{req.Area.MAEntity}}
	play := map[string]any{
		"media_id":   media.URI(),
		"media_type": mediaType(media.Kind),
		"enqueue":    "replace",
	}
	if req.Radio {
		play["radio_mode"] = true
	}
	steps := []struct {
		domain, service string
		data            map[string]any
	}{
		{"media_player", "volume_set", map[string]any{"volume_level": float64(req.Volume) / 100}},
		{"music_assistant", "play_media", play},
		{"media_player", "shuffle_set", map[string]any{"shuffle": req.Shuffle}},
		{"media_player", "repeat_set", map[string]any{"repeat": repeat}},
	}
	for _, s := range steps {
		if _, err := p.ha.CallService(ctx, s.domain, s.service, s.data, target, false); err != nil {
			return fmt.Errorf("music assistant: %w", err)
		}
	}
	return nil
}

// mediaType is the MA media_type of a kind. Only called for kinds with a URI.
func mediaType(k catalog.Kind) string {
	switch k {
	case catalog.KindSong:
		return "track"
	case catalog.KindStation:
		return "radio"
	}
	return string(k)
}

type searchItem struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type searchResponse struct {
	Albums    []searchItem `json:"albums"`
	Playlists []searchItem `json:"playlists"`
}

// Search runs music_assistant.search and returns the first apple_music:// hit.
// A Query without Artist searches by name only. Albums come before playlists. Library items (library://) are skipped: Sonos
// can't play them.
func (p *Player) Search(ctx context.Context, area rooms.Area, q player.Query) (catalog.Media, error) {
	entryID, err := p.configEntry(ctx, area)
	if err != nil {
		return catalog.Media{}, err
	}
	data := map[string]any{
		"config_entry_id": entryID,
		"name":            q.Album,
		"media_type":      []string{"album", "playlist"},
		"limit":           SearchLimit,
	}
	if q.Artist != "" {
		data["artist"] = q.Artist // a name-only search must not filter by an empty artist
	}
	raw, err := p.ha.CallService(ctx, "music_assistant", "search", data, nil, true)
	if err != nil {
		return catalog.Media{}, fmt.Errorf("music assistant: %w", err)
	}
	var res searchResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return catalog.Media{}, fmt.Errorf("music assistant: search response: %w", err)
	}
	for _, it := range append(res.Albums, res.Playlists...) {
		if !strings.HasPrefix(it.URI, "apple_music://") {
			continue
		}
		if m, err := catalog.Parse(it.URI); err == nil {
			return m, nil
		}
	}
	return catalog.Media{}, fmt.Errorf("music assistant: %q — %q: %w", q.Artist, q.Album, player.ErrNotFound)
}

// configEntry returns the MA config entry id: from settings, else the entry
// of the area's MA entity, else the entry of any MA entity.
func (p *Player) configEntry(ctx context.Context, area rooms.Area) (string, error) {
	if id := p.settings.Settings().MusicAssistant.ConfigEntryID; id != nil && *id != "" {
		return *id, nil
	}
	entities, err := p.ha.Entities(ctx)
	if err != nil {
		return "", fmt.Errorf("music assistant: %w", err)
	}
	var first string
	for _, e := range entities {
		if e.Platform != rooms.MusicAssistantPlatform || e.ConfigEntryID == "" {
			continue
		}
		if e.EntityID == area.MAEntity {
			return e.ConfigEntryID, nil
		}
		if first == "" {
			first = e.ConfigEntryID
		}
	}
	if first == "" {
		return "", ErrNoConfigEntry
	}
	return first, nil
}
