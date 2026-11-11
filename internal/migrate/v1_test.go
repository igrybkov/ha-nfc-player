package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// openV2 opens dir with the v1→v2 step, whatever Current is now.
func openV2(dir string) *store.Set {
	return store.OpenSet(dir, store.SetOptions{
		Current: 2,
		Files:   []string{"tags.yaml", "pools.yaml", "settings.yaml", "templates.yaml", "backgrounds.yaml", "records.yaml"},
		Steps:   []store.Step{v1to2},
	})
}

func load(t *testing.T, set *store.Set, name string) string {
	t.Helper()
	n, err := store.In(set, name, func() yaml.Node { return yaml.Node{} }).Load()
	if err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(&n)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestV1ToV2(t *testing.T) {
	set := openV2(copyDir(t, filepath.Join("testdata", "v1to2")))
	got := load(t, set, "records.yaml")
	want, err := os.ReadFile(filepath.Join("testdata", "v1to2.golden.yaml"))
	if os.Getenv("UPDATE_GOLDEN") != "" {
		_ = os.WriteFile(filepath.Join("testdata", "v1to2.golden.yaml"), []byte(got), 0o644)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("records.yaml differs from the golden file; run with UPDATE_GOLDEN=1 and review the diff\n%s", got)
	}
	if tpl := load(t, set, "templates.yaml"); !strings.HasPrefix(tpl, "version: 2\ntemplates:\n") || !strings.Contains(tpl, "- id: avery") {
		t.Errorf("templates.yaml = %q", tpl)
	}
}

func TestV1ToV2RecordsFileWins(t *testing.T) {
	dir := copyDir(t, filepath.Join("testdata", "v1to2"))
	if err := os.WriteFile(filepath.Join(dir, "records.yaml"), []byte("version: 2\nrecords: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := load(t, openV2(dir), "records.yaml"); got != "version: 2\nrecords: []\n" {
		t.Errorf("records.yaml = %q", got)
	}
}
