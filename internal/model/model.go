// Package model holds the types and interfaces shared by all packages.
//
// YAML and JSON keys match the files in examples/ and nfc_player/DOCS.md.
// Pointer fields have no omitempty on purpose: a nil pointer means "use the
// default" and is written as an explicit null, so files keep their shape.
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

// Record is one record in records.yaml: an HA tag name and what it plays.
// Scanning a tag whose name matches plays one of its items, picked level
// by level (see records.Store.Pick). The scanned record's settings apply
// to whatever is picked, also from inside another record.
type Record struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"` // the HA tag name it matches (NormalizeName)
	// Aliases are ids it had before: "tag:<id>" for a v1 tag entry,
	// "pool:<id>" for a v1 pool, a plain id after a rename. Play events
	// may still name those.
	Aliases []string     `yaml:"aliases" json:"aliases"`
	Items   []RecordItem `yaml:"items" json:"items"`
	Shuffle Shuffle      `yaml:"shuffle" json:"shuffle"`
	Repeat  *Repeat      `yaml:"repeat" json:"repeat"` // nil = all
	// VolumeOffset is added to the area's volume, in percentage points
	// (-100..100); nil = the area volume as is.
	VolumeOffset *int           `yaml:"volume_offset" json:"volume_offset"`
	Player       *string        `yaml:"player" json:"player"` // player name; nil = settings chain
	Label        LabelOverrides `yaml:"label" json:"label"`
}

// RecordItem is one item of a record. Exactly one field is set.
type RecordItem struct {
	Media  string `yaml:"media,omitempty" json:"media,omitempty"`   // see PlayCommand.Media
	Record string `yaml:"record,omitempty" json:"record,omitempty"` // another record's id
	// Name is what the editor shows for a media link ("Deep Focus"),
	// filled from the music service when the link is added. Display only.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// Shuffle says per media kind whether to shuffle. Nil takes the default:
// albums play in the order their artist meant, everything else shuffles.
type Shuffle struct {
	Albums    *bool `yaml:"albums" json:"albums"`
	Artists   *bool `yaml:"artists" json:"artists"`
	Playlists *bool `yaml:"playlists" json:"playlists"`
	Other     *bool `yaml:"other" json:"other"` // songs, stations, searches by name
}

// Media kinds for Shuffle.For.
const (
	MediaAlbum    = "album"
	MediaArtist   = "artist"
	MediaPlaylist = "playlist"
	MediaOther    = "other"
)

// For says whether to shuffle media of kind (MediaAlbum … MediaOther; any
// other kind counts as MediaOther).
func (s Shuffle) For(kind string) bool {
	v, def := s.Other, true
	switch kind {
	case MediaAlbum:
		v, def = s.Albums, false
	case MediaArtist:
		v = s.Artists
	case MediaPlaylist:
		v = s.Playlists
	}
	if v == nil {
		return def
	}
	return *v
}

// LabelOverrides replace the values derived from the record when printing.
type LabelOverrides struct {
	Title    *string `yaml:"title" json:"title"`
	Subtitle *string `yaml:"subtitle" json:"subtitle"`
	Artwork  *string `yaml:"artwork" json:"artwork"` // URL or path under config_dir; nil = auto
}

// Subject is what a label and its art are made for: a record, or one media
// item of it. URL is the media the art is looked up for; Label.Artwork,
// when set, wins.
type Subject struct {
	ID    string
	Name  string
	URL   string
	Label LabelOverrides
}

// NormalizeName turns an HA tag name into the record lookup key: cut at the
// first "|", trim spaces, lower case. Scan and records must both use it.
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
	// OuterBleedMM is extra bleed on the paper-edge side of the first and
	// last columns, where printers drift the most; nil = DefaultOuterBleedMM.
	OuterBleedMM *float64 `yaml:"outer_bleed_mm" json:"outer_bleed_mm"`
	// DefaultBackground is the background id a print starts with; "" or an
	// id that no longer exists means DefaultBackgroundID.
	DefaultBackground string `yaml:"default_background" json:"default_background"`
}

// DefaultOuterBleedMM is PrintSettings.OuterBleedMM when unset.
const DefaultOuterBleedMM = 1.0

// OuterBleed returns OuterBleedMM, or DefaultOuterBleedMM when unset.
func (p PrintSettings) OuterBleed() float64 {
	if p.OuterBleedMM == nil {
		return DefaultOuterBleedMM
	}
	return *p.OuterBleedMM
}

// DefaultBackgroundID is the background used when none is chosen.
const DefaultBackgroundID = "white"

// Offset shifts the print, in mm. Positive Y moves the print up.
type Offset struct {
	XMM float64 `yaml:"x" json:"x"`
	YMM float64 `yaml:"y" json:"y"`
}

// DefaultSettings returns the settings a new install starts with.
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
	ScanStopped ScanResult = "stopped" // a toggle found music playing and stopped it
)

// Scan sources.
const (
	SourceTag   = "tag"   // tag_scanned event
	SourceEvent = "event" // nfc_player_play / nfc_player_stop event
	SourceUI    = "ui"    // play or stop from the web UI
)

// Scan is one handled tag_scanned event, play or stop command.
type Scan struct {
	Time     time.Time  `json:"time"`
	TagID    string     `json:"tag_id"`
	TagName  string     `json:"tag_name"`
	DeviceID string     `json:"device_id"`
	AreaID   string     `json:"area_id"`
	RecordID string     `json:"record_id"` // record played; empty when none matched
	Player   string     `json:"player"`    // player that handled it
	Result   ScanResult `json:"result"`
	Error    string     `json:"error"`
	Source   string     `json:"source"` // SourceTag, SourceEvent or SourceUI
	Media    string     `json:"media"`  // media item played
	Entity   string     `json:"entity"` // target player entity, when given
}

// SettingsSource returns the current settings. Implemented by config,
// read by scan, rooms and players.
type SettingsSource interface {
	Settings() Settings
}

// ScanLog is the scan history: scan writes, web reads.
// Implemented in scan as an in-memory ring buffer of 50.
type ScanLog interface {
	Add(Scan)
	Recent() []Scan // newest first
}

// PlayService is used by web for test play, the play box and the areas
// screen. Implemented by scan.
type PlayService interface {
	// PlayRecord plays a record in an area, as a tag scan would.
	PlayRecord(ctx context.Context, id, areaID string) (player string, err error)
	// Play runs a play command: from an nfc_player_play event or the UI.
	// It is recorded in the ScanLog.
	Play(ctx context.Context, cmd PlayCommand) (PlayResult, error)
	// Stop stops playback on the target: from an nfc_player_stop event or
	// the UI. It is recorded in the ScanLog.
	Stop(ctx context.Context, t Target) (player string, err error)
	Areas(ctx context.Context) ([]AreaInfo, error)
}

// Target is where a command plays: an HA area, or one media player entity.
// Exactly one is set. An entity must belong to a registered player's
// integration (sonos or music_assistant).
type Target struct {
	AreaID string `json:"area"`
	Entity string `json:"player"`
}

// PlayCommand is one play request. What it plays is the first non-empty
// of Media, Record, Tag and Pool.
//
// Media is a music URL (catalog.Parse), an "Artist — Album" name, or any
// other text, which is searched as a name (Music Assistant).
// Record is a record id, alias or name (NormalizeName).
// Tag and Pool are v1 names for Record: an old tag entry or pool id
// ("tag:"/"pool:" alias) or a record id or name.
//
// Nil settings defer to the record, then the defaults. Shuffle, when set,
// applies to every media kind.
type PlayCommand struct {
	Target
	Media   string  `json:"media"`
	Record  string  `json:"record"`
	Tag     string  `json:"tag"`
	Pool    string  `json:"pool"`
	Volume  *int    `json:"volume"`
	Shuffle *bool   `json:"shuffle"`
	Repeat  *Repeat `json:"repeat"`
	Radio   bool    `json:"radio"`  // Music Assistant radio mode; Sonos can't
	Toggle  bool    `json:"toggle"` // if the target is playing, stop instead
	Source  string  `json:"-"`      // SourceEvent or SourceUI
}

// PlayResult is the outcome of a play command.
type PlayResult struct {
	Player   string `json:"player"`    // player that played or stopped it
	Stopped  bool   `json:"stopped"`   // Toggle stopped music instead of playing
	RecordID string `json:"record_id"` // record played, if any
	Media    string `json:"media"`     // media item played
}

// AreaInfo is an HA area with its resolved players and volume.
type AreaInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SonosEntity string `json:"sonos_entity"`
	MAEntity    string `json:"ma_entity"`
	Volume      int    `json:"volume"`
}

// ArtworkSource returns square, cached artwork for a subject.
// Used by labels and web. Implemented by artwork.
type ArtworkSource interface {
	Artwork(ctx context.Context, s Subject, refresh bool) (image.Image, error)
}
