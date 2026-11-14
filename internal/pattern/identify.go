// Package pattern draws test images shown on the monitors.
package pattern

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var (
	bg     = color.RGBA{0x14, 0x16, 0x1c, 0xff}
	grid   = color.RGBA{0x2a, 0x2e, 0x38, 0xff}
	grid5  = color.RGBA{0x3c, 0x42, 0x50, 0xff}
	accent = color.RGBA{0x4c, 0x9a, 0xff, 0xff}
	ink    = color.RGBA{0xf2, 0xf4, 0xf8, 0xff}
	quiet  = color.RGBA{0xa0, 0xa8, 0xb8, 0xff}
)

// Identify draws a full screen card: a grid every 100 px, an accent border,
// a large number and a few lines of detail.
func Identify(w, h, number int, title string, details []string) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	for x := 0; x < w; x += 100 {
		c := grid
		if x%500 == 0 {
			c = grid5
		}
		fill(img, image.Rect(x, 0, x+2, h), c)
	}
	for y := 0; y < h; y += 100 {
		c := grid
		if y%500 == 0 {
			c = grid5
		}
		fill(img, image.Rect(0, y, w, y+2), c)
	}
	b := max(8, h/120)
	fill(img, image.Rect(0, 0, w, b), accent)
	fill(img, image.Rect(0, h-b, w, h), accent)
	fill(img, image.Rect(0, 0, b, h), accent)
	fill(img, image.Rect(w-b, 0, w, h), accent)

	big := h / 26 // number height ~ h/2
	num := itoa(number)
	nw := textWidth(num) * big
	text(img, (w-nw)/2, h/2+big*4, num, big, accent)

	s := max(3, h/300)
	y := h/2 + big*7
	tw := textWidth(title) * s * 2
	text(img, (w-tw)/2, y, title, s*2, ink)
	y += 13*s*2 + 8*s
	for _, d := range details {
		dw := textWidth(d) * s
		text(img, (w-dw)/2, y, d, s, quiet)
		y += 13*s + 4*s
	}
	return img
}

func fill(img *image.RGBA, r image.Rectangle, c color.Color) {
	draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
}

func textWidth(s string) int { return 7 * len(s) }

// text draws s with the 7x13 bitmap font, scaled up by an integer factor.
// baseline is the y of the text's baseline in img.
func text(img *image.RGBA, x, baseline int, s string, scale int, c color.Color) {
	small := image.NewRGBA(image.Rect(0, 0, textWidth(s), 13))
	d := font.Drawer{Dst: small, Src: &image.Uniform{c}, Face: basicfont.Face7x13, Dot: fixed.P(0, 11)}
	d.DrawString(s)
	dst := image.Rect(x, baseline-11*scale, x+small.Bounds().Dx()*scale, baseline+2*scale)
	draw.NearestNeighbor.Scale(img, dst, small, small.Bounds(), draw.Over, nil)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
