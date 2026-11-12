package musicassistant

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/ha/hatest"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/player/sonos"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

type settings struct{ s model.Settings }

func (s *settings) Settings() model.Settings { return s.s }

func defaults() *settings { return &settings{model.DefaultSettings()} }

var office = rooms.Area{ID: "office", SonosEntity: "media_player.office", MAEntity: "media_player.office_ma", Volume: 15}

func maTarget() *ha.Target { return &ha.Target{EntityID: []string{"media_player.office_ma"}} }

func entities() []ha.Entity {
	return []ha.Entity{
		{EntityID: "media_player.office", Platform: "sonos", ConfigEntryID: "sonos-entry"},
		{EntityID: "media_player.kitchen_ma", Platform: "music_assistant", ConfigEntryID: "ma-other"},
		{EntityID: "media_player.office_ma", Platform: "music_assistant", ConfigEntryID: "ma-entry"},
	}
}

// searchResult is a music_assistant.search response with a library hit
// first, as MA returns it.
const searchResult = `{
  "artists": [],
  "albums": [
    {"media_type": "album", "uri": "library://album/17", "name": "Divisive", "provider": "library"},
    {"media_type": "album", "uri": "apple_music://album/1646032515", "name": "Divisive", "provider": "apple_music"}
  ],
  "playlists": [
    {"media_type": "playlist", "uri": "apple_music://playlist/pl.abc", "name": "Divisive Mix"}
  ],
  "tracks": []
}`

func TestPlayAlbumSequence(t *testing.T) {
	f := &hatest.Fake{}
	p := New(f, defaults())
	m, _ := catalog.Parse("https://music.apple.com/us/album/divisive/1646032515")
	req := player.Request{Media: m, Area: office, Volume: 20, Shuffle: true, Repeat: "off"}
	if !p.Supports(req) {
		t.Fatal("album not supported")
	}
	if err := p.Play(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	want := []hatest.ServiceCall{
		{Domain: "media_player", Service: "volume_set", Data: map[string]any{"volume_level": 0.2}, Target: maTarget()},
		{Domain: "music_assistant", Service: "play_media", Data: map[string]any{
			"media_id": "apple_music://album/1646032515", "media_type": "album", "enqueue": "replace",
		}, Target: maTarget()},
		{Domain: "media_player", Service: "shuffle_set", Data: map[string]any{"shuffle": true}, Target: maTarget()},
		{Domain: "media_player", Service: "repeat_set", Data: map[string]any{"repeat": "off"}, Target: maTarget()},
	}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Errorf("calls:\n got %+v\nwant %+v", f.Calls, want)
	}
}

func TestPlayQuerySearchesThenPlays(t *testing.T) {
	f := &hatest.Fake{EntityList: entities(), Respond: func(c hatest.ServiceCall) (json.RawMessage, error) {
		if c.Service == "search" {
			return json.RawMessage(searchResult), nil
		}
		return nil, nil
	}}
	p := New(f, defaults())
	req := player.Request{Query: &player.Query{Artist: "Disturbed", Album: "Divisive"}, Area: office, Volume: 15}
	if err := p.Play(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	search := f.Calls[0]
	wantSearch := hatest.ServiceCall{Domain: "music_assistant", Service: "search", ReturnResponse: true, Data: map[string]any{
		"config_entry_id": "ma-entry", // the entry of the area's MA entity
		"name":            "Divisive",
		"artist":          "Disturbed",
		"media_type":      []string{"album", "playlist"},
		"limit":           SearchLimit,
	}}
	if !reflect.DeepEqual(search, wantSearch) {
		t.Errorf("search call:\n got %+v\nwant %+v", search, wantSearch)
	}
	if got := f.Calls[2].Data["media_id"]; got != "apple_music://album/1646032515" {
		t.Errorf("play_media media_id = %v (library:// must be skipped)", got)
	}
	if len(f.Calls) != 5 {
		t.Errorf("got %d calls, want search + 4", len(f.Calls))
	}
}

func TestSearch(t *testing.T) {
	respond := func(body string) func(hatest.ServiceCall) (json.RawMessage, error) {
		return func(hatest.ServiceCall) (json.RawMessage, error) { return json.RawMessage(body), nil }
	}
	q := player.Query{Artist: "Metallica", Album: "Essential"}

	t.Run("playlist when no album", func(t *testing.T) {
		f := &hatest.Fake{EntityList: entities(), Respond: respond(`{"albums":[{"uri":"library://album/1"}],"playlists":[{"uri":"apple_music://playlist/pl.e55d"}]}`)}
		m, err := New(f, defaults()).Search(context.Background(), office, q)
		if err != nil || m.Kind != catalog.KindPlaylist || m.ID != "pl.e55d" {
			t.Fatalf("Search = %+v, %v", m, err)
		}
		if m.ShareURL() != "https://music.apple.com/us/playlist/playlist-pl.e55d/pl.e55d" {
			t.Errorf("ShareURL = %s", m.ShareURL())
		}
	})
	// Real MA results carry an instance suffix on the provider; it used to
	// make every hit look unplayable.
	t.Run("suffixed apple playlist", func(t *testing.T) {
		f := &hatest.Fake{EntityList: entities(), Respond: respond(`{"albums":[{"uri":"library://album/1"}],"playlists":[{"uri":"apple_music--P6Nxuakq://playlist/pl.e55d"}]}`)}
		m, err := New(f, defaults()).Search(context.Background(), office, q)
		if err != nil || m.Kind != catalog.KindPlaylist || m.ID != "pl.e55d" {
			t.Fatalf("Search = %+v, %v", m, err)
		}
		if m.URI() != "apple_music://playlist/pl.e55d" {
			t.Errorf("URI = %s, want the domain form", m.URI())
		}
	})
	t.Run("ranking", func(t *testing.T) {
		tests := []struct {
			name, body, want string
		}{
			{"apple playlist beats spotify album",
				`{"albums":[{"uri":"spotify--RiSye5Z5://album/5gzLOflH95LkKYE6XSXE9k"}],"playlists":[{"uri":"apple_music--P6Nxuakq://playlist/pl.e55d"}]}`,
				"apple_music://playlist/pl.e55d"},
			{"apple album beats apple playlist",
				`{"albums":[{"uri":"spotify://album/5gzLOflH95LkKYE6XSXE9k"},{"uri":"apple_music://album/123"}],"playlists":[{"uri":"apple_music://playlist/pl.x"}]}`,
				"apple_music://album/123"},
			{"spotify when no apple hit",
				`{"albums":[{"uri":"library://album/1"}],"playlists":[{"uri":"spotify--RiSye5Z5://playlist/37i9dQZF1DXcBWIGoYBM5M"}]}`,
				"spotify://playlist/37i9dQZF1DXcBWIGoYBM5M"},
			{"spotify album beats spotify playlist",
				`{"albums":[{"uri":"spotify--RiSye5Z5://album/5gzLOflH95LkKYE6XSXE9k"}],"playlists":[{"uri":"spotify://playlist/37i9dQZF1DXcBWIGoYBM5M"}]}`,
				"spotify://album/5gzLOflH95LkKYE6XSXE9k"},
			{"share links skipped",
				`{"albums":[{"uri":"https://music.apple.com/us/album/x/1"}],"playlists":[{"uri":"spotify://playlist/37i9dQZF1DXcBWIGoYBM5M"}]}`,
				"spotify://playlist/37i9dQZF1DXcBWIGoYBM5M"},
		}
		for _, tt := range tests {
			f := &hatest.Fake{EntityList: entities(), Respond: respond(tt.body)}
			m, err := New(f, defaults()).Search(context.Background(), office, q)
			if err != nil || m.URI() != tt.want {
				t.Errorf("%s: Search = %s, %v; want %s", tt.name, m.URI(), err, tt.want)
			}
		}
	})
	t.Run("name only", func(t *testing.T) {
		f := &hatest.Fake{EntityList: entities(), Respond: respond(searchResult)}
		if _, err := New(f, defaults()).Search(context.Background(), office, player.Query{Album: "Divisive"}); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.Calls[0].Data["artist"]; ok || f.Calls[0].Data["name"] != "Divisive" {
			t.Errorf("search data = %v, want name only", f.Calls[0].Data)
		}
	})
	t.Run("with artist", func(t *testing.T) {
		f := &hatest.Fake{EntityList: entities(), Respond: respond(searchResult)}
		if _, err := New(f, defaults()).Search(context.Background(), office, q); err != nil {
			t.Fatal(err)
		}
		if f.Calls[0].Data["artist"] != "Metallica" {
			t.Errorf("search data = %v", f.Calls[0].Data)
		}
	})
	t.Run("nothing found", func(t *testing.T) {
		f := &hatest.Fake{EntityList: entities(), Respond: respond(`{"albums":[],"playlists":[]}`)}
		_, err := New(f, defaults()).Search(context.Background(), office, q)
		if !errors.Is(err, player.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("config entry from settings", func(t *testing.T) {
		f := &hatest.Fake{Respond: respond(searchResult)}
		s := defaults()
		id := "01JEADXBMRS3ATRJJKFG8YQH9D"
		s.s.MusicAssistant.ConfigEntryID = &id
		if _, err := New(f, s).Search(context.Background(), office, q); err != nil {
			t.Fatal(err)
		}
		if got := f.Calls[0].Data["config_entry_id"]; got != id {
			t.Errorf("config_entry_id = %v", got)
		}
	})
	t.Run("config entry from any MA entity", func(t *testing.T) {
		f := &hatest.Fake{EntityList: entities(), Respond: respond(searchResult)}
		if _, err := New(f, defaults()).Search(context.Background(), rooms.Area{ID: "garage"}, q); err != nil {
			t.Fatal(err)
		}
		if got := f.Calls[0].Data["config_entry_id"]; got != "ma-other" {
			t.Errorf("config_entry_id = %v", got)
		}
	})
	t.Run("no config entry", func(t *testing.T) {
		f := &hatest.Fake{}
		_, err := New(f, defaults()).Search(context.Background(), office, q)
		if !errors.Is(err, ErrNoConfigEntry) || len(f.Calls) != 0 {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSupports(t *testing.T) {
	p := New(&hatest.Fake{}, defaults())
	noMA := rooms.Area{ID: "x", SonosEntity: "media_player.x"}
	tests := []struct {
		name string
		req  player.Request
		want bool
	}{
		{"album", player.Request{Media: catalog.Media{Kind: catalog.KindAlbum, ID: "1"}, Area: office}, true},
		{"station", player.Request{Media: catalog.Media{Kind: catalog.KindStation, ID: "ra.1"}, Area: office}, false}, // NotImplementedError in MA
		{"query", player.Request{Query: &player.Query{}, Area: office}, true},
		{"unknown kind", player.Request{Media: catalog.Media{Kind: catalog.KindUnknown, ID: "1"}, Area: office}, false},
		{"empty media", player.Request{Area: office}, false},
		{"no MA entity", player.Request{Query: &player.Query{}, Area: noMA}, false},
		{"radio", player.Request{Media: catalog.Media{Kind: catalog.KindAlbum, ID: "1"}, Area: office, Radio: true}, true},
		{"spotify album", player.Request{Media: spotifyMedia(catalog.KindAlbum), Area: office}, true},
		{"spotify playlist", player.Request{Media: spotifyMedia(catalog.KindPlaylist), Area: office}, true},
		{"spotify song", player.Request{Media: spotifyMedia(catalog.KindSong), Area: office}, true},
		{"spotify artist", player.Request{Media: spotifyMedia(catalog.KindArtist), Area: office}, true},
	}
	for _, tt := range tests {
		if got := p.Supports(tt.req); got != tt.want {
			t.Errorf("%s: Supports = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func spotifyMedia(k catalog.Kind) catalog.Media {
	return catalog.Media{Service: catalog.ServiceSpotify, Kind: k, ID: "0OdUWJ0sBjDrqHygGUXeCF"}
}

func TestPlayRadioMode(t *testing.T) {
	f := &hatest.Fake{}
	m, _ := catalog.Parse("https://music.apple.com/us/artist/the-dead-south/668262442")
	if err := New(f, defaults()).Play(context.Background(), player.Request{Media: m, Area: office, Volume: 20, Radio: true}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"media_id": "apple_music://artist/668262442", "media_type": "artist", "enqueue": "replace", "radio_mode": true}
	if got := f.Calls[1].Data; !reflect.DeepEqual(got, want) {
		t.Errorf("play_media data = %v, want %v", got, want)
	}
}

// Radio skips Sonos (first in the chain) and falls through to MA.
func TestRouterRadioSkipsSonos(t *testing.T) {
	f := &hatest.Fake{}
	ma := New(f, defaults())
	r := player.NewRouter(defaults(), ma, sonos.New(f, nil), ma)
	m, _ := catalog.Parse("https://music.apple.com/us/album/divisive/1646032515")
	name, err := r.Play(context.Background(), player.Request{Media: m, Area: office, Volume: 15, Radio: true}, "")
	if err != nil || name != "music_assistant" {
		t.Fatalf("Play = %q, %v", name, err)
	}
	for _, c := range f.Calls {
		if c.Target.EntityID[0] != "media_player.office_ma" {
			t.Errorf("call on %v, want only the MA entity", c.Target.EntityID)
		}
	}
}

// Sonos can't play a Spotify artist (UPnP error 714), so the router skips
// it and MA plays the spotify:// URI. Sonos has an iTunes client here, so
// the skip is the Spotify rule, not a missing artist lookup.
func TestRouterSpotifyArtistGoesToMA(t *testing.T) {
	f := &hatest.Fake{}
	ma := New(f, defaults())
	r := player.NewRouter(defaults(), ma, sonos.New(f, &catalog.ITunes{}), ma)
	m, err := catalog.Parse("https://open.spotify.com/artist/0OdUWJ0sBjDrqHygGUXeCF?si=x")
	if err != nil {
		t.Fatal(err)
	}
	name, err := r.Play(context.Background(), player.Request{Media: m, Area: office, Volume: 15, Shuffle: true}, "")
	if err != nil || name != "music_assistant" {
		t.Fatalf("Play = %q, %v", name, err)
	}
	want := map[string]any{"media_id": "spotify://artist/0OdUWJ0sBjDrqHygGUXeCF", "media_type": "artist", "enqueue": "replace"}
	if got := f.Calls[1].Data; !reflect.DeepEqual(got, want) {
		t.Errorf("play_media data = %v, want %v", got, want)
	}
	for _, c := range f.Calls {
		if c.Target.EntityID[0] != "media_player.office_ma" {
			t.Errorf("call on %v, want only the MA entity", c.Target.EntityID)
		}
	}
}

// The router hands a Query to Sonos after MA resolved it: one search, then
// the Sonos sequence with the share link.
func TestRouterQueryOnSonos(t *testing.T) {
	f := &hatest.Fake{EntityList: entities(), Respond: func(c hatest.ServiceCall) (json.RawMessage, error) {
		if c.Service == "search" {
			return json.RawMessage(searchResult), nil
		}
		return nil, nil
	}}
	ma := New(f, defaults())
	r := player.NewRouter(defaults(), ma, sonos.New(f, nil), ma)
	name, err := r.Play(context.Background(), player.Request{Query: &player.Query{Artist: "Disturbed", Album: "Divisive"}, Area: office, Volume: 15}, "")
	if err != nil || name != "sonos" {
		t.Fatalf("Play = %q, %v", name, err)
	}
	var services []string
	for _, c := range f.Calls {
		services = append(services, c.Domain+"."+c.Service)
	}
	want := []string{"music_assistant.search", "media_player.media_stop", "media_player.volume_set", "media_player.repeat_set", "media_player.shuffle_set", "media_player.play_media", "media_player.repeat_set", "media_player.shuffle_set"}
	if !reflect.DeepEqual(services, want) {
		t.Fatalf("services = %v", services)
	}
	if got := f.Calls[5].Data["media_content_id"]; got != "https://music.apple.com/us/album/album-1646032515/1646032515" {
		t.Errorf("media_content_id = %v", got)
	}
}

func TestPlayUnmute(t *testing.T) {
	f := &hatest.Fake{}
	m, _ := catalog.Parse("https://music.apple.com/us/album/divisive/1646032515")
	req := player.Request{Media: m, Area: office, Volume: 20, Unmute: true}
	if err := New(f, defaults()).Play(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	want := hatest.ServiceCall{Domain: "media_player", Service: "volume_mute", Data: map[string]any{"is_volume_muted": false}, Target: maTarget()}
	if len(f.Calls) < 3 || f.Calls[0].Service != "volume_set" || !reflect.DeepEqual(f.Calls[1], want) || f.Calls[2].Service != "play_media" {
		t.Errorf("calls = %+v", f.Calls)
	}
}
