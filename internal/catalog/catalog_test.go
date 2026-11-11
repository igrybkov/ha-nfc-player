package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		kind    Kind
		id      string
		country string
	}{
		// The Music Map from docs/PLAN.md section 1.
		{"metallica playlist", "https://music.apple.com/us/playlist/metallica-essentials/pl.e55da0d4a18140e3b1120339e2608aa5", KindPlaylist, "pl.e55da0d4a18140e3b1120339e2608aa5", "us"},
		{"disturbed album", "https://music.apple.com/us/album/divisive/1646032515", KindAlbum, "1646032515", "us"},
		{"ccr playlist", "https://music.apple.com/us/playlist/creedence-clearwater-revival-essentials/pl.a64e22c649a64385b037a8f1a1559cb1", KindPlaylist, "pl.a64e22c649a64385b037a8f1a1559cb1", "us"},
		{"queen album", "https://music.apple.com/us/album/bohemian-rhapsody-the-original-soundtrack/6781027361", KindAlbum, "6781027361", "us"},
		{"ivasiuk artist cyrillic slug", "https://music.apple.com/us/artist/%D0%B2%D0%BE%D0%BB%D0%BE%D0%B4%D0%B8%D0%BC%D0%B8%D1%80-%D1%96%D0%B2%D0%B0%D1%81%D1%8E%D0%BA/1797008889", KindArtist, "1797008889", "us"},
		{"ray charles album", "https://music.apple.com/us/album/the-atlantic-hits/1544753335", KindAlbum, "1544753335", "us"},
		{"kenny rogers playlist", "https://music.apple.com/us/playlist/kenny-rogers-essentials/pl.97c239b65c7e4e3eb786f27c74790b8b", KindPlaylist, "pl.97c239b65c7e4e3eb786f27c74790b8b", "us"},

		{"song link", "https://music.apple.com/us/song/one/1234567", KindSong, "1234567", "us"},
		{"song without slug", "https://music.apple.com/us/song/1234567", KindSong, "1234567", "us"},
		{"album link with song param", "https://music.apple.com/ua/album/divisive/1646032515?i=1646032520", KindSong, "1646032520", "ua"},
		{"album without slug", "https://music.apple.com/us/album/1646032515", KindAlbum, "1646032515", "us"},
		{"no storefront", "https://music.apple.com/album/divisive/1646032515", KindAlbum, "1646032515", "us"},
		{"station", "https://music.apple.com/us/station/apple-music-1/ra.978194965", KindStation, "ra.978194965", "us"},
		{"unknown kind", "https://music.apple.com/us/music-video/foo/1500", KindUnknown, "1500", "us"},
		{"spaces trimmed", "  https://music.apple.com/us/album/divisive/1646032515\n", KindAlbum, "1646032515", "us"},

		{"uri album", "apple_music://album/1646032515", KindAlbum, "1646032515", "us"},
		{"uri playlist", "apple_music://playlist/pl.e55da0d4a18140e3b1120339e2608aa5", KindPlaylist, "pl.e55da0d4a18140e3b1120339e2608aa5", "us"},
		{"uri track", "apple_music://track/1646032520", KindSong, "1646032520", "us"},
		{"uri artist", "apple_music://artist/1797008889", KindArtist, "1797008889", "us"},
		// Music Assistant search returns provider URIs with an instance suffix.
		{"suffixed apple uri", "apple_music--P6Nxuakq://playlist/pl.e55da0d4a18140e3b1120339e2608aa5", KindPlaylist, "pl.e55da0d4a18140e3b1120339e2608aa5", "us"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if m.Kind != tt.kind || m.ID != tt.id || m.Country != tt.country || m.Service != ServiceAppleMusic {
				t.Errorf("got kind=%s id=%s country=%s service=%s, want kind=%s id=%s country=%s service=apple_music",
					m.Kind, m.ID, m.Country, m.Service, tt.kind, tt.id, tt.country)
			}
		})
	}
}

func TestParseSpotify(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		kind Kind
		id   string
	}{
		{"spotify album", "https://open.spotify.com/album/5gzLOflH95LkKYE6XSXE9k", KindAlbum, spAlbum},
		{"spotify playlist si", "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M?si=1a2b3c4d5e6f", KindPlaylist, "37i9dQZF1DXcBWIGoYBM5M"},
		{"spotify track intl", "https://open.spotify.com/intl-de/track/4uLU6hMCjMI75M1A2tKUQC?si=x", KindSong, "4uLU6hMCjMI75M1A2tKUQC"},
		{"spotify artist", "https://open.spotify.com/artist/0OdUWJ0sBjDrqHygGUXeCF", KindArtist, "0OdUWJ0sBjDrqHygGUXeCF"},
		{"spotify link trailing slash", "https://open.spotify.com/album/5gzLOflH95LkKYE6XSXE9k/", KindAlbum, spAlbum},
		{"spotify colon uri", "spotify:album:5gzLOflH95LkKYE6XSXE9k", KindAlbum, spAlbum},
		{"spotify colon track", "spotify:track:4uLU6hMCjMI75M1A2tKUQC", KindSong, "4uLU6hMCjMI75M1A2tKUQC"},
		{"spotify ma uri", "spotify://artist/0OdUWJ0sBjDrqHygGUXeCF", KindArtist, "0OdUWJ0sBjDrqHygGUXeCF"},
		{"spotify ma track", "spotify://track/4uLU6hMCjMI75M1A2tKUQC", KindSong, "4uLU6hMCjMI75M1A2tKUQC"},
		{"suffixed spotify uri", "spotify--RiSye5Z5://album/5gzLOflH95LkKYE6XSXE9k", KindAlbum, spAlbum},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if m.Kind != tt.kind || m.ID != tt.id || m.Service != ServiceSpotify || m.Country != "" {
				t.Errorf("got %+v, want kind=%s id=%s service=spotify, no country", m, tt.kind, tt.id)
			}
		})
	}
}

// spAlbum is a real Spotify album id from a Music Assistant search result.
const spAlbum = "5gzLOflH95LkKYE6XSXE9k"

func TestParseErrors(t *testing.T) {
	for _, raw := range []string{
		"",
		"Metallica — Essential",
		"https://open.spotify.com/album/abc",
		"ftp://music.apple.com/us/album/x/1",
		"https://music.apple.com/us/",
		"https://music.apple.com/us/album/divisive/not-a-number",
		"https://music.apple.com/us/playlist/metallica/12345",
		"apple_music://album/",
		"apple_music://album/abc",
		"apple_music://podcast/1",
		"apple_music://album/1/2",
		"library://album/12",
		"apple_music://radio/ra.1&sn=9", // goes into a Sonos URI
		"apple_music://radio/ra.",
		"apple_music--://album/1",
		"apple_music--P6Nxuakq://album/abc",

		"https://open.spotify.com/album/5gzLOflH95LkKYE6XSXE9",    // 21 chars
		"https://open.spotify.com/album/5gzLOflH95LkKYE6XSXE9kX",  // 23 chars
		"https://open.spotify.com/album/5gzLOflH95LkKYE6XSXE-k",   // not base62
		"https://open.spotify.com/album",                          // no id
		"https://open.spotify.com/album/5gzLOflH95LkKYE6XSXE9k/x", // extra segment
		"https://open.spotify.com/intl-de/",
		"spotify:album:short",
		"spotify:album:5gzLOflH95LkKYE6XSXE9k:x",
		"spotify://album/5gzLOflH95LkKYE6XSXE9k/x",
		"spotify://song/4uLU6hMCjMI75M1A2tKUQC", // MA and Spotify call it track
		"spotify--RiSye5Z5://album/bad",
		"spotify://radio/5gzLOflH95LkKYE6XSXE9k",
	} {
		if m, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) = %+v, want error", raw, m)
		}
	}
	for _, raw := range []string{
		"https://example.com/x/y",
		"https://open.spotify.com/show/5gzLOflH95LkKYE6XSXE9k",
		"https://open.spotify.com/episode/5gzLOflH95LkKYE6XSXE9k?si=x",
		"https://open.spotify.com/user/spotify",
		"spotify:show:5gzLOflH95LkKYE6XSXE9k",
		"spotify://episode/5gzLOflH95LkKYE6XSXE9k",
		"spotify--RiSye5Z5://show/5gzLOflH95LkKYE6XSXE9k",
	} {
		if _, err := Parse(raw); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q): err = %v, want ErrUnsupported", raw, err)
		}
	}
}

func TestShareURL(t *testing.T) {
	tests := []struct {
		raw, want string
	}{
		// http(s) links pass through unchanged, as in the old automation.
		{"https://music.apple.com/us/album/divisive/1646032515", "https://music.apple.com/us/album/divisive/1646032515"},
		{"https://music.apple.com/us/artist/%D0%B2%D0%BE%D0%BB%D0%BE%D0%B4%D0%B8%D0%BC%D0%B8%D1%80-%D1%96%D0%B2%D0%B0%D1%81%D1%8E%D0%BA/1797008889", "https://music.apple.com/us/artist/%D0%B2%D0%BE%D0%BB%D0%BE%D0%B4%D0%B8%D0%BC%D0%B8%D1%80-%D1%96%D0%B2%D0%B0%D1%81%D1%8E%D0%BA/1797008889"},
		// apple_music:// URIs use the section 1 rule.
		{"apple_music://album/1646032515", "https://music.apple.com/us/album/album-1646032515/1646032515"},
		{"apple_music://playlist/pl.e55da0d4a18140e3b1120339e2608aa5", "https://music.apple.com/us/playlist/playlist-pl.e55da0d4a18140e3b1120339e2608aa5/pl.e55da0d4a18140e3b1120339e2608aa5"},
		{"apple_music://artist/1797008889", "https://music.apple.com/us/artist/artist-1797008889/1797008889"},
		{"apple_music://track/42", "https://music.apple.com/us/song/track/42"},
		{"apple_music--P6Nxuakq://album/1646032515", "https://music.apple.com/us/album/album-1646032515/1646032515"},
		// Spotify links are rebuilt, so ?si= and /intl-xx/ never reach Sonos.
		{"https://open.spotify.com/intl-de/album/5gzLOflH95LkKYE6XSXE9k?si=abc", "https://open.spotify.com/album/5gzLOflH95LkKYE6XSXE9k"},
		{"spotify:playlist:37i9dQZF1DXcBWIGoYBM5M", "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M"},
		{"spotify://track/4uLU6hMCjMI75M1A2tKUQC", "https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC"},
		{"spotify--RiSye5Z5://artist/0OdUWJ0sBjDrqHygGUXeCF", "https://open.spotify.com/artist/0OdUWJ0sBjDrqHygGUXeCF"},
	}
	for _, tt := range tests {
		m, err := Parse(tt.raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.raw, err)
		}
		if got := m.ShareURL(); got != tt.want {
			t.Errorf("ShareURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// Suffixed provider URIs come back in the domain form.
func TestURINormalizesInstanceSuffix(t *testing.T) {
	for raw, want := range map[string]string{
		"apple_music--P6Nxuakq://playlist/pl.xxx":      "apple_music://playlist/pl.xxx",
		"spotify--RiSye5Z5://album/" + spAlbum:         "spotify://album/" + spAlbum,
		"https://open.spotify.com/track/" + spAlbum:    "spotify://track/" + spAlbum,
		"spotify:artist:" + spAlbum:                    "spotify://artist/" + spAlbum,
		"https://music.apple.com/us/album/x/123?i=456": "apple_music://track/456",
	} {
		m, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		if got := m.URI(); got != want {
			t.Errorf("URI(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestURI(t *testing.T) {
	tests := []struct {
		m    Media
		want string
	}{
		{Media{Kind: KindAlbum, ID: "1"}, "apple_music://album/1"},
		{Media{Kind: KindPlaylist, ID: "pl.x"}, "apple_music://playlist/pl.x"},
		{Media{Kind: KindSong, ID: "2"}, "apple_music://track/2"},
		{Media{Kind: KindArtist, ID: "3"}, "apple_music://artist/3"},
		{Media{Kind: KindStation, ID: "ra.4"}, "apple_music://radio/ra.4"},
		{Media{Kind: KindUnknown, ID: "5"}, ""},
		{Media{Service: ServiceAppleMusic, Kind: KindAlbum, ID: "1"}, "apple_music://album/1"},
		{Media{Service: ServiceSpotify, Kind: KindAlbum, ID: spAlbum}, "spotify://album/" + spAlbum},
		{Media{Service: ServiceSpotify, Kind: KindPlaylist, ID: "p"}, "spotify://playlist/p"},
		{Media{Service: ServiceSpotify, Kind: KindSong, ID: "t"}, "spotify://track/t"},
		{Media{Service: ServiceSpotify, Kind: KindArtist, ID: "a"}, "spotify://artist/a"},
		{Media{Service: ServiceSpotify, Kind: KindStation, ID: "s"}, ""},
	}
	for _, tt := range tests {
		if got := tt.m.URI(); got != tt.want {
			t.Errorf("URI(%+v) = %q, want %q", tt.m, got, tt.want)
		}
	}
}

// lookupBody is a trimmed real response: artist first, then songs, plus a
// music video that must be skipped.
const lookupBody = `{
 "resultCount": 4,
 "results": [
  {"wrapperType": "artist", "artistType": "Artist", "artistName": "Volodymyr Ivasiuk", "artistId": 1797008889},
  {"wrapperType": "track", "kind": "song", "trackId": 1797008901, "trackName": "Червона рута", "artistName": "Volodymyr Ivasiuk"},
  {"wrapperType": "track", "kind": "music-video", "trackId": 99, "trackName": "Video", "artistName": "Volodymyr Ivasiuk"},
  {"wrapperType": "track", "kind": "song", "trackId": 1797008902, "trackName": "Водограй", "artistName": "Volodymyr Ivasiuk"}
 ]
}`

func TestArtistSongs(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lookup" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write([]byte(lookupBody))
	}))
	defer srv.Close()

	c := &ITunes{HTTP: srv.Client(), BaseURL: srv.URL}
	songs, err := c.ArtistSongs(context.Background(), "1797008889", 50)
	if err != nil {
		t.Fatal(err)
	}
	if want := "country=us&entity=song&id=1797008889&limit=50"; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
	if len(songs) != 2 {
		t.Fatalf("got %d songs, want 2: %+v", len(songs), songs)
	}
	if songs[0].TrackID != 1797008901 || songs[0].URL != "https://music.apple.com/us/song/track/1797008901" || songs[0].Name != "Червона рута" {
		t.Errorf("songs[0] = %+v", songs[0])
	}
	if songs[1].TrackID != 1797008902 {
		t.Errorf("songs[1] = %+v", songs[1])
	}
}

func TestArtistSongsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := &ITunes{HTTP: srv.Client(), BaseURL: srv.URL}
	if _, err := c.ArtistSongs(context.Background(), "1", 50); err == nil {
		t.Fatal("want error on 503")
	}
}
