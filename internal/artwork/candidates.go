package artwork

import (
	"context"
	"errors"
	"fmt"
	"html"
	"image"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// Candidate is one image the art picker offers for an entry.
type Candidate struct {
	URL   string `json:"url"`   // stored as label.artwork when picked
	Label string `json:"label"` // what it shows: "Cover", an artist name
	Kind  string `json:"kind"`  // KindCover or KindArtist
}

// Candidate kinds.
const (
	KindCover  = "cover"  // the media's own art
	KindArtist = "artist" // an artist on the album or playlist
)

// Limits of a candidate lookup. Each artist costs one page fetch.
const (
	maxArtists       = 8
	artistParallel   = 4
	artistTimeout    = 8 * time.Second
	candidatesTTL    = time.Hour
	maxCandidateKeys = 256
)

var (
	// artistLink is an Apple Music artist page link in a page's data. The
	// host is any, for tests; the fetch checks it against AllowHost.
	artistLink   = regexp.MustCompile(`https?://[a-z0-9.:-]+/[a-z]{2}/artist/[^/"\s?]+/\d+`)
	ogTitle      = regexp.MustCompile(`<meta[^>]+property="og:title"[^>]+content="([^"]+)"`)
	appleSuffix  = regexp.MustCompile(`\s+on Apple Music$`)
	errNoArtists = errors.New("no artist links")
)

type candidateSet struct {
	at  time.Time
	out []Candidate
}

// Candidates lists images for e: its own art first (the lookup the chain
// would do, without the label override), then for an Apple Music link the
// artists on its page, the most frequent first. Artists are best effort: a
// failed page or artist is logged and left out. Spotify media gets its own
// art only, since oEmbed names no artists. Results are kept for an hour.
func (c *Cache) Candidates(ctx context.Context, e model.Entry) ([]Candidate, error) {
	e.Label.Artwork = nil
	key := e.Name + "\n" + e.URL
	c.candMu.Lock()
	if set, ok := c.candidates[key]; ok && c.clock().Sub(set.at) < candidatesTTL {
		c.candMu.Unlock()
		return set.out, nil
	}
	c.candMu.Unlock()

	out := []Candidate{}
	own, err := c.SourceURL(ctx, e)
	switch {
	case err == nil:
		out = append(out, Candidate{URL: own, Label: "Cover", Kind: KindCover})
	case ctx.Err() != nil:
		return nil, ctx.Err()
	default:
		c.logger().Info("art picker: no cover", "name", e.Name, "error", err)
	}
	transient := err != nil && !errors.Is(err, ErrNotFound)
	// Not catalog.Parse alone: it rejects hosts other than Apple's, which
	// tests use. The fetch checks the host against AllowHost anyway.
	if m, perr := catalog.Parse(e.URL); isHTTP(e.URL) && (perr != nil || !m.IsSpotify()) {
		artists, aerr := c.artists(ctx, e.URL)
		if aerr != nil && !errors.Is(aerr, errNoArtists) {
			c.logger().Warn("art picker: artists", "url", e.URL, "error", aerr)
			transient = true
		}
		for _, a := range artists {
			if a.URL != own {
				out = append(out, a)
			}
		}
	}
	if !transient {
		c.candMu.Lock()
		if c.candidates == nil || len(c.candidates) >= maxCandidateKeys {
			c.candidates = map[string]candidateSet{}
		}
		c.candidates[key] = candidateSet{at: c.clock(), out: out}
		c.candMu.Unlock()
	}
	return out, nil
}

// SourceURL returns the URL or path e's art comes from: the label
// override, the index, or the lookup chain (recorded in the index).
func (c *Cache) SourceURL(ctx context.Context, e model.Entry) (string, error) {
	if override := overrideOf(e); override != "" {
		return override, nil
	}
	key := e.Name + "\n" + e.URL
	index, err := c.readIndex()
	if err != nil {
		c.logger().Warn("artwork index unreadable; resolving again", "error", err)
	}
	if u := index[key]; u != "" {
		return u, nil
	}
	return c.resolveKey(ctx, e, key, false)
}

// artists reads the artist links on an Apple Music page and fetches each
// artist's picture, the most linked artists first.
func (c *Cache) artists(ctx context.Context, pageURL string) ([]Candidate, error) {
	page, err := c.get(ctx, pageURL, maxPageBytes, true)
	if err != nil {
		return nil, fmt.Errorf("page %s: %w", pageURL, err)
	}
	links := rankArtists(string(page), pageURL)
	if len(links) == 0 {
		return nil, errNoArtists
	}
	if len(links) > maxArtists {
		links = links[:maxArtists]
	}
	found := make([]Candidate, len(links))
	sem := make(chan struct{}, artistParallel)
	var wg sync.WaitGroup
	for i, link := range links {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			actx, cancel := context.WithTimeout(ctx, artistTimeout)
			defer cancel()
			cand, err := c.artist(actx, link)
			if err != nil {
				c.logger().Info("art picker: artist skipped", "url", link, "error", err)
				return
			}
			found[i] = cand
		}()
	}
	wg.Wait()
	out := []Candidate{}
	for _, cand := range found {
		if cand.URL != "" {
			out = append(out, cand)
		}
	}
	return out, ctx.Err()
}

// rankArtists returns the distinct artist links on a page, the most
// frequent first, ties in page order. The page's own link is left out.
func rankArtists(page, self string) []string {
	count := map[string]int{}
	var order []string
	for _, l := range artistLink.FindAllString(page, -1) {
		if strings.HasPrefix(self, l) {
			continue
		}
		if count[l] == 0 {
			order = append(order, l)
		}
		count[l]++
	}
	// A stable insertion sort keeps page order among equal counts.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && count[order[j]] > count[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	return order
}

// artist reads an artist page: og:image as a square, og:title as the name.
func (c *Cache) artist(ctx context.Context, link string) (Candidate, error) {
	page, err := c.get(ctx, link, maxPageBytes, true)
	if err != nil {
		return Candidate{}, err
	}
	img := ogImagePattern.FindSubmatch(page)
	if img == nil {
		return Candidate{}, errors.New("no og:image")
	}
	name := link[strings.LastIndex(link[:strings.LastIndexByte(link, '/')], "/")+1 : strings.LastIndexByte(link, '/')]
	if t := ogTitle.FindSubmatch(page); t != nil {
		name = appleSuffix.ReplaceAllString(html.UnescapeString(string(t[1])), "")
	}
	u := lastSegment.ReplaceAllString(html.UnescapeString(string(img[1])), "/1200x1200bb.jpg")
	return Candidate{URL: u, Label: name, Kind: KindArtist}, nil
}

// Preview loads a candidate image for the picker, square. Only hosts the
// lookup chain may use are fetched, so the picker can't be pointed at
// another site; images are cached like chain art.
func (c *Cache) Preview(ctx context.Context, u string) (image.Image, error) {
	if !isHTTP(u) {
		return nil, fmt.Errorf("preview %q: %w", u, ErrBlocked)
	}
	img, err := c.loadURL(ctx, u, false, true)
	if err != nil {
		return nil, err
	}
	return cropSquare(img), nil
}
