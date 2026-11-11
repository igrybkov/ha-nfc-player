package labels_test

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
)

// TestPreview renders every format and placement with each sample cover.
// With PREVIEW_DIR set it also writes the PNGs there, to look at.
func TestPreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	for _, f := range labels.FormatList {
		size, fonts := f.Reference()
		tpl := labels.Template{ID: string(f), Label: size, Format: f, Font: fonts}
		for _, a := range labels.ArtPlacements {
			for _, cover := range append([]string{""}, labels.SampleCovers...) {
				th := builtinTheme(t, "black")
				th.Formats = labels.Formats{Small: labels.Style{Art: a}, Large: labels.Style{Art: a}, Dot: labels.Style{Art: a, Mono: cover == "busy"}}
				item := labels.Item{Title: "The Very Long Artist Name", Subtitle: "An Album Title That Wraps"}
				if cover != "" {
					img, err := labels.SampleCover(cover)
					if err != nil {
						t.Fatal(err)
					}
					item.Art = img
				}
				var buf bytes.Buffer
				if err := labels.RenderPreview(&buf, labels.Preview{Template: tpl, Theme: th, Item: item, InsetMM: 1.5}); err != nil {
					t.Fatalf("%s %s %s: %v", f, a, cover, err)
				}
				img, err := png.Decode(bytes.NewReader(buf.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				if want := int(size.WidthIn * labels.DefaultPreviewPxPerIn); img.Bounds().Dx() != want {
					t.Errorf("%s: width %d px, want %d", f, img.Bounds().Dx(), want)
				}
				if dir != "" {
					name := string(f) + "-" + string(a) + "-" + cover + ".png"
					if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}
