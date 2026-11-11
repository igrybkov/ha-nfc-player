package labels

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/igrybkov/ha-nfc-player/assets"
)

// Preview is one label rendered to PNG, as it prints.
type Preview struct {
	Template Template // label size, format and font sizes; the page and grid are not used
	Theme    Theme
	Item     Item
	InsetMM  float64
	PxPerIn  float64 // output resolution; 0 = DefaultPreviewPxPerIn
}

// DefaultPreviewPxPerIn is the preview resolution when none is given.
const DefaultPreviewPxPerIn = 160

// MaxPreviewPxPerIn caps the preview resolution, so one request can't ask for
// a huge image.
const MaxPreviewPxPerIn = 400

// RenderPreview writes p as a PNG: the label shape on a transparent
// background, drawn by the same code as the PDF. Text widths come from the
// PDF library, so lines wrap and shrink exactly as they print.
func RenderPreview(w io.Writer, p Preview) error {
	t := p.Template
	if !t.Format.valid() {
		return fmt.Errorf("format %q is not one of %v", t.Format, FormatList)
	}
	if !(t.Label.WidthIn > 0 && t.Label.HeightIn > 0 && t.Label.WidthIn <= MaxSizeIn && t.Label.HeightIn <= MaxSizeIn) {
		return fmt.Errorf("label size %gx%g in is out of range", t.Label.WidthIn, t.Label.HeightIn)
	}
	if !(t.Font.TitlePt > 0 && t.Font.SubtitlePt > 0) {
		return errors.New("font sizes must be > 0")
	}
	if err := p.Theme.Validate(); err != nil {
		return err
	}
	if !(p.InsetMM >= 0) {
		return errors.New("inset must be >= 0 mm")
	}
	if err := checkInset(t.Label, p.InsetMM); err != nil {
		return err
	}
	ppi := p.PxPerIn
	if ppi <= 0 {
		ppi = DefaultPreviewPxPerIn
	}
	ppi = min(ppi, MaxPreviewPxPerIn)

	label := Rect{W: t.Label.WidthIn * PointsPerInch, H: t.Label.HeightIn * PointsPerInch}
	c, err := newRasterCanvas(label, ppi/PointsPerInch)
	if err != nil {
		return err
	}
	// Paper first: a white theme draws no fill of its own.
	c.SetFill(white)
	c.fillShape(label, t.Format.Round())
	if err := drawSlot(c, label, bleed{}, p.InsetMM*PointsPerMM, p.Item, p.Theme, t); err != nil {
		return err
	}
	// Cut to the label shape: the bleed and paper outside it don't show.
	out := image.NewRGBA(c.img.Bounds())
	draw.DrawMask(out, out.Bounds(), c.img, image.Point{}, c.shapeMask(label, t.Format.Round()), image.Point{}, draw.Over)
	return png.Encode(w, out)
}

// SampleCover returns one of the made-up covers in assets/samples: "pale",
// "dark" or "busy".
func SampleCover(name string) (image.Image, error) {
	switch name {
	case "pale", "dark", "busy":
	default:
		return nil, fmt.Errorf("no sample cover %q", name)
	}
	raw, err := assets.Samples.ReadFile("samples/" + name + ".jpg")
	if err != nil {
		return nil, err
	}
	return jpeg.Decode(bytes.NewReader(raw))
}

// SampleCovers are the names SampleCover takes, pale to busy.
var SampleCovers = []string{"pale", "dark", "busy"}

// rasterCanvas implements surface on an RGBA image. Points map to pixels by
// scale. Fills and images follow the clip; text is not clipped, since the
// layout keeps it inside the label.
type rasterCanvas struct {
	img        *image.RGBA
	scale      float64 // pixels per point
	fill, text color.RGBA
	clip       *image.Alpha // nil = no clip
	style      FontStyle
	size       float64
	faces      map[faceKey]font.Face
}

type faceKey struct {
	style FontStyle
	size  float64
}

var _ surface = (*rasterCanvas)(nil)

func newRasterCanvas(page Rect, scale float64) (*rasterCanvas, error) {
	w, h := max(1, int(math.Ceil(page.W*scale))), max(1, int(math.Ceil(page.H*scale)))
	return &rasterCanvas{
		img:   image.NewRGBA(image.Rect(0, 0, w, h)),
		scale: scale,
		fill:  black,
		text:  black,
		faces: map[faceKey]font.Face{},
	}, nil
}

func (c *rasterCanvas) px(r Rect) image.Rectangle {
	return image.Rect(
		int(math.Round(r.X*c.scale)), int(math.Round(r.Y*c.scale)),
		int(math.Round((r.X+r.W)*c.scale)), int(math.Round((r.Y+r.H)*c.scale)),
	)
}

func (c *rasterCanvas) SetFill(col color.RGBA) { c.fill = col }

func (c *rasterCanvas) Rect(r Rect) {
	c.paint(c.px(r), image.NewUniform(c.fill), image.Point{})
}

func (c *rasterCanvas) Outline(r Rect, widthPt float64) {
	d := widthPt
	for _, side := range []Rect{{r.X, r.Y, r.W, d}, {r.X, r.Y + r.H - d, r.W, d}, {r.X, r.Y, d, r.H}, {r.X + r.W - d, r.Y, d, r.H}} {
		c.Rect(side)
	}
}

func (c *rasterCanvas) Image(img image.Image, r Rect) error {
	dst := c.px(r)
	if dst.Empty() {
		return nil
	}
	scaled := image.NewRGBA(image.Rect(0, 0, dst.Dx(), dst.Dy()))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Src, nil)
	c.paint(dst, scaled, image.Point{})
	return nil
}

// paint draws src over dst through the clip.
func (c *rasterCanvas) paint(dst image.Rectangle, src image.Image, sp image.Point) {
	if c.clip == nil {
		draw.Draw(c.img, dst, src, sp, draw.Over)
		return
	}
	draw.DrawMask(c.img, dst, src, sp, c.clip, dst.Min, draw.Over)
}

func (c *rasterCanvas) SetTextColor(col color.RGBA) { c.text = col }

func (c *rasterCanvas) SetFont(style FontStyle, sizePt float64) error {
	if !(sizePt > 0) {
		return fmt.Errorf("font size %g must be > 0", sizePt)
	}
	c.style, c.size = style, sizePt
	return nil
}

func (c *rasterCanvas) Text(x, baselineY float64, s string) error {
	if c.size == 0 {
		return errNoFont
	}
	f, err := c.face()
	if err != nil {
		return err
	}
	d := font.Drawer{
		Dst:  c.img,
		Src:  image.NewUniform(c.text),
		Face: f,
		Dot:  fixed.Point26_6{X: fixed.Int26_6(math.Round(x * c.scale * 64)), Y: fixed.Int26_6(math.Round(baselineY * c.scale * 64))},
	}
	d.DrawString(s)
	return nil
}

// TextWidth measures with the PDF library, so wrapping matches the PDF.
func (c *rasterCanvas) TextWidth(s string) (float64, error) {
	if c.size == 0 {
		return 0, errNoFont
	}
	return measureText(c.style, c.size, s)
}

func (c *rasterCanvas) face() (font.Face, error) {
	k := faceKey{c.style, c.size}
	if f, ok := c.faces[k]; ok {
		return f, nil
	}
	fonts, err := rasterFonts()
	if err != nil {
		return nil, err
	}
	f, err := opentype.NewFace(fonts[c.style], &opentype.FaceOptions{Size: c.size * c.scale, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	c.faces[k] = f
	return f, nil
}

func (c *rasterCanvas) artDPI() float64 { return c.scale * PointsPerInch }

func (c *rasterCanvas) fillShape(r Rect, round bool) {
	draw.DrawMask(c.img, c.img.Bounds(), image.NewUniform(c.fill), image.Point{}, c.shapeMask(r, round), image.Point{}, draw.Over)
}

// pushClip sets the clip. Labels never nest clips, so there is one level.
func (c *rasterCanvas) pushClip(r Rect, round bool) { c.clip = c.shapeMask(r, round) }

func (c *rasterCanvas) popClip() { c.clip = nil }

// shapeMask is an antialiased mask of the label shape in r, the same polygon
// the PDF uses.
func (c *rasterCanvas) shapeMask(r Rect, round bool) *image.Alpha {
	b := c.img.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	for i, p := range shapePoints(r, round) {
		x, y := float32(p.X*c.scale), float32(p.Y*c.scale)
		if i == 0 {
			z.MoveTo(x, y)
		} else {
			z.LineTo(x, y)
		}
	}
	z.ClosePath()
	mask := image.NewAlpha(b)
	z.Draw(mask, b, image.Opaque, image.Point{})
	return mask
}

// rasterFonts parses the embedded fonts once.
var rasterFonts = sync.OnceValues(func() (map[FontStyle]*opentype.Font, error) {
	out := map[FontStyle]*opentype.Font{}
	for style, file := range map[FontStyle]string{Regular: fontFiles[fontRegularAlias], Bold: fontFiles[fontBoldAlias]} {
		data, err := assets.Fonts.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("font %s: %w", file, err)
		}
		f, err := opentype.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("font %s: %w", file, err)
		}
		out[style] = f
	}
	return out, nil
})

// measurer is one PDF canvas kept for measuring text, so previews wrap
// lines with the PDF's own widths.
var (
	measureMu sync.Mutex
	measurer  = sync.OnceValues(func() (*pdfCanvas, error) { return newPDFCanvas(Size{WidthIn: 1, HeightIn: 1}) })
)

func measureText(style FontStyle, size float64, s string) (float64, error) {
	m, err := measurer()
	if err != nil {
		return 0, err
	}
	measureMu.Lock()
	defer measureMu.Unlock()
	if err := m.SetFont(style, size); err != nil {
		return 0, err
	}
	return m.TextWidth(s)
}
