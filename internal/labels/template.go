// Package labels renders label sheets to PDF: templates, backgrounds,
// pluggable layouts, slot math and the PDF canvas.
//
// Owner: track B (docs/PLAN.md section 10). The geometry is a port of
// docs/reference/nfc-music-labels.py, which was tuned against real paper.
//
// # Coordinates
//
// Everything a layout sees is in points (1 in = 72 pt), origin at the top-left
// of the page, Y down. Render computes each slot, fills the bleed for non-white
// backgrounds, clips to the slot shrunk by the inset (rounded corners), and
// calls the layout with that inset rect as cell. A layout never needs to know
// about offsets, bleed or clipping.
//
// # Adding a layout
//
// Add one file to internal/labels/layouts:
//
//	package layouts
//
//	import "github.com/igrybkov/ha-nfc-player/internal/labels"
//
//	func init() { labels.RegisterLayout(myLayout{}) }
//
//	type myLayout struct{}
//
//	func (myLayout) ID() string   { return "my-layout" } // used in templates
//	func (myLayout) Name() string { return "My layout" } // shown in the UI
//	func (myLayout) Draw(c labels.Canvas, cell labels.Rect, item labels.Item, bg labels.Background, t labels.Template) error {
//		...
//	}
//
// Helpers for layouts: FitTextBlock (wrap, shrink and ellipsize title and
// subtitle, then draw them bottom-aligned), CropToRatio, ResizeArt and
// ArtPixels (art at ArtDPI), FadeBottom (gradient composited into the art),
// and LabelPad. The labels package must not import layouts; the binary
// blank-imports it.
package labels

import (
	_ "embed"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Template describes a label sheet. Sizes are in inches, fonts in points.
type Template struct {
	ID      string    `yaml:"id" json:"id"`
	Name    string    `yaml:"name" json:"name"`
	Builtin bool      `yaml:"builtin" json:"builtin"`
	Page    Size      `yaml:"page" json:"page"`
	Label   Size      `yaml:"label" json:"label"`
	Grid    Grid      `yaml:"grid" json:"grid"`
	Margin  Margin    `yaml:"margin" json:"margin"`
	Gap     Gap       `yaml:"gap" json:"gap"`
	Layout  string    `yaml:"layout" json:"layout"` // registered Layout ID
	Font    FontSizes `yaml:"font" json:"font"`
}

// Size is a width and height in inches.
type Size struct {
	WidthIn  float64 `yaml:"width_in" json:"width_in"`
	HeightIn float64 `yaml:"height_in" json:"height_in"`
}

// Grid is the number of label columns and rows on a page.
type Grid struct {
	Cols int `yaml:"cols" json:"cols"`
	Rows int `yaml:"rows" json:"rows"`
}

// Margin is the distance from the page edge to the first label, in inches.
type Margin struct {
	LeftIn float64 `yaml:"left_in" json:"left_in"`
	TopIn  float64 `yaml:"top_in" json:"top_in"`
}

// Gap is the space between labels, in inches.
type Gap struct {
	ColIn float64 `yaml:"col_in" json:"col_in"`
	RowIn float64 `yaml:"row_in" json:"row_in"`
}

// FontSizes are the starting font sizes, in points.
type FontSizes struct {
	TitlePt    float64 `yaml:"title_pt" json:"title_pt"`
	SubtitlePt float64 `yaml:"subtitle_pt" json:"subtitle_pt"`
}

//go:embed builtin/templates.yaml
var builtinTemplatesYAML []byte

// BuiltinTemplates returns the read-only templates embedded in the binary.
func BuiltinTemplates() ([]Template, error) {
	var ts []Template
	if err := yaml.Unmarshal(builtinTemplatesYAML, &ts); err != nil {
		return nil, err
	}
	return ts, nil
}

// PerSheet is the number of label slots on one page.
func (t Template) PerSheet() int { return t.Grid.Cols * t.Grid.Rows }

// fitTolerance absorbs float error when a grid exactly fills the page.
const fitTolerance = 1e-9

// Validate checks sizes, that the grid fits the page and that the layout is
// registered. Uniqueness of ids is checked by ValidateTemplates.
func (t Template) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if t.ID == "" {
		add("id is required")
	}
	positive := []struct {
		name string
		v    float64
	}{
		{"page.width_in", t.Page.WidthIn},
		{"page.height_in", t.Page.HeightIn},
		{"label.width_in", t.Label.WidthIn},
		{"label.height_in", t.Label.HeightIn},
		{"grid.cols", float64(t.Grid.Cols)},
		{"grid.rows", float64(t.Grid.Rows)},
		{"font.title_pt", t.Font.TitlePt},
		{"font.subtitle_pt", t.Font.SubtitlePt},
	}
	for _, p := range positive {
		if !(p.v > 0) { // also catches NaN
			add("%s must be > 0", p.name)
		}
	}
	nonNegative := []struct {
		name string
		v    float64
	}{
		{"margin.left_in", t.Margin.LeftIn},
		{"margin.top_in", t.Margin.TopIn},
		{"gap.col_in", t.Gap.ColIn},
		{"gap.row_in", t.Gap.RowIn},
	}
	for _, p := range nonNegative {
		if !(p.v >= 0) {
			add("%s must be >= 0", p.name)
		}
	}
	if len(errs) == 0 { // the fit check only makes sense with sane sizes
		if w := t.gridWidthIn(); w > t.Page.WidthIn+fitTolerance {
			add("grid is %.4g in wide with the left margin; page is %.4g in", w, t.Page.WidthIn)
		}
		if h := t.gridHeightIn(); h > t.Page.HeightIn+fitTolerance {
			add("grid is %.4g in tall with the top margin; page is %.4g in", h, t.Page.HeightIn)
		}
	}
	if _, ok := LookupLayout(t.Layout); !ok {
		add("layout %q is not registered", t.Layout)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("template %q: %w", t.ID, err)
	}
	return nil
}

func (t Template) gridWidthIn() float64 {
	n := float64(t.Grid.Cols)
	return t.Margin.LeftIn + n*t.Label.WidthIn + (n-1)*t.Gap.ColIn
}

func (t Template) gridHeightIn() float64 {
	n := float64(t.Grid.Rows)
	return t.Margin.TopIn + n*t.Label.HeightIn + (n-1)*t.Gap.RowIn
}

// ValidateTemplates validates each template and checks that ids are unique.
// Pass built-ins and user templates together, so a user id can't shadow a
// built-in.
func ValidateTemplates(ts []Template) error {
	seen := map[string]bool{}
	var errs []error
	for _, t := range ts {
		if err := t.Validate(); err != nil {
			errs = append(errs, err)
		}
		if seen[t.ID] {
			errs = append(errs, fmt.Errorf("template %q: duplicate id", t.ID))
		}
		seen[t.ID] = true
	}
	return errors.Join(errs...)
}
