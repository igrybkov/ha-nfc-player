package artwork

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
)

// titleTTL is how long a media link's name is kept. Names rarely change;
// a miss is kept for NegativeTTL.
const titleTTL = 24 * time.Hour

type titleEntry struct {
	at    time.Time
	title string
}

// Title returns the name a music service shows for a media link: the
// Spotify oEmbed title, or an Apple Music page's og:title without " on
// Apple Music" ("Country Roads", "Divisive - Album by Disturbed"). It
// returns ErrNotFound when the service names nothing.
func (c *Cache) Title(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	m, perr := catalog.Parse(raw)
	key := strings.SplitN(raw, "?", 2)[0]
	if perr == nil {
		key = fmt.Sprintf("%s:%s:%s", m.Service, m.Kind, m.ID)
	}
	c.candMu.Lock()
	e, ok := c.titles[key]
	c.candMu.Unlock()
	ttl := titleTTL
	if e.title == "" {
		ttl = NegativeTTL
	}
	if ok && c.clock().Sub(e.at) < ttl {
		if e.title == "" {
			return "", ErrNotFound
		}
		return e.title, nil
	}

	var title string
	var err error
	switch {
	case perr == nil && m.IsSpotify():
		var data oembed
		if data, err = c.spotifyOEmbed(ctx, m); err == nil {
			title = data.Title
		}
	case perr == nil && !isHTTP(m.ShareURL()):
		return "", ErrNotFound // an apple_music:// URI with no page
	case perr == nil || isHTTP(raw):
		// Not catalog.Parse alone: it rejects hosts other than Apple's,
		// which tests use. The fetch checks the host against AllowHost.
		page := raw
		if perr == nil {
			page = m.ShareURL()
		}
		var body []byte
		if body, err = c.get(ctx, page, maxPageBytes, true); err == nil {
			if t := ogTitle.FindSubmatch(body); t != nil {
				title = strings.TrimSpace(appleSuffix.ReplaceAllString(html.UnescapeString(string(t[1])), ""))
			}
		}
	default:
		return "", fmt.Errorf("%q is not a music link: %w", raw, ErrNotFound)
	}
	if err != nil {
		if ctx.Err() != nil || isTransient(err) {
			return "", err // not remembered: worth another try
		}
		c.logger().Info("no media title", "url", raw, "error", err)
	}
	c.candMu.Lock()
	if c.titles == nil || len(c.titles) >= maxCacheKeys {
		c.titles = map[string]titleEntry{}
	}
	c.titles[key] = titleEntry{at: c.clock(), title: title}
	c.candMu.Unlock()
	if title == "" {
		return "", ErrNotFound
	}
	return title, nil
}
