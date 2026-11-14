package vision

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Outline is a monitor to draw on the review overlay.
type Outline struct {
	Label   string
	H       *H // monitor pixels -> photo pixels; nil = not found
	W, Hgt  int
	Palette int
}

// Overlay draws the detected screens on a downscaled, darkened copy of the
// photo so a person can check the result at a glance.
func Overlay(photo image.Image, outs []Outline, blobs []Blob, width int) *image.RGBA {
	b := photo.Bounds()
	s := float64(width) / float64(b.Dx())
	h := int(float64(b.Dy()) * s)
	img := image.NewRGBA(image.Rect(0, 0, width, h))
	draw.ApproxBiLinear.Scale(img, img.Bounds(), photo, b, draw.Src, nil)
	for i := range img.Pix {
		if i%4 != 3 {
			img.Pix[i] = uint8(float64(img.Pix[i]) * 0.55)
		}
	}
	for _, bl := range blobs {
		c := color.RGBA{200, 200, 200, 255}
		if bl.Class >= 0 {
			c = Palette[bl.Class]
		}
		ring(img, bl.C.X*s, bl.C.Y*s, math.Max(3, math.Sqrt(float64(bl.Area))*0.6*s+2), c)
	}
	for _, o := range outs {
		if o.H == nil {
			continue
		}
		c := Palette[o.Palette%len(Palette)]
		corners := []Pt{{0, 0}, {float64(o.W), 0}, {float64(o.W), float64(o.Hgt)}, {0, float64(o.Hgt)}}
		var ps []Pt
		for _, k := range corners {
			p := o.H.Apply(k)
			ps = append(ps, Pt{p.X * s, p.Y * s})
		}
		for i := range ps {
			line(img, ps[i], ps[(i+1)%4], c, 3)
		}
		ctr := o.H.Apply(Pt{float64(o.W) / 2, float64(o.Hgt) / 2})
		label(img, int(ctr.X*s)-len(o.Label)*7, int(ctr.Y*s), o.Label, 2, c)
	}
	return img
}

func line(img *image.RGBA, a, b Pt, c color.RGBA, t int) {
	n := int(math.Hypot(b.X-a.X, b.Y-a.Y)) + 1
	for i := 0; i <= n; i++ {
		f := float64(i) / float64(n)
		x, y := int(a.X+(b.X-a.X)*f), int(a.Y+(b.Y-a.Y)*f)
		for dy := -t / 2; dy <= t/2; dy++ {
			for dx := -t / 2; dx <= t/2; dx++ {
				if image.Pt(x+dx, y+dy).In(img.Bounds()) {
					img.SetRGBA(x+dx, y+dy, c)
				}
			}
		}
	}
}

func ring(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	for a := 0.0; a < 2*math.Pi; a += 0.5 / r {
		x, y := int(cx+r*math.Cos(a)), int(cy+r*math.Sin(a))
		if image.Pt(x, y).In(img.Bounds()) {
			img.SetRGBA(x, y, c)
		}
	}
}

func label(img *image.RGBA, x, y int, s string, scale int, c color.RGBA) {
	small := image.NewRGBA(image.Rect(0, 0, 7*len(s)+2, 15))
	draw.Draw(small, small.Bounds(), &image.Uniform{color.RGBA{0, 0, 0, 200}}, image.Point{}, draw.Src)
	d := font.Drawer{Dst: small, Src: &image.Uniform{c}, Face: basicfont.Face7x13, Dot: fixed.P(1, 12)}
	d.DrawString(s)
	dst := image.Rect(x, y-12*scale, x+small.Bounds().Dx()*scale, y+3*scale)
	draw.NearestNeighbor.Scale(img, dst, small, small.Bounds(), draw.Over, nil)
}
