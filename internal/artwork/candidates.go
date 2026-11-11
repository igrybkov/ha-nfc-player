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

// Candidate is one image the art picker offers for a subject.
type Candidate struct {
	URL   string `json:"url"`   // stored as label.artwork when picked
	Label string `json:"label"` // what it shows: "Cover", an artist or album name
	Kind  string `json:"kind"`  // KindCover, KindArtist, KindAlbum or KindPlaylist
}

// Candidate kinds.
const (
	KindCover    = "cover"    // the media's own art
	KindArtist   = "artist"   // an artist linked from the media's page
	KindAlbum    = "album"    // an album linked from the page
	KindPlaylist = "playlist" // a playlist linked from the page
)

// CandidatePage is one page of candidates. Next is the offset of the next
// page, or -1 when there is none.
type CandidatePage struct {
	Items []Candidate `json:"items"`
	Next  int         `json:"next"`
}

// Limits of a candidate lookup. Each linked item costs one page fetch, so
// they load a page at a time, as the picker scrolls.
const (
	linkParallel = 4
	linkTimeout  = 8 * time.Second
	candidateTTL = time.Hour
	maxCacheKeys = 2048
)

// linkKinds is the order candidates come in: artists, then albums, then
// playlists, each the most linked first.
var linkKinds = []string{KindArtist, KindAlbum, KindPlaylist}

var (
	// pageLink is an Apple Music artist, album or playlist link in a page's
	// data. The host is any, for tests; the fetch checks it against
	// AllowHost. A song link (album?i=) counts as its album.
	pageLink    = regexp.MustCompile(`https?://[a-z0-9.:-]+/[a-z]{2}/(artist|album|playlist)/[^/"\s?]+/([A-Za-z0-9.]+)`)
	ogTitle     = regexp.MustCompile(`<meta[^>]+property="og:title"[^>]+content="([^"]+)"`)
	appleSuffix = regexp.MustCompile(`\s+on Apple Music$`)
)

type linkSet struct {
	at    time.Time
	links []string
}

type linkResult struct {
	at   time.Time
	cand Candidate // empty URL: the link had no image
}

// Candidates returns a page of images for sub. The first page starts with
// sub's own art (the lookup the chain would do, without the label override).
// For an Apple Music link the rest are the artists, albums and playlists
// linked from its page, limit at a time from offset. They are best effort:
// a page that fails is logged and left out, and an image already offered
// is not offered again. Spotify media gets its own art only, since oEmbed
// links nothing. Pages and linked items are kept for an hour.
func (c *Cache) Candidates(ctx context.Context, sub model.Subject, offset, limit int) (CandidatePage, error) {
	sub.Label.Artwork = nil
	page := CandidatePage{Items: []Candidate{}, Next: -1}
	own := ""
	if u, err := c.SourceURL(ctx, sub); err == nil {
		own = u
		if offset == 0 {
			page.Items = append(page.Items, Candidate{URL: u, Label: "Cover", Kind: KindCover})
		}
	} else if ctx.Err() != nil {
		return page, ctx.Err()
	} else {
		c.logger().Info("art picker: no cover", "name", sub.Name, "error", err)
	}
	// Not catalog.Parse alone: it rejects hosts other than Apple's, which
	// tests use. The fetch checks the host against AllowHost anyway.
	if m, perr := catalog.Parse(sub.URL); !isHTTP(sub.URL) || (perr == nil && m.IsSpotify()) {
		return page, nil
	}
	links, err := c.pageLinks(ctx, sub.URL)
	if err != nil {
		c.logger().Warn("art picker: page links", "url", sub.URL, "error", err)
		return page, nil
	}
	if offset >= len(links) {
		return page, nil
	}
	end := min(offset+limit, len(links))
	if end < len(links) {
		page.Next = end
	}
	seen := map[string]bool{own: true}
	for _, cand := range c.linkCandidates(ctx, links[offset:end]) {
		if cand.URL != "" && !seen[cand.URL] {
			seen[cand.URL] = true
			page.Items = append(page.Items, cand)
		}
	}
	return page, ctx.Err()
}

// SourceURL returns the URL or path sub's art comes from: the label
// override, the index, or the lookup chain (recorded in the index).
func (c *Cache) SourceURL(ctx context.Context, sub model.Subject) (string, error) {
	if override := overrideOf(sub); override != "" {
		return override, nil
	}
	key := sub.Name + "\n" + sub.URL
	index, err := c.readIndex()
	if err != nil {
		c.logger().Warn("artwork index unreadable; resolving again", "error", err)
	}
	if u := index[key]; u != "" {
		return u, nil
	}
	return c.resolveKey(ctx, sub, key, false)
}

// pageLinks returns the ranked links of an Apple Music page, cached.
func (c *Cache) pageLinks(ctx context.Context, pageURL string) ([]string, error) {
	c.candMu.Lock()
	if set, ok := c.pageSets[pageURL]; ok && c.clock().Sub(set.at) < candidateTTL {
		c.candMu.Unlock()
		return set.links, nil
	}
	c.candMu.Unlock()
	body, err := c.get(ctx, pageURL, maxPageBytes, true)
	if err != nil {
		return nil, fmt.Errorf("page %s: %w", pageURL, err)
	}
	links := rankLinks(string(body), pageURL)
	c.candMu.Lock()
	if c.pageSets == nil || len(c.pageSets) >= maxCacheKeys {
		c.pageSets = map[string]linkSet{}
	}
	c.pageSets[pageURL] = linkSet{at: c.clock(), links: links}
	c.candMu.Unlock()
	return links, nil
}

// rankLinks returns the distinct links on a page by kind (linkKinds
// order), the most frequent first, ties in page order. A link with the
// page's own id is left out.
func rankLinks(page, self string) []string {
	count := map[string]int{}
	byKind := map[string][]string{}
	ids := map[string]string{} // id → first link seen, so slug variants count once
	for _, m := range pageLink.FindAllStringSubmatch(page, -1) {
		kind, id := m[1], m[2]
		if strings.HasSuffix(self, "/"+id) || strings.Contains(self, "/"+id+"?") {
			continue
		}
		l, ok := ids[kind+"/"+id]
		if !ok {
			l = m[0]
			ids[kind+"/"+id] = l
			byKind[kind] = append(byKind[kind], l)
		}
		count[l]++
	}
	var out []string
	for _, kind := range linkKinds {
		order := byKind[kind]
		// A stable insertion sort keeps page order among equal counts.
		for i := 1; i < len(order); i++ {
			for j := i; j > 0 && count[order[j]] > count[order[j-1]]; j-- {
				order[j], order[j-1] = order[j-1], order[j]
			}
		}
		out = append(out, order...)
	}
	return out
}

// linkCandidates fetches each link's image in parallel, from cache when it
// can. The result is parallel to links; a link without art has an empty URL.
func (c *Cache) linkCandidates(ctx context.Context, links []string) []Candidate {
	out := make([]Candidate, len(links))
	sem := make(chan struct{}, linkParallel)
	var wg sync.WaitGroup
	for i, link := range links {
		c.candMu.Lock()
		r, ok := c.linkCache[link]
		c.candMu.Unlock()
		if ok && c.clock().Sub(r.at) < candidateTTL {
			out[i] = r.cand
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			lctx, cancel := context.WithTimeout(ctx, linkTimeout)
			defer cancel()
			cand, err := c.linkCandidate(lctx, link)
			if err != nil {
				c.logger().Info("art picker: link skipped", "url", link, "error", err)
				if ctx.Err() != nil || isTransient(err) {
					return // not remembered: worth another try
				}
			}
			out[i] = cand
			c.candMu.Lock()
			if c.linkCache == nil || len(c.linkCache) >= maxCacheKeys {
				c.linkCache = map[string]linkResult{}
			}
			c.linkCache[link] = linkResult{at: c.clock(), cand: cand}
			c.candMu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// linkCandidate reads a linked page: og:image as a square, og:title (less
// " on Apple Music") as the label.
func (c *Cache) linkCandidate(ctx context.Context, link string) (Candidate, error) {
	m := pageLink.FindStringSubmatch(link)
	page, err := c.get(ctx, link, maxPageBytes, true)
	if err != nil {
		return Candidate{}, err
	}
	img := ogImagePattern.FindSubmatch(page)
	if img == nil {
		return Candidate{}, errors.New("no og:image")
	}
	name := m[2]
	if t := ogTitle.FindSubmatch(page); t != nil {
		name = appleSuffix.ReplaceAllString(html.UnescapeString(string(t[1])), "")
	}
	u := lastSegment.ReplaceAllString(html.UnescapeString(string(img[1])), "/1200x1200bb.jpg")
	return Candidate{URL: u, Label: name, Kind: m[1]}, nil
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
