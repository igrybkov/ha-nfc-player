package labels

import (
	_ "embed"
	"errors"
	"fmt"
	"image/color"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// Background is a label color scheme. Colors are "#RRGGBB".
type Background struct {
	ID         string  `yaml:"id" json:"id"`
	Name       string  `yaml:"name" json:"name"`
	Builtin    bool    `yaml:"builtin" json:"builtin"`
	Background string  `yaml:"background" json:"background"`
	Text       string  `yaml:"text" json:"text"`
	Fade       *string `yaml:"fade" json:"fade"`           // full layout gradient color; nil = black
	FadeText   *string `yaml:"fade_text" json:"fade_text"` // full layout text over the fade; nil = white
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
func (b Background) Validate() error {
	var errs []error
	if b.ID == "" {
		errs = append(errs, errors.New("id is required"))
	}
	if _, err := ParseHexColor(b.Background); err != nil {
		errs = append(errs, fmt.Errorf("background: %w", err))
	}
	if _, err := ParseHexColor(b.Text); err != nil {
		errs = append(errs, fmt.Errorf("text: %w", err))
	}
	for name, v := range map[string]*string{"fade": b.Fade, "fade_text": b.FadeText} {
		if v == nil {
			continue
		}
		if _, err := ParseHexColor(*v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("background %q: %w", b.ID, err)
	}
	return nil
}

// ValidateBackgrounds validates each background and checks that ids are
// unique. Pass built-ins and user backgrounds together, so a user id can't
// shadow a built-in.
func ValidateBackgrounds(bs []Background) error {
	seen := map[string]bool{}
	var errs []error
	for _, b := range bs {
		if err := b.Validate(); err != nil {
			errs = append(errs, err)
		}
		if seen[b.ID] {
			errs = append(errs, fmt.Errorf("background %q: duplicate id", b.ID))
		}
		seen[b.ID] = true
	}
	return errors.Join(errs...)
}

// IsWhite reports whether the background is white, so no bleed is needed.
func (b Background) IsWhite() bool {
	c, err := ParseHexColor(b.Background)
	return err == nil && c == white
}

// BackgroundColor returns the label fill color. An invalid value gives white.
func (b Background) BackgroundColor() color.RGBA { return hexOr(b.Background, white) }

// TextColor returns the text color. An invalid value gives black.
func (b Background) TextColor() color.RGBA { return hexOr(b.Text, black) }

// FadeColor returns the full layout gradient color: Fade, else black (the
// script's look: a dark band under light text on any background).
func (b Background) FadeColor() color.RGBA {
	if b.Fade != nil {
		return hexOr(*b.Fade, black)
	}
	return black
}

// FadeTextColor returns the full layout text color over the fade: FadeText,
// else white.
func (b Background) FadeTextColor() color.RGBA {
	if b.FadeText != nil {
		return hexOr(*b.FadeText, white)
	}
	return white
}

var (
	white = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	black = color.RGBA{0, 0, 0, 0xFF}
)

// ParseHexColor parses "#RRGGBB" (any case) into an opaque color.
func ParseHexColor(s string) (color.RGBA, error) {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{}, fmt.Errorf("%q is not a #RRGGBB color", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("%q is not a #RRGGBB color", s)
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xFF}, nil
}

func hexOr(s string, fallback color.RGBA) color.RGBA {
	if c, err := ParseHexColor(s); err == nil {
		return c
	}
	return fallback
}
