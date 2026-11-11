package main

import (
	"path/filepath"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/migrate"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/records"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// The seed files in examples/ load through the real stores and keep the
// values of the old automation.

// seedFile opens a seed file in the set of examples/, as the app would.
func seedFile[T any](name string, def func() T) *store.File[T] {
	return store.In(migrate.Open(filepath.Join("..", "..", "examples"), nil), name, def)
}

func TestSeedRecords(t *testing.T) {
	f := seedFile(records.FileName, records.DefaultFile)
	file, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	recs := records.New(f)
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
	if file.Version != migrate.Current || len(file.Records) != len(want)+1 {
		t.Fatalf("version %d, %d records; want %d, %d", file.Version, len(file.Records), migrate.Current, len(want)+1)
	}
	// Every record passes the store's validation, in file order (items
	// point back to earlier records).
	check := records.New(store.Open(filepath.Join(t.TempDir(), records.FileName), records.DefaultFile))
	for _, r := range recs.All() {
		if _, err := check.Create(r); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
		if r.ID != records.Slug(r.Name) {
			t.Errorf("%s: id is not the slug of the name (%s)", r.ID, records.Slug(r.Name))
		}
		// Scans look records up by normalized name.
		if got, ok := recs.ByName(model.NormalizeName(r.Name + " | x")); !ok || got.ID != r.ID {
			t.Errorf("%s: ByName finds %q, %v", r.ID, got.ID, ok)
		}
		if r.ID == "kitchen" {
			continue
		}
		w, ok := want[r.ID]
		if !ok {
			t.Errorf("unexpected record %q", r.ID)
			continue
		}
		if len(r.Items) != 1 {
			t.Errorf("%s: items %+v", r.ID, r.Items)
			continue
		}
		m, err := catalog.Parse(r.Items[0].Media)
		if err != nil || m.Kind != w.kind {
			t.Errorf("%s: media parses as %v, %v; want %s", r.ID, m.Kind, err, w.kind)
		}
		if r.Name != w.name || r.Shuffle.For(records.MediaKind(r.Items[0].Media)) != w.shuffle ||
			r.Repeat == nil || *r.Repeat != model.RepeatAll || r.Volume != nil || r.Player != nil {
			t.Errorf("%s: %+v", r.ID, r)
		}
	}

	// The kitchen button hold: eight items, the first a record.
	k, err := recs.Get("kitchen")
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "Kitchen" || k.Shuffle != (model.Shuffle{}) || k.Repeat == nil || *k.Repeat != model.RepeatAll ||
		k.Volume == nil || *k.Volume != 30 || k.Player != nil || len(k.Items) != 8 {
		t.Errorf("kitchen = %+v", k)
	}
	if k.Items[0].Record != "kenny-rogers-21-number-ones" || k.Items[1].Media != "Cat Stevens — Teaser and the Firecat" {
		t.Errorf("items = %+v", k.Items[:2])
	}
	if p, err := recs.Pick("kitchen"); err != nil || p.Record.ID != "kitchen" || p.Media == "" {
		t.Errorf("Pick(kitchen) = %+v, %v", p, err)
	}
}

func TestSeedSettings(t *testing.T) {
	f := seedFile(config.SettingsFileName, model.DefaultSettings)
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
