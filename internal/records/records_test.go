package records

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

const (
	albumURL  = "https://music.apple.com/us/album/teaser/1"
	artistURL = "https://music.apple.com/us/artist/dead-south/2"
	listURL   = "https://music.apple.com/us/playlist/boombox/pl.3"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(store.Open(filepath.Join(t.TempDir(), FileName), DefaultFile))
}

func mustCreate(t *testing.T, s *Store, r model.Record) model.Record {
	t.Helper()
	r, err := s.Create(r)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func media(m ...string) []model.RecordItem {
	var out []model.RecordItem
	for _, x := range m {
		out = append(out, model.RecordItem{Media: x})
	}
	return out
}

func TestCRUDAndLookups(t *testing.T) {
	s := newStore(t)
	a := mustCreate(t, s, model.Record{Name: " Five Finger Death Punch ", Items: media(artistURL)})
	if a.ID != "five-finger-death-punch" || a.Name != "Five Finger Death Punch" {
		t.Fatalf("created %+v", a)
	}
	if _, err := s.Create(model.Record{Name: "five finger death punch | x"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("same name: %v", err)
	}
	if _, err := s.Create(model.Record{Name: "Bad", Items: []model.RecordItem{{Record: "nope"}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("missing record item: %v", err)
	}
	if _, err := s.Create(model.Record{Name: "Bad", Items: []model.RecordItem{{Media: "https://example.com/x"}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad URL: %v", err)
	}
	mix := mustCreate(t, s, model.Record{Name: "Mix", Items: []model.RecordItem{{Record: a.ID}, {Media: "Cat Stevens — Teaser"}}})

	if r, ok := s.ByName("five finger death punch"); !ok || r.ID != a.ID {
		t.Errorf("ByName: %+v %v", r, ok)
	}
	if r, ok := s.ByName("mix"); !ok || r.ID != mix.ID {
		t.Errorf("ByName id: %+v %v", r, ok)
	}

	// Rename: references follow, the old id stays an alias.
	a.ID = "ffdp"
	if _, err := s.Update("five-finger-death-punch", a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(mix.ID); got.Items[0].Record != "ffdp" {
		t.Errorf("reference not renamed: %+v", got.Items)
	}
	for _, tt := range []struct{ ns, ref, want string }{
		{"", "ffdp", "ffdp"},
		{"", "five-finger-death-punch", "ffdp"},
		{"pool", "five-finger-death-punch", "ffdp"},
		{"tag", "Mix", "mix"},
	} {
		if r, ok := s.Resolve(tt.ns, tt.ref); !ok || r.ID != tt.want {
			t.Errorf("Resolve(%q, %q) = %q, %v", tt.ns, tt.ref, r.ID, ok)
		}
	}
	if used := s.UsedBy("ffdp"); len(used) != 1 || used[0].ID != "mix" {
		t.Errorf("UsedBy = %+v", used)
	}
	if err := s.Delete("ffdp"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("ffdp"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
}

func TestResolveAliasWinsPerNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	f := store.Open(path, DefaultFile)
	err := f.Save(File{Records: []model.Record{
		{ID: "kitchen", Name: "Kitchen", Aliases: []string{"tag:kitchen"}, Items: media(albumURL)},
		{ID: "kitchen-pool", Name: "Kitchen (pool)", Aliases: []string{"pool:kitchen"}, Items: media(listURL)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(f)
	if r, _ := s.Resolve("pool", "kitchen"); r.ID != "kitchen-pool" {
		t.Errorf("pool:kitchen → %q", r.ID)
	}
	if r, _ := s.Resolve("tag", "kitchen"); r.ID != "kitchen" {
		t.Errorf("tag:kitchen → %q", r.ID)
	}
}

// seq returns a random source that yields vs in turn.
func seq(vs ...int) func(int) int {
	i := 0
	return func(n int) int {
		v := vs[i%len(vs)] % n
		i++
		return v
	}
}

func TestPickLevelByLevel(t *testing.T) {
	s := newStore(t)
	big := mustCreate(t, s, model.Record{Name: "Big", Items: media(albumURL, listURL, "A — B", "C — D")})
	mix := mustCreate(t, s, model.Record{Name: "Mix", Items: []model.RecordItem{{Record: big.ID}, {Media: artistURL}}})

	// Level one is a coin flip between Big and the artist, whatever Big holds.
	counts := map[string]int{}
	for i := range 400 {
		s.intn = seq(i%2, i/2)
		s.last = map[string][]int{}
		p, err := s.Pick(mix.ID)
		if err != nil {
			t.Fatal(err)
		}
		counts[p.Media]++
		if p.Record.ID != mix.ID {
			t.Fatalf("pick record %q", p.Record.ID)
		}
	}
	if counts[artistURL] != 200 {
		t.Errorf("artist picked %d of 400, want 200: %v", counts[artistURL], counts)
	}
	p, _ := func() (Pick, error) { s.intn = seq(0, 0); s.last = map[string][]int{}; return s.Pick(mix.ID) }()
	if p.Media != albumURL || p.Kind != model.MediaAlbum || len(p.Path) != 2 || p.Path[1] != big.ID {
		t.Errorf("pick %+v", p)
	}
}

func TestPickAvoidsLastAndDeadBranches(t *testing.T) {
	s := newStore(t)
	empty := mustCreate(t, s, model.Record{Name: "Empty"})
	r := mustCreate(t, s, model.Record{Name: "R", Items: []model.RecordItem{{Record: empty.ID}, {Media: albumURL}, {Media: listURL}}})
	s.intn = seq(0)
	first, err := s.Pick(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Pick(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Media == second.Media {
		t.Errorf("same pick twice: %s", first.Media)
	}
	if _, err := s.Pick(empty.ID); !errors.Is(err, ErrNothingToPlay) {
		t.Errorf("empty: %v", err)
	}

	// A single option is picked again.
	one := mustCreate(t, s, model.Record{Name: "One", Items: media(albumURL)})
	for range 2 {
		if p, err := s.Pick(one.ID); err != nil || p.Media != albumURL {
			t.Fatalf("one: %+v %v", p, err)
		}
	}
}

func TestPickLoops(t *testing.T) {
	s := newStore(t)
	a := mustCreate(t, s, model.Record{Name: "A", Items: media(albumURL)})
	b := mustCreate(t, s, model.Record{Name: "B", Items: []model.RecordItem{{Record: a.ID}}})
	// A loops back to B; B to A: neither hangs, both find the album.
	a.Items = append(a.Items, model.RecordItem{Record: b.ID})
	if _, err := s.Update(a.ID, a); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		s.intn = seq(i)
		for _, id := range []string{a.ID, b.ID} {
			if p, err := s.Pick(id); err != nil || p.Media != albumURL {
				t.Fatalf("pick %s: %+v, %v", id, p, err)
			}
		}
	}
}

func TestVolumeOffsetRange(t *testing.T) {
	s := newStore(t)
	for _, v := range []int{-101, 101} {
		if _, err := s.Create(model.Record{Name: "R", Items: media(albumURL), VolumeOffset: &v}); !errors.Is(err, ErrInvalid) {
			t.Errorf("offset %d: %v", v, err)
		}
	}
	v := -100
	if _, err := s.Create(model.Record{Name: "R", Items: media(albumURL), VolumeOffset: &v}); err != nil {
		t.Error(err)
	}
}

func TestMediaKind(t *testing.T) {
	for text, want := range map[string]string{
		albumURL:               model.MediaAlbum,
		artistURL:              model.MediaArtist,
		listURL:                model.MediaPlaylist,
		"Cat Stevens — Teaser": model.MediaAlbum,
		"jazz for a rainy day": model.MediaOther,
		"https://music.apple.com/us/station/classic-rock-station/ra.985500514": model.MediaOther,
	} {
		if got := MediaKind(text); got != want {
			t.Errorf("MediaKind(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestSubject(t *testing.T) {
	s := newStore(t)
	art := "covers/a.jpg"
	inner := mustCreate(t, s, model.Record{Name: "Inner", Items: media(listURL), Label: model.LabelOverrides{Artwork: &art}})
	outer := mustCreate(t, s, model.Record{Name: "Outer", Items: []model.RecordItem{{Media: "a name"}, {Record: inner.ID}, {Media: albumURL}}})
	if sub := s.Subject(outer); sub.Label.Artwork == nil || *sub.Label.Artwork != art || sub.ID != outer.ID || sub.Name != "Outer" {
		t.Errorf("outer %+v", sub)
	}
	if sub := s.Subject(inner); sub.URL != "" || *sub.Label.Artwork != art {
		t.Errorf("inner %+v", sub)
	}
	plain := mustCreate(t, s, model.Record{Name: "Plain", Items: media("a name", albumURL)})
	if sub := s.Subject(plain); sub.URL != albumURL || sub.Label.Artwork != nil {
		t.Errorf("plain %+v", sub)
	}
}

func TestShuffleDefaults(t *testing.T) {
	var sh model.Shuffle
	if sh.For(model.MediaAlbum) || !sh.For(model.MediaArtist) || !sh.For(model.MediaPlaylist) || !sh.For("station") {
		t.Error("defaults: albums in order, the rest shuffled")
	}
	yes := true
	sh.Albums = &yes
	if !sh.For(model.MediaAlbum) {
		t.Error("albums override")
	}
}

func TestDedupe(t *testing.T) {
	s := newStore(t)
	r := mustCreate(t, s, model.Record{Name: "FFDP", Items: []model.RecordItem{
		{Media: artistURL},
		{Media: artistURL + "?l=en"},
		{Media: "Cat Stevens — Teaser"},
		{Media: "  cat stevens —   teaser "},
		{Media: "https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy?si=1"},
		{Media: "https://open.spotify.com/intl-de/album/4aawyAB9vmqN3uQ7FjRGTy"},
		{Media: listURL},
	}})
	if len(r.Items) != 4 || r.Items[0].Media != artistURL || r.Items[3].Media != listURL {
		t.Fatalf("items %+v", r.Items)
	}

	// A file written with duplicates reads without them.
	f := store.Open(filepath.Join(t.TempDir(), FileName), DefaultFile)
	if err := f.Save(File{Records: []model.Record{{ID: "x", Name: "X", Items: media(albumURL, albumURL)}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := New(f).Get("x"); len(got.Items) != 1 {
		t.Errorf("read %+v", got.Items)
	}
}
