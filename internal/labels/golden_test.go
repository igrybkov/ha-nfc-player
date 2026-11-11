package labels_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// goldenCase is one job for the geometry check against the Python script
// (docs/reference/golden.py). The script runs with the same values.
type goldenCase struct {
	Name       string  `json:"name"`
	Template   string  `json:"template"`   // built-in template id
	Script     string  `json:"script"`     // script --template value
	Background string  `json:"background"` // built-in background id, script --background
	StartRow   int     `json:"start_row"`
	StartCol   int     `json:"start_col"`
	OffsetX    float64 `json:"offset_x"`
	OffsetY    float64 `json:"offset_y"`
}

var goldenCases = []goldenCase{
	{"5260-black", "avery-5260", "5260", "black", 2, 2, 0, -0.75},
	{"5434-black", "avery-5434", "5434", "black", 2, 2, 0, -0.75},
	{"5260-white", "avery-5260", "5260", "white", 2, 2, 0, -0.75},
	{"5434-white", "avery-5434", "5434", "white", 2, 2, 0, -0.75},
	{"5434-black-shift", "avery-5434", "5434", "black", 1, 1, 0.5, 0.5},
}

// goldenItems are the labels of every case: name and solid art color.
var goldenItems = []struct {
	Name string     `json:"name"`
	Art  color.RGBA `json:"-"`
	RGB  string     `json:"rgb"` // Art as hex, for the raster check
	File string     `json:"file"`
}{
	{"Alpha — One", color.RGBA{220, 30, 30, 255}, "DC1E1E", "red.jpg"},
	{"Beta — Two", color.RGBA{30, 180, 30, 255}, "1EB41E", "green.jpg"},
	{"Гамма — Три", color.RGBA{30, 60, 220, 255}, "1E3CDC", "blue.jpg"},
}

// Print values shared with the script (its defaults, passed explicitly).
const goldenInsetMM, goldenBleedMM, goldenEdgeBleedMM = 1.5, 1.5, 0.4

// TestGoldenRender writes the Go side of the geometry check: the art JPEGs,
// one PDF per case and cases.json for docs/reference/golden.py. It runs only
// with GOLDEN_DIR set:
//
//	GOLDEN_DIR=/tmp/golden go test ./internal/labels -run TestGoldenRender
func TestGoldenRender(t *testing.T) {
	dir := os.Getenv("GOLDEN_DIR")
	if dir == "" {
		t.Skip("GOLDEN_DIR not set")
	}
	var items []labels.Item
	for _, it := range goldenItems {
		img := image.NewRGBA(image.Rect(0, 0, 600, 600))
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = it.Art.R, it.Art.G, it.Art.B, 255
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, it.File), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		items = append(items, labels.ItemFromSubject(model.Subject{Name: it.Name}, img))
	}
	for _, c := range goldenCases {
		job := labels.Job{
			Template: builtinTemplate(t, c.Template),
			Theme:    builtinTheme(t, c.Background),
			Items:    items,
			StartRow: c.StartRow, StartCol: c.StartCol,
			Offset:  model.Offset{XMM: c.OffsetX, YMM: c.OffsetY},
			InsetMM: goldenInsetMM, BleedMM: goldenBleedMM, EdgeBleedMM: goldenEdgeBleedMM,
		}
		var buf bytes.Buffer
		if err := labels.Render(&buf, job); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.Name+"-go.pdf"), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.MarshalIndent(map[string]any{
		"cases": goldenCases, "items": goldenItems,
		"inset_mm": goldenInsetMM, "bleed_mm": goldenBleedMM, "edge_bleed_mm": goldenEdgeBleedMM,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
