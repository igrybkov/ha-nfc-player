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
	page := func(title, img string) string {
		return `<meta property="og:title" content="` + title + ` on Apple Music"><meta property="og:image" content="` + img + `">`
	}
	link := func(kind, slug, id string) string {
		return `"url": "` + base + `/us/` + kind + `/` + slug + `/` + id + `"`
	}
	// Tyler is linked three times (once by another slug), Zach twice, Wyatt's
	// page has no image. An album, a song of it, a playlist, and the page itself.
	f.bodies["/us/playlist/roads/pl.1"] = `<meta property="og:image" content="` + base + `/img/pl/1200x630cw.png">` +
		link("playlist", "more", "pl.2") + link("album", "one", "10") + `"` + base + `/us/album/one/10?i=11"` +
		link("artist", "zach", "2") + link("artist", "tyler", "1") + link("artist", "tyler-c", "1") +
		link("artist", "wyatt", "3") + link("artist", "zach", "2") + link("artist", "tyler", "1") +
		link("playlist", "roads", "pl.1")
	f.bodies["/us/artist/tyler/1"] = page("Tyler Childers", base+"/img/ty/1200x630cw.png")
	f.bodies["/us/artist/zach/2"] = page("Zach &amp; Co", base+"/img/za/1200x630cw.png")
	f.bodies["/us/artist/wyatt/3"] = `<html></html>`
	f.bodies["/us/album/one/10"] = page("One - Single by Zach", base+"/img/za/1200x630cw.png") // same image as Zach: dropped
	f.bodies["/us/playlist/more/pl.2"] = page("More", base+"/img/more/1200x630wp.jpg")
	c := f.cache(t)

	ctx := context.Background()
	override := "mine.jpg"
	e := model.Entry{Name: "Country Roads", URL: base + "/us/playlist/roads/pl.1", Label: model.LabelOverrides{Artwork: &override}}
	var got []Candidate
	next, pages := 0, 0
	for next >= 0 {
		p, err := c.Candidates(ctx, e, next, 2)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p.Items...)
		next = p.Next
		pages++
	}
	want := []Candidate{
		{URL: base + "/img/pl/1200x1200bb.jpg", Label: "Cover", Kind: KindCover},
		{URL: base + "/img/ty/1200x1200bb.jpg", Label: "Tyler Childers", Kind: KindArtist},
		{URL: base + "/img/za/1200x1200bb.jpg", Label: "Zach & Co", Kind: KindArtist},
		{URL: base + "/img/za/1200x1200bb.jpg", Label: "One - Single by Zach", Kind: KindAlbum},
		{URL: base + "/img/more/1200x1200bb.jpg", Label: "More", Kind: KindPlaylist},
	}
	// Five links (tyler, zach, wyatt, album, playlist) in pages of two.
	if pages != 3 {
		t.Errorf("pages = %d", pages)
	}
	// Dedup is per page; the UI drops the album's repeat of Zach's image.
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Linked pages are cached.
	before := f.count("/us/artist/tyler/1")
	if _, err := c.Candidates(ctx, e, 0, 2); err != nil {
		t.Fatal(err)
	}
	if f.count("/us/artist/tyler/1") != before || f.count("/us/playlist/roads/pl.1") > 2 {
		t.Error("candidates not cached")
	}

	// Spotify gets its own art only.
	share := "https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy"
	f.oembed[share] = base + "/img/sp.jpg"
	p, err := c.Candidates(ctx, model.Entry{Name: "S", URL: share}, 0, 10)
	if err != nil || len(p.Items) != 1 || p.Items[0].Kind != KindCover || p.Next != -1 {
		t.Fatalf("spotify %+v, %v", p, err)
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
