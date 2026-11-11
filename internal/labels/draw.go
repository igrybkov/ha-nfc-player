package labels

import (
	"image"
	"image/color"
	"math"
)

// Rect is a box in points, origin at the top-left of the page, Y down.
type Rect struct{ X, Y, W, H float64 }

// FontStyle selects the embedded font.
type FontStyle int

const (
	Regular FontStyle = iota
	Bold
)

// Canvas is what a label draws on. All units are points, origin top-left.
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

// surface is a Canvas that can also draw a whole slot: the PDF page and the
// PNG preview.
type surface interface {
	Canvas
	artDPI() float64              // art is resampled to this before it is drawn
	fillShape(r Rect, round bool) // r, or the ellipse in r, in the fill color
	pushClip(r Rect, round bool)  // clip to r with rounded corners, or the ellipse in r
	popClip()
}

// bleed is how far the background fill reaches past each side of a label.
type bleed struct{ left, top, right, bottom float64 }

// drawSlot draws one label in slot r: the background fill with bleed for a
// non-white theme, then the label in r shrunk by inset, clipped to the
// label shape.
func drawSlot(c surface, r Rect, b bleed, inset float64, item Item, th Theme, t Template) error {
	round := t.Format.Round()
	if !th.IsWhite() {
		// Bleed past the edge, so printer drift leaves no white strip.
		c.SetFill(th.BackgroundColor())
		c.fillShape(Rect{r.X - b.left, r.Y - b.top, r.W + b.left + b.right, r.H + b.top + b.bottom}, round)
	}
	cell := r.Inset(inset)
	c.pushClip(cell, round)
	c.SetTextColor(th.TextColor())
	err := drawLabel(c, cell, item, th, t)
	c.popClip()
	return err
}

// drawLabel lays out one label in cell with the theme's style for the
// template's format. Text on a round label stays inside the square inscribed
// in the circle, centered.
func drawLabel(c surface, cell Rect, item Item, th Theme, t Template) error {
	style := th.Style(t.Format)
	round := t.Format.Round()
	if item.Art == nil || style.Art == ArtNone {
		return drawTextOnly(c, cell, item, th, t)
	}
	pad := LabelPad
	area := textArea(cell, round)
	dpi := c.artDPI()
	art := func(r Rect) error {
		if r.W < 1 || r.H < 1 {
			return nil
		}
		w, h := artPixels(r.W, dpi), artPixels(r.H, dpi)
		img := ResizeArt(CropToRatio(item.Art, r.W/r.H), w, h)
		if style.Mono {
			Grayscale(img)
		}
		return c.Image(img, r)
	}

	switch style.Art {
	case ArtLeft, ArtRight:
		// Art is a full-height strip, square on a wide label.
		aw := min(cell.H, cell.W/2)
		artRect := Rect{X: cell.X, Y: cell.Y, W: aw, H: cell.H}
		// Text sits beside the art, inside the label. On a round label it
		// may use the chord a quarter height off center, wider than the
		// inscribed square.
		lo, hi := area.X, area.X+area.W
		if round {
			half := cell.W / 2 * math.Sqrt(0.75)
			lo, hi = cell.X+cell.W/2-half, cell.X+cell.W/2+half
		}
		x0, x1 := max(cell.X+aw, lo), hi
		textX := x0 + 1.5*pad
		if style.Art == ArtRight {
			artRect.X = cell.X + cell.W - aw
			x0, x1 = lo, min(artRect.X, hi)
			textX = x0 + pad
		}
		if err := art(artRect); err != nil {
			return err
		}
		block, err := FitTextBlock(c, item.Title, item.Subtitle, x1-x0-2.5*pad, t.Font)
		if err != nil {
			return err
		}
		c.SetTextColor(th.TextColor())
		return block.Draw(c, textX, cell.Y+(cell.H+block.Height())/2)

	case ArtTop, ArtBottom:
		block, err := FitTextBlock(c, item.Title, item.Subtitle, area.W-2*pad, t.Font)
		if err != nil {
			return err
		}
		var bottom float64
		var artRect Rect
		if style.Art == ArtTop {
			bottom = area.Y + area.H - pad
			artRect = Rect{X: cell.X, Y: cell.Y, W: cell.W, H: bottom - block.Height() - pad - cell.Y}
		} else {
			bottom = area.Y + pad + block.Height()
			artRect = Rect{X: cell.X, Y: bottom + pad, W: cell.W, H: cell.Y + cell.H - bottom - pad}
		}
		if err := art(artRect); err != nil {
			return err
		}
		c.SetTextColor(th.TextColor())
		return drawBlock(c, block, area, bottom, round)

	default: // ArtBackground
		block, err := FitTextBlock(c, item.Title, item.Subtitle, area.W-2*pad, t.Font)
		if err != nil {
			return err
		}
		pxW, pxH := artPixels(cell.W, dpi), artPixels(cell.H, dpi)
		img := ResizeArt(CropToRatio(item.Art, cell.W/cell.H), pxW, pxH)
		if style.Mono {
			Grayscale(img)
		}
		bottom := area.Y + area.H - pad
		// The fade covers the text plus some air above it, so it reads on any
		// cover.
		bandPt := block.Height() + 2.5*pad
		if round {
			bandPt = cell.Y + cell.H - (bottom - block.Height()) + 1.5*pad
		}
		band := min(pxH, int(math.Round(bandPt/cell.H*float64(pxH))))
		FadeBottom(img, band, th.FadeColor())
		if err := c.Image(img, cell); err != nil {
			return err
		}
		c.SetTextColor(th.FadeTextColor())
		return drawBlock(c, block, area, bottom, round)
	}
}

// drawTextOnly draws a label with no art. On a rectangle the text block is
// bottom-aligned at LabelPad inside cell (script behavior); on a round label
// it is centered.
func drawTextOnly(c Canvas, cell Rect, item Item, th Theme, t Template) error {
	round := t.Format.Round()
	area := textArea(cell, round)
	block, err := FitTextBlock(c, item.Title, item.Subtitle, area.W-2*LabelPad, t.Font)
	if err != nil {
		return err
	}
	c.SetTextColor(th.TextColor())
	if round {
		return block.DrawCentered(c, area.X+area.W/2, area.Y+(area.H+block.Height())/2)
	}
	return block.Draw(c, area.X+LabelPad, area.Y+area.H-LabelPad)
}

// drawBlock draws block with its bottom at bottom: left-aligned in area, or
// centered on a round label.
func drawBlock(c Canvas, block TextBlock, area Rect, bottom float64, round bool) error {
	if round {
		return block.DrawCentered(c, area.X+area.W/2, bottom)
	}
	return block.Draw(c, area.X+LabelPad, bottom)
}

// textArea is where text may go: the cell, or on a round label the square
// inscribed in the ellipse.
func textArea(cell Rect, round bool) Rect {
	if !round {
		return cell
	}
	w, h := cell.W/math.Sqrt2, cell.H/math.Sqrt2
	return Rect{X: cell.X + (cell.W-w)/2, Y: cell.Y + (cell.H-h)/2, W: w, H: h}
}
