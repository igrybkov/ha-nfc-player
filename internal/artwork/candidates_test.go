package artwork

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/model"
)

func TestCandidates(t *testing.T) {
	f := newFakeApple(t)
	base := f.srv.URL
	f.bodies = map[string]string{}
	artistPage := func(name, img string) string {
		return `<meta property="og:title" content="` + name + ` on Apple Music"><meta property="og:image" content="` + img + `">`
	}
	// Tyler is linked three times, Zach twice, Wyatt's page has no image.
	link := func(slug, id string) string { return `"url": "` + base + `/us/artist/` + slug + `/` + id + `"` }
	f.bodies["/us/playlist/roads/pl.1"] = `<meta property="og:image" content="` + base + `/img/pl/1200x630cw.png">` +
		link("zach", "2") + link("tyler", "1") + link("tyler", "1") + link("wyatt", "3") + link("zach", "2") + link("tyler", "1")
	f.bodies["/us/artist/tyler/1"] = artistPage("Tyler Childers", base+"/img/ty/1200x630cw.png")
	f.bodies["/us/artist/zach/2"] = artistPage("Zach &amp; Co", base+"/img/za/1200x630cw.png")
	f.bodies["/us/artist/wyatt/3"] = `<html></html>`
	c := f.cache(t)

	ctx := context.Background()
	override := "mine.jpg"
	e := model.Entry{Name: "Country Roads", URL: base + "/us/playlist/roads/pl.1", Label: model.LabelOverrides{Artwork: &override}}
	got, err := c.Candidates(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{
		{URL: base + "/img/pl/1200x1200bb.jpg", Label: "Cover", Kind: KindCover},
		{URL: base + "/img/ty/1200x1200bb.jpg", Label: "Tyler Childers", Kind: KindArtist},
		{URL: base + "/img/za/1200x1200bb.jpg", Label: "Zach & Co", Kind: KindArtist},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Cached: no second page fetch.
	before := f.count("/us/artist/tyler/1")
	if _, err := c.Candidates(ctx, e); err != nil {
		t.Fatal(err)
	}
	if f.count("/us/artist/tyler/1") != before {
		t.Error("candidates not cached")
	}

	// Spotify gets its own art only.
	share := "https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy"
	f.oembed[share] = base + "/img/sp.jpg"
	got, err = c.Candidates(ctx, model.Entry{Name: "S", URL: share})
	if err != nil || len(got) != 1 || got[0].Kind != KindCover {
		t.Fatalf("spotify %+v, %v", got, err)
	}
}

func TestPreviewOnlyChainHosts(t *testing.T) {
	f := newFakeApple(t)
	c := f.cache(t)
	img, err := c.Preview(context.Background(), f.srv.URL+"/img/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != b.Dy() {
		t.Errorf("not square: %v", b)
	}
	c.AllowHost = func(string) bool { return false }
	if _, err := c.Preview(context.Background(), f.srv.URL+"/img/b.png"); err == nil || !strings.Contains(err.Error(), "not allowed") && !errors.Is(err, ErrBlocked) {
		t.Errorf("other host: %v", err)
	}
	if _, err := c.Preview(context.Background(), "art/local.jpg"); !errors.Is(err, ErrBlocked) {
		t.Errorf("path: %v", err)
	}
}
