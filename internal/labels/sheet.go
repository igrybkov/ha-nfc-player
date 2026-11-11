package labels

import (
	"errors"
	"fmt"
	"image"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// Item is one label to print.
type Item struct {
	Title, Subtitle string
	Art             image.Image // already cropped square; nil = no art
}

// Job is one print run.
type Job struct {
	Template                      Template
	Theme                         Theme
	Items                         []Item
	Copies                        int
	StartRow, StartCol            int // 1-based
	Skip                          int // empty slots before the first label, after the start slot
	Offset                        model.Offset
	InsetMM, BleedMM, EdgeBleedMM float64
	OuterBleedMM                  float64 // extra bleed on the paper-edge side of the first and last columns
	Outline                       bool    // draw label outlines (template preview)
}

// SlotState is the state of one slot in the sheet map.
type SlotState string

const (
	SlotUsed    SlotState = "used"    // before the start slot or skipped
	SlotPrinted SlotState = "printed" // gets a label in this job
	SlotFree    SlotState = "free"    // left empty after the last label
)

// Separator splits tag names like "Artist — Album".
const Separator = "—"

// TitleSubtitle splits a tag name on the first em dash: artist, album.
// With no dash the whole name is the title.
func TitleSubtitle(name string) (title, subtitle string) {
	title, subtitle, _ = strings.Cut(name, Separator)
	return strings.TrimSpace(title), strings.TrimSpace(subtitle)
}

// ItemFromSubject builds the label for a subject: title and subtitle from
// the name, replaced by the subject's label overrides when set.
func ItemFromSubject(s model.Subject, art image.Image) Item {
	title, subtitle := TitleSubtitle(s.Name)
	if s.Label.Title != nil {
		title = *s.Label.Title
	}
	if s.Label.Subtitle != nil {
		subtitle = *s.Label.Subtitle
	}
	return Item{Title: title, Subtitle: subtitle, Art: art}
}

// copies returns Copies, treating 0 or less as 1.
func (j Job) copies() int { return max(j.Copies, 1) }

// LabelCount is the number of labels the job prints.
func (j Job) LabelCount() int { return len(j.Items) * j.copies() }

// skipSlots is the number of slots before the first label: everything before
// the start slot plus Skip. A zero start row or column means 1.
func (j Job) skipSlots(t Template) int {
	row, col := max(j.StartRow, 1), max(j.StartCol, 1)
	return (row-1)*t.Grid.Cols + (col - 1) + max(j.Skip, 0)
}

// validate checks the job values that Template.Validate doesn't cover.
func (j Job) validate(t Template) error {
	var errs []error
	if j.StartRow > t.Grid.Rows || j.StartRow < 0 {
		errs = append(errs, fmt.Errorf("start row %d is outside 1-%d", j.StartRow, t.Grid.Rows))
	}
	if j.StartCol > t.Grid.Cols || j.StartCol < 0 {
		errs = append(errs, fmt.Errorf("start column %d is outside 1-%d", j.StartCol, t.Grid.Cols))
	}
	if j.Skip < 0 {
		errs = append(errs, errors.New("skip must be >= 0"))
	}
	if len(errs) == 0 && j.skipSlots(t) >= t.PerSheet() {
		// The first page would be blank. That is a mistake, not a print run.
		errs = append(errs, fmt.Errorf("start slot plus skip (%d) leaves no free slot on the first sheet of %d", j.skipSlots(t), t.PerSheet()))
	}
	for name, v := range map[string]float64{"inset": j.InsetMM, "bleed": j.BleedMM, "edge bleed": j.EdgeBleedMM, "outer bleed": j.OuterBleedMM} {
		if !(v >= 0) {
			errs = append(errs, fmt.Errorf("%s must be >= 0 mm", name))
		}
	}
	if j.LabelCount() == 0 && !j.Outline {
		errs = append(errs, errors.New("no labels to print"))
	}
	return errors.Join(errs...)
}

// pageCount is the number of pages the job needs. An empty outline job still
// gets one page.
func pageCount(t Template, j Job) int {
	per := t.PerSheet()
	if per <= 0 {
		return 0
	}
	return max(1, ceilDiv(j.skipSlots(t)+j.LabelCount(), per))
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// SheetMap returns the slot states per page, row-major ([page][row*cols+col]).
// It uses t for the geometry and job for the start slot, skip, items and copies.
func SheetMap(t Template, job Job) [][]SlotState {
	per := t.PerSheet()
	skip, end := job.skipSlots(t), job.skipSlots(t)+job.LabelCount()
	pages := make([][]SlotState, pageCount(t, job))
	for p := range pages {
		pages[p] = make([]SlotState, per)
		for i := range pages[p] {
			switch slot := p*per + i; {
			case slot < skip:
				pages[p][i] = SlotUsed
			case slot < end:
				pages[p][i] = SlotPrinted
			default:
				pages[p][i] = SlotFree
			}
		}
	}
	return pages
}

// placed is one label in its slot.
type placed struct {
	page, row, col int
	item           *Item
	// bottomBleedMM is how far the background fill extends below the label.
	bottomBleedMM float64
}

// place lays the job's labels out on pages. Labels flow across columns, then
// down rows, then onto new pages. Each copy follows its original (A A B B).
func place(t Template, j Job) []placed {
	per, cols := t.PerSheet(), t.Grid.Cols
	skip := j.skipSlots(t)
	total := skip + j.LabelCount()
	out := make([]placed, 0, j.LabelCount())
	for n := range j.LabelCount() {
		slot := skip + n
		page, inPage := slot/per, slot%per
		// Slots used on this page, counting the skipped ones before the labels.
		pageLen := min(per, total-page*per)
		row, col := inPage/cols, inPage%cols
		out = append(out, placed{
			page: page, row: row, col: col,
			item:          &j.Items[n/j.copies()],
			bottomBleedMM: bottomBleed(row, inPage, pageLen, t.Grid, j.BleedMM, j.EdgeBleedMM),
		})
	}
	return out
}

// bottomBleed is the downward bleed of a label (script: below_ok). Columns
// have a gap, so the side and top bleed is always full. Rows touch, so bleed
// fully down only onto a label printed in this run, or onto the sheet margin
// under the last row. A free label below gets only the small edge bleed. The
// labels above are always printed or used already, so the top needs no check.
func bottomBleed(row, inPage, pageLen int, g Grid, bleedMM, edgeBleedMM float64) float64 {
	if row == g.Rows-1 || inPage+g.Cols < pageLen {
		return bleedMM
	}
	return edgeBleedMM
}

// Units.
const (
	PointsPerInch = 72.0
	PointsPerMM   = PointsPerInch / 25.4
)

// slotRect is the label rectangle in points, top-left origin. Positive
// Offset.XMM moves the print right, positive Offset.YMM moves it up (the
// script's convention: a saved y of -0.75 prints 0.75 mm lower).
func slotRect(t Template, off model.Offset, row, col int) Rect {
	return Rect{
		X: (t.Margin.LeftIn+float64(col)*(t.Label.WidthIn+t.Gap.ColIn))*PointsPerInch + off.XMM*PointsPerMM,
		Y: (t.Margin.TopIn+float64(row)*(t.Label.HeightIn+t.Gap.RowIn))*PointsPerInch - off.YMM*PointsPerMM,
		W: t.Label.WidthIn * PointsPerInch,
		H: t.Label.HeightIn * PointsPerInch,
	}
}

// Inset shrinks r by d on every side.
func (r Rect) Inset(d float64) Rect { return Rect{r.X + d, r.Y + d, r.W - 2*d, r.H - 2*d} }
