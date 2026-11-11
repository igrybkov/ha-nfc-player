package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestLoadSupervisor(t *testing.T) {
	c, err := load(
		env(map[string]string{"SUPERVISOR_TOKEN": "tok", "HA_URL": "https://ignored"}),
		files(map[string]string{"/data/options.json": `{"log_level":"debug","config_dir":"/homeassistant/x"}`}),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		LogLevel: "debug", ConfigDir: "/homeassistant/x", DataDir: "/data", Listen: ":8099",
		HAURL: "ws://supervisor/core/websocket", HAToken: "tok", Supervisor: true,
	}
	if c != want {
		t.Fatalf("got %+v\nwant %+v", c, want)
	}
}

func TestLoadDefaultsWithoutOptions(t *testing.T) {
	c, err := load(env(map[string]string{"SUPERVISOR_TOKEN": "tok"}), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfigDir != "/homeassistant/nfc_player" || c.LogLevel != "info" || c.DataDir != "/data" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadDevMode(t *testing.T) {
	c, err := load(env(map[string]string{
		"HA_URL": "https://hass.example/", "HA_TOKEN": "t",
		"DATA_DIR": "/tmp/d", "CONFIG_DIR": "/tmp/c", "LISTEN": "127.0.0.1:9000",
	}), files(map[string]string{"/tmp/d/options.json": `{"config_dir":"/from/options","log_level":"warn"}`}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Supervisor || c.HAURL != "wss://hass.example/api/websocket" || c.HAToken != "t" {
		t.Fatalf("ha: %+v", c)
	}
	if c.ConfigDir != "/tmp/c" {
		t.Errorf("env CONFIG_DIR must win over options: %q", c.ConfigDir)
	}
	if c.LogLevel != "warn" || c.Listen != "127.0.0.1:9000" {
		t.Errorf("got %+v", c)
	}
}

func TestLoadListenDefaults(t *testing.T) {
	dev, err := load(env(map[string]string{"HA_URL": "https://hass.example", "HA_TOKEN": "t"}), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if dev.Listen != "127.0.0.1:8099" {
		t.Errorf("outside Supervisor: Listen = %q, want loopback only", dev.Listen)
	}
	sup, err := load(env(map[string]string{"SUPERVISOR_TOKEN": "tok"}), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if sup.Listen != ":8099" {
		t.Errorf("under Supervisor: Listen = %q, want :8099", sup.Listen)
	}
	sup, err = load(env(map[string]string{"SUPERVISOR_TOKEN": "tok", "LISTEN": ":9000"}), files(nil))
	if err != nil || sup.Listen != ":9000" {
		t.Errorf("LISTEN env under Supervisor: %q, %v", sup.Listen, err)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]struct {
		env   map[string]string
		files map[string]string
	}{
		"no ha url":      {env: map[string]string{"HA_TOKEN": "t"}},
		"no token":       {env: map[string]string{"HA_URL": "http://x"}},
		"bad scheme":     {env: map[string]string{"HA_URL": "ftp://x", "HA_TOKEN": "t"}},
		"bad level":      {env: map[string]string{"SUPERVISOR_TOKEN": "t", "LOG_LEVEL": "loud"}},
		"bad options":    {env: map[string]string{"SUPERVISOR_TOKEN": "t"}, files: map[string]string{"/data/options.json": "{"}},
		"no host in url": {env: map[string]string{"HA_URL": "http://", "HA_TOKEN": "t"}},
	}
	for name, tc := range cases {
		if _, err := load(env(tc.env), files(tc.files)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func newSettings(t *testing.T) (*SettingsStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), SettingsFileName)
	return NewSettingsStore(store.Open(path, model.DefaultSettings)), path
}

func TestSettingsDefaultWhenMissing(t *testing.T) {
	s, path := newSettings(t)
	got := s.Settings()
	if got.Rooms.DefaultVolume != 15 || got.Print.Offsets["avery-5260"].YMM != -0.75 {
		t.Fatalf("got %+v", got)
	}
	// Reading must not create the file: HA's git repo would commit it.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("settings.yaml written on read (stat err %v)", err)
	}
}

func TestSettingsFillsMissingSections(t *testing.T) {
	s, path := newSettings(t)
	if err := os.WriteFile(path, []byte("version: 1\nrooms:\n  areas:\n    office: {volume: 10}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := s.Settings()
	if len(got.Players.Default) == 0 || len(got.Readers.Integrations) == 0 || got.Rooms.DefaultVolume != 15 {
		t.Fatalf("defaults not filled: %+v", got)
	}
	if *got.Rooms.Areas["office"].Volume != 10 || len(got.Rooms.Areas) != 1 {
		t.Fatalf("areas: %+v", got.Rooms.Areas)
	}
	if got.Print.Offsets == nil {
		t.Fatal("offsets map is nil")
	}
}

func TestSettingsKeepsLastGoodOnBrokenFile(t *testing.T) {
	s, path := newSettings(t)
	v := model.DefaultSettings()
	v.Rooms.DefaultVolume = 42
	if err := s.Save(v); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("rooms: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().Rooms.DefaultVolume; got != 42 {
		t.Fatalf("default_volume = %d, want last good 42", got)
	}
}

func TestSettingsSaveDoesNotOverwriteBrokenFile(t *testing.T) {
	s, path := newSettings(t)
	_ = s.Settings()
	broken := []byte("rooms: [\n")
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(model.DefaultSettings()); err == nil {
		t.Fatal("Save must fail while settings.yaml is broken")
	}
	if raw, _ := os.ReadFile(path); string(raw) != string(broken) {
		t.Fatal("broken settings.yaml was overwritten")
	}
}

func TestSettingsSaveValidates(t *testing.T) {
	s, _ := newSettings(t)
	vol := func(v int) *int { return &v }
	bad := []func(v *model.Settings){
		func(v *model.Settings) { v.Rooms.DefaultVolume = 101 },
		func(v *model.Settings) { v.Rooms.Areas["office"] = model.AreaSettings{Volume: vol(-1)} },
		func(v *model.Settings) { v.Players.Default = []string{"sonos", "sonos"} },
		func(v *model.Settings) { v.Players.Default = []string{""} },
		func(v *model.Settings) { v.Readers.Integrations = []string{} },
		func(v *model.Settings) { v.Print.BleedMM = -1 },
		func(v *model.Settings) { v.Print.InsetMM = 50 },
		func(v *model.Settings) { v.Print.Offsets["x"] = model.Offset{YMM: 30} },
	}
	for i, mod := range bad {
		v := model.DefaultSettings()
		mod(&v)
		if err := s.Save(v); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: got %v, want ErrInvalid", i, err)
		}
	}
	v := model.DefaultSettings()
	v.Players.Default = []string{"music_assistant"}
	if err := s.Save(v); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().Players.Default; len(got) != 1 || got[0] != "music_assistant" {
		t.Fatalf("not saved: %v", got)
	}
}

// Track B owns Validate; these tests use a stand-in so they do not depend on it.
func fakeTemplateValidate(t labels.Template) error {
	if t.Grid.Cols <= 0 || t.Grid.Rows <= 0 {
		return errors.New("grid must be positive")
	}
	return nil
}

func newTemplates(t *testing.T) (*TemplateStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), TemplatesFileName)
	s, err := NewTemplateStore(store.Open(path, DefaultTemplatesFile))
	if err != nil {
		t.Fatal(err)
	}
	s.validate = fakeTemplateValidate
	return s, path
}

func TestTemplatesListBuiltinsFirst(t *testing.T) {
	s, _ := newTemplates(t)
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 || all[0].ID != "avery-5260" || !all[0].Builtin {
		t.Fatalf("got %+v", all)
	}
}

func TestTemplatesCRUD(t *testing.T) {
	s, path := newTemplates(t)
	base, err := s.Get("avery-5260")
	if err != nil {
		t.Fatal(err)
	}

	// Duplicate a built-in: new id from the name, builtin cleared.
	cp := base
	cp.ID, cp.Name = "", "Avery 5260 (my printer)"
	got, err := s.Create(cp)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "avery-5260-my-printer" || got.Builtin {
		t.Fatalf("got id=%q builtin=%v", got.ID, got.Builtin)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "avery-5260-my-printer") || strings.Contains(string(raw), "builtin: true") {
		t.Fatalf("file content:\n%s", raw)
	}

	// Explicit ids must not clash with built-ins or user items.
	cp.ID = "avery-5260"
	if _, err := s.Create(cp); !errors.Is(err, ErrDuplicate) {
		t.Errorf("builtin id clash: %v", err)
	}
	cp.ID = got.ID
	if _, err := s.Create(cp); !errors.Is(err, ErrDuplicate) {
		t.Errorf("user id clash: %v", err)
	}

	// Validation goes through validate and wraps ErrInvalid.
	bad := got
	bad.Grid.Cols = 0
	if _, err := s.Update(got.ID, bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid update: %v", err)
	}

	upd := got
	upd.Grid.Rows = 9
	upd.Builtin = true // ignored
	if r, err := s.Update(got.ID, upd); err != nil || r.Grid.Rows != 9 || r.Builtin {
		t.Fatalf("update: %+v %v", r, err)
	}

	// Built-ins are read-only.
	if _, err := s.Update("avery-5260", base); !errors.Is(err, ErrReadOnly) {
		t.Errorf("update builtin: %v", err)
	}
	if err := s.Delete("avery-5260"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("delete builtin: %v", err)
	}

	if err := s.Delete(got.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(got.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete: %v", err)
	}
	if err := s.Delete(got.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestTemplatesHandEditedBuiltinIDIsHidden(t *testing.T) {
	s, path := newTemplates(t)
	if err := os.WriteFile(path, []byte("templates:\n  - id: avery-5260\n    name: Shadow\n    builtin: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range all {
		if tpl.Name == "Shadow" {
			t.Fatal("user item shadowing a built-in must be hidden")
		}
	}
}

func TestThemesCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), ThemesFileName)
	s, err := NewThemeStore(store.Open(path, DefaultThemesFile))
	if err != nil {
		t.Fatal(err)
	}
	s.validate = func(b labels.Theme) error {
		if !strings.HasPrefix(b.Background, "#") {
			return errors.New("bad color")
		}
		return nil
	}

	all, _ := s.List()
	if len(all) != 2 || all[0].ID != "white" {
		t.Fatalf("builtins: %+v", all)
	}
	fade := "#112233"
	b, err := s.Create(labels.Theme{Name: "Navy", Background: "#000080", Text: "#FFFFFF", Fade: &fade})
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != "navy" {
		t.Fatalf("id = %q", b.ID)
	}
	if _, err := s.Create(labels.Theme{Name: "Bad", Background: "red"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid: %v", err)
	}
	if _, err := s.Create(labels.Theme{Name: " "}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no name: %v", err)
	}
	b.Text = "#EEEEEE"
	if _, err := s.Update("navy", b); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("navy"); got.Text != "#EEEEEE" || *got.Fade != fade {
		t.Fatalf("got %+v", got)
	}
	if err := s.Delete("white"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("delete builtin: %v", err)
	}
}
