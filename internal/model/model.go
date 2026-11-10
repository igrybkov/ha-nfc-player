// Package model holds the types and interfaces shared by all work tracks.
//
// Owner: phase 0. Read-only for tracks A, B, C and D (docs/PLAN.md section 10).
// Change it only by updating docs/PLAN.md section 5 first.
//
// YAML and JSON keys match the files in docs/PLAN.md section 4. Pointer fields
// have no omitempty on purpose: a nil pointer means "use the default" and is
// written as an explicit null, so files keep the shape shown in the plan.
package model

import (
	"context"
	"image"
	"strings"
	"time"
)

// Repeat is the repeat mode of a playback request.
type Repeat string

const (
	RepeatOff Repeat = "off"
	RepeatOne Repeat = "one"
	RepeatAll Repeat = "all"
)

// Valid reports whether r is one of the known repeat modes.
func (r Repeat) Valid() bool {
	switch r {
	case RepeatOff, RepeatOne, RepeatAll:
		return true
	}
	return false
}

// Entry is one tag entry in tags.yaml.
type Entry struct {
	ID      string         `yaml:"id" json:"id"`
	Name    string         `yaml:"name" json:"name"`
	TagIDs  []string       `yaml:"tag_ids" json:"tag_ids"`
	URL     string         `yaml:"url" json:"url"`
	Shuffle bool           `yaml:"shuffle" json:"shuffle"`
	Repeat  Repeat         `yaml:"repeat" json:"repeat"`
	Volume  *int           `yaml:"volume" json:"volume"` // percent; nil = room default
	Player  *string        `yaml:"player" json:"player"` // player name; nil = settings chain
	Label   LabelOverrides `yaml:"label" json:"label"`
}

// LabelOverrides replace the values derived from the entry when printing.
type LabelOverrides struct {
	Title    *string `yaml:"title" json:"title"`
	Subtitle *string `yaml:"subtitle" json:"subtitle"`
	Artwork  *string `yaml:"artwork" json:"artwork"` // URL or path under config_dir; nil = auto
}

// NormalizeName turns an HA tag name into the registry lookup key: cut at the
// first "|", trim spaces, lower case. Scan and registry must both use it.
func NormalizeName(name string) string {
	name, _, _ = strings.Cut(name, "|")
	return strings.ToLower(strings.TrimSpace(name))
}

// Settings is settings.yaml.
type Settings struct {
	Version        int                    `yaml:"version" json:"version"`
	Readers        ReaderSettings         `yaml:"readers" json:"readers"`
	Players        PlayerSettings         `yaml:"players" json:"players"`
	Rooms          RoomSettings           `yaml:"rooms" json:"rooms"`
	MusicAssistant MusicAssistantSettings `yaml:"music_assistant" json:"music_assistant"`
	Print          PrintSettings          `yaml:"print" json:"print"`
}

// ReaderSettings limits which tag readers are accepted.
type ReaderSettings struct {
	// Integrations lists integration domains; only scans from devices of these
	// integrations are handled.
	Integrations []string `yaml:"integrations" json:"integrations"`
}

// PlayerSettings is the default player chain.
type PlayerSettings struct {
	Default  []string `yaml:"default" json:"default"`   // player names, tried in order
	Fallback bool     `yaml:"fallback" json:"fallback"` // on error, try the next player
}

// RoomSettings holds per-area playback settings.
type RoomSettings struct {
	DefaultVolume        int                     `yaml:"default_volume" json:"default_volume"`
	Areas                map[string]AreaSettings `yaml:"areas" json:"areas"` // key: HA area_id
	SonosModelPreference []string                `yaml:"sonos_model_preference" json:"sonos_model_preference"`
}

// AreaSettings overrides room defaults for one HA area.
type AreaSettings struct {
	Volume      *int    `yaml:"volume" json:"volume"`
	SonosEntity *string `yaml:"sonos_entity" json:"sonos_entity"`
	MAEntity    *string `yaml:"ma_entity" json:"ma_entity"`
}

// MusicAssistantSettings configures the Music Assistant player.
type MusicAssistantSettings struct {
	ConfigEntryID *string `yaml:"config_entry_id" json:"config_entry_id"` // nil = auto-detect
}

// PrintSettings holds printer calibration and bleed values, all in mm.
type PrintSettings struct {
	Offsets     map[string]Offset `yaml:"offsets" json:"offsets"` // key: template id
	InsetMM     float64           `yaml:"inset_mm" json:"inset_mm"`
	BleedMM     float64           `yaml:"bleed_mm" json:"bleed_mm"`
	EdgeBleedMM float64           `yaml:"edge_bleed_mm" json:"edge_bleed_mm"`
}

// Offset shifts the print, in mm. Positive Y moves the print up.
type Offset struct {
	XMM float64 `yaml:"x" json:"x"`
	YMM float64 `yaml:"y" json:"y"`
}

// DefaultSettings returns the settings from docs/PLAN.md section 4.
func DefaultSettings() Settings {
	vol := func(v int) *int { return &v }
	return Settings{
		Version: 1,
		Readers: ReaderSettings{Integrations: []string{"esphome"}},
		Players: PlayerSettings{Default: []string{"sonos", "music_assistant"}, Fallback: true},
		Rooms: RoomSettings{
			DefaultVolume: 15,
			Areas: map[string]AreaSettings{
				"family_room":     {Volume: vol(30)},
				"office":          {Volume: vol(15)},
				"master_bathroom": {Volume: vol(25)},
				"master_bedroom":  {Volume: vol(25)},
			},
			SonosModelPreference: []string{"Arc", "Five", "One", "Port"},
		},
		Print: PrintSettings{
			Offsets:     map[string]Offset{"avery-5260": {XMM: 0, YMM: -0.75}},
			InsetMM:     1.5,
			BleedMM:     1.5,
			EdgeBleedMM: 0.4,
		},
	}
}

// ScanResult is the outcome of one tag scan.
type ScanResult string

const (
	ScanPlayed  ScanResult = "played"
	ScanNoMatch ScanResult = "no_match"
	ScanIgnored ScanResult = "ignored"
	ScanError   ScanResult = "error"
)

// Scan is one handled tag_scanned event.
type Scan struct {
	Time     time.Time  `json:"time"`
	TagID    string     `json:"tag_id"`
	TagName  string     `json:"tag_name"`
	DeviceID string     `json:"device_id"`
	AreaID   string     `json:"area_id"`
	EntryID  string     `json:"entry_id"` // empty when no match
	Player   string     `json:"player"`   // player that handled it
	Result   ScanResult `json:"result"`
	Error    string     `json:"error"`
}

// EntryLookup is the read side of the registry, used by scan.
// Implemented by registry (track C).
type EntryLookup interface {
	ByTagID(tagID string) (Entry, bool)
	ByName(name string) (Entry, bool) // name already normalized by caller (NormalizeName)
	All() []Entry
}

// SettingsSource returns the current settings. Implemented by config
// (track C), read by tracks A and B.
type SettingsSource interface {
	Settings() Settings
}

// ScanLog is the scan history: scan (track A) writes, web (track C) reads.
// Implemented in scan as an in-memory ring buffer of 50.
type ScanLog interface {
	Add(Scan)
	Recent() []Scan // newest first
}

// PlayService is used by web for test play and the areas screen.
// Implemented by scan (track A).
type PlayService interface {
	PlayEntry(ctx context.Context, entry Entry, areaID string) (player string, err error)
	Areas(ctx context.Context) ([]AreaInfo, error)
}

// AreaInfo is an HA area with its resolved players and volume.
type AreaInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SonosEntity string `json:"sonos_entity"`
	MAEntity    string `json:"ma_entity"`
	Volume      int    `json:"volume"`
}

// ArtworkSource returns square, cached artwork for an entry.
// Used by labels and web. Implemented by artwork (track B).
type ArtworkSource interface {
	Artwork(ctx context.Context, e Entry, refresh bool) (image.Image, error)
}
