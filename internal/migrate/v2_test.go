package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/store"
)

func TestV2ToV3(t *testing.T) {
	set := store.OpenSet(copyDir(t, filepath.Join("testdata", "v2")), store.SetOptions{
		Current: 3, Files: Files, Steps: []store.Step{v1to2, v2to3},
	})
	got := load(t, set, "records.yaml")
	if os.Getenv("WRITE_V3") != "" {
		_ = os.WriteFile(filepath.Join("testdata", "v3", "records.yaml"), []byte(got), 0o644)
	}
	switch {
	case !strings.HasPrefix(got, "version: 3\n"):
		t.Errorf("version: %q", got[:20])
	case strings.Contains(got, "others"):
		t.Error("others item kept")
	case strings.Contains(got, "volume:"), !strings.Contains(got, "volume_offset: null"):
		t.Errorf("volume not converted:\n%s", got)
	}
}
