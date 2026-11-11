package labels

import (
	"image"
	"image/color"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// avery5260 matches builtin/templates.yaml; internal tests can't import
// layouts, so they don't go through Validate.
var avery5260 = Template{
	ID: "avery-5260", Page: Size{8.5, 11}, Label: Size{2.625, 1},
	Grid: Grid{Cols: 3, Rows: 10}, Margin: Margin{0.1875, 0.5}, Gap: Gap{0.125, 0},
	Format: "large", Font: FontSizes{11, 8.5},
}

func items(names ...string) []Item {
	out := make([]Item, len(names))
	for i, n := range names {
		out[i] = Item{Title: n}
	}
	return out
}

func TestSkipSlots(t *testing.T) {
	cases := []struct {
		name                 string
		row, col, skip, want int
	}{
		{"zero means first slot", 0, 0, 0, 0},
		{"first slot", 1, 1, 0, 0},
		{"row 2 col 2", 2, 2, 0, 4},
		{"start plus skip add up", 1, 2, 3, 4},
		{"last slot", 10, 3, 0, 29},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := Job{StartRow: c.row, StartCol: c.col, Skip: c.skip}
			if got := j.skipSlots(avery5260); got != c.want {
				t.Errorf("skipSlots = %d, want %d", got, c.want)
			}
		})
	}
}

func TestPlaceFlowAndCopies(t *testing.T) {
	j := Job{Items: items("A", "B"), Copies: 2, StartRow: 1, StartCol: 2}
	got := place(avery5260, j)
	type pos struct {
		page, row, col int
		title          string
	}
	want := []pos{{0, 0, 1, "A"}, {0, 0, 2, "A"}, {0, 1, 0, "B"}, {0, 1, 1, "B"}}
	if len(got) != len(want) {
		t.Fatalf("placed %d labels, want %d", len(got), len(want))
	}
	for i, p := range got {
		if g := (pos{p.page, p.row, p.col, p.item.Title}); g != want[i] {
			t.Errorf("label %d at %+v, want %+v", i, g, want[i])
		}
	}
}

func TestPlacePageBreak(t *testing.T) {
	j := Job{Items: items("A", "B", "C"), StartRow: 10, StartCol: 3}
	got := place(avery5260, j)
	if got[0].page != 0 || got[0].row != 9 || got[0].col != 2 {
		t.Errorf("first label at page %d row %d col %d, want last slot of page 0", got[0].page, got[0].row, got[0].col)
	}
	for _, p := range got[1:] {
		if p.page != 1 || p.row != 0 {
			t.Errorf("label %q at page %d row %d, want page 1 row 0", p.item.Title, p.page, p.row)
		}
	}
	if n := pageCount(avery5260, j); n != 2 {
		t.Errorf("pageCount = %d, want 2", n)
	}
}

func TestSheetMap(t *testing.T) {
	small := Template{Grid: Grid{Cols: 2, Rows: 2}}
	U, P, F := SlotUsed, SlotPrinted, SlotFree
	cases := []struct {
		name string
		job  Job
		want [][]SlotState
	}{
		{"start 1,2 with one label", Job{Items: items("A"), StartRow: 1, StartCol: 2},
			[][]SlotState{{U, P, F, F}}},
		{"copies spill to a second page", Job{Items: items("A", "B"), Copies: 2, StartRow: 2, StartCol: 1},
			[][]SlotState{{U, U, P, P}, {P, P, F, F}}},
		{"skip after start", Job{Items: items("A"), StartRow: 1, StartCol: 1, Skip: 2},
			[][]SlotState{{U, U, P, F}}},
		{"no items still shows the sheet", Job{StartRow: 2, StartCol: 1},
			[][]SlotState{{U, U, F, F}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SheetMap(small, c.job); !reflect.DeepEqual(got, c.want) {
				t.Errorf("SheetMap = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBottomBleed(t *testing.T) {
	const bleed, edge = 1.5, 0.4
	cases := []struct {
		name  string
		job   Job
		label int // index into the placed labels
		want  float64
	}{
		{"label below printed in this job", Job{Items: items("A", "B", "C", "D")}, 0, bleed},
		{"slot below left free", Job{Items: items("A", "B", "C", "D")}, 1, edge},
		{"one row only: all free below", Job{Items: items("A", "B", "C")}, 2, edge},
		{"last row bleeds onto the sheet margin", Job{Items: items("A"), StartRow: 10, StartCol: 1}, 0, bleed},
		{"partial sheet from 2,2: row 2 col 2 above a printed label", Job{Items: items("A", "B", "C", "D", "E"), StartRow: 2, StartCol: 2}, 0, bleed},
		{"partial sheet from 2,2: row 3 above a free row", Job{Items: items("A", "B", "C", "D", "E"), StartRow: 2, StartCol: 2}, 2, edge},
		{"second page: free slot below", Job{Items: items(strings.Split(strings.Repeat("x", 31), "")...)}, 30, edge},
		{"first page: row 9 above printed row 10", Job{Items: items(strings.Split(strings.Repeat("x", 31), "")...)}, 26, bleed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.job.BleedMM, c.job.EdgeBleedMM = bleed, edge
			got := place(avery5260, c.job)[c.label].bottomBleedMM
			if got != c.want {
				t.Errorf("bottom bleed = %g, want %g", got, c.want)
			}
		})
	}
}

func TestSlotRectOffsetSigns(t *testing.T) {
	base := slotRect(avery5260, model.Offset{}, 1, 1)
	wantX := (0.1875 + 2.625 + 0.125) * 72
	wantY := (0.5 + 1) * 72
	if math.Abs(base.X-wantX) > 1e-9 || math.Abs(base.Y-wantY) > 1e-9 || base.W != 2.625*72 || base.H != 72 {
		t.Fatalf("slot rect = %+v, want X %g Y %g", base, wantX, wantY)
	}
	// The saved 5260 calibration (y = -0.75 mm) must print lower: Y grows
	// with a top-left origin. Positive x moves right.
	shifted := slotRect(avery5260, model.Offset{XMM: 1, YMM: -0.75}, 1, 1)
	if d := shifted.Y - base.Y; math.Abs(d-0.75*PointsPerMM) > 1e-9 {
		t.Errorf("y offset -0.75 mm moved the slot by %g pt, want +%g (down)", d, 0.75*PointsPerMM)
	}
	if d := shifted.X - base.X; math.Abs(d-PointsPerMM) > 1e-9 {
		t.Errorf("x offset 1 mm moved the slot by %g pt, want +%g (right)", d, PointsPerMM)
	}
}

func TestJobValidate(t *testing.T) {
	cases := []struct {
		name string
		job  Job
		ok   bool
	}{
		{"fine", Job{Items: items("A"), StartRow: 3, StartCol: 2}, true},
		{"row out of range", Job{Items: items("A"), StartRow: 11, StartCol: 1}, false},
		{"col out of range", Job{Items: items("A"), StartRow: 1, StartCol: 4}, false},
		{"skip leaves a blank first page", Job{Items: items("A"), StartRow: 10, StartCol: 3, Skip: 1}, false},
		{"negative bleed", Job{Items: items("A"), BleedMM: -1}, false},
		{"nothing to print", Job{}, false},
		{"outline with no items is a preview", Job{Outline: true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.job.validate(avery5260); (err == nil) != c.ok {
				t.Errorf("validate() = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestTitleSubtitle(t *testing.T) {
	cases := []struct{ name, title, subtitle string }{
		{"Metallica — Essential", "Metallica", "Essential"},
		{"Volodymyr Ivasiuk", "Volodymyr Ivasiuk", ""},
		{"A — B — C", "A", "B — C"},
		{"  Володимир Івасюк —  Пісні ", "Володимир Івасюк", "Пісні"},
	}
	for _, c := range cases {
		title, subtitle := TitleSubtitle(c.name)
		if title != c.title || subtitle != c.subtitle {
			t.Errorf("TitleSubtitle(%q) = %q, %q; want %q, %q", c.name, title, subtitle, c.title, c.subtitle)
		}
	}
}

func TestItemFromSubjectOverrides(t *testing.T) {
	str := func(s string) *string { return &s }
	e := model.Subject{Name: "Queen — Bohemian Rhapsody"}
	if it := ItemFromSubject(e, nil); it.Title != "Queen" || it.Subtitle != "Bohemian Rhapsody" {
		t.Errorf("no overrides: %+v", it)
	}
	e.Label = model.LabelOverrides{Title: str("QUEEN"), Subtitle: str("")}
	if it := ItemFromSubject(e, nil); it.Title != "QUEEN" || it.Subtitle != "" {
		t.Errorf("overrides: %+v", it)
	}
}

func TestThemeColors(t *testing.T) {
	fade, fadeText := "#112233", "#445566"
	b := Theme{ID: "x", Background: "#FFFFFF", Text: "#000000"}
	// Unset fade colors keep the script's look on any background.
	if b.FadeColor() != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("fade without Fade = %v, want black", b.FadeColor())
	}
	if b.FadeTextColor() != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("fade text without FadeText = %v, want white", b.FadeTextColor())
	}
	b.Fade, b.FadeText = &fade, &fadeText
	if b.FadeColor() != (color.RGBA{0x11, 0x22, 0x33, 255}) {
		t.Errorf("fade = %v", b.FadeColor())
	}
	if b.FadeTextColor() != (color.RGBA{0x44, 0x55, 0x66, 255}) {
		t.Errorf("fade text = %v", b.FadeTextColor())
	}
	if (Theme{Background: "#000000"}).IsWhite() {
		t.Error("black is white")
	}
	if !(Theme{Background: "#FFFFFF"}).IsWhite() {
		t.Error("white is not white")
	}
	for _, bad := range []string{"", "#FFF", "FFFFFF", "#GGGGGG", "#+12345", "#1234567"} {
		if _, err := ParseHexColor(bad); err == nil {
			t.Errorf("ParseHexColor(%q) accepted", bad)
		}
	}
}

// fakeCanvas measures text as 0.5 × size per rune and records drawn text.
type fakeCanvas struct {
	size  float64
	style FontStyle
	texts []string
}

func (f *fakeCanvas) SetFill(color.RGBA)            {}
func (f *fakeCanvas) Rect(Rect)                     {}
func (f *fakeCanvas) Outline(Rect, float64)         {}
func (f *fakeCanvas) Image(image.Image, Rect) error { return nil }
func (f *fakeCanvas) SetTextColor(color.RGBA)       {}
func (f *fakeCanvas) SetFont(s FontStyle, size float64) error {
	f.style, f.size = s, size
	return nil
}
func (f *fakeCanvas) Text(_, _ float64, s string) error { f.texts = append(f.texts, s); return nil }
func (f *fakeCanvas) TextWidth(s string) (float64, error) {
	return float64(len([]rune(s))) * f.size * 0.5, nil
}

func TestFitLines(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		size      float64
		width     float64
		wantLines []string
		wantSize  float64
	}{
		// 10 pt → 5 pt per rune. "Hello world" is 11 runes = 55 pt.
		{"fits as is", "Hello world", 10, 60, []string{"Hello world"}, 10},
		{"wraps onto two lines", "Hello world", 10, 30, []string{"Hello", "world"}, 10},
		// Three words of 5 runes, 2 lines max at width 30: shrink until two
		// fit on one line: 11 runes × 0.5 × size ≤ 30 → size ≤ 5.45 → 5.
		{"shrinks to fit two lines", "aaaaa bbbbb ccccc", 10, 30, []string{"aaaaa bbbbb", "ccccc"}, 5},
		// At the 5 pt floor: 2.5 pt per rune, 12 runes fit in 30 pt.
		{"ellipsizes an overlong word", "Supercalifragilistic", 10, 30, []string{"Supercalifr…"}, 5},
		{"drops extra lines with an ellipsis", "aaaaaaaaaaaa bbbbbbbbbbbb cccccccccccc", 10, 30,
			[]string{"aaaaaaaaaaaa", "bbbbbbbbbbb…"}, 5},
		{"empty text", "", 10, 30, nil, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines, size, err := FitLines(&fakeCanvas{}, c.text, Bold, c.size, c.width, 2, 5)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(lines, c.wantLines) || size != c.wantSize {
				t.Errorf("FitLines = %q at %g pt, want %q at %g pt", lines, size, c.wantLines, c.wantSize)
			}
		})
	}
}

func TestTextBlockDrawOrderAndHeight(t *testing.T) {
	f := &fakeCanvas{}
	b, err := FitTextBlock(f, "Title", "Sub", 1000, FontSizes{TitlePt: 10, SubtitlePt: 8})
	if err != nil {
		t.Fatal(err)
	}
	if want := 10*1.15 + 8*1.15; math.Abs(b.Height()-want) > 1e-9 {
		t.Errorf("height = %g, want %g", b.Height(), want)
	}
	if err := b.Draw(f, 0, 100); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.texts, []string{"Sub", "Title"}) {
		t.Errorf("drew %q; want subtitle first (bottom), then title", f.texts)
	}
}

func TestCropToRatioAndFade(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	if b := CropToRatio(src, 1.5).Bounds(); b.Dx() != 100 || b.Dy() != 67 || b.Min.Y != 16 {
		t.Errorf("crop to 1.5 = %v, want 100x67 centered", b)
	}

	img := image.NewRGBA(image.Rect(0, 0, 4, 10))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	FadeBottom(img, 5, color.RGBA{0, 0, 0, 255})
	if img.RGBAAt(0, 4) != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("row above the band changed: %v", img.RGBAAt(0, 4))
	}
	if img.RGBAAt(0, 5) != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("top band row should be clear, got %v", img.RGBAAt(0, 5))
	}
	// Bottom row: alpha = round(190 × 0.8^0.8) = 159 → 255 × 96/255 = 96.
	if got := img.RGBAAt(3, 9); got != (color.RGBA{96, 96, 96, 255}) {
		t.Errorf("bottom row = %v, want 96 gray", got)
	}
}
