package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

func newRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tags.yaml")
	return New(store.Open(path, DefaultTagsFile)), path
}

func entry(name, url string) model.Entry {
	return model.Entry{Name: name, URL: url}
}

func ptr[T any](v T) *T { return &v }

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Metallica — Essential":              "metallica-essential",
		"  Queen — Bohemian Rhapsody (OST) ": "queen-bohemian-rhapsody-ost",
		"Володимир Івасюк":                   "володимир-івасюк",
		"AC/DC":                              "ac-dc",
		"—":                                  "entry",
		"":                                   "entry",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := model.Entry{Name: "A — B", URL: "apple_music://album/1", Repeat: model.RepeatAll}
	if err := Validate(ok); err != nil {
		t.Fatalf("valid entry: %v", err)
	}
	bad := []func(e *model.Entry){
		func(e *model.Entry) { e.Name = " " },
		func(e *model.Entry) { e.Name = "| suffix only" },
		func(e *model.Entry) { e.URL = "" },
		func(e *model.Entry) { e.Repeat = "sometimes" },
		func(e *model.Entry) { e.Repeat = "" },
		func(e *model.Entry) { e.Volume = ptr(101) },
		func(e *model.Entry) { e.Volume = ptr(-1) },
		func(e *model.Entry) { e.Player = ptr(" ") },
		func(e *model.Entry) { e.TagIDs = []string{""} },
		func(e *model.Entry) { e.URL = "https://example.com/us/album/x/1" },      // not Apple
		func(e *model.Entry) { e.URL = "spotify:album:1" },                       // unsupported scheme
		func(e *model.Entry) { e.URL = "apple_music://album/not-a-number" },      // parses, bad id
		func(e *model.Entry) { e.URL = "https://music.apple.com/us/playlist/x" }, // no id
		func(e *model.Entry) { e.Pool = "kitchen" },                              // url and pool
		func(e *model.Entry) { e.URL, e.Pool = "", "Kitchen" },                   // not a slug
		func(e *model.Entry) { e.URL, e.Pool = "", "kitchen mix" },
		func(e *model.Entry) { e.URL, e.Pool = "", "-kitchen" },
	}
	for i, mod := range bad {
		e := ok
		mod(&e)
		if err := Validate(e); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: got %v, want ErrInvalid", i, err)
		}
	}
	for _, u := range []string{
		"https://music.apple.com/us/album/divisive/1646032515",
		"https://music.apple.com/us/playlist/metallica-essentials/pl.e55da0d4a18140e3b1120339e2608aa5",
		"apple_music://playlist/pl.abc",
		"apple_music://artist/123",
	} {
		e := ok
		e.URL = u
		if err := Validate(e); err != nil {
			t.Errorf("url %q: %v", u, err)
		}
	}
	for _, v := range []int{0, 100} {
		e := ok
		e.Volume = ptr(v)
		if err := Validate(e); err != nil {
			t.Errorf("volume %d: %v", v, err)
		}
	}
}

func TestPoolEntries(t *testing.T) {
	r, _ := newRegistry(t)
	// Trimmed, and an unknown pool is fine at save time.
	e, err := r.Create(model.Entry{Name: "Kitchen mix", Pool: "  kitchen  "})
	if err != nil {
		t.Fatal(err)
	}
	if e.Pool != "kitchen" || e.URL != "" {
		t.Fatalf("got pool %q url %q", e.Pool, e.URL)
	}
	for _, p := range []string{model.PoolAll, "кухня", "mix-2"} {
		if _, err := r.Create(model.Entry{Name: "Pool " + p, Pool: p}); err != nil {
			t.Errorf("pool %q: %v", p, err)
		}
	}
	// Switch the entry from pool to url and back.
	e.Pool, e.URL = "", "apple_music://album/1"
	if e, err = r.Update(e.ID, e); err != nil || e.URL == "" || e.Pool != "" {
		t.Fatalf("to url: %+v, %v", e, err)
	}
	e.Pool, e.URL = model.PoolAll, ""
	if e, err = r.Update(e.ID, e); err != nil || e.Pool != model.PoolAll {
		t.Fatalf("to pool: %+v, %v", e, err)
	}
	// Whitespace-only url and pool count as unset.
	if _, err := r.Create(model.Entry{Name: "Nothing", URL: " ", Pool: " "}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no source: got %v, want ErrInvalid", err)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	r, path := newRegistry(t)
	raw := `version: 1
tags:
  - id: metallica-essential
    name: Metallica — Essential
    url: https://music.apple.com/us/playlist/x/pl.1
    shuffle: true
`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	all := r.All()
	if len(all) != 1 {
		t.Fatalf("got %d entries", len(all))
	}
	e := all[0]
	if e.Repeat != model.RepeatAll {
		t.Errorf("repeat = %q, want all", e.Repeat)
	}
	if e.TagIDs == nil {
		t.Error("TagIDs is nil, want []")
	}
	if e.Volume != nil || e.Player != nil {
		t.Error("nil pointers must stay nil")
	}
}

func TestAllOnMissingFileIsEmptyAndWritesNothing(t *testing.T) {
	r, path := newRegistry(t)
	all := r.All()
	if all == nil || len(all) != 0 {
		t.Fatalf("got %#v, want empty slice", all)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("tags.yaml written on read (stat err %v)", err)
	}
}

func TestCRUD(t *testing.T) {
	r, path := newRegistry(t)

	e, err := r.Create(model.Entry{
		Name:   "  Metallica — Essential ",
		URL:    "https://music.apple.com/us/playlist/x/pl.1",
		TagIDs: []string{" abc ", "abc", ""},
		Player: ptr(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "metallica-essential" || e.Name != "Metallica — Essential" {
		t.Fatalf("got id=%q name=%q", e.ID, e.Name)
	}
	if e.Repeat != model.RepeatAll || len(e.TagIDs) != 1 || e.TagIDs[0] != "abc" || e.Player != nil {
		t.Fatalf("normalize failed: %+v", e)
	}

	got, err := r.Get(e.ID)
	if err != nil || got.URL != e.URL {
		t.Fatalf("Get: %+v %v", got, err)
	}

	e.Shuffle = true
	e.Volume = ptr(40)
	upd, err := r.Update(e.ID, e)
	if err != nil {
		t.Fatal(err)
	}
	if !upd.Shuffle || *upd.Volume != 40 {
		t.Fatalf("update not applied: %+v", upd)
	}

	// Persisted: a fresh registry on the same file sees the change.
	fresh := New(store.Open(path, DefaultTagsFile))
	if got, _ := fresh.Get(e.ID); !got.Shuffle {
		t.Fatal("update not persisted")
	}

	if err := r.Delete(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := r.Delete(e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestCreateConflicts(t *testing.T) {
	r, _ := newRegistry(t)
	a, err := r.Create(model.Entry{Name: "A — B", URL: "apple_music://album/1", TagIDs: []string{"t1"}})
	if err != nil {
		t.Fatal(err)
	}

	// Same normalized name.
	if _, err := r.Create(entry("a — b | reader note", "apple_music://album/1")); !errors.Is(err, ErrDuplicate) {
		t.Errorf("same name: %v", err)
	}
	// Same tag id.
	if _, err := r.Create(model.Entry{Name: "C", URL: "apple_music://album/1", TagIDs: []string{"t1"}}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("same tag id: %v", err)
	}
	// Explicit id taken.
	if _, err := r.Create(model.Entry{ID: a.ID, Name: "Other", URL: "apple_music://album/1"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("explicit id: %v", err)
	}
	// Different name, same slug: suffix.
	b, err := r.Create(entry("A / B", "apple_music://album/1"))
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != "a-b-2" {
		t.Errorf("id = %q, want a-b-2", b.ID)
	}
	// Invalid.
	if _, err := r.Create(entry("X", "")); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid: %v", err)
	}
}

func TestUpdateRules(t *testing.T) {
	r, _ := newRegistry(t)
	a, _ := r.Create(entry("A", "apple_music://album/1"))
	b, _ := r.Create(entry("B", "apple_music://album/1"))

	if _, err := r.Update("missing", entry("Z", "apple_music://album/1")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if _, err := r.Update(a.ID, model.Entry{ID: "other", Name: "A", URL: "apple_music://album/1"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("id change: %v", err)
	}
	if _, err := r.Update(b.ID, entry("a", "apple_music://album/1")); !errors.Is(err, ErrDuplicate) {
		t.Errorf("rename onto other: %v", err)
	}
	// Renaming itself (case change) is fine.
	if _, err := r.Update(a.ID, entry("a", "apple_music://album/2")); err != nil {
		t.Errorf("self rename: %v", err)
	}
}

func TestLookup(t *testing.T) {
	r, _ := newRegistry(t)
	if _, err := r.Create(model.Entry{Name: "Metallica — Essential", URL: "apple_music://album/1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(model.Entry{Name: "Other", URL: "apple_music://album/2", TagIDs: []string{"tag-9"}}); err != nil {
		t.Fatal(err)
	}

	e, ok := r.ByName(model.NormalizeName(" METALLICA — essential | living room"))
	if !ok || e.URL != "apple_music://album/1" {
		t.Fatalf("ByName: %+v %v", e, ok)
	}
	if _, ok := r.ByName(""); ok {
		t.Error("empty name must not match")
	}
	if _, ok := r.ByName("nope"); ok {
		t.Error("unknown name matched")
	}

	e, ok = r.ByTagID("tag-9")
	if !ok || e.URL != "apple_music://album/2" {
		t.Fatalf("ByTagID: %+v %v", e, ok)
	}
	if _, ok := r.ByTagID(""); ok {
		t.Error("empty tag id must not match")
	}

	all := r.All()
	all[0].Name = "mutated"
	if got := r.All()[0].Name; got == "mutated" {
		t.Error("All must return a copy")
	}
}

func TestBrokenFileKeepsServingLastGood(t *testing.T) {
	r, path := newRegistry(t)
	if _, err := r.Create(entry("A", "apple_music://album/1")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tags: [oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.ByName("a"); !ok {
		t.Fatal("lookup must use the last good content")
	}
	if _, err := r.Create(entry("B", "apple_music://album/1")); err == nil {
		t.Fatal("create must fail while the file is broken")
	}
}
