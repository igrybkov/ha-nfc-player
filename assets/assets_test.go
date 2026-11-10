package assets

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

func TestFontsCoverCyrillic(t *testing.T) {
	const sample = "Володимир Івасюк ґєїі ҐЄЇІ — Metallica"
	for _, name := range []string{"fonts/NotoSans-Regular.ttf", "fonts/NotoSans-Bold.ttf"} {
		data, err := Fonts.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := sfnt.Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var buf sfnt.Buffer
		for _, r := range sample {
			idx, err := f.GlyphIndex(&buf, r)
			if err != nil {
				t.Fatalf("%s: %q: %v", name, r, err)
			}
			if idx == 0 {
				t.Errorf("%s: no glyph for %q", name, r)
			}
		}
	}
}
