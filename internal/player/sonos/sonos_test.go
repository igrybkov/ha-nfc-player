package sonos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/ha/hatest"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

var familyRoom = rooms.Area{ID: "family_room", SonosEntity: "media_player.arc", Volume: 30}

func target() *ha.Target { return &ha.Target{EntityID: []string{"media_player.arc"}} }

func mustParse(t *testing.T, raw string) catalog.Media {
	t.Helper()
	m, err := catalog.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPlayAlbumSequence(t *testing.T) {
	f := &hatest.Fake{}
	p := New(f, nil)
	url := "https://music.apple.com/us/album/divisive/1646032515"
	req := player.Request{Media: mustParse(t, url), Area: familyRoom, Volume: 15, Shuffle: true, Repeat: "one"}
	if err := p.Play(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	want := []hatest.ServiceCall{
		{Domain: "media_player", Service: "media_stop", Target: target()},
		{Domain: "media_player", Service: "volume_set", Data: map[string]any{"volume_level": 0.15}, Target: target()},
		{Domain: "media_player", Service: "repeat_set", Data: map[string]any{"repeat": "one"}, Target: target()},
		{Domain: "media_player", Service: "shuffle_set", Data: map[string]any{"shuffle": true}, Target: target()},
		{Domain: "media_player", Service: "play_media", Data: map[string]any{"media_content_id": url, "media_content_type": "music"}, Target: target()},
	}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Errorf("calls:\n got %+v\nwant %+v", f.Calls, want)
	}
}

func TestPlayAppleMusicURIUsesShareLink(t *testing.T) {
	f := &hatest.Fake{}
	p := New(f, nil)
	req := player.Request{Media: mustParse(t, "apple_music://playlist/pl.abc"), Area: familyRoom}
	if err := p.Play(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	last := f.Calls[len(f.Calls)-1]
	if got := last.Data["media_content_id"]; got != "https://music.apple.com/us/playlist/playlist-pl.abc/pl.abc" {
		t.Errorf("media_content_id = %v", got)
	}
	// Missing repeat means "all", the old default.
	if got := f.Calls[2].Data["repeat"]; got != "all" {
		t.Errorf("repeat = %v", got)
	}
}

func itunesServer(t *testing.T, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "1797008889" || r.URL.Query().Get("limit") != "50" {
			t.Errorf("lookup query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPlayArtistQueuesSongs(t *testing.T) {
	results := []map[string]any{{"wrapperType": "artist", "artistId": 1797008889}}
	for _, id := range []int{11, 12, 13} {
		results = append(results, map[string]any{"wrapperType": "track", "kind": "song", "trackId": id})
	}
	body, _ := json.Marshal(map[string]any{"resultCount": len(results), "results": results})
	srv := itunesServer(t, string(body))

	f := &hatest.Fake{}
	p := New(f, &catalog.ITunes{HTTP: srv.Client(), BaseURL: srv.URL})
	artist := "https://music.apple.com/us/artist/%D0%B2%D0%BE%D0%BB%D0%BE%D0%B4%D0%B8%D0%BC%D0%B8%D1%80-%D1%96%D0%B2%D0%B0%D1%81%D1%8E%D0%BA/1797008889"
	req := player.Request{Media: mustParse(t, artist), Area: familyRoom, Volume: 25, Shuffle: true, Repeat: "all"}
	if !p.Supports(req) {
		t.Fatal("artist not supported")
	}
	if err := p.Play(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	song := func(id, enqueue string) hatest.ServiceCall {
		return hatest.ServiceCall{Domain: "media_player", Service: "play_media", Target: target(), Data: map[string]any{
			"media_content_id": "https://music.apple.com/us/song/track/" + id, "media_content_type": "music", "enqueue": enqueue,
		}}
	}
	want := []hatest.ServiceCall{
		{Domain: "media_player", Service: "media_stop", Target: target()},
		{Domain: "media_player", Service: "volume_set", Data: map[string]any{"volume_level": 0.25}, Target: target()},
		{Domain: "media_player", Service: "repeat_set", Data: map[string]any{"repeat": "all"}, Target: target()},
		{Domain: "media_player", Service: "shuffle_set", Data: map[string]any{"shuffle": true}, Target: target()},
		song("11", "replace"),
		song("12", "add"),
		song("13", "add"),
	}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Errorf("calls:\n got %+v\nwant %+v", f.Calls, want)
	}
}

func TestPlayArtistLookupFailureLeavesMusicAlone(t *testing.T) {
	srv := itunesServer(t, `{"resultCount":1,"results":[{"wrapperType":"artist"}]}`)
	f := &hatest.Fake{}
	p := New(f, &catalog.ITunes{HTTP: srv.Client(), BaseURL: srv.URL})
	req := player.Request{Media: catalog.Media{Kind: catalog.KindArtist, ID: "1797008889"}, Area: familyRoom}
	if err := p.Play(context.Background(), req); err == nil {
		t.Fatal("want error for an artist without songs")
	}
	if len(f.Calls) != 0 {
		t.Errorf("service calls after a failed lookup: %+v", f.Calls)
	}
}

func TestPlayStopsOnServiceError(t *testing.T) {
	boom := errors.New("unavailable")
	f := &hatest.Fake{Respond: func(c hatest.ServiceCall) (json.RawMessage, error) {
		if c.Service == "volume_set" {
			return nil, boom
		}
		return nil, nil
	}}
	p := New(f, nil)
	err := p.Play(context.Background(), player.Request{Media: catalog.Media{Kind: catalog.KindAlbum, ID: "1"}, Area: familyRoom})
	if !errors.Is(err, boom) || len(f.Calls) != 2 {
		t.Fatalf("err = %v, calls = %d", err, len(f.Calls))
	}
}

func TestSupports(t *testing.T) {
	p := New(&hatest.Fake{}, &catalog.ITunes{})
	noApple := New(&hatest.Fake{}, nil)
	tests := []struct {
		name string
		p    *Player
		req  player.Request
		want bool
	}{
		{"album", p, player.Request{Media: catalog.Media{Kind: catalog.KindAlbum}, Area: familyRoom}, true},
		{"playlist", p, player.Request{Media: catalog.Media{Kind: catalog.KindPlaylist}, Area: familyRoom}, true},
		{"song", p, player.Request{Media: catalog.Media{Kind: catalog.KindSong}, Area: familyRoom}, true},
		{"artist", p, player.Request{Media: catalog.Media{Kind: catalog.KindArtist}, Area: familyRoom}, true},
		{"artist without apple", noApple, player.Request{Media: catalog.Media{Kind: catalog.KindArtist}, Area: familyRoom}, false},
		{"station", p, player.Request{Media: catalog.Media{Kind: catalog.KindStation}, Area: familyRoom}, false},
		{"query", p, player.Request{Query: &player.Query{}, Area: familyRoom}, false},
		{"no entity", p, player.Request{Media: catalog.Media{Kind: catalog.KindAlbum}, Area: rooms.Area{ID: "x"}}, false},
		{"radio", p, player.Request{Media: catalog.Media{Kind: catalog.KindAlbum}, Area: familyRoom, Radio: true}, false},
	}
	for _, tt := range tests {
		if got := tt.p.Supports(tt.req); got != tt.want {
			t.Errorf("%s: Supports = %v, want %v", tt.name, got, tt.want)
		}
	}
}
