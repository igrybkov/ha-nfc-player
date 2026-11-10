package artwork

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// fakeApple serves an iTunes API, Apple Music pages and art images, and
// counts requests per path.
type fakeApple struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	lookup map[string]string // album id → artworkUrl100 ("" = no results)
	search string            // artworkUrl100 for any search ("" = no results)
	pages  map[string]string // path → og:image content
	gate   chan struct{}     // when set, /search waits for it to close
	status int               // when set, /search and /lookup answer with it
}

func newFakeApple(t *testing.T) *fakeApple {
	f := &fakeApple{t: t, hits: map[string]int{}, lookup: map[string]string{}, pages: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeApple) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	f.mu.Unlock()
	results := func(art string) {
		w.Header().Set("Content-Type", "text/javascript") // like the real API
		var rs []map[string]string
		if art != "" {
			rs = append(rs, map[string]string{"artworkUrl100": art})
		}
		json.NewEncoder(w).Encode(map[string]any{"resultCount": len(rs), "results": rs})
	}
	switch {
	case f.status != 0 && (r.URL.Path == "/lookup" || r.URL.Path == "/search"):
		w.WriteHeader(f.status)
	case r.URL.Path == "/redirect":
		http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)
	case r.URL.Path == "/lookup":
		results(f.lookup[r.URL.Query().Get("id")])
	case r.URL.Path == "/search":
		if f.gate != nil {
			<-f.gate
		}
		if r.URL.Query().Get("entity") != "album" {
			f.t.Errorf("search entity = %q", r.URL.Query().Get("entity"))
		}
		results(f.search)
	case strings.HasPrefix(r.URL.Path, "/status/"): // /status/NNN/...
		code, _ := strconv.Atoi(strings.Split(r.URL.Path, "/")[2])
		w.WriteHeader(code)
	case strings.HasPrefix(r.URL.Path, "/img/"):
		w.Write(pngBytes(f.t, 120, 80))
	default:
		og, ok := f.pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`<html><head><meta name="x"><meta property="og:image" content="` + og + `"></head></html>`))
	}
}

func (f *fakeApple) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func pngBytes(t *testing.T, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// cache returns a Cache that may use the fake: its host is the only one the
// chain allows, and the dial guard is off so 127.0.0.1 is reachable.
func (f *fakeApple) cache(t *testing.T) *Cache {
	return &Cache{
		Dir: t.TempDir(), ConfigDir: t.TempDir(), HTTP: f.srv.Client(), ITunesURL: f.srv.URL,
		AllowHost:    func(host string) bool { return host == "127.0.0.1" },
		allowPrivate: true,
	}
}

func TestResolveURLChain(t *testing.T) {
	f := newFakeApple(t)
	base := f.srv.URL
	f.lookup["1646032515"] = base + "/img/Music/v4/ab/cd/source/100x100bb.jpg"
	f.pages["/us/playlist/metallica-essentials/pl.e55d"] = base + "/img/Features/x/1200x630SC.FPESS03.jpg?l=en-US&amp;w=1"
	f.pages["/us/album/pulled/999"] = base + "/img/og/pulled/1200x630bb.jpg"
	f.search = base + "/img/search/60x60bb.jpg"
	c := f.cache(t)
	str := func(s string) *string { return &s }

	cases := []struct {
		name  string
		entry model.Entry
		want  string
	}{
		{"album via iTunes lookup",
			model.Entry{Name: "Disturbed — Divisible", URL: base + "/us/album/divisive/1646032515"},
			base + "/img/Music/v4/ab/cd/source/1200x1200bb.jpg"},
		{"playlist via og:image, query and entity dropped",
			model.Entry{Name: "Metallica — Essential", URL: base + "/us/playlist/metallica-essentials/pl.e55d"},
			base + "/img/Features/x/1200x1200bb.jpg"},
		{"album lookup empty falls to og:image",
			model.Entry{Name: "Gone — Pulled", URL: base + "/us/album/pulled/999"},
			base + "/img/og/pulled/1200x1200bb.jpg"},
		{"page missing falls to search",
			model.Entry{Name: "Muse — Origin of Symmetry", URL: base + "/us/artist/muse/404"},
			base + "/img/search/1200x1200bb.jpg"},
		{"no URL: search",
			model.Entry{Name: "Muse — Origin of Symmetry"},
			base + "/img/search/1200x1200bb.jpg"},
		{"override wins",
			model.Entry{Name: "X", URL: base + "/us/album/divisive/1646032515", Label: model.LabelOverrides{Artwork: str(" https://example.test/a.jpg ")}},
			"https://example.test/a.jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.ResolveURL(context.Background(), tc.entry)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("ResolveURL = %q, want %q", got, tc.want)
			}
		})
	}

	f.search = ""
	if _, err := c.ResolveURL(context.Background(), model.Entry{Name: "Nothing"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("nothing found: err = %v, want ErrNotFound", err)
	}
}

func TestArtworkCachesIndexAndImages(t *testing.T) {
	f := newFakeApple(t)
	f.lookup["42"] = f.srv.URL + "/img/a/100x100bb.jpg"
	c := f.cache(t)
	e := model.Entry{Name: "Queen — Bohemian Rhapsody", URL: f.srv.URL + "/us/album/br/42"}
	ctx := context.Background()

	img, err := c.Artwork(ctx, e, false)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 80 || b.Dy() != 80 {
		t.Errorf("art is %v, want an 80x80 square crop of 120x80", b)
	}

	// The index maps "{name}\n{url}" to the art URL.
	data, err := os.ReadFile(filepath.Join(c.Dir, "artwork.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]string
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	if got := index[e.Name+"\n"+e.URL]; got != f.srv.URL+"/img/a/1200x1200bb.jpg" {
		t.Errorf("index entry = %q", got)
	}

	// Second call: no network at all.
	if _, err := c.Artwork(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/lookup"); n != 1 {
		t.Errorf("lookups = %d after a cached call, want 1", n)
	}
	if n := f.count("/img/a/1200x1200bb.jpg"); n != 1 {
		t.Errorf("image downloads = %d after a cached call, want 1", n)
	}

	// Refresh bypasses both the index and the image file.
	if _, err := c.Artwork(ctx, e, true); err != nil {
		t.Fatal(err)
	}
	if f.count("/lookup") != 2 || f.count("/img/a/1200x1200bb.jpg") != 2 {
		t.Errorf("refresh did not go to the network: lookups %d, downloads %d",
			f.count("/lookup"), f.count("/img/a/1200x1200bb.jpg"))
	}
}

func TestArtworkOverrides(t *testing.T) {
	f := newFakeApple(t)
	c := f.cache(t)
	ctx := context.Background()
	str := func(s string) *string { return &s }

	if err := os.MkdirAll(filepath.Join(c.ConfigDir, "art"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.ConfigDir, "art", "ivasiuk.png"), pngBytes(t, 50, 70), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, pngBytes(t, 10, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"art/ivasiuk.png", filepath.Join(c.ConfigDir, "art", "ivasiuk.png")} {
		img, err := c.Artwork(ctx, model.Entry{Name: "Volodymyr Ivasiuk", Label: model.LabelOverrides{Artwork: str(p)}}, false)
		if err != nil {
			t.Fatalf("path %q: %v", p, err)
		}
		if b := img.Bounds(); b.Dx() != 50 || b.Dy() != 50 {
			t.Errorf("path %q: art is %v, want 50x50", p, b)
		}
	}
	for _, p := range []string{"../" + filepath.Base(filepath.Dir(outside)) + "/secret.png", outside} {
		if _, err := c.Artwork(ctx, model.Entry{Name: "X", Label: model.LabelOverrides{Artwork: str(p)}}, false); err == nil {
			t.Errorf("path %q outside the config dir was accepted", p)
		}
	}

	// A URL override is downloaded and cached, but never put in the index.
	e := model.Entry{Name: "Y", URL: f.srv.URL + "/us/album/x/1", Label: model.LabelOverrides{Artwork: str(f.srv.URL + "/img/override.png")}}
	if _, err := c.Artwork(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "artwork.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("override was written to the index (stat err %v)", err)
	}
	if f.count("/lookup") != 0 {
		t.Error("override still ran the lookup chain")
	}
}

func TestArtworkHTTPErrors(t *testing.T) {
	f := newFakeApple(t)
	c := f.cache(t)
	f.search = f.srv.URL + "/missing/100x100bb.jpg" // resolves, but the image 404s
	_, err := c.Artwork(context.Background(), model.Entry{Name: "Broken"}, false)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want a 404 download error", err)
	}
}

func TestCropSquare(t *testing.T) {
	img := image.NewRGBA(image.Rect(10, 20, 110, 60)) // 100x40, offset bounds
	img.SetRGBA(60, 40, color.RGBA{1, 2, 3, 255})
	sq := cropSquare(img)
	if b := sq.Bounds(); b != image.Rect(40, 20, 80, 60) {
		t.Errorf("crop = %v, want centered 40x40 at (40,20)", b)
	}
	if sq.At(60, 40) != (color.RGBA{1, 2, 3, 255}) {
		t.Error("crop lost the center pixel")
	}
}

func TestNegativeCache(t *testing.T) {
	f := newFakeApple(t) // search finds nothing
	c := f.cache(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	e := model.Entry{Name: "Nobody — Nothing"}
	ctx := context.Background()

	for range 3 {
		if _, err := c.Artwork(ctx, e, false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if n := f.count("/search"); n != 1 {
		t.Errorf("searches = %d within the TTL, want 1", n)
	}

	if _, err := c.Artwork(ctx, e, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refresh: err = %v", err)
	}
	if n := f.count("/search"); n != 2 {
		t.Errorf("searches = %d after refresh, want 2 (refresh bypasses the miss)", n)
	}

	now = now.Add(NegativeTTL + time.Minute)
	f.search = f.srv.URL + "/img/late/100x100bb.jpg" // art shows up later
	if _, err := c.Artwork(ctx, e, false); err != nil {
		t.Fatalf("after the TTL: %v", err)
	}
	if n := f.count("/search"); n != 3 {
		t.Errorf("searches = %d after the TTL, want 3", n)
	}
}

func TestConcurrentLookupsShareOneRun(t *testing.T) {
	f := newFakeApple(t)
	f.search = f.srv.URL + "/img/shared/100x100bb.jpg"
	f.gate = make(chan struct{})
	c := f.cache(t)
	e := model.Entry{Name: "Ray Charles — Hits"}
	const callers = 8

	errs := make(chan error, callers)
	for range callers {
		go func() {
			_, err := c.Artwork(context.Background(), e, false)
			errs <- err
		}()
	}
	// Hold the leader's search until every other caller waits on it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.flightMu.Lock()
		waiting := 0
		if fl := c.flights[e.Name+"\n"]; fl != nil {
			waiting = fl.waiters
		}
		c.flightMu.Unlock()
		if waiting == callers-1 {
			break
		}
		if time.Now().After(deadline) {
			close(f.gate)
			t.Fatalf("only %d callers joined the lookup", waiting)
		}
		time.Sleep(time.Millisecond)
	}
	close(f.gate)
	for range callers {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	if n := f.count("/search"); n != 1 {
		t.Errorf("searches = %d for %d concurrent callers, want 1", n, callers)
	}
}

func TestGoneIndexURLResolvesAgain(t *testing.T) {
	for _, code := range []int{404, 410} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			f := newFakeApple(t)
			f.search = f.srv.URL + "/img/new/100x100bb.jpg"
			c := f.cache(t)
			e := model.Entry{Name: "Disturbed — Divisive"}
			key := e.Name + "\n"
			dead := f.srv.URL + "/status/" + strconv.Itoa(code) + "/old.jpg"
			if err := c.updateIndex(func(i map[string]string) { i[key] = dead }); err != nil {
				t.Fatal(err)
			}

			if _, err := c.Artwork(context.Background(), e, false); err != nil {
				t.Fatalf("Artwork: %v", err)
			}
			index, err := c.readIndex()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := index[key], f.srv.URL+"/img/new/1200x1200bb.jpg"; got != want {
				t.Errorf("index entry = %q, want %q", got, want)
			}
			if n := f.count("/search"); n != 1 {
				t.Errorf("searches = %d, want 1", n)
			}
		})
	}
}

func TestIndexURLServerErrorKeepsEntry(t *testing.T) {
	f := newFakeApple(t)
	c := f.cache(t)
	e := model.Entry{Name: "Flaky"}
	key := e.Name + "\n"
	flaky := f.srv.URL + "/status/503/art.jpg"
	if err := c.updateIndex(func(i map[string]string) { i[key] = flaky }); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Artwork(context.Background(), e, false); err == nil {
		t.Fatal("503 download succeeded")
	}
	index, _ := c.readIndex()
	if index[key] != flaky {
		t.Errorf("a 503 dropped the index entry: %q", index[key])
	}
	if n := f.count("/search"); n != 0 {
		t.Errorf("a 503 re-ran the chain (%d searches)", n)
	}
}

func TestChainHostAllowlist(t *testing.T) {
	f := newFakeApple(t)
	f.search = f.srv.URL + "/img/s/100x100bb.jpg"
	c := f.cache(t)
	c.AllowHost = nil // default: Apple hosts only
	e := model.Entry{Name: "Muse — Origin", URL: f.srv.URL + "/us/album/origin/7"}

	_, err := c.Artwork(context.Background(), e, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (blocked steps are a clean miss)", err)
	}
	for _, p := range []string{"/lookup", "/search", "/us/album/origin/7"} {
		if n := f.count(p); n != 0 {
			t.Errorf("%s fetched %d times from a host outside the allowlist", p, n)
		}
	}

	for host, want := range map[string]bool{
		"music.apple.com": true, "itunes.apple.com": true, "is1-ssl.mzstatic.com": true,
		"mzstatic.com": false, "evilmzstatic.com": false, "apple.com": false, "127.0.0.1": false,
	} {
		if got := DefaultAllowHost(host); got != want {
			t.Errorf("DefaultAllowHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestChainRedirectChecked(t *testing.T) {
	f := newFakeApple(t)
	other := newFakeApple(t)
	c := f.cache(t)
	e := model.Entry{Name: "Redirected"}
	key := e.Name + "\n"
	// The second server is reached as "localhost", which AllowHost refuses.
	target := strings.Replace(other.srv.URL, "127.0.0.1", "localhost", 1) + "/img/x.png"
	if err := c.updateIndex(func(i map[string]string) { i[key] = f.srv.URL + "/redirect?to=" + target }); err != nil {
		t.Fatal(err)
	}
	_, err := c.Artwork(context.Background(), e, false)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if n := other.count("/img/x.png"); n != 0 {
		t.Errorf("redirect target fetched %d times", n)
	}

	// The same redirect to an allowed host works.
	if err := c.updateIndex(func(i map[string]string) { i[key] = f.srv.URL + "/redirect?to=/img/y.png" }); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Artwork(context.Background(), e, false); err != nil {
		t.Fatalf("allowed redirect: %v", err)
	}
}

func TestOverrideDialGuard(t *testing.T) {
	f := newFakeApple(t)
	c := f.cache(t)
	c.allowPrivate = false
	u := f.srv.URL + "/img/override.png"
	e := model.Entry{Name: "Local", Label: model.LabelOverrides{Artwork: &u}}
	_, err := c.Artwork(context.Background(), e, false)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked for a loopback override", err)
	}
	if n := f.count("/img/override.png"); n != 0 {
		t.Errorf("loopback override fetched %d times", n)
	}
	if isTransient(err) {
		t.Error("a blocked dial counts as transient")
	}
}

func TestPublicIP(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "17.253.144.10": true, "2606:4700::1111": true,
		"127.0.0.1": false, "::1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.40.20": false,
		"100.64.0.1": false, "100.127.255.254": false, "169.254.169.254": false, "fe80::1": false,
		"fc00::1": false, "0.0.0.0": false, "::": false, "224.0.0.1": false, "ff02::1": false,
		"::ffff:10.0.0.1": false, "::ffff:8.8.8.8": true,
	} {
		if got := publicIP(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicIP(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestTransientErrorIsNotCached(t *testing.T) {
	f := newFakeApple(t)
	f.status = http.StatusServiceUnavailable
	c := f.cache(t)
	e := model.Entry{Name: "Flaky — Search"}
	ctx := context.Background()

	for i := 1; i <= 2; i++ {
		_, err := c.Artwork(ctx, e, false)
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("call %d: err = %v, want a transient error, not ErrNotFound", i, err)
		}
		if n := f.count("/search"); n != i {
			t.Errorf("call %d: searches = %d; a transient failure was cached", i, n)
		}
	}

	// A 4xx is a clean answer: that miss is cached.
	f.status = http.StatusBadRequest
	for range 2 {
		if _, err := c.Artwork(ctx, e, false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if n := f.count("/search"); n != 3 {
		t.Errorf("searches = %d, want 3 (the 4xx miss is cached)", n)
	}
}

// bombPNG is a valid tiny PNG whose header claims w×h pixels.
func bombPNG(t *testing.T, w, h uint32) []byte {
	data := pngBytes(t, 1, 1)
	binary.BigEndian.PutUint32(data[16:], w)
	binary.BigEndian.PutUint32(data[20:], h)
	binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
	return data
}

func TestDecompressionBomb(t *testing.T) {
	bomb := bombPNG(t, 10000, 10000) // 100 Mpx
	if _, err := decodeImage(bomb); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("decodeImage: err = %v, want ErrTooLarge", err)
	}
	if _, err := decodeImage(bombPNG(t, 1, 1)); err != nil {
		t.Fatalf("1x1 control image: %v", err)
	}

	f := newFakeApple(t)
	c := f.cache(t)
	ctx := context.Background()

	// Override file.
	if err := os.WriteFile(filepath.Join(c.ConfigDir, "bomb.png"), bomb, 0o644); err != nil {
		t.Fatal(err)
	}
	p := "bomb.png"
	if _, err := c.Artwork(ctx, model.Entry{Name: "F", Label: model.LabelOverrides{Artwork: &p}}, false); !errors.Is(err, ErrTooLarge) {
		t.Errorf("override file: err = %v, want ErrTooLarge", err)
	}

	// Cache file: a planted bomb is not decoded; the image is downloaded again.
	u := f.srv.URL + "/img/cached.png"
	if _, err := c.loadURL(ctx, u, false, true); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(c.Dir, imageDir))
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache dir: %v, %v", entries, err)
	}
	if err := os.WriteFile(filepath.Join(c.Dir, imageDir, entries[0].Name()), bomb, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.loadURL(ctx, u, false, true); err != nil {
		t.Fatalf("after a planted bomb: %v", err)
	}
	if n := f.count("/img/cached.png"); n != 2 {
		t.Errorf("downloads = %d, want 2 (the bomb in the cache was refused)", n)
	}
}

func TestDownloadedBombRefused(t *testing.T) {
	bomb := bombPNG(t, 8000, 8000) // 64 Mpx
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(bomb) }))
	t.Cleanup(srv.Close)
	c := &Cache{Dir: t.TempDir(), HTTP: srv.Client(), allowPrivate: true}
	if _, err := c.loadURL(context.Background(), srv.URL+"/a.png", false, false); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}
