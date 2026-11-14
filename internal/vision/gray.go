package vision

import (
	"image"
	"math"
)

// Gray is a float luminance image.
type Gray struct {
	W, H int
	Pix  []float32
}

// NewGray converts img to luminance, downscaled so its longer side is at
// most maxDim (0 = keep). It returns the factor that maps Gray pixels back
// to img pixels.
func NewGray(img image.Image, maxDim int) (*Gray, float64) {
	src := img
	back := 1.0
	if maxDim > 0 {
		var rgba *image.RGBA
		rgba, back = Downscale(img, maxDim)
		src = rgba
	}
	b := src.Bounds()
	g := &Gray{W: b.Dx(), H: b.Dy(), Pix: make([]float32, b.Dx()*b.Dy())}
	if rgba, ok := src.(*image.RGBA); ok {
		for i := range g.Pix {
			p := rgba.Pix[i*4 : i*4+3]
			g.Pix[i] = 0.299*float32(p[0]) + 0.587*float32(p[1]) + 0.114*float32(p[2])
		}
		return g, back
	}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			r, gg, bb, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			g.Pix[y*g.W+x] = (0.299*float32(r) + 0.587*float32(gg) + 0.114*float32(bb)) / 257
		}
	}
	return g, back
}

// At samples with bilinear interpolation; ok is false outside the image.
func (g *Gray) At(x, y float64) (float32, bool) {
	if x < 0 || y < 0 || x > float64(g.W-1) || y > float64(g.H-1) {
		return 0, false
	}
	x0, y0 := int(x), int(y)
	x1, y1 := min(x0+1, g.W-1), min(y0+1, g.H-1)
	fx, fy := float32(x-float64(x0)), float32(y-float64(y0))
	a := g.Pix[y0*g.W+x0]*(1-fx) + g.Pix[y0*g.W+x1]*fx
	b := g.Pix[y1*g.W+x0]*(1-fx) + g.Pix[y1*g.W+x1]*fx
	return a*(1-fy) + b*fy, true
}

// Grad samples the gradient with central differences.
func (g *Gray) Grad(x, y float64) (gx, gy float32, ok bool) {
	l, ok1 := g.At(x-1, y)
	r, ok2 := g.At(x+1, y)
	u, ok3 := g.At(x, y-1)
	d, ok4 := g.At(x, y+1)
	if !(ok1 && ok2 && ok3 && ok4) {
		return 0, 0, false
	}
	return (r - l) / 2, (d - u) / 2, true
}

// Blur applies three box blurs (close to a Gaussian with sigma ~ r).
func (g *Gray) Blur(r int) *Gray {
	if r < 1 {
		return g
	}
	out := &Gray{W: g.W, H: g.H, Pix: append([]float32(nil), g.Pix...)}
	tmp := make([]float32, len(out.Pix))
	for pass := 0; pass < 3; pass++ {
		boxH(out.Pix, tmp, g.W, g.H, r)
		boxV(tmp, out.Pix, g.W, g.H, r)
	}
	return out
}

func boxH(src, dst []float32, w, h, r int) {
	for y := 0; y < h; y++ {
		row := src[y*w : (y+1)*w]
		var s float32
		n := 0
		for x := -r; x < w+r; x++ {
			if x+r < w {
				s += row[x+r]
				n++
			}
			if x-r-1 >= 0 {
				s -= row[x-r-1]
				n--
			}
			if x >= 0 && x < w {
				dst[y*w+x] = s / float32(n)
			}
		}
	}
}

func boxV(src, dst []float32, w, h, r int) {
	for x := 0; x < w; x++ {
		var s float32
		n := 0
		for y := -r; y < h+r; y++ {
			if y+r < h {
				s += src[(y+r)*w+x]
				n++
			}
			if y-r-1 >= 0 {
				s -= src[(y-r-1)*w+x]
				n--
			}
			if y >= 0 && y < h {
				dst[y*w+x] = s / float32(n)
			}
		}
	}
}

// Half returns the image at half size (2x2 average).
func (g *Gray) Half() *Gray {
	out := &Gray{W: g.W / 2, H: g.H / 2}
	out.Pix = make([]float32, out.W*out.H)
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			i := 2*y*g.W + 2*x
			out.Pix[y*out.W+x] = (g.Pix[i] + g.Pix[i+1] + g.Pix[i+g.W] + g.Pix[i+g.W+1]) / 4
		}
	}
	return out
}

// ncc is the normalized cross-correlation of paired samples.
func ncc(a, b []float32) float64 {
	n := float64(len(a))
	if n < 10 {
		return 0
	}
	var sa, sb float64
	for i := range a {
		sa += float64(a[i])
		sb += float64(b[i])
	}
	ma, mb := sa/n, sb/n
	var cov, va, vb float64
	for i := range a {
		da, db := float64(a[i])-ma, float64(b[i])-mb
		cov += da * db
		va += da * da
		vb += db * db
	}
	if va == 0 || vb == 0 {
		return 0
	}
	return cov / math.Sqrt(va*vb)
}
