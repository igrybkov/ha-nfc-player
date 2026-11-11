// Package catalog parses music URLs (Apple Music and Spotify share links and
// provider URIs), builds share URLs and looks up songs in the iTunes API.
//
// Owner: track A (docs/PLAN.md section 10). Track B calls Parse.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
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

// DefaultCountry is the storefront used when a URL has none (apple_music:// URIs).
const DefaultCountry = "us"

// ErrUnsupported means the input is not an Apple Music or Spotify link or URI.
var ErrUnsupported = errors.New("unsupported music URL")

// Service is the streaming service a Media belongs to.
type Service string

const (
	ServiceAppleMusic Service = "apple_music"
	ServiceSpotify    Service = "spotify"
)

// Media is a parsed music URL.
type Media struct {
	URL     string  // original input
	Service Service // "" reads as ServiceAppleMusic
	Kind    Kind
	ID      string // catalog id, e.g. "1646032515", "pl.e55d..." or a Spotify base62 id
	Country string // Apple storefront, e.g. "us"; "" for Spotify
}

// IsSpotify reports whether m is Spotify media.
func (m Media) IsSpotify() bool { return m.Service == ServiceSpotify }

// Song is one track from an iTunes lookup.
type Song struct {
	TrackID int64
	Name    string
	Artist  string
	URL     string // https://music.apple.com/us/song/track/<TrackID>
}

var (
	stationID = regexp.MustCompile(`^ra\.[A-Za-z0-9._-]+$`)
	spotifyID = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)
	// providerURI matches <provider>://, plus the <provider>--<instance>://
	// form Music Assistant search returns for each provider instance.
	providerURI = regexp.MustCompile(`^(apple_music|spotify)(?:--[A-Za-z0-9]+)?://`)
)

var appleHosts = map[string]bool{
	"music.apple.com":     true,
	"geo.music.apple.com": true,
	"itunes.apple.com":    true,
}

// uriKinds maps the kind in apple_music://<kind>/<id> (Music Assistant names
// plus the share link names) to Kind.
var uriKinds = map[string]Kind{
	"album":    KindAlbum,
	"playlist": KindPlaylist,
	"track":    KindSong,
	"song":     KindSong,
	"artist":   KindArtist,
	"radio":    KindStation,
	"station":  KindStation,
}

// spotifyKinds maps the kind in Spotify links and URIs to Kind. Other
// Spotify kinds (show, episode, user, ...) are unsupported.
var spotifyKinds = map[string]Kind{
	"album":    KindAlbum,
	"playlist": KindPlaylist,
	"track":    KindSong,
	"artist":   KindArtist,
}

// Parse reads a music link or URI:
//
//   - Apple Music share links (music.apple.com/...)
//   - Spotify share links (open.spotify.com/...)
//   - apple_music://<kind>/<id> and spotify://<kind>/<id>, also with the
//     Music Assistant instance suffix (spotify--RiSye5Z5://album/<id>)
//   - spotify:<kind>:<id>
//
// Apple share link shapes: /<cc>/<kind>/<slug>/<id> and /<cc>/<kind>/<id>. An
// album link with ?i=<id> points to one song of that album and parses as
// KindSong. Apple paths with other kinds (music-video, curator, ...) parse as
// KindUnknown. Spotify kinds other than album, playlist, track and artist are
// ErrUnsupported.
func Parse(raw string) (Media, error) {
	s := strings.TrimSpace(raw)
	// Before url.Parse: "apple_music" has an underscore, which a URL scheme can't.
	if p := providerURI.FindStringSubmatch(s); p != nil {
		return parseURI(s, Service(p[1]), s[len(p[0]):])
	}
	if rest, ok := strings.CutPrefix(s, "spotify:"); ok {
		kindName, id, _ := strings.Cut(rest, ":")
		return parseSpotify(s, kindName, id)
	}
	u, err := url.Parse(s)
	if err != nil {
		return Media{}, fmt.Errorf("%w: %q: %v", ErrUnsupported, raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return Media{}, fmt.Errorf("%w: %q", ErrUnsupported, raw)
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case appleHosts[host]:
		return parseShareLink(s, u)
	case host == spotifyHost:
		return parseSpotifyLink(s, u)
	}
	return Media{}, fmt.Errorf("%w: %q", ErrUnsupported, raw)
}

// parseURI reads the <kind>/<id> rest of a provider URI.
func parseURI(raw string, service Service, rest string) (Media, error) {
	kindName, id, ok := strings.Cut(rest, "/")
	if !ok || strings.Contains(id, "/") {
		return Media{}, fmt.Errorf("%w: %q", ErrUnsupported, raw)
	}
	if service == ServiceSpotify {
		return parseSpotify(raw, kindName, id)
	}
	kind, known := uriKinds[kindName]
	if !known || id == "" {
		return Media{}, fmt.Errorf("%w: %q", ErrUnsupported, raw)
	}
	m := Media{URL: raw, Service: ServiceAppleMusic, Kind: kind, ID: id, Country: DefaultCountry}
	if err := checkID(m); err != nil {
		return Media{}, fmt.Errorf("%q: %w", raw, err)
	}
	return m, nil
}

// spotifyHost is the host of Spotify share links.
const spotifyHost = "open.spotify.com"

// parseSpotifyLink reads open.spotify.com/[intl-<lang>/]<kind>/<id>. The
// query (?si=...) is ignored.
func parseSpotifyLink(raw string, u *url.URL) (Media, error) {
	var parts []string
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 && strings.HasPrefix(parts[0], "intl-") {
		parts = parts[1:]
	}
	if len(parts) != 2 {
		return Media{}, fmt.Errorf("%w: %q: want /<kind>/<id> in path", ErrUnsupported, raw)
	}
	return parseSpotify(raw, parts[0], parts[1])
}

// parseSpotify checks a Spotify kind and id from any of the Spotify forms.
func parseSpotify(raw, kindName, id string) (Media, error) {
	kind, known := spotifyKinds[kindName]
	if !known {
		return Media{}, fmt.Errorf("%w: %q: Spotify kind %q", ErrUnsupported, raw, kindName)
	}
	if !spotifyID.MatchString(id) {
		return Media{}, fmt.Errorf("%q: Spotify %s id %q is not 22 letters and digits", raw, kind, id)
	}
	return Media{URL: raw, Service: ServiceSpotify, Kind: kind, ID: id}, nil
}

func parseShareLink(raw string, u *url.URL) (Media, error) {
	var parts []string
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	country := DefaultCountry
	if len(parts) > 0 && len(parts[0]) == 2 {
		country = strings.ToLower(parts[0])
		parts = parts[1:]
	}
	if len(parts) < 2 {
		return Media{}, fmt.Errorf("%w: %q: no kind and id in path", ErrUnsupported, raw)
	}
	m := Media{URL: raw, Service: ServiceAppleMusic, Kind: KindUnknown, Country: country, ID: parts[len(parts)-1]}
	switch parts[0] {
	case "album":
		m.Kind = KindAlbum
		if i := u.Query().Get("i"); i != "" {
			m.Kind, m.ID = KindSong, i
		}
	case "playlist":
		m.Kind = KindPlaylist
	case "song":
		m.Kind = KindSong
	case "artist":
		m.Kind = KindArtist
	case "station":
		m.Kind = KindStation
	}
	if err := checkID(m); err != nil {
		return Media{}, fmt.Errorf("%q: %w", raw, err)
	}
	return m, nil
}

// checkID rejects ids that don't fit the kind, so a slug is never taken for an id.
func checkID(m Media) error {
	switch m.Kind {
	case KindAlbum, KindSong, KindArtist:
		if _, err := strconv.ParseUint(m.ID, 10, 64); err != nil {
			return fmt.Errorf("%s id %q is not numeric", m.Kind, m.ID)
		}
	case KindPlaylist:
		if !strings.HasPrefix(m.ID, "pl.") {
			return fmt.Errorf("playlist id %q does not start with \"pl.\"", m.ID)
		}
	case KindStation:
		// The id goes into a Sonos URI as is, so it must be plain.
		if !stationID.MatchString(m.ID) {
			return fmt.Errorf("station id %q is not \"ra.\" and letters, digits, \".\", \"_\" or \"-\"", m.ID)
		}
	}
	return nil
}

// ShareURL returns a music.apple.com or open.spotify.com link Sonos can play.
//
// Spotify links are always built from kind and id as
// https://open.spotify.com/<kind>/<id>, with track for songs, so a ?si= query
// or an /intl-xx/ prefix never reaches Sonos.
//
// An Apple http(s) input is returned unchanged: that is what the old
// automation passed to Sonos. Anything else is built from kind and id as
// https://music.apple.com/<cc>/<kind>/<kind>-<id>/<id>, except songs, which
// use https://music.apple.com/<cc>/song/track/<id> like the artist queue.
func (m Media) ShareURL() string {
	if m.IsSpotify() {
		if kind := m.uriKind(); kind != "" {
			return "https://" + spotifyHost + "/" + kind + "/" + m.ID
		}
		return ""
	}
	if strings.HasPrefix(m.URL, "https://") || strings.HasPrefix(m.URL, "http://") {
		return m.URL
	}
	cc := m.Country
	if cc == "" {
		cc = DefaultCountry
	}
	if m.Kind == KindSong {
		return SongURL(cc, m.ID)
	}
	return fmt.Sprintf("https://music.apple.com/%s/%s/%s-%s/%s", cc, m.Kind, m.Kind, m.ID, m.ID)
}

// URI returns the Music Assistant URI apple_music://<kind>/<id> or
// spotify://<kind>/<id>, or "" for a kind Music Assistant has no name for.
// It is always the domain form, without an instance suffix.
func (m Media) URI() string {
	kind := m.uriKind()
	if kind == "" {
		return ""
	}
	service := m.Service
	if service == "" {
		service = ServiceAppleMusic
	}
	return string(service) + "://" + kind + "/" + m.ID
}

// uriKind is the kind name Music Assistant and Spotify use, or "" if there
// is none. Spotify has no stations.
func (m Media) uriKind() string {
	switch m.Kind {
	case KindAlbum, KindPlaylist, KindArtist:
		return string(m.Kind)
	case KindSong:
		return "track"
	case KindStation:
		if !m.IsSpotify() {
			return "radio"
		}
	}
	return ""
}

// SongURL is the share link of one song by track id.
func SongURL(country, trackID string) string {
	return "https://music.apple.com/" + country + "/song/track/" + trackID
}

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

const (
	defaultITunesURL = "https://itunes.apple.com"
	maxLookupBody    = 4 << 20
)

// ArtistSongs returns up to limit songs of an artist from the US storefront.
// The API answers with content type text/javascript; the body is JSON.
func (c *ITunes) ArtistSongs(ctx context.Context, artistID string, limit int) ([]Song, error) {
	base := c.BaseURL
	if base == "" {
		base = defaultITunesURL
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	q := url.Values{
		"id":      {artistID},
		"entity":  {"song"},
		"limit":   {strconv.Itoa(limit)},
		"country": {DefaultCountry},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/lookup?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itunes lookup %s: %w", artistID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("itunes lookup %s: status %s", artistID, resp.Status)
	}
	var body struct {
		Results []struct {
			WrapperType string `json:"wrapperType"`
			Kind        string `json:"kind"`
			TrackID     int64  `json:"trackId"`
			TrackName   string `json:"trackName"`
			ArtistName  string `json:"artistName"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxLookupBody)).Decode(&body); err != nil {
		return nil, fmt.Errorf("itunes lookup %s: decode: %w", artistID, err)
	}
	var songs []Song
	for _, r := range body.Results {
		// The first result is the artist itself. Music videos are tracks too.
		if r.WrapperType != "track" || (r.Kind != "" && r.Kind != "song") || r.TrackID == 0 {
			continue
		}
		songs = append(songs, Song{
			TrackID: r.TrackID,
			Name:    r.TrackName,
			Artist:  r.ArtistName,
			URL:     SongURL(DefaultCountry, strconv.FormatInt(r.TrackID, 10)),
		})
	}
	return songs, nil
}
