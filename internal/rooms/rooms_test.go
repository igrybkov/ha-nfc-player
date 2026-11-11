package rooms

import (
	"context"
	"errors"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/ha/hatest"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

type staticSettings model.Settings

func (s *staticSettings) Settings() model.Settings { return model.Settings(*s) }

func ptr[T any](v T) *T { return &v }

func fixture() *hatest.Fake {
	return &hatest.Fake{
		AreaList: []ha.Area{
			{AreaID: "family_room", Name: "Family Room"},
			{AreaID: "office", Name: "Office"},
			{AreaID: "garage", Name: "Garage"},
			{AreaID: "kitchen", Name: "Kitchen"},
		},
		DeviceList: []ha.Device{
			{ID: "reader-fr", AreaID: "family_room", Model: "M5Stack Atom"},
			{ID: "reader-noarea"},
			{ID: "phone", AreaID: "office"},
			{ID: "sonos-sub", AreaID: "family_room", Model: "Sub"},
			{ID: "sonos-one", AreaID: "family_room", Model: "One"},
			{ID: "sonos-arc", AreaID: "family_room", Model: "Arc"},
			{ID: "sonos-office", AreaID: "office", Model: "Era 100"},
			{ID: "sonos-moved", AreaID: "office", Model: "Five"},
			{ID: "ma-server"},
		},
		EntityList: []ha.Entity{
			{EntityID: "binary_sensor.reader_fr", DeviceID: "reader-fr", Platform: "esphome"},
			{EntityID: "sensor.reader_noarea", DeviceID: "reader-noarea", Platform: "esphome"},
			{EntityID: "event.reader_noarea", DeviceID: "reader-noarea", Platform: "esphome", AreaID: "garage"},
			{EntityID: "device_tracker.phone", DeviceID: "phone", Platform: "mobile_app"},
			{EntityID: "media_player.sub", DeviceID: "sonos-sub", Platform: "sonos"},
			{EntityID: "media_player.one", DeviceID: "sonos-one", Platform: "sonos"},
			{EntityID: "media_player.arc", DeviceID: "sonos-arc", Platform: "sonos"},
			{EntityID: "switch.arc_loudness", DeviceID: "sonos-arc", Platform: "sonos"},
			{EntityID: "media_player.office", DeviceID: "sonos-office", Platform: "sonos"},
			// Device in office, entity moved to the kitchen.
			{EntityID: "media_player.five", DeviceID: "sonos-moved", Platform: "sonos", AreaID: "kitchen"},
			{EntityID: "media_player.disabled", DeviceID: "sonos-office", Platform: "sonos", DisabledBy: "user"},
			{EntityID: "media_player.family_room_ma", DeviceID: "ma-server", Platform: "music_assistant", AreaID: "family_room"},
			{EntityID: "media_player.office_ma", DeviceID: "ma-server", Platform: "music_assistant", AreaID: "office"},
		},
	}
}

func settings() *staticSettings {
	s := staticSettings(model.DefaultSettings())
	return &s
}

func TestAreaResolution(t *testing.T) {
	r := New(fixture(), settings())
	ctx := context.Background()

	tests := []struct {
		id   string
		want Area
	}{
		{"family_room", Area{ID: "family_room", Name: "Family Room", SonosEntity: "media_player.arc", MAEntity: "media_player.family_room_ma", Volume: 30}},
		{"office", Area{ID: "office", Name: "Office", SonosEntity: "media_player.office", MAEntity: "media_player.office_ma", Volume: 15}},
		// Entity area override wins over the device area.
		{"kitchen", Area{ID: "kitchen", Name: "Kitchen", SonosEntity: "media_player.five", Volume: 15}},
		{"garage", Area{ID: "garage", Name: "Garage", Volume: 15}},
	}
	for _, tt := range tests {
		got, err := r.Area(ctx, tt.id)
		if err != nil {
			t.Fatalf("Area(%s): %v", tt.id, err)
		}
		if got != tt.want {
			t.Errorf("Area(%s) = %+v, want %+v", tt.id, got, tt.want)
		}
	}
	if _, err := r.Area(ctx, "attic"); !errors.Is(err, ErrUnknownArea) {
		t.Errorf("unknown area: err = %v", err)
	}
}

func TestSonosModelPreference(t *testing.T) {
	f := fixture()
	s := settings()
	r := New(f, s)
	ctx := context.Background()

	// Without the Arc, One is next in line; Sub is not in the list.
	f.EntityList = removeEntity(f.EntityList, "media_player.arc")
	a, _ := r.Area(ctx, "family_room")
	if a.SonosEntity != "media_player.one" {
		t.Errorf("without Arc: %s, want media_player.one", a.SonosEntity)
	}
	// Nothing preferred left: first by entity_id.
	f.EntityList = removeEntity(f.EntityList, "media_player.one")
	a, _ = r.Area(ctx, "family_room")
	if a.SonosEntity != "media_player.sub" {
		t.Errorf("no preferred model: %s, want media_player.sub", a.SonosEntity)
	}
}

func TestAreaOverrides(t *testing.T) {
	s := settings()
	s.Rooms.Areas = map[string]model.AreaSettings{
		"family_room": {SonosEntity: ptr("media_player.sub"), MAEntity: ptr("media_player.custom_ma")},
		"office":      {SonosEntity: ptr(""), Volume: ptr(40)},
	}
	s.Rooms.DefaultVolume = 20
	r := New(fixture(), s)
	ctx := context.Background()

	a, _ := r.Area(ctx, "family_room")
	if a.SonosEntity != "media_player.sub" || a.MAEntity != "media_player.custom_ma" || a.Volume != 20 {
		t.Errorf("family_room = %+v", a)
	}
	// An empty override string means "no override".
	a, _ = r.Area(ctx, "office")
	if a.SonosEntity != "media_player.office" || a.Volume != 40 {
		t.Errorf("office = %+v", a)
	}

	s.Rooms.DefaultVolume = 0
	a, _ = r.Area(ctx, "garage")
	if a.Volume != FallbackVolume {
		t.Errorf("garage volume with default 0 = %d, want %d", a.Volume, FallbackVolume)
	}
}

func TestEntity(t *testing.T) {
	f := fixture()
	f.EntityList = append(f.EntityList,
		ha.Entity{EntityID: "media_player.portable_ma", DeviceID: "ma-server", Platform: "music_assistant"},
		ha.Entity{EntityID: "media_player.tv", DeviceID: "sonos-office", Platform: "cast"})
	s := settings()
	s.Rooms.Areas["office"] = model.AreaSettings{Volume: ptr(40), SonosEntity: ptr("media_player.disabled")}
	r := New(f, s)
	ctx := context.Background()

	tests := []struct {
		entity string
		want   Area
	}{
		{"media_player.arc", Area{ID: "family_room", Name: "Family Room", SonosEntity: "media_player.arc", Volume: 30}},
		{"media_player.office_ma", Area{ID: "office", Name: "Office", MAEntity: "media_player.office_ma", Volume: 40}},
		// Entity area override; the area override of the Sonos entity is not applied.
		{"media_player.five", Area{ID: "kitchen", Name: "Kitchen", SonosEntity: "media_player.five", Volume: 15}},
		{"media_player.office", Area{ID: "office", Name: "Office", SonosEntity: "media_player.office", Volume: 40}},
		{"media_player.portable_ma", Area{MAEntity: "media_player.portable_ma", Volume: 15}},
	}
	for _, tt := range tests {
		got, err := r.Entity(ctx, tt.entity)
		if err != nil {
			t.Fatalf("Entity(%s): %v", tt.entity, err)
		}
		if got != tt.want {
			t.Errorf("Entity(%s) = %+v, want %+v", tt.entity, got, tt.want)
		}
	}

	errs := []struct {
		entity string
		want   error
	}{
		{"media_player.nope", ErrUnknownEntity},
		{"media_player.tv", ErrUnsupportedEntity},
		{"switch.arc_loudness", ErrUnsupportedEntity},
		{"event.reader_noarea", ErrUnsupportedEntity},
	}
	for _, tt := range errs {
		if _, err := r.Entity(ctx, tt.entity); !errors.Is(err, tt.want) {
			t.Errorf("Entity(%s): err = %v, want %v", tt.entity, err, tt.want)
		}
	}
}

func TestAreas(t *testing.T) {
	r := New(fixture(), settings())
	areas, err := r.Areas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(areas) != 4 || areas[0].ID != "family_room" || areas[0].SonosEntity != "media_player.arc" {
		t.Errorf("Areas = %+v", areas)
	}
}

func TestDeviceArea(t *testing.T) {
	r := New(fixture(), settings())
	ctx := context.Background()
	esphome := []string{"esphome"}

	tests := []struct {
		device       string
		integrations []string
		area         string
		allowed      bool
	}{
		{"reader-fr", esphome, "family_room", true},
		{"reader-noarea", esphome, "garage", true}, // entity area fallback
		{"phone", esphome, "office", false},
		{"phone", []string{"esphome", "mobile_app"}, "office", true},
		{"unknown", esphome, "", false},
		{"", esphome, "", false},
		{"reader-fr", nil, "family_room", false},
	}
	for _, tt := range tests {
		area, allowed, err := r.DeviceArea(ctx, tt.device, tt.integrations)
		if err != nil {
			t.Fatal(err)
		}
		if area != tt.area || allowed != tt.allowed {
			t.Errorf("DeviceArea(%q, %v) = %q, %v; want %q, %v", tt.device, tt.integrations, area, allowed, tt.area, tt.allowed)
		}
	}
}

func TestVolume(t *testing.T) {
	a := Area{Volume: 30}
	tests := []struct {
		entry *int
		want  int
	}{
		{nil, 30},
		{ptr(50), 50},
		{ptr(0), 0},
		{ptr(150), 100},
		{ptr(-5), 0},
	}
	for _, tt := range tests {
		if got := Volume(model.Entry{Volume: tt.entry}, a); got != tt.want {
			t.Errorf("Volume(entry=%v) = %d, want %d", tt.entry, got, tt.want)
		}
	}
}

func removeEntity(list []ha.Entity, id string) []ha.Entity {
	var out []ha.Entity
	for _, e := range list {
		if e.EntityID != id {
			out = append(out, e)
		}
	}
	return out
}
