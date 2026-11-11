package labels

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"sync"

	"github.com/igrybkov/ha-nfc-player/assets"
)

// Full font embedding.
//
// gopdf embeds each TrueType font as a subset that keeps only the glyph
// tables (no cmap, OS/2, name or post). An HP Color LaserJet Pro M478f took
// such PDFs and printed 0 pages, with no error. The same PDF with the full
// TTF in each FontFile2 stream printed fine.
//
// gopdf has no option to skip subsetting, and its PDF objects are private, so
// embedFullFonts rewrites gopdf's output instead. The swap is safe because
// gopdf keeps the original glyph ids: the subset has the full font's glyph
// count, and the text codes (Identity-H, CIDToGIDMap /Identity) are original
// glyph ids. embedFullFonts checks the glyph counts match.
//
// It relies on gopdf's output layout: objects 1..N in order, each
// "N 0 obj\n...endobj\n\n", then one classic xref section and a trailer. It
// returns an error on anything else, so a gopdf upgrade that changes the
// layout fails the render instead of printing a broken PDF.

// fontFiles maps the gopdf font alias (the descriptor's /FontName) to the
// embedded asset.
var fontFiles = map[string]string{
	fontRegularAlias: "fonts/NotoSans-Regular.ttf",
	fontBoldAlias:    "fonts/NotoSans-Bold.ttf",
}

// fullFont is one font ready to embed.
type fullFont struct {
	data      []byte // the TTF file
	flate     []byte // data, zlib-compressed; shared, never modify
	numGlyphs int
}

// fullFonts compresses the fonts once per process. They never change.
var fullFonts = sync.OnceValues(func() (map[string]fullFont, error) {
	out := make(map[string]fullFont, len(fontFiles))
	for alias, file := range fontFiles {
		data, err := assets.Fonts.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("font %s: %w", file, err)
		}
		n, err := ttfNumGlyphs(data)
		if err != nil {
			return nil, fmt.Errorf("font %s: %w", file, err)
		}
		var buf bytes.Buffer
		zw, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
		if err != nil {
			return nil, err
		}
		if _, err := zw.Write(data); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		out[alias] = fullFont{data: data, flate: buf.Bytes(), numGlyphs: n}
	}
	return out, nil
})

var errPDFLayout = errors.New("unexpected gopdf output")

func layoutErr(format string, args ...any) error {
	return fmt.Errorf("embed fonts: %w: %s", errPDFLayout, fmt.Sprintf(format, args...))
}

var (
	startxrefRe  = regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n?$`)
	xrefHeadRe   = regexp.MustCompile(`^xref\n0 (\d+)\n`)
	xrefEntryRe  = regexp.MustCompile(`^(\d{10}) (\d{5}) ([fn]) ?\n`)
	sizeRe       = regexp.MustCompile(`/Size (\d+)\n`)
	descriptorRe = regexp.MustCompile(`/Type /FontDescriptor\n`)
	fontFile2Re  = regexp.MustCompile(`/FontFile2 (\d+) 0 R\n`)
	fontNameRe   = regexp.MustCompile(`/FontName /(\S+)\n`)
	fontStreamRe = regexp.MustCompile(`^<</Length (\d+)\n/Filter /FlateDecode\n/Length1 (\d+)\n>>\nstream\n`)
)

// embedFullFonts returns pdf, gopdf's output, with each FontFile2 stream
// replaced by the full font file and the xref rebuilt. Other objects are
// copied byte for byte. fonts maps the descriptor /FontName to the font.
func embedFullFonts(pdf []byte, fonts map[string]fullFont) ([]byte, error) {
	doc, err := splitPDF(pdf)
	if err != nil {
		return nil, err
	}
	objs := doc.objs

	// Find the descriptors and the stream each one points at.
	streamFont := map[int]string{} // FontFile2 object number → alias
	seen := map[string]bool{}
	for i, body := range objs {
		dict := dictOf(body)
		if !descriptorRe.Match(dict) {
			continue
		}
		ref, name := fontFile2Re.FindSubmatch(dict), fontNameRe.FindSubmatch(dict)
		if ref == nil || name == nil {
			return nil, layoutErr("font descriptor %d 0 obj has no /FontFile2 or /FontName", i+1)
		}
		alias := string(name[1])
		if _, ok := fonts[alias]; !ok {
			return nil, layoutErr("font descriptor %d 0 obj: unknown font %q", i+1, alias)
		}
		num, _ := strconv.Atoi(string(ref[1]))
		if num < 1 || num > len(objs) {
			return nil, layoutErr("font descriptor %d 0 obj: /FontFile2 %d 0 R does not exist", i+1, num)
		}
		if seen[alias] {
			return nil, layoutErr("font %q has two descriptors", alias)
		}
		if _, ok := streamFont[num]; ok {
			return nil, layoutErr("two font descriptors share /FontFile2 %d 0 R", num)
		}
		seen[alias] = true
		streamFont[num] = alias
	}
	if len(streamFont) != len(fonts) {
		return nil, layoutErr("found %d font descriptors, want %d", len(streamFont), len(fonts))
	}

	for num, alias := range streamFont {
		f := fonts[alias]
		if err := checkSubset(objs[num-1], f); err != nil {
			return nil, fmt.Errorf("embed fonts: font %q (%d 0 obj): %w", alias, num, err)
		}
		var b bytes.Buffer
		fmt.Fprintf(&b, "<</Length %d\n/Filter /FlateDecode\n/Length1 %d\n>>\nstream\n", len(f.flate), len(f.data))
		b.Write(f.flate)
		b.WriteString("\nendstream\n")
		objs[num-1] = b.Bytes()
	}
	return doc.join(), nil
}

// pdfFile is gopdf output cut into parts.
type pdfFile struct {
	header  []byte   // "%PDF-1.7" and the binary comment line
	objs    [][]byte // objs[i] is the body of object i+1, without "N 0 obj\n" and "endobj\n\n"
	trailer []byte   // from "trailer\n" up to "startxref"
}

// splitPDF cuts gopdf output into a pdfFile. It walks the xref table, so
// binary stream data is never scanned for keywords.
func splitPDF(pdf []byte) (*pdfFile, error) {
	m := startxrefRe.FindSubmatch(pdf)
	if m == nil {
		return nil, layoutErr("no startxref at the end")
	}
	xrefAt, _ := strconv.Atoi(string(m[1]))
	if xrefAt <= 0 || xrefAt >= len(pdf) {
		return nil, layoutErr("startxref %d is outside the file", xrefAt)
	}
	rest := pdf[xrefAt:]
	head := xrefHeadRe.FindSubmatch(rest)
	if head == nil {
		return nil, layoutErr("startxref %d does not point at one xref section from object 0", xrefAt)
	}
	size, _ := strconv.Atoi(string(head[1]))
	if size < 2 {
		return nil, layoutErr("xref has %d entries", size)
	}
	rest = rest[len(head[0]):]
	offsets := make([]int, 0, size)
	for i := range size {
		e := xrefEntryRe.FindSubmatch(rest)
		if e == nil {
			return nil, layoutErr("bad xref entry %d", i)
		}
		want := "00000 n"
		if i == 0 {
			want = "65535 f"
		}
		if string(e[2])+" "+string(e[3]) != want {
			return nil, layoutErr("xref entry %d is not a plain in-use object", i)
		}
		off, _ := strconv.Atoi(string(e[1]))
		offsets = append(offsets, off)
		rest = rest[len(e[0]):]
	}
	if !bytes.HasPrefix(rest, []byte("trailer\n")) {
		return nil, layoutErr("no trailer after the xref")
	}
	end := bytes.Index(rest, []byte("startxref\n"))
	if end < 0 {
		return nil, layoutErr("no startxref after the trailer")
	}
	trailer := rest[:end]
	sz := sizeRe.FindSubmatch(trailer)
	if sz == nil || string(sz[1]) != strconv.Itoa(size) {
		return nil, layoutErr("trailer /Size does not match %d xref entries", size)
	}
	if bytes.Contains(trailer, []byte("/Encrypt")) {
		return nil, layoutErr("encrypted PDF")
	}

	if offsets[1] <= 0 || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, layoutErr("no header before object 1")
	}
	doc := &pdfFile{header: pdf[:offsets[1]], trailer: trailer}
	for i, start := range offsets[1:] {
		end := xrefAt // object i+1 ends where object i+2 (or the xref) starts
		if i+2 < size {
			end = offsets[i+2]
		}
		if end <= start {
			return nil, layoutErr("xref offsets for object %d are out of order", i+1)
		}
		obj := pdf[start:end]
		open := fmt.Sprintf("%d 0 obj\n", i+1)
		const closeObj = "endobj\n\n"
		if !bytes.HasPrefix(obj, []byte(open)) || !bytes.HasSuffix(obj, []byte(closeObj)) {
			return nil, layoutErr("object %d is not \"%d 0 obj ... endobj\" at offset %d", i+1, i+1, start)
		}
		doc.objs = append(doc.objs, obj[len(open):len(obj)-len(closeObj)])
	}
	return doc, nil
}

// join writes the header, the objects and a new xref. It keeps the trailer
// dictionary as is; /Size stays right since the object count is unchanged.
func (d *pdfFile) join() []byte {
	var b bytes.Buffer
	b.Write(d.header)
	offsets := make([]int, len(d.objs))
	for i, body := range d.objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n", i+1)
		b.Write(body)
		b.WriteString("endobj\n\n")
	}
	xrefAt := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(d.objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	b.Write(d.trailer)
	fmt.Fprintf(&b, "startxref\n%d\n%%%%EOF\n", xrefAt)
	return b.Bytes()
}

// dictOf returns the dictionary part of an object body: all of it, or the
// part before "stream" for a stream object.
func dictOf(body []byte) []byte {
	if i := bytes.Index(body, []byte(">>\nstream\n")); i >= 0 {
		return body[:i+2]
	}
	return body
}

// checkSubset checks that body is a gopdf FontFile2 stream whose subset
// keeps the full font's glyph ids (same glyph count), so the full font can
// take its place.
func checkSubset(body []byte, f fullFont) error {
	m := fontStreamRe.FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("%w: not a FlateDecode stream with /Length1", errPDFLayout)
	}
	n, _ := strconv.Atoi(string(m[1]))
	data := body[len(m[0]):]
	if len(data) != n+len("\nendstream\n") || !bytes.HasSuffix(data, []byte("\nendstream\n")) {
		return fmt.Errorf("%w: stream /Length %d does not match its data", errPDFLayout, n)
	}
	zr, err := zlib.NewReader(bytes.NewReader(data[:n]))
	if err != nil {
		return fmt.Errorf("inflate subset: %w", err)
	}
	subset, err := io.ReadAll(zr)
	if err != nil {
		return fmt.Errorf("inflate subset: %w", err)
	}
	got, err := ttfNumGlyphs(subset)
	if err != nil {
		return fmt.Errorf("subset: %w", err)
	}
	if got != f.numGlyphs {
		return fmt.Errorf("subset has %d glyphs, full font %d: gopdf renumbered glyphs, the full font would print wrong glyphs", got, f.numGlyphs)
	}
	return nil
}

// ttfNumGlyphs reads maxp.numGlyphs from a TrueType file.
func ttfNumGlyphs(ttf []byte) (int, error) {
	maxp, err := ttfTable(ttf, "maxp")
	if err != nil {
		return 0, err
	}
	if len(maxp) < 6 {
		return 0, errors.New("short maxp table")
	}
	return int(binary.BigEndian.Uint16(maxp[4:])), nil
}

// ttfTable returns the data of the named table of a TrueType file.
func ttfTable(ttf []byte, tag string) ([]byte, error) {
	if len(ttf) < 12 {
		return nil, errors.New("short TrueType header")
	}
	n := int(binary.BigEndian.Uint16(ttf[4:]))
	for i := range n {
		rec := 12 + 16*i
		if rec+16 > len(ttf) {
			return nil, errors.New("short TrueType table directory")
		}
		if string(ttf[rec:rec+4]) != tag {
			continue
		}
		off, size := int(binary.BigEndian.Uint32(ttf[rec+8:])), int(binary.BigEndian.Uint32(ttf[rec+12:]))
		if off < 0 || size < 0 || off+size > len(ttf) {
			return nil, fmt.Errorf("table %s is outside the file", tag)
		}
		return ttf[off : off+size], nil
	}
	return nil, fmt.Errorf("no %s table", tag)
}
