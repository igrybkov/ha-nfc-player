package labels

import (
	"strings"
)

// LabelPad is the text padding inside a label, in points (script: pad).
const LabelPad = 0.06 * PointsPerInch

// Text fitting limits from the script.
const (
	maxTitleLines    = 2
	maxSubtitleLines = 2
	minTitlePt       = 5.0
	minSubtitlePt    = 4.5
	fontStepPt       = 0.5
	lineAdvance      = 1.15 // line height as a multiple of the font size
	baselineRise     = 0.2  // baseline sits this much of the size above the line bottom
	ellipsis         = "…"
)

// TextBlock is a title (bold) over a subtitle (regular), already wrapped and
// shrunk to fit a width.
type TextBlock struct {
	Title, Subtitle     []string
	TitlePt, SubtitlePt float64
}

// FitTextBlock wraps title and subtitle into at most two lines each within
// width points. Each shrinks on its own in 0.5 pt steps from the template
// size, and any line still too wide is cut with an ellipsis.
func FitTextBlock(c Canvas, title, subtitle string, width float64, sizes FontSizes) (TextBlock, error) {
	var b TextBlock
	var err error
	if subtitle != "" {
		b.Subtitle, b.SubtitlePt, err = FitLines(c, subtitle, Regular, sizes.SubtitlePt, width, maxSubtitleLines, minSubtitlePt)
		if err != nil {
			return b, err
		}
	}
	b.Title, b.TitlePt, err = FitLines(c, title, Bold, sizes.TitlePt, width, maxTitleLines, minTitlePt)
	return b, err
}

// Height is the block height in points.
func (b TextBlock) Height() float64 {
	return float64(len(b.Subtitle))*b.SubtitlePt*lineAdvance + float64(len(b.Title))*b.TitlePt*lineAdvance
}

// Draw draws the block with its bottom edge at bottomY (Y down), subtitle at
// the bottom and title above it, left-aligned at x, in the current text
// color.
func (b TextBlock) Draw(c Canvas, x, bottomY float64) error {
	return b.draw(c, bottomY, func(string) (float64, error) { return x, nil })
}

// DrawCentered draws the block like Draw, each line centered on centerX.
func (b TextBlock) DrawCentered(c Canvas, centerX, bottomY float64) error {
	return b.draw(c, bottomY, func(line string) (float64, error) {
		w, err := c.TextWidth(line)
		return centerX - w/2, err
	})
}

func (b TextBlock) draw(c Canvas, bottomY float64, lineX func(string) (float64, error)) error {
	cursor := 0.0 // distance above bottomY
	for _, part := range []struct {
		style FontStyle
		lines []string
		size  float64
	}{{Regular, b.Subtitle, b.SubtitlePt}, {Bold, b.Title, b.TitlePt}} {
		if len(part.lines) == 0 {
			continue
		}
		if err := c.SetFont(part.style, part.size); err != nil {
			return err
		}
		for i := len(part.lines) - 1; i >= 0; i-- {
			x, err := lineX(part.lines[i])
			if err != nil {
				return err
			}
			if err := c.Text(x, bottomY-cursor-part.size*baselineRise, part.lines[i]); err != nil {
				return err
			}
			cursor += part.size * lineAdvance
		}
	}
	return nil
}

// FitLines shrinks text until it wraps into maxLines within width; it stops
// at minSize. Lines beyond maxLines are dropped and the last kept one gets an
// ellipsis; any line still too wide is cut to fit with an ellipsis. It
// returns the lines and the final size, and leaves that font set on c.
func FitLines(c Canvas, text string, style FontStyle, size, width float64, maxLines int, minSize float64) ([]string, float64, error) {
	var lines []string
	tooWide := func(s string) (bool, error) {
		w, err := c.TextWidth(s)
		return w > width, err
	}
	for {
		if err := c.SetFont(style, size); err != nil {
			return nil, 0, err
		}
		var err error
		if lines, err = wrap(c, text, width); err != nil {
			return nil, 0, err
		}
		fits := len(lines) <= maxLines
		for _, l := range lines {
			if !fits {
				break
			}
			wide, err := tooWide(l)
			if err != nil {
				return nil, 0, err
			}
			fits = !wide
		}
		if fits || size <= minSize {
			break
		}
		size -= fontStepPt
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] += ellipsis
	}
	for i, line := range lines {
		wide, err := tooWide(line)
		if err != nil {
			return nil, 0, err
		}
		if !wide {
			continue
		}
		r := []rune(line)
		for len(r) > 0 {
			if wide, err = tooWide(string(r) + ellipsis); err != nil {
				return nil, 0, err
			} else if !wide {
				break
			}
			r = r[:len(r)-1]
		}
		lines[i] = strings.TrimRight(string(r), " "+ellipsis) + ellipsis
	}
	return lines, size, nil
}

// wrap breaks text into lines greedily on whitespace, with the current font.
// A word wider than width stays alone on its line (like reportlab's
// simpleSplit); FitLines ellipsizes it.
func wrap(c Canvas, text string, width float64) ([]string, error) {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			continue
		}
		cur := words[0]
		for _, w := range words[1:] {
			cand := cur + " " + w
			cw, err := c.TextWidth(cand)
			if err != nil {
				return nil, err
			}
			if cw <= width {
				cur = cand
				continue
			}
			lines = append(lines, cur)
			cur = w
		}
		lines = append(lines, cur)
	}
	return lines, nil
}
