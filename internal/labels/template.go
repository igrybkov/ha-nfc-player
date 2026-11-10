// Package labels renders label sheets to PDF: templates, backgrounds,
// pluggable layouts, slot math and the PDF canvas.
//
// Owner: track B (docs/PLAN.md section 10). Layouts live in labels/layouts and
// register themselves in init; import that package for its side effect.
package labels

import (
	_ "embed"

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

// Validate checks sizes, that the grid fits the page and that the layout is
// registered. Uniqueness of ids is checked by the caller.
func (t Template) Validate() error { panic("TODO: track B") }
