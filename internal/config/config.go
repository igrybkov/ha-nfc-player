// Package config loads runtime config (env and /data/options.json) and serves
// settings.yaml as a model.SettingsSource. It also serves the user label
// templates and themes merged with the built-ins.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// Config is the process configuration.
type Config struct {
	LogLevel   string // debug | info | warn | error
	ConfigDir  string // records/settings/templates/themes YAML; default /homeassistant/nfc_player
	DataDir    string // cache and options.json; default /data
	Listen     string // default :8099 under Supervisor, else 127.0.0.1:8099
	HAURL      string // websocket URL
	HAToken    string
	Supervisor bool // running under Supervisor (SUPERVISOR_TOKEN set)
}

// File names inside ConfigDir. records.yaml is records.FileName; names of
// older versions are in package migrate.
const (
	SettingsFileName  = "settings.yaml"
	TemplatesFileName = "templates.yaml"
	ThemesFileName    = "themes.yaml"
)

// Path returns the full path of a file in ConfigDir.
func (c Config) Path(name string) string { return filepath.Join(c.ConfigDir, name) }

const (
	defaultDataDir   = "/data"
	defaultConfigDir = "/homeassistant/nfc_player"
	// DefaultListen is the LISTEN default under Supervisor, where Ingress
	// reaches the app over the hassio network. The healthcheck reads its port.
	DefaultListen = ":8099"
	// DefaultDevListen is the LISTEN default outside Supervisor: loopback
	// only, since the UI has no login of its own.
	DefaultDevListen = "127.0.0.1:8099"
)

// options is /data/options.json, written by Supervisor from the app options.
type options struct {
	LogLevel  string `json:"log_level"`
	ConfigDir string `json:"config_dir"`
}

// Load reads env (HA_URL, HA_TOKEN, DATA_DIR, CONFIG_DIR, LISTEN, LOG_LEVEL,
// SUPERVISOR_TOKEN) and <DataDir>/options.json (log_level, config_dir).
// LISTEN defaults to DefaultListen under Supervisor, else DefaultDevListen.
//
// Precedence: env, then options.json, then defaults. Env wins so a dev run can
// point at local dirs while reusing a copied options.json.
//
// Under Supervisor (SUPERVISOR_TOKEN set) the HA websocket is the Supervisor
// proxy and HA_URL/HA_TOKEN are ignored. Outside it, HA_URL and HA_TOKEN are
// required; HA_URL may be the HA base URL (https://hass.example) or a full
// websocket URL.
func Load() (Config, error) { return load(os.Getenv, os.ReadFile) }

func load(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	c := Config{
		DataDir: firstNonEmpty(getenv("DATA_DIR"), defaultDataDir),
	}

	var opts options
	optsPath := filepath.Join(c.DataDir, "options.json")
	raw, err := readFile(optsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Config{}, fmt.Errorf("read %s: %w", optsPath, err)
	default:
		if err := json.Unmarshal(raw, &opts); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", optsPath, err)
		}
	}

	c.ConfigDir = firstNonEmpty(getenv("CONFIG_DIR"), opts.ConfigDir, defaultConfigDir)
	c.LogLevel = strings.ToLower(firstNonEmpty(getenv("LOG_LEVEL"), opts.LogLevel, "info"))
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.LogLevel) {
		return Config{}, fmt.Errorf("log_level must be debug, info, warn or error, not %q", c.LogLevel)
	}

	if tok := getenv("SUPERVISOR_TOKEN"); tok != "" {
		c.Supervisor = true
		c.Listen = firstNonEmpty(getenv("LISTEN"), DefaultListen)
		c.HAURL = ha.SupervisorURL
		c.HAToken = tok
		return c, nil
	}
	c.Listen = firstNonEmpty(getenv("LISTEN"), DefaultDevListen)

	c.HAToken = getenv("HA_TOKEN")
	if getenv("HA_URL") == "" || c.HAToken == "" {
		return Config{}, errors.New("outside Supervisor, HA_URL and HA_TOKEN are required")
	}
	c.HAURL, err = ha.WebsocketURL(getenv("HA_URL"))
	if err != nil {
		return Config{}, err
	}
	return c, nil
}

// SlogLevel returns the slog level for a Config.LogLevel value.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// SettingsStore serves settings.yaml. Missing file = model.DefaultSettings.
type SettingsStore struct {
	file *store.File[model.Settings]
}

var _ model.SettingsSource = (*SettingsStore)(nil)

// NewSettingsStore returns a SettingsStore on f. Open f with
// model.DefaultSettings as the default.
func NewSettingsStore(f *store.File[model.Settings]) *SettingsStore {
	return &SettingsStore{file: f}
}

// Settings returns the current settings. On a read error it logs and keeps
// the last good value (the store returns it with the error).
func (s *SettingsStore) Settings() model.Settings {
	v, err := s.file.Load()
	if err != nil {
		slog.Warn("settings: load", "path", s.file.Path(), "err", err)
	}
	fillDefaults(&v)
	return v
}

// Save validates and writes settings. Like Update, it refuses to overwrite a
// settings.yaml that does not parse, so a broken hand edit is not lost.
func (s *SettingsStore) Save(v model.Settings) error {
	fillDefaults(&v)
	if err := ValidateSettings(v); err != nil {
		return err
	}
	return s.file.Update(func(cur *model.Settings) error {
		*cur = v
		return nil
	})
}

// Update applies fn to the current settings and saves the result, validated,
// under the file lock.
func (s *SettingsStore) Update(fn func(*model.Settings) error) error {
	return s.file.Update(func(v *model.Settings) error {
		fillDefaults(v)
		if err := fn(v); err != nil {
			return err
		}
		return ValidateSettings(*v)
	})
}

// fillDefaults fills sections missing from a hand-written file. Values that
// can be legitimately zero (bleed, offsets) are left alone.
func fillDefaults(v *model.Settings) {
	d := model.DefaultSettings()
	if v.Version == 0 {
		v.Version = d.Version
	}
	if v.Readers.Integrations == nil {
		v.Readers.Integrations = d.Readers.Integrations
	}
	if v.Players.Default == nil {
		v.Players.Default = d.Players.Default
	}
	if v.Rooms.DefaultVolume == 0 {
		v.Rooms.DefaultVolume = d.Rooms.DefaultVolume
	}
	if v.Rooms.Areas == nil {
		v.Rooms.Areas = map[string]model.AreaSettings{}
	}
	if v.Rooms.SonosModelPreference == nil {
		v.Rooms.SonosModelPreference = d.Rooms.SonosModelPreference
	}
	if v.Print.Offsets == nil {
		v.Print.Offsets = map[string]model.Offset{}
	}
}

// ErrInvalid wraps validation errors from this package.
var ErrInvalid = errors.New("invalid")

// Limits for print values, in mm. Wider values are typos, not calibration.
const (
	maxPrintMM  = 10.0
	maxOffsetMM = 25.4
)

// ValidateSettings checks value ranges and required lists.
func ValidateSettings(v model.Settings) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
	}
	if err := nonEmptyUnique("readers.integrations", v.Readers.Integrations); err != nil {
		return err
	}
	if err := nonEmptyUnique("players.default", v.Players.Default); err != nil {
		return err
	}
	if v.Rooms.DefaultVolume < 0 || v.Rooms.DefaultVolume > 100 {
		return bad("rooms.default_volume must be 0..100, not %d", v.Rooms.DefaultVolume)
	}
	for id, a := range v.Rooms.Areas {
		if strings.TrimSpace(id) == "" {
			return bad("rooms.areas has an empty area id")
		}
		if a.Volume != nil && (*a.Volume < 0 || *a.Volume > 100) {
			return bad("rooms.areas.%s.volume must be 0..100, not %d", id, *a.Volume)
		}
	}
	for name, mm := range map[string]float64{
		"print.inset_mm": v.Print.InsetMM, "print.bleed_mm": v.Print.BleedMM, "print.edge_bleed_mm": v.Print.EdgeBleedMM,
		"print.outer_bleed_mm": v.Print.OuterBleed(),
	} {
		if mm < 0 || mm > maxPrintMM {
			return bad("%s must be 0..%g, not %g", name, maxPrintMM, mm)
		}
	}
	for id, o := range v.Print.Offsets {
		if abs(o.XMM) > maxOffsetMM || abs(o.YMM) > maxOffsetMM {
			return bad("print.offsets.%s must be within ±%g mm", id, maxOffsetMM)
		}
	}
	return nil
}

func nonEmptyUnique(field string, vs []string) error {
	if len(vs) == 0 {
		return fmt.Errorf("%w: %s needs at least one value", ErrInvalid, field)
	}
	seen := map[string]bool{}
	for _, s := range vs {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%w: %s has an empty value", ErrInvalid, field)
		}
		if seen[s] {
			return fmt.Errorf("%w: %s has %q twice", ErrInvalid, field, s)
		}
		seen[s] = true
	}
	return nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
