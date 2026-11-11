// Package labels renders label sheets to PDF and single labels to PNG:
// templates, themes, slot math and the canvases.
//
// The geometry is a port of
// docs/reference/nfc-music-labels.py, which was tuned against real paper.
//
// # Coordinates
//
// Everything drawLabel sees is in points (1 in = 72 pt), origin at the
// top-left of the page, Y down. Render computes each slot, fills the bleed
// for non-white themes, clips to the slot shrunk by the inset (rounded
// corners, or an ellipse for dots), and draws the label in that inset rect.
// The PDF and the PNG preview share this path (drawSlot), so the preview
// shows what prints.
//
// # Formats and themes
//
// A template is paper: page, label size, grid and font sizes, plus its
// format (small, large or dot). A theme is the look: colors, and for each
// format where the art goes (left, right, top, bottom, background, none)
// and whether it prints in gray. A print uses the theme's style for the
// template's format.
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
	Format  Format    `yaml:"format" json:"format"`
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

// Caps that keep one template from making a huge PDF or slot map.
const (
	MaxSlots  = 1000 // cols*rows
	MaxSizeIn = 50.0 // page and label width and height
)

// Validate checks sizes, that the grid fits the page and that the layout is
// known. Uniqueness of ids is checked by ValidateTemplates.
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
	for _, p := range positive[:4] { // page and label sizes
		if p.v > MaxSizeIn {
			add("%s must be <= %g", p.name, MaxSizeIn)
		}
	}
	// Each side alone first, so the product can't overflow.
	if t.Grid.Cols > MaxSlots || t.Grid.Rows > MaxSlots || t.Grid.Cols*t.Grid.Rows > MaxSlots {
		add("grid.cols*grid.rows must be <= %d", MaxSlots)
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
	if !t.Format.valid() {
		add("format %q is not one of %v", t.Format, FormatList)
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

// Format is the kind of label a template prints. A theme sets its look per
// format.
type Format string

const (
	FormatSmall Format = "small" // a small rectangle, like Avery 5434 (1.5×1 in)
	FormatLarge Format = "large" // a wide rectangle, like Avery 5260 (2.625×1 in)
	FormatDot   Format = "dot"   // a round label
)

// FormatList lists every format, in the order the UI shows them.
var FormatList = []Format{FormatSmall, FormatLarge, FormatDot}

func (f Format) valid() bool { return f == FormatSmall || f == FormatLarge || f == FormatDot }

// Round reports whether labels of this format are round.
func (f Format) Round() bool { return f == FormatDot }

// DefaultArt is the placement a theme uses when it sets none: art behind
// the text on small labels and dots, art left on large labels.
func (f Format) DefaultArt() Art {
	if f == FormatLarge {
		return ArtLeft
	}
	return ArtBackground
}

// Reference is the label size and font sizes that previews of the format
// use when no template of that format exists.
func (f Format) Reference() (Size, FontSizes) {
	switch f {
	case FormatLarge:
		return Size{WidthIn: 2.625, HeightIn: 1}, FontSizes{TitlePt: 11, SubtitlePt: 8.5}
	case FormatDot:
		return Size{WidthIn: 1.5, HeightIn: 1.5}, FontSizes{TitlePt: 9, SubtitlePt: 7}
	default:
		return Size{WidthIn: 1.5, HeightIn: 1}, FontSizes{TitlePt: 8, SubtitlePt: 6.5}
	}
}
