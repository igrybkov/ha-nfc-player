package labels

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"

	"github.com/signintech/gopdf"

	"github.com/igrybkov/ha-nfc-player/assets"
)

// Geometry from the script.
const (
	cornerRadiusPt    = 0.04 * PointsPerInch // inset clip and outline corners
	outlineWidthPt    = 0.3
	cornerArcPoints   = 8  // polygon points per rounded corner
	ellipsePointCount = 96 // polygon points per round label
	artJPEGQuality    = 90 // art is DCT-passed into the PDF; PNG at 600 dpi is huge
	fontRegularAlias  = "regular"
	fontBoldAlias     = "bold"
)

var outlineGray = color.RGBA{153, 153, 153, 0xFF} // script: Color(0.6, 0.6, 0.6)

// Render writes the job as a PDF to w. It validates the template, the
// theme and the job first. Each label is drawn with the theme's style for
// the template's format inside the slot shrunk by InsetMM, clipped to a
// rounded rect (an ellipse for dots). Non-white themes bleed BleedMM past
// the label left, right and top; downward only when the slot below is
// printed in this job or the label is in the last row, else EdgeBleedMM.
// The first and last columns bleed OuterBleedMM more toward the paper edge.
// Outline adds a gray outline on every slot of every page, so an outline
// job with no items prints one page of outlines.
func Render(w io.Writer, job Job) error {
	t := job.Template
	if err := t.Validate(); err != nil {
		return err
	}
	if err := job.Theme.Validate(); err != nil {
		return err
	}
	if err := job.validate(t); err != nil {
		return fmt.Errorf("print job: %w", err)
	}
	inset := job.InsetMM * PointsPerMM
	if err := checkInset(t.Label, job.InsetMM); err != nil {
		return err
	}

	c, err := newPDFCanvas(t.Page)
	if err != nil {
		return err
	}
	bleedPt := job.BleedMM * PointsPerMM
	placements := place(t, job)
	next := 0
	for page := range pageCount(t, job) {
		c.pdf.AddPage()
		for ; next < len(placements) && placements[next].page == page; next++ {
			p := placements[next]
			b := bleed{bleedPt, bleedPt, bleedPt, p.bottomBleedMM * PointsPerMM}
			if p.col == 0 {
				b.left += job.OuterBleedMM * PointsPerMM
			}
			if p.col == t.Grid.Cols-1 {
				b.right += job.OuterBleedMM * PointsPerMM
			}
			if err := drawSlot(c, slotRect(t, job.Offset, p.row, p.col), b, inset, *p.item, job.Theme, t); err != nil {
				return fmt.Errorf("label %q (page %d, row %d, column %d): %w", p.item.Title, page+1, p.row+1, p.col+1, err)
			}
		}
		if job.Outline {
			for row := range t.Grid.Rows {
				for col := range t.Grid.Cols {
					c.shapeOutline(slotRect(t, job.Offset, row, col), t.Format.Round(), outlineGray)
				}
			}
		}
	}
	var raw bytes.Buffer
	if _, err := c.pdf.WriteTo(&raw); err != nil {
		return fmt.Errorf("write pdf: %w", err)
	}
	fonts, err := fullFonts()
	if err != nil {
		return err
	}
	out, err := embedFullFonts(raw.Bytes(), fonts)
	if err != nil {
		return err
	}
	if _, err := w.Write(out); err != nil {
		return fmt.Errorf("write pdf: %w", err)
	}
	return nil
}

// pdfCanvas implements Canvas on gopdf. Fonts come from assets.Fonts.
//
// gopdf notes:
//   - gopdf embeds font subsets that some printers drop without a word.
//     Render swaps in the full fonts after gopdf writes; see fontembed.go.
//   - ImageFrom re-encodes to PNG and failed on a decoded JPEG (*image.YCbCr,
//     "16-bit depth not supported"). Image encodes JPEG and passes the bytes
//     through gopdf.ImageHolderByBytes instead (DCT passthrough, much smaller
//     at 600 dpi). gopdf dedupes images by md5 of the bytes, so copies of a
//     label share one image object.
//   - Text and fills share the PDF non-stroking color, and a clip restore
//     resets it. Rect sets the fill color again on every call.
//   - gopdf has no gradient API; layouts composite the fade in Go.
type pdfCanvas struct {
	pdf    *gopdf.GoPdf
	fill   color.RGBA
	family string // current font alias, for TextWidth
}

var _ Canvas = (*pdfCanvas)(nil)

func newPDFCanvas(page Size) (*pdfCanvas, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{
		Unit:     gopdf.UnitPT,
		PageSize: gopdf.Rect{W: page.WidthIn * PointsPerInch, H: page.HeightIn * PointsPerInch},
	})
	for alias, file := range fontFiles {
		data, err := assets.Fonts.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("font %s: %w", file, err)
		}
		if err := pdf.AddTTFFontData(alias, data); err != nil {
			return nil, fmt.Errorf("font %s: %w", file, err)
		}
	}
	return &pdfCanvas{pdf: pdf, fill: black}, nil
}

func (c *pdfCanvas) SetFill(col color.RGBA) { c.fill = col }

func (c *pdfCanvas) Rect(r Rect) {
	c.pdf.SetFillColor(c.fill.R, c.fill.G, c.fill.B)
	c.pdf.RectFromUpperLeftWithStyle(r.X, r.Y, r.W, r.H, "F")
}

// Outline strokes r with the current fill color.
func (c *pdfCanvas) Outline(r Rect, widthPt float64) {
	c.pdf.SetStrokeColor(c.fill.R, c.fill.G, c.fill.B)
	c.pdf.SetLineWidth(widthPt)
	c.pdf.RectFromUpperLeftWithStyle(r.X, r.Y, r.W, r.H, "D")
}

// shapeOutline strokes the label shape: a rounded rect, or an ellipse.
func (c *pdfCanvas) shapeOutline(r Rect, round bool, col color.RGBA) {
	c.pdf.SetStrokeColor(col.R, col.G, col.B)
	c.pdf.SetLineWidth(outlineWidthPt)
	c.pdf.Polygon(shapePoints(r, round), "D")
}

func (c *pdfCanvas) artDPI() float64 { return ArtDPI }

func (c *pdfCanvas) fillShape(r Rect, round bool) {
	if !round {
		c.Rect(r)
		return
	}
	c.pdf.SetFillColor(c.fill.R, c.fill.G, c.fill.B)
	c.pdf.Polygon(ellipsePoints(r), "F")
}

func (c *pdfCanvas) Image(img image.Image, r Rect) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: artJPEGQuality}); err != nil {
		return fmt.Errorf("encode art: %w", err)
	}
	holder, err := gopdf.ImageHolderByBytes(buf.Bytes())
	if err != nil {
		return err
	}
	return c.pdf.ImageByHolder(holder, r.X, r.Y, &gopdf.Rect{W: r.W, H: r.H})
}

func (c *pdfCanvas) SetTextColor(col color.RGBA) { c.pdf.SetTextColor(col.R, col.G, col.B) }

func (c *pdfCanvas) SetFont(style FontStyle, sizePt float64) error {
	family := fontRegularAlias
	if style == Bold {
		family = fontBoldAlias
	}
	if !(sizePt > 0) {
		return fmt.Errorf("font size %g must be > 0", sizePt)
	}
	if err := c.pdf.SetFontWithStyle(family, gopdf.Regular, sizePt); err != nil {
		return err
	}
	c.family = family
	return nil
}

func (c *pdfCanvas) Text(x, baselineY float64, s string) error {
	if c.family == "" {
		return errNoFont
	}
	c.pdf.SetXY(x, baselineY)
	return c.pdf.Text(s)
}

func (c *pdfCanvas) TextWidth(s string) (float64, error) {
	if c.family == "" {
		return 0, errNoFont
	}
	return c.pdf.MeasureTextWidth(s)
}

var errNoFont = errors.New("labels: SetFont must be called before drawing text")

// pushClip clips drawing to r with rounded corners, or to the ellipse in
// r, until popClip.
func (c *pdfCanvas) pushClip(r Rect, round bool) {
	c.pdf.SaveGraphicsState()
	c.pdf.ClipPolygon(shapePoints(r, round))
}

func (c *pdfCanvas) popClip() { c.pdf.RestoreGraphicsState() }

// shapePoints is the outline of a label: the ellipse in r, or r with
// rounded corners.
func shapePoints(r Rect, round bool) []gopdf.Point {
	if round {
		return ellipsePoints(r)
	}
	return roundRectPoints(r, cornerRadiusPt)
}

// ellipsePoints approximates the ellipse in r with a polygon, clockwise
// (Y down).
func ellipsePoints(r Rect) []gopdf.Point {
	pts := make([]gopdf.Point, ellipsePointCount)
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	for i := range pts {
		a := float64(i) / ellipsePointCount * 2 * math.Pi
		pts[i] = gopdf.Point{X: cx + r.W/2*math.Cos(a), Y: cy + r.H/2*math.Sin(a)}
	}
	return pts
}

// roundRectPoints approximates a rounded rect with a polygon, clockwise from
// the top-left corner (Y down).
func roundRectPoints(r Rect, radius float64) []gopdf.Point {
	radius = min(radius, r.W/2, r.H/2)
	corners := []struct{ cx, cy, from float64 }{
		{r.X + radius, r.Y + radius, math.Pi},             // top-left: 180° → 270°
		{r.X + r.W - radius, r.Y + radius, 1.5 * math.Pi}, // top-right
		{r.X + r.W - radius, r.Y + r.H - radius, 0},       // bottom-right
		{r.X + radius, r.Y + r.H - radius, 0.5 * math.Pi}, // bottom-left
	}
	pts := make([]gopdf.Point, 0, 4*(cornerArcPoints+1))
	for _, k := range corners {
		for i := range cornerArcPoints + 1 {
			a := k.from + float64(i)/cornerArcPoints*math.Pi/2
			pts = append(pts, gopdf.Point{X: k.cx + radius*math.Cos(a), Y: k.cy + radius*math.Sin(a)})
		}
	}
	return pts
}

// checkInset fails when the inset leaves no room on the label.
func checkInset(label Size, insetMM float64) error {
	inset := insetMM * PointsPerMM
	if 2*inset >= label.WidthIn*PointsPerInch || 2*inset >= label.HeightIn*PointsPerInch {
		return fmt.Errorf("print job: inset %.2g mm leaves no room on a %gx%g in label", insetMM, label.WidthIn, label.HeightIn)
	}
	return nil
}
