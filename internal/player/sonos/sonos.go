// Package sonos plays Apple Music and Spotify share links on native Sonos
// media players.
//
// Owner: track A (docs/PLAN.md section 10).
package sonos

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/player"
)

// ArtistSongLimit is how many songs an artist link queues.
const ArtistSongLimit = 50

// Player is the Sonos player.
type Player struct {
	ha      ha.Client
	apple   catalog.Apple
	shuffle func(n int, swap func(i, j int)) // rand.Shuffle; tests replace it
}

var _ player.Player = (*Player)(nil)

// New returns a Sonos player. apple is used to queue artist songs.
func New(c ha.Client, apple catalog.Apple) *Player {
	return &Player{ha: c, apple: apple, shuffle: rand.Shuffle}
}

func (p *Player) Name() string { return "sonos" }

// Supports album, playlist, song, artist and station media in an area with
// a Sonos entity. It can't search, so a Query is left to the router, and it has no
// radio mode. Spotify is album, playlist and song only: Sonos refuses a
// Spotify artist link (UPnP error 714), so that goes to Music Assistant.
func (p *Player) Supports(req player.Request) bool {
	if req.Query != nil || req.Radio || req.Area.SonosEntity == "" {
		return false
	}
	if req.Media.IsSpotify() {
		switch req.Media.Kind {
		case catalog.KindAlbum, catalog.KindPlaylist, catalog.KindSong:
			return true
		}
		return false
	}
	switch req.Media.Kind {
	case catalog.KindAlbum, catalog.KindPlaylist, catalog.KindSong, catalog.KindStation:
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
//
// Sonos starts a new queue at its first track even in shuffle mode, and a
// queue replace resets repeat. So an artist's songs are shuffled here before
// they are queued, and repeat and shuffle are set again at the end.
func (p *Player) Play(ctx context.Context, req player.Request) error {
	if !p.Supports(req) {
		return fmt.Errorf("sonos: unsupported request (kind %q, entity %q)", req.Media.Kind, req.Area.SonosEntity)
	}
	if req.Media.Kind == catalog.KindStation {
		return p.playStation(ctx, req)
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
		if req.Shuffle {
			p.shuffle(len(urls), func(i, j int) { urls[i], urls[j] = urls[j], urls[i] })
		}
	}

	repeat := req.Repeat
	if repeat == "" {
		repeat = "all"
	}
	target := &ha.Target{EntityID: []string{req.Area.SonosEntity}}
	modes := []step{
		{"repeat_set", map[string]any{"repeat": repeat}},
		{"shuffle_set", map[string]any{"shuffle": req.Shuffle}},
	}
	steps := append([]step{
		{"media_stop", nil},
		{"volume_set", map[string]any{"volume_level": float64(req.Volume) / 100}},
	}, modes...)
	if err := p.run(ctx, target, steps); err != nil {
		return err
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
	return p.run(ctx, target, modes)
}

// step is one media_player service call.
type step struct {
	service string
	data    map[string]any
}

func (p *Player) run(ctx context.Context, target *ha.Target, steps []step) error {
	for _, s := range steps {
		if _, err := p.ha.CallService(ctx, "media_player", s.service, s.data, target, false); err != nil {
			return fmt.Errorf("sonos: %w", err)
		}
	}
	return nil
}

var errNoSongs = errors.New("no songs found")

// appleMusicSID is the Sonos service id of Apple Music.
const appleMusicSID = "204"

// accountRe finds the Apple Music account number (sn) in a Sonos media URI.
var accountRe = regexp.MustCompile(`[?&]sid=` + appleMusicSID + `&(?:[^&]*&)*?sn=(\d+)`)

// errNoAccount means no Sonos speaker has played Apple Music yet, so the
// account number for a station URI is unknown.
var errNoAccount = errors.New("no Apple Music account found on Sonos; play any Apple Music song on a Sonos speaker once, then retry")

// playStation plays an Apple Music station. Sonos takes no share link for
// it, only its own radio URI, which names the Apple Music account the
// speakers use. A station has no shuffle or repeat.
func (p *Player) playStation(ctx context.Context, req player.Request) error {
	sn, err := p.account(ctx)
	if err != nil {
		return fmt.Errorf("sonos: station: %w", err)
	}
	// Written as Sonos writes it ("%3a"). catalog.Parse allows only
	// [A-Za-z0-9._-] in a station id, so it needs no escaping.
	uri := "x-sonosapi-radio:radio%3a" + req.Media.ID + "?sid=" + appleMusicSID + "&flags=8300&sn=" + sn
	target := &ha.Target{EntityID: []string{req.Area.SonosEntity}}
	return p.run(ctx, target, []step{
		{"media_stop", nil},
		{"volume_set", map[string]any{"volume_level": float64(req.Volume) / 100}},
		{"play_media", map[string]any{"media_content_id": uri, "media_content_type": "music"}},
	})
}

// account returns the Apple Music account number from the media URI of any
// Sonos speaker that plays (or last played) Apple Music.
func (p *Player) account(ctx context.Context) (string, error) {
	states, err := p.ha.States(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range states {
		id, _ := s.Attributes["media_content_id"].(string)
		if m := accountRe.FindStringSubmatch(id); m != nil {
			return m[1], nil
		}
	}
	return "", errNoAccount
}
