package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// Every version has fixtures, and each loads at Current. A new version
// without fixtures fails here.
func TestFixturesOfEveryVersion(t *testing.T) {
	for v := 1; v <= Current; v++ {
		dir := filepath.Join("testdata", fmt.Sprintf("v%d", v))
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 {
			t.Fatalf("version %d has no fixtures in %s: %v", v, dir, err)
		}
		set := Open(copyDir(t, dir), nil)
		for _, name := range Files {
			if _, err := store.In(set, name, func() yaml.Node { return yaml.Node{} }).Load(); err != nil {
				t.Errorf("v%d %s: %v", v, name, err)
			}
		}
	}
}

// copyDir copies fixtures to a temp dir, so a test that writes can't
// change them.
func copyDir(t *testing.T, dir string) string {
	t.Helper()
	out := t.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, e.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
