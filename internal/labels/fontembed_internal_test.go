package labels

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// rawGopdf returns gopdf's own output (fonts not yet swapped) for one page
// with text in both fonts.
func rawGopdf(t *testing.T) []byte {
	t.Helper()
	c, err := newPDFCanvas(avery5260.Page)
	if err != nil {
		t.Fatal(err)
	}
	c.pdf.AddPage()
	for _, style := range []FontStyle{Regular, Bold} {
		if err := c.SetFont(style, 11); err != nil {
			t.Fatal(err)
		}
		if err := c.Text(72, 72, "Metallica — Володимир"); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if _, err := c.pdf.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testFonts(t *testing.T) map[string]fullFont {
	t.Helper()
	fonts, err := fullFonts()
	if err != nil {
		t.Fatal(err)
	}
	return fonts
}

// replaceOnce replaces the first match of re in a copy of b.
func replaceOnce(t *testing.T, b []byte, re string, repl func(m []byte) []byte) []byte {
	t.Helper()
	loc := regexp.MustCompile(re).FindIndex(b)
	if loc == nil {
		t.Fatalf("no %q in the PDF", re)
	}
	out := append([]byte{}, b[:loc[0]]...)
	out = append(out, repl(b[loc[0]:loc[1]])...)
	return append(out, b[loc[1]:]...)
}

// bumpNumber adds d to the last number in m, keeping its width.
func bumpNumber(d int) func(m []byte) []byte {
	return func(m []byte) []byte {
		loc := regexp.MustCompile(`\d+`).FindAllIndex(m, -1)
		last := loc[len(loc)-1]
		n, _ := strconv.Atoi(string(m[last[0]:last[1]]))
		num := fmt.Sprintf("%0*d", last[1]-last[0], n+d)
		return append(append(append([]byte{}, m[:last[0]]...), num...), m[last[1]:]...)
	}
}

func literal(s string) func([]byte) []byte { return func([]byte) []byte { return []byte(s) } }

func TestEmbedFullFontsRawRoundTrip(t *testing.T) {
	raw := rawGopdf(t)
	out, err := embedFullFonts(raw, testFonts(t))
	if err != nil {
		t.Fatal(err)
	}
	// Running it again finds full fonts with the same glyph counts and
	// changes nothing: the output parses like gopdf's own.
	again, err := embedFullFonts(out, testFonts(t))
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if !bytes.Equal(out, again) {
		t.Error("second pass changed the PDF")
	}
	// Objects other than the two font files are unchanged.
	before, err := splitPDF(raw)
	if err != nil {
		t.Fatal(err)
	}
	after, err := splitPDF(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.objs) != len(after.objs) || !bytes.Equal(before.header, after.header) || !bytes.Equal(before.trailer, after.trailer) {
		t.Fatal("header, trailer or object count changed")
	}
	changed := 0
	for i := range before.objs {
		if !bytes.Equal(before.objs[i], after.objs[i]) {
			changed++
		}
	}
	if changed != 2 {
		t.Errorf("%d objects changed, want 2 (the font files)", changed)
	}
}

func TestEmbedFullFontsRejectsMalformed(t *testing.T) {
	raw := rawGopdf(t)
	fonts := testFonts(t)
	xrefEntry := func(obj int) string { // the nth in-use xref line
		return fmt.Sprintf(`xref\n0 \d+\n(?:\d{10} \d{5} [fn] \n){%d}\d{10}`, obj)
	}
	cases := []struct {
		name   string
		pdf    []byte
		fonts  map[string]fullFont
		errHas string
	}{
		{"empty", nil, fonts, "no startxref"},
		{"not a pdf", []byte("hello\n"), fonts, "no startxref"},
		{"truncated", raw[:len(raw)/2], fonts, "no startxref"},
		{"no trailing EOF", raw[:len(raw)-6], fonts, "no startxref"},
		{"startxref off by one", replaceOnce(t, raw, `startxref\n\d+`, bumpNumber(-1)), fonts, "does not point at one xref section"},
		{"xref offset off by one", replaceOnce(t, raw, xrefEntry(3), bumpNumber(1)), fonts, `is not "2 0 obj ... endobj"`}, // object 2 now ends mid-object 3
		{"xref offsets swapped", swapXref(t, raw, 2, 3), fonts, "out of order"},
		{"wrong /Size", replaceOnce(t, raw, `/Size \d+`, bumpNumber(1)), fonts, "/Size does not match"},
		{"encrypted", replaceOnce(t, raw, `/Root 1 0 R\n`, literal("/Root 1 0 R\n/Encrypt 2 0 R\n")), fonts, "encrypted"},
		{"object header", replaceOnce(t, raw, `\n3 0 obj\n`, literal("\n3 1 obj\n")), fonts, `is not "3 0 obj ... endobj"`},
		{"unknown font", replaceOnce(t, raw, `/FontName /bold\n`, literal("/FontName /bolt\n")), fonts, `unknown font "bolt"`},
		{"FontFile2 out of range", replaceOnce(t, raw, `/FontFile2 \d\d 0 R`, literal("/FontFile2 99 0 R")), fonts, "does not exist"},
		{"font stream not flate", replaceOnce(t, raw, `/FlateDecode\n/Length1`, literal("/LZWDecode  \n/Length1")), fonts, "not a FlateDecode stream"},
		{"missing font", raw, withFont(fonts, "italic", fonts[fontRegularAlias]), "found 2 font descriptors, want 3"},
		{"glyph ids renumbered", raw, withGlyphs(fonts, fontBoldAlias, 10), "renumbered glyphs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := embedFullFonts(c.pdf, c.fonts)
			if err == nil {
				t.Fatalf("no error; wrote %d bytes", len(out))
			}
			if !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("err = %v, want it to say %q", err, c.errHas)
			}
			if c.errHas != "renumbered glyphs" && !errors.Is(err, errPDFLayout) {
				t.Errorf("err = %v, want errPDFLayout", err)
			}
			if out != nil {
				t.Error("returned a PDF with the error")
			}
		})
	}
}

// swapXref swaps the xref offsets of objects a and b.
func swapXref(t *testing.T, pdf []byte, a, b int) []byte {
	t.Helper()
	xref := bytes.LastIndex(pdf, []byte("xref\n0 "))
	if xref < 0 {
		t.Fatal("no xref")
	}
	first := xref + 5 + bytes.IndexByte(pdf[xref+5:], '\n') + 1 // entry 0, after "xref\n0 N\n"
	out := append([]byte{}, pdf...)
	ea, eb := first+20*a, first+20*b
	copy(out[ea:ea+10], pdf[eb:eb+10])
	copy(out[eb:eb+10], pdf[ea:ea+10])
	return out
}

func withFont(fonts map[string]fullFont, alias string, f fullFont) map[string]fullFont {
	out := maps.Clone(fonts)
	out[alias] = f
	return out
}

func withGlyphs(fonts map[string]fullFont, alias string, n int) map[string]fullFont {
	f := fonts[alias]
	f.numGlyphs = n
	return withFont(fonts, alias, f)
}
