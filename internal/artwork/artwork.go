// Package artwork resolves, downloads, caches and square-crops entry artwork.
//
// Owner: track B (docs/PLAN.md section 10).
package artwork

import (
	"context"
	"image"
	"net/http"

	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// Cache implements model.ArtworkSource with a disk cache under Dir
// (images keyed by sha256 of URL, plus artwork.json: entry key to art URL).
type Cache struct {
	Dir       string // e.g. /data/cache
	ConfigDir string // base for relative label.artwork paths
	HTTP      *http.Client
}

var _ model.ArtworkSource = (*Cache)(nil)

// Artwork returns square art for e. refresh skips the cache.
func (c *Cache) Artwork(ctx context.Context, e model.Entry, refresh bool) (image.Image, error) {
	panic("TODO: track B")
}

// ResolveURL finds the art URL for e (override, iTunes lookup, og:image, search).
func (c *Cache) ResolveURL(ctx context.Context, e model.Entry) (string, error) {
	panic("TODO: track B")
}
