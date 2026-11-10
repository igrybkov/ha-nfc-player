package labels

import (
	"image"
	"image/color"
	"sort"
	"sync"
)

// Rect is a box in points, origin at the top-left of the page, Y down.
type Rect struct{ X, Y, W, H float64 }

// FontStyle selects the embedded font.
type FontStyle int

const (
	Regular FontStyle = iota
	Bold
)

// Canvas wraps the PDF library. All units are points, origin top-left.
type Canvas interface {
	SetFill(c color.RGBA)
	Rect(r Rect) // filled with the current fill color
	Outline(r Rect, widthPt float64)
	Image(img image.Image, r Rect) error
	SetTextColor(c color.RGBA)
	SetFont(style FontStyle, sizePt float64) error
	Text(x, baselineY float64, s string) error
	TextWidth(s string) (float64, error) // with the current font
}

// Layout draws one label into a cell.
type Layout interface {
	ID() string // "side", "full"
	Name() string
	Draw(c Canvas, cell Rect, item Item, bg Background, t Template) error
}

var (
	layoutsMu sync.RWMutex
	layouts   = map[string]Layout{}
)

// RegisterLayout adds a layout. It panics on a duplicate ID.
func RegisterLayout(l Layout) {
	layoutsMu.Lock()
	defer layoutsMu.Unlock()
	if _, dup := layouts[l.ID()]; dup {
		panic("labels: duplicate layout " + l.ID())
	}
	layouts[l.ID()] = l
}

// LookupLayout returns a registered layout by ID.
func LookupLayout(id string) (Layout, bool) {
	layoutsMu.RLock()
	defer layoutsMu.RUnlock()
	l, ok := layouts[id]
	return l, ok
}

// Layouts returns all registered layouts sorted by ID.
func Layouts() []Layout {
	layoutsMu.RLock()
	defer layoutsMu.RUnlock()
	out := make([]Layout, 0, len(layouts))
	for _, l := range layouts {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
