// Package layouts holds the built-in label layouts. Each registers itself
// with labels.RegisterLayout in init; blank-import this package to use them.
// To add a layout, add one file here; see the labels package doc.
//
// Owner: track B (docs/PLAN.md section 10).
package layouts

import "github.com/igrybkov/ha-nfc-player/internal/labels"

func init() { labels.RegisterLayout(side{}) }

// side: square art flush left, text block vertically centered to the right.
type side struct{}

func (side) ID() string   { return "side" }
func (side) Name() string { return "Art left, text right" }

func (side) Draw(c labels.Canvas, cell labels.Rect, item labels.Item, bg labels.Background, t labels.Template) error {
	if item.Art == nil {
		return labels.DrawTextOnly(c, cell, item, bg, t)
	}
	size := cell.H
	px := labels.ArtPixels(size)
	art := labels.ResizeArt(labels.CropToRatio(item.Art, 1), px, px)
	if err := c.Image(art, labels.Rect{X: cell.X, Y: cell.Y, W: size, H: size}); err != nil {
		return err
	}

	textX := cell.X + size + 1.5*labels.LabelPad
	textW := cell.W - size - 2.5*labels.LabelPad
	block, err := labels.FitTextBlock(c, item.Title, item.Subtitle, textW, t.Font)
	if err != nil {
		return err
	}
	c.SetTextColor(bg.TextColor())
	return block.Draw(c, textX, cell.Y+(size+block.Height())/2)
}
