package artwork

import (
	"context"
	"errors"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/model"
)

func TestTitle(t *testing.T) {
	f := newFakeApple(t)
	f.titles = map[string]string{}
	f.bodies = map[string]string{
		"/us/playlist/roads/pl.1": `<meta property="og:title" content="Country Roads on Apple Music">`,
		"/us/album/none/2":        `<html></html>`,
	}
	share := "https://open.spotify.com/playlist/37i9dQZF1DWZeKCadgRdKQ"
	f.oembed[share] = ""
	f.titles[share] = "Deep Focus"
	c := f.cache(t)
	ctx := context.Background()

	for raw, want := range map[string]string{
		f.srv.URL + "/us/playlist/roads/pl.1": "Country Roads",
		share + "?si=abc":                     "Deep Focus",
	} {
		if got, err := c.Title(ctx, raw); err != nil || got != want {
			t.Errorf("Title(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := c.Title(ctx, f.srv.URL+"/us/album/none/2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no og:title: %v", err)
	}
	if _, err := c.Title(ctx, "Cat Stevens — Teaser"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a name: %v", err)
	}
	// Kept: a second ask makes no request.
	before := f.count("/oembed")
	if _, err := c.Title(ctx, share); err != nil || f.count("/oembed") != before {
		t.Errorf("title not cached: %v, %d requests", err, f.count("/oembed")-before)
	}
}

// Spotify's own playlists have no oEmbed thumbnail: the cover comes from
// the embed page.
func TestSpotifyEmbedArtworkFallback(t *testing.T) {
	f := newFakeApple(t)
	share := "https://open.spotify.com/playlist/37i9dQZF1DWZeKCadgRdKQ"
	f.oembed[share] = ""
	cover := "https://image-cdn-ak.spotifycdn.com/image/ab67706f000000026020f2f6476db518ef747da4"
	f.bodies = map[string]string{"/embed/playlist/37i9dQZF1DWZeKCadgRdKQ": `<script>{"url":"` + cover + `"}</script>`}
	got, err := f.cache(t).ResolveURL(context.Background(), model.Subject{Name: "Deep Focus", URL: share})
	if err != nil || got != cover {
		t.Fatalf("ResolveURL = %q, %v", got, err)
	}
}
