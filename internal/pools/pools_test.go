package pools

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// entries is a minimal model.EntryLookup.
type entries []model.Entry

func (es entries) ByTagID(string) (model.Entry, bool) { return model.Entry{}, false }
func (es entries) ByName(string) (model.Entry, bool)  { return model.Entry{}, false }
func (es entries) All() []model.Entry                 { return es }

func ptr[T any](v T) *T { return &v }

const (
	kennyURL = "https://music.apple.com/us/playlist/kenny-rogers-essentials/pl.6a4c7a1c2b0a4d0c9d6d4b9f0e2a1c3d"
	southURL = "https://music.apple.com/us/artist/the-dead-south/668262442"
)

var testEntries = entries{
	{ID: "kenny", Name: "Kenny Rogers — 21 Number Ones", URL: kennyURL},
	{ID: "metallica", Name: "Metallica — Essential", URL: "apple_music://playlist/pl.e55d"},
	{ID: "kitchen-tag", Name: "Kitchen", Pool: "kitchen"},
}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pools.yaml")
	return New(store.Open(path, DefaultFile), testEntries), path
}

func kitchen() model.Pool {
	return model.Pool{
		Name:    "Kitchen",
		Shuffle: ptr(true),
		Repeat:  ptr(model.RepeatAll),
		Volume:  ptr(30),
		Items: []model.PoolItem{
			{Tag: "kenny"},
			{Media: "Cat Stevens — Teaser and the Firecat"},
			{Media: southURL},
		},
	}
}

func TestCRUD(t *testing.T) {
	s, path := newStore(t)
	if got := s.All(); len(got) != 0 || got == nil {
		t.Fatalf("empty store All = %#v", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reading created the file")
	}

	p, err := s.Create(kitchen())
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "kitchen" {
		t.Errorf("id = %q, want the slug of the name", p.ID)
	}
	// The same name again gets a suffix; a chosen id that is taken is a duplicate.
	if p2, err := s.Create(kitchen()); err != nil || p2.ID != "kitchen-2" {
		t.Errorf("second Create = %q, %v", p2.ID, err)
	}
	dup := kitchen()
	dup.ID = "kitchen"
	if _, err := s.Create(dup); !errors.Is(err, ErrDuplicate) {
		t.Errorf("taken id: err = %v", err)
	}
	// "All" slugs to the reserved id and gets a suffix.
	all := kitchen()
	all.Name = "All"
	if p3, err := s.Create(all); err != nil || p3.ID != "all-2" {
		t.Errorf("Create(All) = %q, %v", p3.ID, err)
	}

	// Only a name without letters or digits falls back to "pool".
	for name, want := range map[string]string{"Entry": "entry", "— ★ —": "pool"} {
		np := kitchen()
		np.Name = name
		if got, err := s.Create(np); err != nil || got.ID != want {
			t.Errorf("Create(%q) = %q, %v; want %q", name, got.ID, err, want)
		}
		if err := s.Delete(want); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Get("kitchen")
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(nope): err = %v", err)
	}

	// Update keeps the id when empty and renames when set.
	upd := kitchen()
	upd.Volume = nil
	if got, err := s.Update("kitchen", upd); err != nil || got.ID != "kitchen" || got.Volume != nil {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	upd.ID = "kitchen-2"
	if _, err := s.Update("kitchen", upd); !errors.Is(err, ErrDuplicate) {
		t.Errorf("rename to a taken id: err = %v", err)
	}
	upd.ID = "cooking"
	if got, err := s.Update("kitchen", upd); err != nil || got.ID != "cooking" {
		t.Fatalf("rename = %+v, %v", got, err)
	}
	if _, ok := s.Pool("kitchen"); ok {
		t.Error("old id still found after rename")
	}
	if _, err := s.Update("nope", kitchen()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(nope): err = %v", err)
	}

	if err := s.Delete("cooking"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("cooking"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete: err = %v", err)
	}
	var ids []string
	for _, p := range s.All() {
		ids = append(ids, p.ID)
	}
	if !reflect.DeepEqual(ids, []string{"kitchen-2", "all-2"}) {
		t.Errorf("ids = %v", ids)
	}

	// The file has the documented shape, with explicit nulls.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version: 1", "id: kitchen-2", "volume: 30", "player: null", "- tag: kenny", "- media: " + southURL} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("file has no %q:\n%s", want, raw)
		}
	}
}

func TestValidate(t *testing.T) {
	s, _ := newStore(t)
	bad := map[string]func(p *model.Pool){
		"no name":       func(p *model.Pool) { p.Name = " " },
		"reserved id":   func(p *model.Pool) { p.ID = "all" },
		"id not a slug": func(p *model.Pool) { p.ID = "Kitchen Pool" },
		"no items":      func(p *model.Pool) { p.Items = nil },
		"empty item":    func(p *model.Pool) { p.Items = []model.PoolItem{{Tag: " "}} },
		"tag and media": func(p *model.Pool) { p.Items = []model.PoolItem{{Tag: "kenny", Media: southURL}} },
		"unknown tag":   func(p *model.Pool) { p.Items = append(p.Items, model.PoolItem{Tag: "nope"}) },
		"nested pool":   func(p *model.Pool) { p.Items = append(p.Items, model.PoolItem{Tag: "kitchen-tag"}) },
		"bad URL": func(p *model.Pool) {
			p.Items = append(p.Items, model.PoolItem{Media: "https://open.spotify.com/album/1"})
		},
		"bad URI":          func(p *model.Pool) { p.Items = append(p.Items, model.PoolItem{Media: "apple_music://album/x"}) },
		"volume over 100":  func(p *model.Pool) { p.Volume = ptr(101) },
		"negative volume":  func(p *model.Pool) { p.Volume = ptr(-1) },
		"bad repeat":       func(p *model.Pool) { p.Repeat = ptr(model.Repeat("sometimes")) },
		"empty repeat set": func(p *model.Pool) { p.Repeat = ptr(model.Repeat("")) },
	}
	for name, mutate := range bad {
		p := kitchen()
		mutate(&p)
		if _, err := s.Create(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if len(s.All()) != 0 {
		t.Fatal("an invalid pool was stored")
	}

	// Trimmed input, null settings, a blank player and name-only media are fine.
	p := model.Pool{Name: "  Mixed ", Player: ptr(" "), Items: []model.PoolItem{{Tag: " kenny "}, {Media: "Volodymyr Ivasiuk"}}}
	got, err := s.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Mixed" || got.Player != nil || got.Items[0].Tag != "kenny" {
		t.Errorf("normalized = %+v", got)
	}
}

func TestPoolAll(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Create(kitchen()); err != nil {
		t.Fatal(err)
	}
	all, ok := s.Pool(model.PoolAll)
	want := []model.PoolItem{{Tag: "kenny"}, {Tag: "metallica"}} // the pool entry is skipped
	if !ok || all.ID != model.PoolAll || all.Name != AllName || !reflect.DeepEqual(all.Items, want) {
		t.Errorf("Pool(all) = %+v, %v", all, ok)
	}
	if k, ok := s.Pool("kitchen"); !ok || len(k.Items) != 3 {
		t.Errorf("Pool(kitchen) = %+v, %v", k, ok)
	}
	if _, ok := s.Pool("nope"); ok {
		t.Error("Pool(nope) found")
	}
	// The built-in pool is not stored.
	for _, p := range s.All() {
		if p.ID == model.PoolAll {
			t.Error("All lists the built-in pool")
		}
	}
}

// A broken hand edit keeps the last good pools and refuses writes.
func TestBrokenFile(t *testing.T) {
	s, path := newStore(t)
	if _, err := s.Create(kitchen()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("pools: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Pool("kitchen"); !ok {
		t.Error("last good pool lost after a broken edit")
	}
	if _, err := s.Create(kitchen()); err == nil {
		t.Error("Create wrote over a broken file")
	}
}

// recordArt records the entry it was asked for.
type recordArt struct{ got []model.Entry }

func (r *recordArt) Artwork(_ context.Context, e model.Entry, _ bool) (image.Image, error) {
	r.got = append(r.got, e)
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}

func TestArtwork(t *testing.T) {
	s, _ := newStore(t)
	mediaFirst := model.Pool{Name: "Media first", Items: []model.PoolItem{{Media: "Some Name"}, {Media: southURL}, {Tag: "kenny"}}}
	noURL := model.Pool{Name: "No URL", Items: []model.PoolItem{{Media: "Some Name"}}}
	for _, p := range []model.Pool{kitchen(), mediaFirst, noURL} {
		if _, err := s.Create(p); err != nil {
			t.Fatal(err)
		}
	}
	// Tag items whose entry was deleted, or plays a pool by now, are skipped.
	gone := model.Pool{ID: "gone", Name: "Gone", Items: []model.PoolItem{{Tag: "kenny"}}}
	if _, err := s.Create(gone); err != nil {
		t.Fatal(err)
	}
	err := s.file.Update(func(f *File) error {
		f.Pools[3].Items = []model.PoolItem{{Tag: "deleted"}, {Tag: "kitchen-tag"}, {Media: southURL}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		entry model.Entry
		url   string
	}{
		{"tag item first", model.Entry{ID: "a", Pool: "kitchen"}, kennyURL},
		{"first media URL", model.Entry{ID: "b", Pool: "media-first"}, southURL},
		{"stale tag items skipped", model.Entry{ID: "c", Pool: "gone"}, southURL},
		{"all pool", model.Entry{ID: "d", Pool: model.PoolAll}, kennyURL},
		{"no URL in pool", model.Entry{ID: "e", Pool: "no-url"}, ""},
		{"unknown pool", model.Entry{ID: "f", Pool: "nope"}, ""},
		{"override wins", model.Entry{ID: "g", Pool: "kitchen", Label: model.LabelOverrides{Artwork: ptr("art.jpg")}}, ""},
		{"URL entry", model.Entry{ID: "h", URL: southURL}, southURL},
	}
	for _, tt := range tests {
		src := &recordArt{}
		a := &Artwork{Pools: s, Source: src}
		if _, err := a.Artwork(context.Background(), tt.entry, false); err != nil {
			t.Fatal(err)
		}
		want := tt.entry
		want.URL = tt.url
		if len(src.got) != 1 || !reflect.DeepEqual(src.got[0], want) {
			t.Errorf("%s: source got %+v, want %+v", tt.name, src.got, want)
		}
	}
}

func TestByName(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Create(kitchen()); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"kitchen":  "kitchen", // name and id
		"all tags": model.PoolAll,
		"all":      model.PoolAll,
		"office":   "",
		"":         "",
	} {
		p, ok := s.ByName(name)
		if ok != (want != "") || p.ID != want {
			t.Errorf("ByName(%q) = %q, %v; want %q", name, p.ID, ok, want)
		}
	}
}
