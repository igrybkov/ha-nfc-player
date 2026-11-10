package labels

import (
	"image"

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
	Background                    Background
	Items                         []Item
	Copies                        int
	StartRow, StartCol            int // 1-based
	Skip                          int // empty slots before the first label, after the start slot
	Offset                        model.Offset
	InsetMM, BleedMM, EdgeBleedMM float64
	Outline                       bool // draw label outlines (template preview)
}

// SlotState is the state of one slot in the sheet map.
type SlotState string

const (
	SlotUsed    SlotState = "used"    // before the start slot or skipped
	SlotPrinted SlotState = "printed" // gets a label in this job
	SlotFree    SlotState = "free"    // left empty after the last label
)

// SheetMap returns the slot states per page, row-major ([page][row*cols+col]).
func SheetMap(t Template, job Job) [][]SlotState { panic("TODO: track B") }

// TitleSubtitle splits a tag name on the first em dash: artist, album.
// With no dash the whole name is the title.
func TitleSubtitle(name string) (title, subtitle string) { panic("TODO: track B") }
