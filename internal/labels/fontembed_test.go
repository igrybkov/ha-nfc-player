package labels_test

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"regexp"
	"strconv"
	"testing"

	"github.com/igrybkov/ha-nfc-player/assets"
	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// This file checks Render's output with its own small PDF reader, apart
// from the code in fontembed.go, so a bug there can't hide itself.

var (
	startxrefAt   = regexp.MustCompile(`startxref\s+(\d+)\s+%%EOF\s*$`)
	xrefSection   = regexp.MustCompile(`^xref\s+0 (\d+)\s+`)
	trailerSize   = regexp.MustCompile(`trailer\s*<<[^>]*/Size (\d+)`)
	descriptorRef = regexp.MustCompile(`/Type /FontDescriptor\n(?:[^\n]*\n)*?/FontFile2 (\d+) 0 R\n/FontName /(\w+)\n`)
	streamHead    = regexp.MustCompile(`^\d+ 0 obj\n<</Length (\d+)\n/Filter /FlateDecode\n/Length1 (\d+)\n>>\nstream\n`)
)

// fontFiles maps the descriptor /FontName to the font asset (pdf.go aliases).
var fontFiles = map[string]string{
	"regular": "fonts/NotoSans-Regular.ttf",
	"bold":    "fonts/NotoSans-Bold.ttf",
}

// xrefOffsets checks the xref, startxref and /Size, and returns the offset
// of each object (index = object number; 0 is the free entry).
func xrefOffsets(t *testing.T, data []byte) []int {
	t.Helper()
	m := startxrefAt.FindSubmatch(data)
	if m == nil {
		t.Fatal("no startxref")
	}
	xref, _ := strconv.Atoi(string(m[1]))
	if xref >= len(data) || !bytes.HasPrefix(data[xref:], []byte("xref")) {
		t.Fatalf("startxref %d does not point at xref", xref)
	}
	head := xrefSection.FindSubmatch(data[xref:])
	if head == nil {
		t.Fatal("bad xref section header")
	}
	n, _ := strconv.Atoi(string(head[1]))
	entries := data[xref+len(head[0]):]
	offsets := make([]int, n)
	for i := range n {
		e := entries[20*i : 20*i+20] // fixed-width entries
		off, err := strconv.Atoi(string(e[:10]))
		if err != nil {
			t.Fatalf("xref entry %d: %q", i, e)
		}
		offsets[i] = off
		if i == 0 {
			continue
		}
		want := strconv.Itoa(i) + " 0 obj"
		if off >= len(data) || !bytes.HasPrefix(data[off:], []byte(want)) {
			t.Errorf("xref entry %d (offset %d) does not point at %q", i, off, want)
		}
	}
	sz := trailerSize.FindSubmatch(data[xref:])
	if sz == nil {
		t.Fatal("no /Size in the trailer")
	}
	if got, _ := strconv.Atoi(string(sz[1])); got != n {
		t.Errorf("/Size = %d, want %d (xref entries)", got, n)
	}
	return offsets
}

// checkFullFonts checks the xref and that each FontFile2 stream holds the
// complete font file its descriptor names.
func checkFullFonts(t *testing.T, data []byte) {
	t.Helper()
	offsets := xrefOffsets(t, data)
	found := map[string]bool{}
	for _, d := range descriptorRef.FindAllSubmatch(data, -1) {
		num, _ := strconv.Atoi(string(d[1]))
		name := string(d[2])
		file, ok := fontFiles[name]
		if !ok {
			t.Errorf("descriptor names unknown font %q", name)
			continue
		}
		found[name] = true
		if num >= len(offsets) {
			t.Fatalf("%s: /FontFile2 %d 0 R is not in the xref", name, num)
		}
		obj := data[offsets[num]:]
		h := streamHead.FindSubmatch(obj)
		if h == nil {
			t.Fatalf("%s: object %d is not a FlateDecode stream: %.80q", name, num, obj)
		}
		length, _ := strconv.Atoi(string(h[1]))
		length1, _ := strconv.Atoi(string(h[2]))
		stream := obj[len(h[0]):]
		if !bytes.HasPrefix(stream[length:], []byte("\nendstream")) {
			t.Fatalf("%s: /Length %d does not end at endstream", name, length)
		}
		zr, err := zlib.NewReader(bytes.NewReader(stream[:length]))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, err := assets.Fonts.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: FontFile2 is %d bytes, not the %d-byte %s", name, len(got), len(want), file)
		}
		if length1 != len(want) {
			t.Errorf("%s: /Length1 = %d, want %d", name, length1, len(want))
		}
		tables := ttfTables(t, got)
		for _, tag := range []string{"cmap", "OS/2", "name", "post", "glyf", "loca", "head", "hhea", "hmtx", "maxp"} {
			if !tables[tag] {
				t.Errorf("%s: embedded font has no %s table", name, tag)
			}
		}
	}
	for name := range fontFiles {
		if !found[name] {
			t.Errorf("no font descriptor for %q", name)
		}
	}
}

// ttfTables reads the tag of each table in a TrueType table directory.
func ttfTables(t *testing.T, ttf []byte) map[string]bool {
	t.Helper()
	if len(ttf) < 12 {
		t.Fatal("short TrueType file")
	}
	n := int(binary.BigEndian.Uint16(ttf[4:6]))
	out := map[string]bool{}
	for i := range n {
		rec := 12 + 16*i
		if rec+16 > len(ttf) {
			t.Fatal("short table directory")
		}
		out[string(ttf[rec:rec+4])] = true
	}
	return out
}

// A typical job: four labels with art on one sheet.
func TestRenderEmbedsFullFonts(t *testing.T) {
	settings := model.DefaultSettings().Print
	tpl := builtinTemplate(t, "avery-5260")
	for _, bg := range []string{"white", "black"} {
		t.Run(bg, func(t *testing.T) {
			job := labels.Job{
				Template:    tpl,
				Background:  builtinBackground(t, bg),
				Items:       sampleItems()[:4],
				Offset:      settings.Offsets[tpl.ID],
				InsetMM:     settings.InsetMM,
				BleedMM:     settings.BleedMM,
				EdgeBleedMM: settings.EdgeBleedMM,
			}
			data := writePDF(t, t.TempDir(), "four-"+bg+".pdf", job)
			checkPDF(t, data, 1) // checks the fonts
		})
	}
}
