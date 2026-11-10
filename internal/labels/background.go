package labels

import (
	_ "embed"

	"go.yaml.in/yaml/v3"
)

// Background is a label color scheme. Colors are "#RRGGBB".
type Background struct {
	ID         string  `yaml:"id" json:"id"`
	Name       string  `yaml:"name" json:"name"`
	Builtin    bool    `yaml:"builtin" json:"builtin"`
	Background string  `yaml:"background" json:"background"`
	Text       string  `yaml:"text" json:"text"`
	Fade       *string `yaml:"fade" json:"fade"` // full layout gradient color; nil = Background
}

//go:embed builtin/backgrounds.yaml
var builtinBackgroundsYAML []byte

// BuiltinBackgrounds returns the read-only backgrounds embedded in the binary.
func BuiltinBackgrounds() ([]Background, error) {
	var bs []Background
	if err := yaml.Unmarshal(builtinBackgroundsYAML, &bs); err != nil {
		return nil, err
	}
	return bs, nil
}

// Validate checks the id and color values.
func (b Background) Validate() error { panic("TODO: track B") }

// IsWhite reports whether the background is white, so no bleed is needed.
func (b Background) IsWhite() bool { panic("TODO: track B") }
