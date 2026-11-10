package layouts

import (
	"math"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
)

func init() { labels.RegisterLayout(full{}) }

// full: art fills the label, gradient fade under the text lines at the bottom.
// The fade goes to the background's fade color and the text over it uses its
// fade text color (default black and white, as in the script). A label with
// no art has no fade, so its text uses the plain text color.
type full struct{}

func (full) ID() string   { return "full" }
func (full) Name() string { return "Full art" }

func (full) Draw(c labels.Canvas, cell labels.Rect, item labels.Item, bg labels.Background, t labels.Template) error {
	if item.Art == nil {
		return labels.DrawTextOnly(c, cell, item, bg, t)
	}
	pad := labels.LabelPad
	block, err := labels.FitTextBlock(c, item.Title, item.Subtitle, cell.W-2*pad, t.Font)
	if err != nil {
		return err
	}

	pxW, pxH := labels.ArtPixels(cell.W), labels.ArtPixels(cell.H)
	art := labels.ResizeArt(labels.CropToRatio(item.Art, cell.W/cell.H), pxW, pxH)
	// The fade covers the text plus some air above it, so it reads on any cover.
	band := min(pxH, int(math.Round((block.Height()+2.5*pad)/cell.H*float64(pxH))))
	labels.FadeBottom(art, band, bg.FadeColor())
	if err := c.Image(art, cell); err != nil {
		return err
	}

	c.SetTextColor(bg.FadeTextColor())
	return block.Draw(c, cell.X+pad, cell.Y+cell.H-pad)
}
