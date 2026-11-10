package labels

import (
	"image"
	"image/color"
	"io"

	"github.com/signintech/gopdf"
)

// Render writes the job as a PDF to w.
func Render(w io.Writer, job Job) error { panic("TODO: track B") }

// pdfCanvas implements Canvas on gopdf. Fonts come from assets.Fonts.
//
// gopdf note (phase 0 smoke test): ImageFrom re-encodes to PNG. It works on an
// *image.RGBA but failed on a decoded JPEG (*image.YCbCr, "16-bit depth not
// supported"). Draw into *image.RGBA first, or pass JPEG bytes through
// gopdf.ImageHolderByBytes (DCT passthrough, much smaller at 600 dpi).
// gopdf has no gradient API; composite the fade in Go.
type pdfCanvas struct {
	pdf *gopdf.GoPdf
}

var _ Canvas = (*pdfCanvas)(nil)

func (c *pdfCanvas) SetFill(col color.RGBA) { c.pdf.SetFillColor(col.R, col.G, col.B) }

func (c *pdfCanvas) Rect(r Rect) { c.pdf.RectFromUpperLeftWithStyle(r.X, r.Y, r.W, r.H, "F") }

func (c *pdfCanvas) Outline(r Rect, widthPt float64) { panic("TODO: track B") }

func (c *pdfCanvas) Image(img image.Image, r Rect) error { panic("TODO: track B") }

func (c *pdfCanvas) SetTextColor(col color.RGBA) { c.pdf.SetTextColor(col.R, col.G, col.B) }

func (c *pdfCanvas) SetFont(style FontStyle, sizePt float64) error { panic("TODO: track B") }

func (c *pdfCanvas) Text(x, baselineY float64, s string) error { panic("TODO: track B") }

func (c *pdfCanvas) TextWidth(s string) (float64, error) { return c.pdf.MeasureTextWidth(s) }
