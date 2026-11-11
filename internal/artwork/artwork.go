// Package artwork resolves, downloads, caches and square-crops entry artwork.
//
// Owner: track B (docs/PLAN.md section 10). The resolution chain is a port of
// docs/reference/nfc-music-labels.py:
//
//  1. The entry's label.artwork override: an http(s) URL, or a file path
//     under ConfigDir. It is never written to the index.
//  2. The art URL found on an earlier run, from artwork.json.
//  3. For a Spotify entry URL: the Spotify oEmbed thumbnail, with the size
//     marker in the image id swapped for the 640px one. The Apple steps
//     below are skipped for it.
//  4. For any other entry URL: the iTunes lookup for an album id
//     (artworkUrl100 with the size swapped to 1200x1200), then the page's
//     og:image with the last path segment swapped to 1200x1200bb.jpg. Both
//     run for any URL, since albums get pulled from Apple Music and the lookup
//     then finds nothing.
//  5. iTunes album search by name.
//
// Each step that fails is logged and the next one runs. A key whose chain
// ran cleanly and found nothing is remembered in memory for NegativeTTL. If a
// step failed with a transient error (network, timeout, 5xx), the chain
// returns that error instead and nothing is remembered. Concurrent lookups
// for one key share a single run. An index URL that now returns 404 or 410 is
// dropped and the chain runs once more.
//
// # Network guard
//
// The chain (pages, iTunes, Spotify oEmbed, the art it finds, index URLs)
// only fetches from hosts AllowHost accepts, checked before each request and on each redirect.
// A label.artwork URL may point at any host, but every connection, chain or
// override, is refused when the resolved IP is loopback, private, link-local,
// CGNAT, unspecified or multicast. The check runs on the dialed IP, so DNS
// rebinding does not get around it.
package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/draw"
	_ "image/gif" // decoders for downloaded and override art
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "golang.org/x/image/webp" // override files are sometimes webp

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// ErrNotFound means every step of the chain ran and none found art.
var ErrNotFound = errors.New("artwork: not found")

// ErrBlocked means a request was refused by the network guard: a host the
// chain may not use, or an address that is not public.
var ErrBlocked = errors.New("artwork: address not allowed")

// ErrTooLarge means an image has more than MaxPixels pixels.
var ErrTooLarge = errors.New("artwork: image too large")

// Defaults for the zero-value fields of Cache.
const (
	DefaultITunesURL = "https://itunes.apple.com"
	DefaultOEmbedURL = "https://open.spotify.com/oembed"
	DefaultUserAgent = "Mozilla/5.0 ha-nfc-player"
	defaultTimeout   = 20 * time.Second

	maxPageBytes  = 5 << 20
	maxJSONBytes  = 2 << 20
	maxImageBytes = 20 << 20
	maxRedirects  = 10

	// MaxPixels caps width*height of any image before it is decoded, so a
	// small file that claims a huge size can't exhaust memory.
	MaxPixels = 50_000_000

	// NegativeTTL is how long a key whose chain found nothing is not looked
	// up again. Refresh skips it.
	NegativeTTL = time.Hour

	indexFile = "artwork.json"
	imageDir  = "art"
)

// Cache implements model.ArtworkSource with a disk cache under Dir: images
// in Dir/art keyed by sha256 of the URL, plus Dir/artwork.json that maps
// "{name}\n{url}" to the resolved art URL. Use it by pointer; the zero value
// with Dir set works.
type Cache struct {
	Dir       string // e.g. /data/cache
	ConfigDir string // base for relative label.artwork paths
	HTTP      *http.Client
	ITunesURL string // default DefaultITunesURL; tests point it at httptest
	OEmbedURL string // Spotify oEmbed endpoint; default DefaultOEmbedURL
	UserAgent string // default DefaultUserAgent; Apple pages want a browser-like one
	Logger    *slog.Logger
	// AllowHost reports whether the lookup chain may fetch from host (lower
	// case, no port). nil = DefaultAllowHost. Override URLs skip it.
	AllowHost func(host string) bool

	// allowPrivate turns off the dial-time IP check; tests set it to reach
	// httptest on 127.0.0.1.
	allowPrivate bool

	clientOnce              sync.Once
	chainHTTP, overrideHTTP *http.Client

	mu sync.Mutex // serializes artwork.json read-modify-write

	flightMu sync.Mutex
	flights  map[string]*flight   // resolutions in progress, by entry key
	failed   map[string]time.Time // when the chain last found nothing, by entry key
	now      func() time.Time     // clock; tests replace it

	candMu     sync.Mutex
	candidates map[string]candidateSet // art picker results, by entry key
}

// flight is one resolution that concurrent callers for the same key wait on.
type flight struct {
	done    chan struct{}
	url     string
	err     error
	waiters int // callers sharing this run besides the leader; for tests
}

var _ model.ArtworkSource = (*Cache)(nil)

// Artwork returns square art for e. refresh skips the index entry and the
// cached image file for this entry, and stores the new ones.
func (c *Cache) Artwork(ctx context.Context, e model.Entry, refresh bool) (image.Image, error) {
	var img image.Image
	var err error
	switch override := overrideOf(e); {
	case override != "" && !isHTTP(override):
		img, err = c.loadFile(override)
	case override != "":
		img, err = c.loadURL(ctx, override, refresh, false)
	default:
		img, err = c.resolveAndLoad(ctx, e, refresh)
	}
	if err != nil {
		return nil, fmt.Errorf("artwork for %q: %w", e.Name, err)
	}
	return cropSquare(img), nil
}

// ResolveURL finds the art URL for e (override, Spotify oEmbed or iTunes
// lookup and og:image, search). It does not use or update the index. A path override is returned
// as given. It returns ErrNotFound only when every step ran cleanly; if a
// step hit a transient error and no later step found art, that error is
// returned instead.
func (c *Cache) ResolveURL(ctx context.Context, e model.Entry) (string, error) {
	if override := overrideOf(e); override != "" {
		return override, nil
	}
	var steps []func() (string, error)
	if m, err := catalog.Parse(e.URL); err == nil && m.IsSpotify() {
		// Not the Apple steps: the album regex would read the leading digits
		// of a base62 id, and og:image is not Apple's image URL scheme.
		steps = append(steps, func() (string, error) { return c.spotifyArtwork(ctx, m) })
	} else if e.URL != "" {
		steps = append(steps,
			func() (string, error) { return c.albumArtwork(ctx, e.URL) },
			func() (string, error) { return c.pageArtwork(ctx, e.URL) })
	}
	steps = append(steps, func() (string, error) { return c.searchArtwork(ctx, e.Name) })
	var transient error
	for _, step := range steps {
		found, err := step()
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			c.logger().Warn("artwork lookup failed", "name", e.Name, "error", err)
			if transient == nil && isTransient(err) {
				transient = err
			}
			continue
		}
		if found != "" {
			return found, nil
		}
	}
	if transient != nil {
		return "", fmt.Errorf("artwork lookup incomplete: %w", transient)
	}
	return "", ErrNotFound
}

func (c *Cache) resolveAndLoad(ctx context.Context, e model.Entry, refresh bool) (image.Image, error) {
	key := e.Name + "\n" + e.URL
	source := ""
	if !refresh {
		index, err := c.readIndex()
		if err != nil {
			c.logger().Warn("artwork index unreadable; resolving again", "error", err)
		}
		source = index[key]
	}
	fromIndex := source != ""
	if !fromIndex {
		var err error
		if source, err = c.resolveKey(ctx, e, key, refresh); err != nil {
			return nil, err
		}
	}
	img, err := c.loadURL(ctx, source, refresh, true)
	if err == nil || !fromIndex || !isGone(err) {
		return img, err
	}
	// Apple pulled the art the index points to. Forget it and look again once.
	c.logger().Info("indexed artwork is gone; resolving again", "name", e.Name, "url", source, "error", err)
	if err := c.updateIndex(func(index map[string]string) {
		if index[key] == source {
			delete(index, key)
		}
	}); err != nil {
		c.logger().Warn("artwork index not saved", "error", err)
	}
	if source, err = c.resolveKey(ctx, e, key, false); err != nil {
		return nil, err
	}
	return c.loadURL(ctx, source, refresh, true)
}

// resolveKey runs the lookup chain for an entry key and stores the result in
// the index. Unless refresh is set, a recent miss returns ErrNotFound with no
// network, and a caller arriving while a lookup for the key runs waits for
// that one. Waiters share the leader's result, including its context error.
func (c *Cache) resolveKey(ctx context.Context, e model.Entry, key string, refresh bool) (string, error) {
	if refresh {
		return c.resolveAndRecord(ctx, e, key)
	}
	c.flightMu.Lock()
	if at, ok := c.failed[key]; ok {
		if c.clock().Sub(at) < NegativeTTL {
			c.flightMu.Unlock()
			return "", fmt.Errorf("%w (cached miss since %s)", ErrNotFound, at.Format(time.RFC3339))
		}
		delete(c.failed, key)
	}
	if f, ok := c.flights[key]; ok {
		f.waiters++
		c.flightMu.Unlock()
		select {
		case <-f.done:
			return f.url, f.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	if c.flights == nil {
		c.flights = map[string]*flight{}
	}
	c.flights[key] = f
	c.flightMu.Unlock()

	f.url, f.err = c.resolveAndRecord(ctx, e, key)

	c.flightMu.Lock()
	delete(c.flights, key)
	c.flightMu.Unlock()
	close(f.done)
	return f.url, f.err
}

// resolveAndRecord runs the chain, saves a hit in the index and remembers a
// clean miss in memory. Context and transient errors are not misses.
func (c *Cache) resolveAndRecord(ctx context.Context, e model.Entry, key string) (string, error) {
	source, err := c.ResolveURL(ctx, e)
	c.flightMu.Lock()
	switch {
	case errors.Is(err, ErrNotFound):
		if c.failed == nil {
			c.failed = map[string]time.Time{}
		}
		c.failed[key] = c.clock()
	case err == nil:
		delete(c.failed, key)
	}
	c.flightMu.Unlock()
	if err != nil {
		return "", err
	}
	if err := c.updateIndex(func(index map[string]string) { index[key] = source }); err != nil {
		c.logger().Warn("artwork index not saved", "error", err)
	}
	return source, nil
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// overrideOf returns the trimmed label.artwork override, or "".
func overrideOf(e model.Entry) string {
	if e.Label.Artwork == nil {
		return ""
	}
	return strings.TrimSpace(*e.Label.Artwork)
}

func isHTTP(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// Lookup steps.

var (
	albumIDPattern = regexp.MustCompile(`/album/(?:[^/]+/)?(\d+)`)
	artSizePattern = regexp.MustCompile(`/\d+x\d+bb\.`)
	ogImagePattern = regexp.MustCompile(`<meta[^>]+property="og:image"[^>]+content="([^"]+)"`)
	lastSegment    = regexp.MustCompile(`/[^/?]+(\?.*)?$`)
)

// itunesResult is the part of an iTunes lookup/search result we use.
type itunesResult struct {
	ArtworkURL100 string `json:"artworkUrl100"`
}

// itunesArtwork swaps the 100x100 rendition for 1200x1200.
func itunesArtwork(results []itunesResult) string {
	if len(results) == 0 || results[0].ArtworkURL100 == "" {
		return ""
	}
	return artSizePattern.ReplaceAllString(results[0].ArtworkURL100, "/1200x1200bb.")
}

// albumArtwork looks up an album URL's id in iTunes. Not an album URL: "".
// This is the script's regex, not catalog.Parse, on purpose: Parse rejects
// hosts other than Apple's (the tests serve pages from httptest), and it
// reads an album link with ?i=<song> as the song, while the art is the album's.
func (c *Cache) albumArtwork(ctx context.Context, rawURL string) (string, error) {
	m := albumIDPattern.FindStringSubmatch(rawURL)
	if m == nil {
		return "", nil
	}
	var data struct{ Results []itunesResult }
	if err := c.getJSON(ctx, c.itunesURL()+"/lookup?id="+m[1], &data); err != nil {
		return "", fmt.Errorf("itunes lookup %s: %w", m[1], err)
	}
	return itunesArtwork(data.Results), nil
}

// pageArtwork reads the page's og:image. og:image is a 1200x630 social card
// (for "Essentials" playlists, the artist photo with a branded overlay); the
// last path segment picks the rendition, so swap it for a square one.
func (c *Cache) pageArtwork(ctx context.Context, pageURL string) (string, error) {
	if !isHTTP(pageURL) {
		return "", nil // apple_music:// URIs have no page
	}
	page, err := c.get(ctx, pageURL, maxPageBytes, true)
	if err != nil {
		return "", fmt.Errorf("page %s: %w", pageURL, err)
	}
	m := ogImagePattern.FindSubmatch(page)
	if m == nil {
		return "", nil
	}
	return lastSegment.ReplaceAllString(html.UnescapeString(string(m[1])), "/1200x1200bb.jpg"), nil
}

// spotifySizes maps the size marker that starts a Spotify image id in an
// oEmbed thumbnail to the 640px one. Other markers stay as they are.
var spotifySizes = map[string]string{
	"ab67616d00001e02": "ab67616d0000b273", // album and track, 300 → 640
	"ab67616100005174": "ab6761610000e5eb", // artist, 320 → 640
}

// spotifyArtwork asks Spotify oEmbed for the thumbnail of the media's
// canonical share link. It needs no key and works for every Spotify kind.
func (c *Cache) spotifyArtwork(ctx context.Context, m catalog.Media) (string, error) {
	share := m.ShareURL()
	var data struct {
		ThumbnailURL string `json:"thumbnail_url"`
	}
	if err := c.getJSON(ctx, c.oembedURL()+"?url="+url.QueryEscape(share), &data); err != nil {
		return "", fmt.Errorf("spotify oembed %s: %w", share, err)
	}
	return spotifyFullSize(data.ThumbnailURL), nil
}

// spotifyImageID is one Spotify image id: a 16-hex size marker and 24 hex.
// A mosaic (a playlist cover made of four ids) does not match, so it keeps
// its size.
var spotifyImageID = regexp.MustCompile(`^[0-9a-f]{40}$`)

// spotifyFullSize swaps the size marker at the start of the image id (the
// last path segment) for the 640px one.
func spotifyFullSize(thumb string) string {
	i := strings.LastIndexByte(thumb, '/')
	if i < 0 || !spotifyImageID.MatchString(thumb[i+1:]) {
		return thumb
	}
	dir, id := thumb[:i+1], thumb[i+1:]
	for small, large := range spotifySizes {
		if rest, ok := strings.CutPrefix(id, small); ok {
			return dir + large + rest
		}
	}
	return thumb
}

// searchArtwork finds the first album for the name in the iTunes search.
func (c *Cache) searchArtwork(ctx context.Context, name string) (string, error) {
	q := url.Values{
		"term":   {strings.ReplaceAll(name, "—", " ")},
		"entity": {"album"},
		"limit":  {"1"},
	}
	var data struct{ Results []itunesResult }
	if err := c.getJSON(ctx, c.itunesURL()+"/search?"+q.Encode(), &data); err != nil {
		return "", fmt.Errorf("itunes search %q: %w", name, err)
	}
	return itunesArtwork(data.Results), nil
}

// Images.

// loadURL returns the image at u, from Dir/art unless refresh is set. chain
// selects the chain policy (AllowHost) over the override one (any public host).
func (c *Cache) loadURL(ctx context.Context, u string, refresh, chain bool) (image.Image, error) {
	sum := sha256.Sum256([]byte(u))
	path := filepath.Join(c.Dir, imageDir, hex.EncodeToString(sum[:])+".img")
	if !refresh {
		if img, err := decodeFile(path); err == nil {
			return img, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			c.logger().Warn("cached artwork unreadable; downloading again", "path", path, "error", err)
		}
	}
	data, err := c.get(ctx, u, maxImageBytes, chain)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", u, err)
	}
	img, err := decodeImage(data)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", u, err)
	}
	if err := writeAtomic(path, data); err != nil {
		c.logger().Warn("artwork not cached", "path", path, "error", err)
	}
	return img, nil
}

// loadFile opens an override path. Relative paths are under ConfigDir;
// absolute ones must be too. os.Root rejects any escape, symlinks included.
func (c *Cache) loadFile(p string) (image.Image, error) {
	if c.ConfigDir == "" {
		return nil, fmt.Errorf("artwork path %q: no config dir set", p)
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(c.ConfigDir, p)
		if err != nil {
			return nil, fmt.Errorf("artwork path %q: %w", p, err)
		}
		p = rel
	}
	f, err := os.OpenInRoot(c.ConfigDir, p)
	if err != nil {
		return nil, fmt.Errorf("artwork path: %w", err)
	}
	defer f.Close()
	data, err := readLimited(f, maxImageBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	img, err := decodeImage(data)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", p, err)
	}
	return img, nil
}

func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readLimited(f, maxImageBytes)
	if err != nil {
		return nil, err
	}
	return decodeImage(data)
}

// readLimited reads all of r, failing when it holds more than limit bytes.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}

// decodeImage reads the header first and refuses images over MaxPixels, so
// a decompression bomb never gets its pixel buffer allocated.
func decodeImage(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, fmt.Errorf("%w: %dx%d", ErrTooLarge, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// cropSquare center-crops img to a square (script: crop_to(image, 1)).
func cropSquare(img image.Image) image.Image {
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	x, y := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	r := image.Rect(x, y, x+side, y+side)
	if r == b {
		return img
	}
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
	return out
}

// Index.

func (c *Cache) readIndex() (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readIndexLocked()
}

func (c *Cache) readIndexLocked() (map[string]string, error) {
	index := map[string]string{}
	data, err := os.ReadFile(filepath.Join(c.Dir, indexFile))
	if errors.Is(err, os.ErrNotExist) {
		return index, nil
	}
	if err != nil {
		return index, err
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return map[string]string{}, fmt.Errorf("%s: %w", indexFile, err)
	}
	return index, nil
}

// updateIndex applies change to artwork.json under the index mutex.
func (c *Cache) updateIndex(change func(index map[string]string)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	index, err := c.readIndexLocked()
	if err != nil {
		c.logger().Warn("artwork index unreadable; starting a new one", "error", err)
	}
	change(index)
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(c.Dir, indexFile), append(data, '\n'))
}

// writeAtomic writes data to a temp file next to path, then renames it.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// HTTP.

// get fetches u. chain applies AllowHost to u and to every redirect.
func (c *Cache) get(ctx context.Context, u string, limit int64, chain bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	client := c.overrideClient()
	if chain {
		if err := c.checkHost(req.URL); err != nil {
			return nil, err
		}
		client = c.chainClient()
	} else if err := checkScheme(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response larger than %d bytes", limit)
	}
	return data, nil
}

// HTTPError is a non-200 response.
type HTTPError struct {
	StatusCode int
	Status     string
}

func (e *HTTPError) Error() string { return "HTTP " + e.Status }

// isGone reports whether err is a 404 or 410 response.
func isGone(err error) bool {
	var h *HTTPError
	return errors.As(err, &h) && (h.StatusCode == http.StatusNotFound || h.StatusCode == http.StatusGone)
}

// isTransient reports whether a lookup error may go away on its own: a
// network error, a timeout, a 5xx or a 429. A blocked address, another 4xx or
// a body that does not parse is a clean answer. The guard check comes first,
// since its errors arrive wrapped in *url.Error like network errors do.
func isTransient(err error) bool {
	if errors.Is(err, ErrBlocked) {
		return false
	}
	var h *HTTPError
	if errors.As(err, &h) {
		return h.StatusCode >= 500 || h.StatusCode == http.StatusTooManyRequests
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	var ue *url.Error
	return errors.As(err, &ne) || errors.As(err, &ue)
}

// getJSON decodes a JSON body. iTunes sends it as text/javascript, so the
// content type is not checked. It always uses the chain policy.
func (c *Cache) getJSON(ctx context.Context, u string, v any) error {
	data, err := c.get(ctx, u, maxJSONBytes, true)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// DefaultAllowHost accepts the hosts the chain needs: Apple Music pages, the
// iTunes API and the mzstatic image CDN; Spotify oEmbed (open.spotify.com)
// and the Spotify image CDNs (*.spotifycdn.com, i.scdn.co, mosaic.scdn.co).
func DefaultAllowHost(host string) bool {
	switch host {
	case "music.apple.com", "geo.music.apple.com", "itunes.apple.com",
		"open.spotify.com", "i.scdn.co", "mosaic.scdn.co":
		return true
	}
	return strings.HasSuffix(host, ".mzstatic.com") || strings.HasSuffix(host, ".spotifycdn.com")
}

func (c *Cache) checkHost(u *url.URL) error {
	if err := checkScheme(u); err != nil {
		return err
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	allow := c.AllowHost
	if allow == nil {
		allow = DefaultAllowHost
	}
	if !allow(host) {
		return fmt.Errorf("%w: host %q", ErrBlocked, host)
	}
	return nil
}

func checkScheme(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q", ErrBlocked, u.Scheme)
	}
	return nil
}

func (c *Cache) chainClient() *http.Client    { c.initClients(); return c.chainHTTP }
func (c *Cache) overrideClient() *http.Client { c.initClients(); return c.overrideHTTP }

// initClients builds the two clients from HTTP (or a default one). Both use
// a copy of its transport with the dial-time IP check and no proxy; a proxy
// would make the check see the proxy's address. They differ in the redirect
// policy. An HTTP whose Transport is not an *http.Transport gets a default
// transport, since the guard needs to set the dialer.
func (c *Cache) initClients() {
	c.clientOnce.Do(func() {
		base := http.Client{Timeout: defaultTimeout}
		if c.HTTP != nil {
			base = *c.HTTP
		}
		t, ok := base.Transport.(*http.Transport)
		if !ok || t == nil {
			t = http.DefaultTransport.(*http.Transport)
		}
		t = t.Clone()
		t.Proxy = nil
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		if !c.allowPrivate {
			dialer.Control = guardDial
		}
		t.DialContext = dialer.DialContext
		base.Transport = t

		chain, override := base, base
		chain.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return c.checkHost(req.URL)
		}
		override.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return checkScheme(req.URL)
		}
		c.chainHTTP, c.overrideHTTP = &chain, &override
	})
}

// cgnat is the carrier-grade NAT range (RFC 6598); IsPrivate does not cover it.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// guardDial is a net.Dialer Control hook: it refuses any address that is not
// a public unicast IP. It sees the IP after DNS, so rebinding can't dodge it.
func guardDial(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrBlocked, address, err)
	}
	if !publicIP(ap.Addr()) {
		return fmt.Errorf("%w: %s is not a public address", ErrBlocked, ap.Addr())
	}
	return nil
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() &&
		!ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsInterfaceLocalMulticast() && !ip.IsMulticast() &&
		!cgnat.Contains(ip)
}

func (c *Cache) itunesURL() string {
	if c.ITunesURL != "" {
		return strings.TrimRight(c.ITunesURL, "/")
	}
	return DefaultITunesURL
}

func (c *Cache) oembedURL() string {
	if c.OEmbedURL != "" {
		return c.OEmbedURL
	}
	return DefaultOEmbedURL
}

func (c *Cache) userAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return DefaultUserAgent
}

func (c *Cache) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
