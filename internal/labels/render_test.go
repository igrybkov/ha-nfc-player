package labels_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
	_ "github.com/igrybkov/ha-nfc-player/internal/labels/layouts"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

func builtinTemplate(t *testing.T, id string) labels.Template {
	t.Helper()
	ts, err := labels.BuiltinTemplates()
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range ts {
		if tpl.ID == id {
			return tpl
		}
	}
	t.Fatalf("no builtin template %q", id)
	return labels.Template{}
}

func builtinBackground(t *testing.T, id string) labels.Background {
	t.Helper()
	bs, err := labels.BuiltinBackgrounds()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("no builtin background %q", id)
	return labels.Background{}
}

func TestBuiltinsValidate(t *testing.T) {
	ts, err := labels.BuiltinTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if err := labels.ValidateTemplates(ts); err != nil {
		t.Error(err)
	}
	bs, err := labels.BuiltinBackgrounds()
	if err != nil {
		t.Fatal(err)
	}
	if err := labels.ValidateBackgrounds(bs); err != nil {
		t.Error(err)
	}
	for _, l := range []string{"side", "full"} {
		if _, ok := labels.LookupLayout(l); !ok {
			t.Errorf("layout %q not registered", l)
		}
	}
}

func TestTemplateValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*labels.Template)
		errHas string
	}{
		{"grid too wide", func(tp *labels.Template) { tp.Grid.Cols = 4 }, "wide"},
		{"grid too tall", func(tp *labels.Template) { tp.Margin.TopIn = 1.01 }, "tall"},
		{"zero label width", func(tp *labels.Template) { tp.Label.WidthIn = 0 }, "label.width_in"},
		{"zero rows", func(tp *labels.Template) { tp.Grid.Rows = 0 }, "grid.rows"},
		{"negative gap", func(tp *labels.Template) { tp.Gap.ColIn = -0.1 }, "gap.col_in"},
		{"zero font", func(tp *labels.Template) { tp.Font.SubtitlePt = 0 }, "font.subtitle_pt"},
		{"unknown layout", func(tp *labels.Template) { tp.Layout = "nope" }, "not registered"},
		{"no id", func(tp *labels.Template) { tp.ID = "" }, "id is required"},
		{"too many slots", func(tp *labels.Template) { tp.Grid.Cols, tp.Grid.Rows = 40, 26 }, "grid.cols*grid.rows"},
		{"overflowing grid", func(tp *labels.Template) { tp.Grid.Cols, tp.Grid.Rows = 1<<40, 1<<40 }, "grid.cols*grid.rows"},
		{"huge page", func(tp *labels.Template) { tp.Page.HeightIn = 50.5 }, "page.height_in must be <= 50"},
		{"huge label", func(tp *labels.Template) { tp.Label.WidthIn = 51 }, "label.width_in must be <= 50"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tpl := builtinTemplate(t, "avery-5260")
			c.mutate(&tpl)
			err := tpl.Validate()
			if err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("Validate() = %v, want error containing %q", err, c.errHas)
			}
		})
	}

	// 5260 fills the letter page exactly across: 3 × 2.625 + 2 × 0.125 +
	// 2 × 0.1875 = 8.5. That must stay valid despite float error.
	if err := builtinTemplate(t, "avery-5260").Validate(); err != nil {
		t.Error(err)
	}
	// The caps themselves are allowed.
	big := labels.Template{ID: "big", Page: labels.Size{WidthIn: 50, HeightIn: 50}, Label: labels.Size{WidthIn: 0.5, HeightIn: 1},
		Grid: labels.Grid{Cols: 100, Rows: 10}, Layout: "side", Font: labels.FontSizes{TitlePt: 8, SubtitlePt: 6}}
	if err := big.Validate(); err != nil {
		t.Errorf("1000 slots on a 50 in page: %v", err)
	}
}

func TestValidateDuplicateIDs(t *testing.T) {
	tpl := builtinTemplate(t, "avery-5260")
	user := tpl
	user.Builtin = false
	if err := labels.ValidateTemplates([]labels.Template{tpl, user}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate template ids: %v", err)
	}
	bg := builtinBackground(t, "black")
	if err := labels.ValidateBackgrounds([]labels.Background{bg, bg}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate background ids: %v", err)
	}
}

func TestBackgroundValidate(t *testing.T) {
	bad := "red"
	cases := []labels.Background{
		{ID: "", Background: "#000000", Text: "#FFFFFF"},
		{ID: "x", Background: "black", Text: "#FFFFFF"},
		{ID: "x", Background: "#000000", Text: "#FFF"},
		{ID: "x", Background: "#000000", Text: "#FFFFFF", Fade: &bad},
		{ID: "x", Background: "#000000", Text: "#FFFFFF", FadeText: &bad},
	}
	for _, b := range cases {
		if err := b.Validate(); err == nil {
			t.Errorf("Validate(%+v) accepted", b)
		}
	}
}

// testArt is a square with a diagonal gradient, so crops and fades show.
func testArt(seed uint8) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for y := range 300 {
		for x := range 300 {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / 299), uint8(y * 255 / 299), seed, 255})
		}
	}
	return img
}

// decodedJPEGSquare mimics artwork.Cache output: a JPEG decoded to
// *image.YCbCr and center-cropped to a square SubImage with non-zero bounds.
func decodedJPEGSquare() image.Image {
	src := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := range 300 {
		for x := range 400 {
			src.SetRGBA(x, y, color.RGBA{uint8(y * 255 / 299), 80, uint8(x * 255 / 399), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		panic(err)
	}
	img, err := jpeg.Decode(&buf)
	if err != nil {
		panic(err)
	}
	return img.(*image.YCbCr).SubImage(image.Rect(50, 0, 350, 300))
}

var pageObject = regexp.MustCompile(`/Type\s*/Page[^s]`)

// checkPDF does a light structural check (no PDF parser dependency): header,
// trailer, and the number of page objects.
func checkPDF(t *testing.T, data []byte, wantPages int) {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("missing %%PDF- header")
	}
	if !bytes.Contains(data[max(0, len(data)-64):], []byte("%%EOF")) {
		t.Fatalf("missing %%%%EOF trailer")
	}
	if !bytes.Contains(data, []byte("startxref")) {
		t.Fatalf("missing startxref")
	}
	if got := len(pageObject.FindAll(data, -1)); got != wantPages {
		t.Errorf("pages = %d, want %d", got, wantPages)
	}
}

// writePDF renders the job into dir and, when LABELS_PREVIEW_DIR is set,
// also there for a visual check.
func writePDF(t *testing.T, dir, name string, job labels.Job) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := labels.Render(&buf, job); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if preview := os.Getenv("LABELS_PREVIEW_DIR"); preview != "" {
		if err := os.WriteFile(filepath.Join(preview, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func sampleItems() []labels.Item {
	str := func(s string) *string { return &s }
	entries := []model.Entry{
		{Name: "Metallica — Essential"},
		{Name: "Volodymyr Ivasiuk", Label: model.LabelOverrides{Title: str("Володимир Івасюк")}},
		{Name: "Queen — Bohemian Rhapsody (The Original Soundtrack)"},
		{Name: "Creedence Clearwater Revival — Essential"},
		{Name: "Ray Charles — The Ultimate Collection"},
		{Name: "Godspeed You! Black Emperor — Lift Your Skinny Fists Like Antennas to Heaven, Remastered Deluxe Anniversary Edition"},
	}
	out := make([]labels.Item, len(entries))
	for i, e := range entries {
		var art image.Image
		switch i {
		case 3: // one label with no art: text-only path
		case 5: // real art: a decoded JPEG (YCbCr) square SubImage, as artwork.Cache returns
			art = decodedJPEGSquare()
		default:
			art = testArt(uint8(i * 50))
		}
		out[i] = labels.ItemFromEntry(e, art)
	}
	return out
}

func TestRenderBuiltins(t *testing.T) {
	dir := t.TempDir()
	settings := model.DefaultSettings().Print
	cases := []struct {
		template, background string
		startRow, startCol   int
		copies               int
		wantPages            int
	}{
		{"avery-5260", "white", 1, 1, 1, 1},
		{"avery-5260", "black", 2, 2, 1, 1},
		{"avery-5260", "black", 10, 1, 1, 2}, // 3 slots left on page 1
		{"avery-5434", "white", 1, 1, 1, 1},
		{"avery-5434", "black", 2, 2, 2, 2}, // 10 labels from slot 3 of 10
	}
	for _, c := range cases {
		name := fmt.Sprintf("%s-%s-r%dc%d-x%d.pdf", c.template, c.background, c.startRow, c.startCol, c.copies)
		t.Run(name, func(t *testing.T) {
			tpl := builtinTemplate(t, c.template)
			job := labels.Job{
				Template:    tpl,
				Background:  builtinBackground(t, c.background),
				Items:       sampleItems(),
				Copies:      c.copies,
				StartRow:    c.startRow,
				StartCol:    c.startCol,
				Offset:      settings.Offsets[tpl.ID],
				InsetMM:     settings.InsetMM,
				BleedMM:     settings.BleedMM,
				EdgeBleedMM: settings.EdgeBleedMM,
			}
			data := writePDF(t, dir, name, job)
			checkPDF(t, data, c.wantPages)
			if got := len(labels.SheetMap(tpl, job)); got != c.wantPages {
				t.Errorf("SheetMap pages = %d, want %d", got, c.wantPages)
			}
		})
	}
}

func TestRenderOutlinePreview(t *testing.T) {
	job := labels.Job{
		Template:   builtinTemplate(t, "avery-5434"),
		Background: builtinBackground(t, "white"),
		Outline:    true,
	}
	checkPDF(t, writePDF(t, t.TempDir(), "outline-5434.pdf", job), 1)
}

func TestRenderSharesArtAcrossCopies(t *testing.T) {
	base := labels.Job{
		Template:   builtinTemplate(t, "avery-5260"),
		Background: builtinBackground(t, "white"),
		Items:      sampleItems()[:1],
		InsetMM:    1.5,
	}
	var one, many bytes.Buffer
	if err := labels.Render(&one, base); err != nil {
		t.Fatal(err)
	}
	base.Copies = 10
	if err := labels.Render(&many, base); err != nil {
		t.Fatal(err)
	}
	// Ten copies reuse one image object; the file grows by far less than
	// one image per copy.
	if many.Len() > one.Len()*2 {
		t.Errorf("10 copies = %d bytes vs %d for one; art is not shared", many.Len(), one.Len())
	}
}

func TestRenderRejectsBadJobs(t *testing.T) {
	good := labels.Job{
		Template:   builtinTemplate(t, "avery-5260"),
		Background: builtinBackground(t, "white"),
		Items:      sampleItems()[:1],
	}
	cases := map[string]func(*labels.Job){
		"no items":          func(j *labels.Job) { j.Items = nil },
		"start out of grid": func(j *labels.Job) { j.StartRow = 11 },
		"inset eats label":  func(j *labels.Job) { j.InsetMM = 13 },
		"bad background":    func(j *labels.Job) { j.Background.Text = "white" },
		"bad template":      func(j *labels.Job) { j.Template.Layout = "nope" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := good
			mutate(&j)
			if err := labels.Render(&bytes.Buffer{}, j); err == nil {
				t.Error("Render accepted a bad job")
			}
		})
	}
}

func TestBuiltinBackgroundFadeColors(t *testing.T) {
	black, white := color.RGBA{0, 0, 0, 255}, color.RGBA{255, 255, 255, 255}
	for _, id := range []string{"white", "black"} {
		b := builtinBackground(t, id)
		if b.Fade == nil || b.FadeText == nil {
			t.Errorf("%s: fade and fade_text must be explicit", id)
		}
		if b.FadeColor() != black || b.FadeTextColor() != white {
			t.Errorf("%s: fade %v, fade text %v; want black under white (script look)", id, b.FadeColor(), b.FadeTextColor())
		}
	}
}
