// Package catalog parses music URLs (Apple Music share links and
// apple_music:// URIs), builds share URLs and looks up songs in the iTunes API.
//
// Owner: track A (docs/PLAN.md section 10). Track B calls Parse.
package catalog

import (
	"context"
	"net/http"
)

// Kind is the kind of media a URL points to.
type Kind string

const (
	KindAlbum    Kind = "album"
	KindPlaylist Kind = "playlist"
	KindSong     Kind = "song"
	KindArtist   Kind = "artist"
	KindStation  Kind = "station"
	KindUnknown  Kind = "unknown"
)

// Media is a parsed music URL.
type Media struct {
	URL     string // original input
	Kind    Kind
	ID      string // catalog id, e.g. "1646032515" or "pl.e55d..."
	Country string // storefront, e.g. "us"
}

// Song is one track from an iTunes lookup.
type Song struct {
	TrackID int64
	Name    string
	Artist  string
	URL     string // https://music.apple.com/us/song/track/<TrackID>
}

// Parse reads a share link (music.apple.com/...) or an apple_music://<kind>/<id> URI.
func Parse(raw string) (Media, error) { panic("TODO: track A") }

// ShareURL returns a music.apple.com link Sonos can play.
func (m Media) ShareURL() string { panic("TODO: track A") }

// Apple looks up catalog data.
type Apple interface {
	ArtistSongs(ctx context.Context, artistID string, limit int) ([]Song, error)
}

// ITunes implements Apple with the public iTunes lookup API.
type ITunes struct {
	HTTP    *http.Client
	BaseURL string // default https://itunes.apple.com; tests point it at httptest
}

var _ Apple = (*ITunes)(nil)

func (c *ITunes) ArtistSongs(ctx context.Context, artistID string, limit int) ([]Song, error) {
	panic("TODO: track A")
}
