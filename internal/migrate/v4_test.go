package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/igrybkov/ha-nfc-player/internal/store"
)

func TestV4ToV5(t *testing.T) {
	dir := copyDir(t, filepath.Join("testdata", "v4"))
	set := store.OpenSet(dir, store.SetOptions{Current: 5, Files: Files, Steps: Steps[:4]})

	themes := load(t, set, "themes.yaml")
	if !strings.HasPrefix(themes, "version: 5\nthemes:\n") || !strings.Contains(themes, "id: navy") {
		t.Errorf("themes.yaml:\n%s", themes)
	}
	if got := load(t, set, "backgrounds.yaml"); strings.Contains(got, "navy") {
		t.Errorf("backgrounds.yaml still read:\n%s", got)
	}
	tpl := load(t, set, "templates.yaml")
	if strings.Contains(tpl, "layout") || !strings.Contains(tpl, "format: large") {
		t.Errorf("templates.yaml:\n%s", tpl)
	}
	settings := load(t, set, "settings.yaml")
	if strings.Contains(settings, "default_background") || !strings.Contains(settings, "default_theme: navy") {
		t.Errorf("settings.yaml:\n%s", settings)
	}

	// The first write flushes the set and deletes backgrounds.yaml.
	f := store.In(set, "settings.yaml", func() yaml.Node { return yaml.Node{} })
	n, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Save(n); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backgrounds.yaml")); !os.IsNotExist(err) {
		t.Errorf("backgrounds.yaml not deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "themes.yaml")); err != nil {
		t.Errorf("themes.yaml not written: %v", err)
	}
}

func TestV4ToV5Layouts(t *testing.T) {
	dir := t.TempDir()
	const tpls = `version: 4
templates:
  - {id: a, layout: full}
  - {id: b, layout: side}
  - {id: c, layout: odd}
  - {id: d}
`
	if err := os.WriteFile(filepath.Join(dir, "templates.yaml"), []byte(tpls), 0o644); err != nil {
		t.Fatal(err)
	}
	set := store.OpenSet(dir, store.SetOptions{Current: 5, Files: Files, Steps: Steps[:4]})
	got := load(t, set, "templates.yaml")
	for _, want := range []string{"id: a, format: small", "id: b, format: large", "id: c, format: large", "id: d, format: large"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestV4ToV5ThemesWin(t *testing.T) {
	dir := copyDir(t, filepath.Join("testdata", "v4"))
	if err := os.WriteFile(filepath.Join(dir, "themes.yaml"), []byte("version: 5\nthemes:\n  - {id: kept}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set := store.OpenSet(dir, store.SetOptions{Current: 5, Files: Files, Steps: Steps[:4]})
	if got := load(t, set, "themes.yaml"); !strings.Contains(got, "kept") || strings.Contains(got, "navy") {
		t.Errorf("themes.yaml:\n%s", got)
	}
}
