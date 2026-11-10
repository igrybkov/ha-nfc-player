package labels

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
)

// ArtDPI is the resolution art is resampled to before it goes into the PDF.
const ArtDPI = 600

// ArtPixels converts a length in points to pixels at ArtDPI.
func ArtPixels(pt float64) int { return max(1, int(math.Round(pt/PointsPerInch*ArtDPI))) }

// CropToRatio center-crops img to width/height = ratio (script: crop_to).
func CropToRatio(img image.Image, ratio float64) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var r image.Rectangle
	if float64(w)/float64(h) > ratio {
		nw := int(math.Round(float64(h) * ratio))
		x := b.Min.X + (w-nw)/2
		r = image.Rect(x, b.Min.Y, x+nw, b.Max.Y)
	} else {
		nh := int(math.Round(float64(w) / ratio))
		y := b.Min.Y + (h-nh)/2
		r = image.Rect(b.Min.X, y, b.Max.X, y+nh)
	}
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
	return out
}

// ResizeArt resamples img to w×h pixels into a new RGBA image.
func ResizeArt(img image.Image, w, h int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(out, out.Bounds(), img, img.Bounds(), draw.Src, nil)
	return out
}

// fadeMaxAlpha and fadeGamma shape the full layout gradient (script: 190, 0.8).
const (
	fadeMaxAlpha = 190
	fadeGamma    = 0.8
)

// FadeBottom blends the bottom band rows of img toward col, from clear at
// the top of the band to fadeMaxAlpha/255 at the bottom. It works in place,
// so the PDF gets one opaque image with no alpha bands.
func FadeBottom(img *image.RGBA, band int, col color.RGBA) {
	b := img.Bounds()
	band = min(band, b.Dy())
	for i := range band {
		a := uint32(math.Round(fadeMaxAlpha * math.Pow(float64(i)/float64(band), fadeGamma)))
		if a == 0 {
			continue
		}
		y := b.Max.Y - band + i
		row := img.Pix[img.PixOffset(b.Min.X, y):img.PixOffset(b.Max.X, y)]
		for x := 0; x < len(row); x += 4 {
			row[x+0] = blend(row[x+0], col.R, a)
			row[x+1] = blend(row[x+1], col.G, a)
			row[x+2] = blend(row[x+2], col.B, a)
			row[x+3] = 0xFF
		}
	}
}

func blend(under, over uint8, a uint32) uint8 {
	return uint8((uint32(under)*(255-a) + uint32(over)*a + 127) / 255)
}
