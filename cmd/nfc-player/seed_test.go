package main

import (
	"path/filepath"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/pools"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// The seed files in examples/ load through the real stores and keep the
// values of the old automation.

func TestSeedTags(t *testing.T) {
	f := store.Open(filepath.Join("..", "..", "examples", config.TagsFile), registry.DefaultTagsFile)
	file, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New(f)
	want := map[string]struct {
		name    string
		shuffle bool
		kind    catalog.Kind
	}{
		"metallica-essential":                             {"Metallica — Essential", true, catalog.KindPlaylist},
		"disturbed-divisible":                             {"Disturbed — Divisible", false, catalog.KindAlbum},
		"creedence-clearwater-revival-essential":          {"Creedence Clearwater Revival — Essential", true, catalog.KindPlaylist},
		"queen-bohemian-rhapsody-the-original-soundtrack": {"Queen — Bohemian Rhapsody (The Original Soundtrack)", false, catalog.KindAlbum},
		"volodymyr-ivasiuk":                               {"Volodymyr Ivasiuk", true, catalog.KindArtist},
		"ray-charles-the-ultimate-collection":             {"Ray Charles — The Ultimate Collection", true, catalog.KindAlbum},
		"kenny-rogers-21-number-ones":                     {"Kenny Rogers — 21 Number Ones", true, catalog.KindPlaylist},
	}
	if file.Version != 1 || len(file.Tags) != len(want) {
		t.Fatalf("version %d, %d tags; want 1, %d", file.Version, len(file.Tags), len(want))
	}
	for _, e := range reg.All() {
		w, ok := want[e.ID]
		if !ok {
			t.Errorf("unexpected entry %q", e.ID)
			continue
		}
		if e.ID != registry.Slug(e.Name) {
			t.Errorf("%s: id is not the slug of the name (%s)", e.ID, registry.Slug(e.Name))
		}
		if err := registry.Validate(e); err != nil {
			t.Errorf("%s: %v", e.ID, err)
		}
		m, err := catalog.Parse(e.URL)
		if err != nil || m.Kind != w.kind {
			t.Errorf("%s: url parses as %v, %v; want %s", e.ID, m.Kind, err, w.kind)
		}
		if e.Name != w.name || e.Shuffle != w.shuffle || e.Repeat != model.RepeatAll ||
			e.Volume != nil || e.Player != nil || e.TagIDs == nil || len(e.TagIDs) != 0 {
			t.Errorf("%s: %+v", e.ID, e)
		}
		// Scans look entries up by normalized name.
		if got, ok := reg.ByName(model.NormalizeName(e.Name + " | x")); !ok || got.ID != e.ID {
			t.Errorf("%s: ByName finds %q, %v", e.ID, got.ID, ok)
		}
	}
}

func TestSeedSettings(t *testing.T) {
	f := store.Open(filepath.Join("..", "..", "examples", config.SettingsFile), model.DefaultSettings)
	if _, err := f.Load(); err != nil {
		t.Fatal(err) // the store returns defaults on a parse error; fail here instead
	}
	s := config.NewSettingsStore(f).Settings()
	if err := config.ValidateSettings(s); err != nil {
		t.Fatal(err)
	}
	vols := map[string]int{}
	for id, a := range s.Rooms.Areas {
		if a.Volume == nil || a.SonosEntity != nil || a.MAEntity != nil {
			t.Errorf("area %s: %+v", id, a)
			continue
		}
		vols[id] = *a.Volume
	}
	wantVols := map[string]int{"family_room": 30, "office": 15, "master_bathroom": 25, "master_bedroom": 25}
	if len(vols) != len(wantVols) {
		t.Errorf("area volumes %v, want %v", vols, wantVols)
	}
	for id, v := range wantVols {
		if vols[id] != v {
			t.Errorf("area volumes %v, want %v", vols, wantVols)
			break
		}
	}
	if s.Rooms.DefaultVolume != 15 {
		t.Errorf("default volume %d", s.Rooms.DefaultVolume)
	}
	if o, ok := s.Print.Offsets["avery-5260"]; !ok || o.XMM != 0 || o.YMM != -0.75 || len(s.Print.Offsets) != 1 {
		t.Errorf("offsets %+v", s.Print.Offsets)
	}
	if s.Print.InsetMM != 1.5 || s.Print.BleedMM != 1.5 || s.Print.EdgeBleedMM != 0.4 {
		t.Errorf("print %+v", s.Print)
	}
	if len(s.Readers.Integrations) != 1 || s.Readers.Integrations[0] != "esphome" || !s.Players.Fallback {
		t.Errorf("readers %+v, players %+v", s.Readers, s.Players)
	}
}

func TestSeedPools(t *testing.T) {
	tags := registry.New(store.Open(filepath.Join("..", "..", "examples", config.TagsFile), registry.DefaultTagsFile))
	f := store.Open(filepath.Join("..", "..", "examples", config.PoolsFile), pools.DefaultFile)
	file, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if file.Version != 1 || len(file.Pools) != 1 {
		t.Fatalf("version %d, %d pools; want 1, 1", file.Version, len(file.Pools))
	}
	p, ok := pools.New(f, tags).Pool("kitchen")
	if !ok {
		t.Fatal("no kitchen pool")
	}
	if p.Name != "Kitchen" || p.Shuffle == nil || !*p.Shuffle || p.Repeat == nil || *p.Repeat != model.RepeatAll ||
		p.Volume == nil || *p.Volume != 30 || p.Player != nil || len(p.Items) != 8 {
		t.Errorf("kitchen = %+v", p)
	}
	if p.Items[0].Tag != "kenny-rogers-21-number-ones" || p.Items[1].Media != "Cat Stevens — Teaser and the Firecat" {
		t.Errorf("items = %+v", p.Items[:2])
	}
	// Every pool passes the store's validation (tag items exist, URLs parse).
	check := pools.New(store.Open(filepath.Join(t.TempDir(), config.PoolsFile), pools.DefaultFile), tags)
	for _, p := range file.Pools {
		if _, err := check.Create(p); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
}
