package labels

import (
	_ "embed"
	"errors"
	"fmt"
	"image/color"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// Theme is the look of a label: colors, plus where the art goes on each
// label format. One theme covers every format, so any template can print
// with it. Colors are "#RRGGBB".
type Theme struct {
	ID         string  `yaml:"id" json:"id"`
	Name       string  `yaml:"name" json:"name"`
	Builtin    bool    `yaml:"builtin" json:"builtin"`
	Background string  `yaml:"background" json:"background"`
	Text       string  `yaml:"text" json:"text"`
	Fade       *string `yaml:"fade" json:"fade"`           // gradient under text on background art; nil = black
	FadeText   *string `yaml:"fade_text" json:"fade_text"` // text over that gradient; nil = white
	Formats    Formats `yaml:"formats" json:"formats"`
}

// Formats holds a style per label format.
type Formats struct {
	Small Style `yaml:"small" json:"small"`
	Large Style `yaml:"large" json:"large"`
	Dot   Style `yaml:"dot" json:"dot"`
}

// Style is how a theme lays out one format.
type Style struct {
	Art  Art  `yaml:"art,omitempty" json:"art"` // "" = the format's default
	Mono bool `yaml:"mono,omitempty" json:"mono"`
}

// Art is where the cover goes on a label.
type Art string

const (
	ArtLeft       Art = "left"
	ArtRight      Art = "right"
	ArtTop        Art = "top"
	ArtBottom     Art = "bottom"
	ArtBackground Art = "background" // fills the label; text over a fade
	ArtNone       Art = "none"
)

// ArtPlacements lists every placement, in the order the UI shows them.
var ArtPlacements = []Art{ArtLeft, ArtRight, ArtTop, ArtBottom, ArtBackground, ArtNone}

func (a Art) valid() bool {
	for _, p := range ArtPlacements {
		if a == p {
			return true
		}
	}
	return false
}

// Style returns the theme's style for f, with the format's default
// placement when none is set.
func (b Theme) Style(f Format) Style {
	var s Style
	switch f {
	case FormatSmall:
		s = b.Formats.Small
	case FormatLarge:
		s = b.Formats.Large
	case FormatDot:
		s = b.Formats.Dot
	}
	if s.Art == "" {
		s.Art = f.DefaultArt()
	}
	return s
}

//go:embed builtin/themes.yaml
var builtinThemesYAML []byte

// BuiltinThemes returns the read-only themes embedded in the binary.
func BuiltinThemes() ([]Theme, error) {
	var bs []Theme
	if err := yaml.Unmarshal(builtinThemesYAML, &bs); err != nil {
		return nil, err
	}
	return bs, nil
}

// Validate checks the id, colors and placements.
func (b Theme) Validate() error {
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
	for _, f := range FormatList {
		if a := b.Style(f).Art; !a.valid() {
			errs = append(errs, fmt.Errorf("formats.%s.art: %q is not one of %v", f, a, ArtPlacements))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("theme %q: %w", b.ID, err)
	}
	return nil
}

// ValidateThemes validates each theme and checks that ids are unique. Pass
// built-ins and user themes together, so a user id can't shadow a built-in.
func ValidateThemes(bs []Theme) error {
	seen := map[string]bool{}
	var errs []error
	for _, b := range bs {
		if err := b.Validate(); err != nil {
			errs = append(errs, err)
		}
		if seen[b.ID] {
			errs = append(errs, fmt.Errorf("theme %q: duplicate id", b.ID))
		}
		seen[b.ID] = true
	}
	return errors.Join(errs...)
}

// IsWhite reports whether the background is white, so no bleed is needed.
func (b Theme) IsWhite() bool {
	c, err := ParseHexColor(b.Background)
	return err == nil && c == white
}

// BackgroundColor returns the label fill color. An invalid value gives white.
func (b Theme) BackgroundColor() color.RGBA { return hexOr(b.Background, white) }

// TextColor returns the text color. An invalid value gives black.
func (b Theme) TextColor() color.RGBA { return hexOr(b.Text, black) }

// FadeColor returns the gradient color under text on background art: Fade,
// else black (the script's look: a dark band under light text on any
// background).
func (b Theme) FadeColor() color.RGBA {
	if b.Fade != nil {
		return hexOr(*b.Fade, black)
	}
	return black
}

// FadeTextColor returns the text color over the fade: FadeText, else white.
func (b Theme) FadeTextColor() color.RGBA {
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
