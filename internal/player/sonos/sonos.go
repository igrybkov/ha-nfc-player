// Package sonos plays Apple Music share links on native Sonos media players.
//
// Owner: track A (docs/PLAN.md section 10).
package sonos

import (
	"context"
	"errors"
	"fmt"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/player"
)

// ArtistSongLimit is how many songs an artist link queues.
const ArtistSongLimit = 50

// Player is the Sonos player.
type Player struct {
	ha    ha.Client
	apple catalog.Apple
}

var _ player.Player = (*Player)(nil)

// New returns a Sonos player. apple is used to queue artist songs.
func New(c ha.Client, apple catalog.Apple) *Player { return &Player{ha: c, apple: apple} }

func (p *Player) Name() string { return "sonos" }

// Supports album, playlist, song and artist media in an area with a Sonos
// entity. It can't search, so a Query is left to the router, and it has no
// radio mode.
func (p *Player) Supports(req player.Request) bool {
	if req.Query != nil || req.Radio || req.Area.SonosEntity == "" {
		return false
	}
	switch req.Media.Kind {
	case catalog.KindAlbum, catalog.KindPlaylist, catalog.KindSong:
		return true
	case catalog.KindArtist:
		return p.apple != nil
	}
	return false
}

// Play runs the sequence of the old play_music_in_a_room script:
// media_stop, volume_set, repeat_set, shuffle_set, play_media. An artist
// becomes a queue of its songs: the first replaces the queue, the rest are
// added. The song lookup runs before media_stop, so a failed lookup leaves
// the current music alone.
func (p *Player) Play(ctx context.Context, req player.Request) error {
	if !p.Supports(req) {
		return fmt.Errorf("sonos: unsupported request (kind %q, entity %q)", req.Media.Kind, req.Area.SonosEntity)
	}
	urls := []string{req.Media.ShareURL()}
	if req.Media.Kind == catalog.KindArtist {
		songs, err := p.apple.ArtistSongs(ctx, req.Media.ID, ArtistSongLimit)
		if err != nil {
			return fmt.Errorf("sonos: artist songs: %w", err)
		}
		if len(songs) == 0 {
			return fmt.Errorf("sonos: artist %s: %w", req.Media.ID, errNoSongs)
		}
		urls = urls[:0]
		for _, s := range songs {
			urls = append(urls, s.URL)
		}
	}

	repeat := req.Repeat
	if repeat == "" {
		repeat = "all"
	}
	target := &ha.Target{EntityID: []string{req.Area.SonosEntity}}
	steps := []struct {
		service string
		data    map[string]any
	}{
		{"media_stop", nil},
		{"volume_set", map[string]any{"volume_level": float64(req.Volume) / 100}},
		{"repeat_set", map[string]any{"repeat": repeat}},
		{"shuffle_set", map[string]any{"shuffle": req.Shuffle}},
	}
	for _, s := range steps {
		if _, err := p.ha.CallService(ctx, "media_player", s.service, s.data, target, false); err != nil {
			return fmt.Errorf("sonos: %w", err)
		}
	}

	for i, u := range urls {
		data := map[string]any{"media_content_id": u, "media_content_type": "music"}
		if req.Media.Kind == catalog.KindArtist {
			data["enqueue"] = "add"
			if i == 0 {
				data["enqueue"] = "replace"
			}
		}
		if _, err := p.ha.CallService(ctx, "media_player", "play_media", data, target, false); err != nil {
			return fmt.Errorf("sonos: play_media %d/%d: %w", i+1, len(urls), err)
		}
	}
	return nil
}

var errNoSongs = errors.New("no songs found")
