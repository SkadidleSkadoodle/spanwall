package render

import (
	"errors"
	"image"
	"image/draw"

	xdraw "golang.org/x/image/draw"
	"math"
	"runtime"
	"sync"

	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
)

// Mapper maps slice pixels to canvas pixels, for monitors with a
// photo-measured shape.
type Mapper interface {
	Map(x, y float64) (float64, float64)
}

// Warp is a perspective transform (row-major 3x3).
type Warp [9]float64

// Map implements Mapper.
func (w *Warp) Map(x, y float64) (float64, float64) {
	d := w[6]*x + w[7]*y + w[8]
	return (w[0]*x + w[1]*y + w[2]) / d, (w[3]*x + w[4]*y + w[5]) / d
}

// MeshWarp interpolates a grid of canvas points spread evenly over the
// slice (NX x NY, row by row), for curved screens.
type MeshWarp struct {
	NX, NY int
	W, H   float64   // slice size in pixels
	Pts    []float64 // canvas x,y per grid point
}

// Map implements Mapper (bilinear inside each cell, extrapolated outside).
func (m *MeshWarp) Map(x, y float64) (float64, float64) {
	u := x / m.W * float64(m.NX-1)
	v := y / m.H * float64(m.NY-1)
	i := min(max(int(u), 0), m.NX-2)
	j := min(max(int(v), 0), m.NY-2)
	fu, fv := u-float64(i), v-float64(j)
	p := func(a, b int) (float64, float64) { k := 2 * (b*m.NX + a); return m.Pts[k], m.Pts[k+1] }
	x00, y00 := p(i, j)
	x10, y10 := p(i+1, j)
	x01, y01 := p(i, j+1)
	x11, y11 := p(i+1, j+1)
	return (x00*(1-fu)+x10*fu)*(1-fv) + (x01*(1-fu)+x11*fu)*fv, (y00*(1-fu)+y10*fu)*(1-fv) + (y01*(1-fu)+y11*fu)*fv
}

// SlicesWarp is Slices, except that displays with a non-nil warp are
// rendered through it instead of their rectangular crop. The crops still
// set the canvas size, so they must cover the warped shapes.
func SlicesWarp(src image.Image, ds []geometry.Display, crops []geometry.Rect, warps []Mapper) ([]*image.RGBA, error) {
	return SlicesFramed(src, ds, crops, warps, Frame{})
}

// SlicesFramed is SlicesWarp with the image zoomed and moved on the canvas
// (fr). Parts of a slice the moved image doesn't cover are black.
func SlicesFramed(src image.Image, ds []geometry.Display, crops []geometry.Rect, warps []Mapper, fr Frame) ([]*image.RGBA, error) {
	if warps == nil {
		warps = make([]Mapper, len(ds))
	}
	if len(warps) != len(ds) || len(crops) != len(ds) {
		return nil, errors.New("render: one crop and one warp (or nil) per display are required")
	}
	anyWarp := false
	for _, w := range warps {
		anyWarp = anyWarp || w != nil
	}
	if !anyWarp && fr.Identity() {
		return Slices(src, ds, crops) // exact Superpaper path
	}
	cw, ch := geometry.Canvas(crops)
	b := src.Bounds()
	f, err := FrameFill(b.Dx(), b.Dy(), cw, ch, fr)
	if err != nil {
		return nil, err
	}
	out := make([]*image.RGBA, len(ds))
	var rgba *image.RGBA
	for i := range ds {
		dst := image.NewRGBA(image.Rect(0, 0, ds[i].ResW, ds[i].ResH))
		if !fr.Identity() {
			draw.Draw(dst, dst.Rect, image.Black, image.Point{}, draw.Src)
		}
		if warps[i] == nil {
			m := f.SourceToSlice(crops[i], ds[i], b.Min)
			parallelBands(dst, func(sub *image.RGBA) { Lanczos3.Transform(sub, m, src, b, xdraw.Src, nil) })
		} else {
			if rgba == nil {
				var ok bool
				if rgba, ok = src.(*image.RGBA); !ok {
					rgba = image.NewRGBA(b)
					draw.Draw(rgba, b, src, b.Min, draw.Src)
				}
			}
			warpInto(dst, rgba, f, warps[i])
		}
		out[i] = dst
	}
	return out, nil
}

// Frame zooms and moves the image on the canvas. Zoom is relative to the
// default fill (0 or 1 = fill); DX, DY move the image's centre by that
// fraction of its displayed width and height.
type Frame struct {
	Zoom float64 `json:"zoom"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
}

// Identity reports whether fr leaves the default fill unchanged.
func (fr Frame) Identity() bool {
	return (fr.Zoom == 0 || fr.Zoom == 1) && fr.DX == 0 && fr.DY == 0
}

// FrameFill is FillTransform with fr applied.
func FrameFill(srcW, srcH, cw, ch int, fr Frame) (Fill, error) {
	if fr.Identity() {
		return FillTransform(srcW, srcH, cw, ch)
	}
	if srcW <= 0 || srcH <= 0 || cw <= 0 || ch <= 0 {
		return Fill{}, errors.New("render: empty source or canvas")
	}
	z := fr.Zoom
	if z <= 0 {
		z = 1
	}
	m := math.Max(float64(cw)/float64(srcW), float64(ch)/float64(srcH)) * z
	dw, dh := float64(srcW)*m, float64(srcH)*m
	x0 := float64(cw)/2 - dw/2 + fr.DX*dw
	y0 := float64(ch)/2 - dh/2 + fr.DY*dh
	return Fill{SX: m, SY: m, OffX: -x0, OffY: -y0}, nil
}

func parallelBands(dst *image.RGBA, fn func(*image.RGBA)) {
	const band = 48
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for y := dst.Rect.Min.Y; y < dst.Rect.Max.Y; y += band {
		sub := dst.SubImage(image.Rect(dst.Rect.Min.X, y, dst.Rect.Max.X, min(y+band, dst.Rect.Max.Y))).(*image.RGBA)
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			fn(sub)
			<-sem
		}()
	}
	wg.Wait()
}

// warpInto fills dst: slice pixel -> canvas (w) -> source (fill), with
// bilinear samples, supersampled where the source is denser than the slice.
func warpInto(dst, src *image.RGBA, f Fill, w Mapper) {
	b := src.Rect
	toSrc := func(x, y float64) (float64, float64) {
		cx, cy := w.Map(x, y)
		return (cx+f.OffX)/f.SX + float64(b.Min.X), (cy+f.OffY)/f.SY + float64(b.Min.Y)
	}
	// Source pixels per slice pixel at the centre sets the supersampling.
	W, H := float64(dst.Rect.Dx()), float64(dst.Rect.Dy())
	x0, y0 := toSrc(W/2, H/2)
	x1, y1 := toSrc(W/2+1, H/2+1)
	n := int(math.Ceil(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))))
	n = max(1, min(n, 3))
	sample := func(sx, sy float64) (r, g, bl float64) {
		if sx < float64(b.Min.X)-0.5 || sy < float64(b.Min.Y)-0.5 || sx > float64(b.Max.X)+0.5 || sy > float64(b.Max.Y)+0.5 {
			return 0, 0, 0 // outside a moved image: black
		}
		sx -= 0.5
		sy -= 0.5
		ix, iy := int(math.Floor(sx)), int(math.Floor(sy))
		fx, fy := sx-float64(ix), sy-float64(iy)
		get := func(x, y int) (float64, float64, float64) {
			x = min(max(x, b.Min.X), b.Max.X-1)
			y = min(max(y, b.Min.Y), b.Max.Y-1)
			o := src.PixOffset(x, y)
			return float64(src.Pix[o]), float64(src.Pix[o+1]), float64(src.Pix[o+2])
		}
		r00, g00, b00 := get(ix, iy)
		r10, g10, b10 := get(ix+1, iy)
		r01, g01, b01 := get(ix, iy+1)
		r11, g11, b11 := get(ix+1, iy+1)
		l := func(a, b, c, d float64) float64 {
			return (a*(1-fx)+b*fx)*(1-fy) + (c*(1-fx)+d*fx)*fy
		}
		return l(r00, r10, r01, r11), l(g00, g10, g01, g11), l(b00, b10, b01, b11)
	}
	parallelBands(dst, func(sub *image.RGBA) {
		inv := 1 / float64(n*n)
		for y := sub.Rect.Min.Y; y < sub.Rect.Max.Y; y++ {
			o := sub.PixOffset(sub.Rect.Min.X, y)
			for x := sub.Rect.Min.X; x < sub.Rect.Max.X; x++ {
				var r, g, bl float64
				for j := 0; j < n; j++ {
					for i := 0; i < n; i++ {
						sx, sy := toSrc(float64(x)+(float64(i)+0.5)/float64(n), float64(y)+(float64(j)+0.5)/float64(n))
						a, c, d := sample(sx, sy)
						r, g, bl = r+a, g+c, bl+d
					}
				}
				sub.Pix[o] = uint8(math.Min(255, r*inv+0.5))
				sub.Pix[o+1] = uint8(math.Min(255, g*inv+0.5))
				sub.Pix[o+2] = uint8(math.Min(255, bl*inv+0.5))
				sub.Pix[o+3] = 255
				o += 4
			}
		}
	})
}
