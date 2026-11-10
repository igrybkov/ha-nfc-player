// Package config loads runtime config (env and /data/options.json) and serves
// settings.yaml as a model.SettingsSource.
//
// Owner: track C (docs/PLAN.md section 10).
package config

import (
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// Config is the process configuration.
type Config struct {
	LogLevel   string // debug | info | warn | error
	ConfigDir  string // tags/settings/templates/backgrounds YAML; default /homeassistant/nfc_player
	DataDir    string // cache and options.json; default /data
	Listen     string // default :8099
	HAURL      string // websocket URL
	HAToken    string
	Supervisor bool // running under Supervisor (SUPERVISOR_TOKEN set)
}

// Load reads env (HA_URL, HA_TOKEN, DATA_DIR, CONFIG_DIR, LISTEN,
// SUPERVISOR_TOKEN) and <DataDir>/options.json (log_level, config_dir).
func Load() (Config, error) { panic("TODO: track C") }

// SettingsStore serves settings.yaml. Missing file = model.DefaultSettings.
type SettingsStore struct {
	file *store.File[model.Settings]
}

var _ model.SettingsSource = (*SettingsStore)(nil)

// NewSettingsStore returns a SettingsStore on f.
func NewSettingsStore(f *store.File[model.Settings]) *SettingsStore {
	return &SettingsStore{file: f}
}

// Settings returns the current settings. On a read error it logs and keeps the last good value.
func (s *SettingsStore) Settings() model.Settings {
	_ = s.file
	panic("TODO: track C")
}

// Save validates and writes settings.
func (s *SettingsStore) Save(v model.Settings) error { panic("TODO: track C") }
