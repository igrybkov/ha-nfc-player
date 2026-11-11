package model

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// planSettings is a full settings.yaml example.
const planSettings = `
version: 1
readers:
  integrations: [esphome]
players:
  default: [sonos, music_assistant]
  fallback: true
rooms:
  default_volume: 15
  areas:
    family_room: {volume: 30, sonos_entity: null, ma_entity: null}
    office: {volume: 15}
    master_bathroom: {volume: 25}
    master_bedroom: {volume: 25}
  sonos_model_preference: [Arc, Five, One, Port]
music_assistant:
  config_entry_id: null
print:
  offsets:
    avery-5260: {x: 0, y: -0.75}
  inset_mm: 1.5
  bleed_mm: 1.5
  edge_bleed_mm: 0.4
`

func TestDefaultSettingsMatchPlan(t *testing.T) {
	var got Settings
	if err := yaml.Unmarshal([]byte(planSettings), &got); err != nil {
		t.Fatal(err)
	}
	if want := DefaultSettings(); !reflect.DeepEqual(got, want) {
		t.Errorf("parsed plan settings:\n%+v\nDefaultSettings:\n%+v", got, want)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	out, err := yaml.Marshal(DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	var got Settings
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, DefaultSettings()) {
		t.Errorf("round trip changed settings:\n%s", out)
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"Metallica — Essential":          "metallica — essential",
		"  Metallica — Essential | x y ": "metallica — essential",
		"Володимир Івасюк|1":             "володимир івасюк",
		"":                               "",
	} {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
