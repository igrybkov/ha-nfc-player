//go:build ignore

// gen draws the sample album covers the theme previews use: one pale, one
// dark and one busy, so a preview shows how text and fades read on each.
// They are made up; regenerate with:
//
//	go run assets/samples/gen.go
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const size = 600

func main() {
	bold := face("assets/fonts/NotoSans-Bold.ttf", 64)
	small := face("assets/fonts/NotoSans-Regular.ttf", 26)

	write("pale", cover(func(x, y float64) color.RGBA {
		// Cream to soft pink, a pale sun.
		c := mix(rgb(0xFB, 0xF1, 0xDC), rgb(0xF6, 0xC9, 0xD0), y)
		if d := math.Hypot(x-0.68, y-0.3); d < 0.18 {
			c = mix(c, rgb(0xFF, 0xE3, 0x9A), 0.8)
		}
		return c
	}), bold, small, rgb(0x3A, 0x2E, 0x39))

	write("dark", cover(func(x, y float64) color.RGBA {
		// Night blue with rings.
		c := mix(rgb(0x0B, 0x10, 0x26), rgb(0x1D, 0x22, 0x4A), x*y)
		if r := math.Hypot(x-0.5, y-0.42); math.Mod(r, 0.09) < 0.012 && r < 0.4 {
			c = mix(c, rgb(0x6C, 0x7C, 0xD8), 0.6)
		}
		return c
	}), bold, small, rgb(0xF2, 0xF2, 0xF7))

	palette := []color.RGBA{rgb(0xE6, 0x39, 0x46), rgb(0xF4, 0xA2, 0x61), rgb(0x2A, 0x9D, 0x8F), rgb(0x26, 0x46, 0x53), rgb(0xE9, 0xC4, 0x6A), rgb(0x8E, 0x44, 0xAD)}
	write("busy", cover(func(x, y float64) color.RGBA {
		// Tilted color blocks.
		i := int(math.Floor((x*3+y*5)*1.4)) + int(math.Floor((x*4-y*2)*1.1))*2
		return palette[((i%len(palette))+len(palette))%len(palette)]
	}), bold, small, rgb(0xFF, 0xFF, 0xFF))
}

func cover(px func(x, y float64) color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.SetRGBA(x, y, px(float64(x)/size, float64(y)/size))
		}
	}
	return img
}

func write(name string, img *image.RGBA, bold, small font.Face, ink color.RGBA) {
	text(img, bold, "ALBUM", 48, 430, ink)
	text(img, bold, "COVER", 48, 500, ink)
	text(img, small, "sample artwork", 52, 552, ink)
	f, err := os.Create(filepath.Join("assets", "samples", name+".jpg"))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
		log.Fatal(err)
	}
}

func text(img draw.Image, f font.Face, s string, x, y int, c color.RGBA) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

func face(path string, size float64) font.Face {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	ft, err := opentype.Parse(data)
	if err != nil {
		log.Fatal(err)
	}
	f, err := opentype.NewFace(ft, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		log.Fatal(err)
	}
	return f
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 0xFF} }

func mix(a, b color.RGBA, t float64) color.RGBA {
	t = min(max(t, 0), 1)
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*(1-t) + float64(y)*t)) }
	return rgb(l(a.R, b.R), l(a.G, b.G), l(a.B, b.B))
}
