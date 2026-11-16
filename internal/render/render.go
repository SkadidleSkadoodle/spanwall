// Package render cuts one source image into a slice per display.
//
// Superpaper first resizes the whole image to the canvas, crops it, then
// resizes each crop to the display's native resolution. Spanwall maps each
// display's crop straight back to the source image and resamples once, which
// is sharper (one resample instead of two) and never allocates the full
// canvas, which can be over 40 megapixels on mixed density setups.
package render

import (
	"errors"
	"image"
	"math"
	"runtime"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
)

// Lanczos3 matches the filter Superpaper uses (Pillow's LANCZOS).
var Lanczos3 = &draw.Kernel{
	Support: 3,
	At: func(t float64) float64 {
		if t < 0 {
			t = -t
		}
		if t == 0 {
			return 1
		}
		if t >= 3 {
			return 0
		}
		pt := math.Pi * t
		return 3 * math.Sin(pt) * math.Sin(pt/3) / (pt * pt)
	},
}

// Fill describes how the source maps onto the canvas after a "cover" resize
// with a centered crop, exactly like Superpaper's resize_to_fill:
// canvas (cx, cy) shows source ((cx+OffX)/SX, (cy+OffY)/SY).
type Fill struct {
	SX, SY     float64
	OffX, OffY float64
}

func pyRound(x float64) int { return int(math.RoundToEven(x)) }

// FillTransform reproduces resize_to_fill's resize and crop decisions.
func FillTransform(srcW, srcH, canvasW, canvasH int) (Fill, error) {
	if srcW <= 0 || srcH <= 0 || canvasW <= 0 || canvasH <= 0 {
		return Fill{}, errors.New("render: empty source or canvas")
	}
	if srcW == canvasW && srcH == canvasH {
		return Fill{SX: 1, SY: 1}, nil
	}
	imgRatio := float64(srcW) / float64(srcH)
	tgtRatio := float64(canvasW) / float64(canvasH)
	var mult float64
	if imgRatio < tgtRatio {
		mult = float64(canvasW) / float64(srcW)
	} else {
		mult = float64(canvasH) / float64(srcH)
	}
	newW := pyRound(mult * float64(srcW))
	newH := pyRound(mult * float64(srcH))
	f := Fill{SX: float64(newW) / float64(srcW), SY: float64(newH) / float64(srcH)}
	if imgRatio < tgtRatio {
		f.OffY = float64(pyRound(float64(newH-canvasH) / 2))
	} else {
		f.OffX = float64(pyRound(float64(newW-canvasW) / 2))
	}
	return f, nil
}

// SourceRect is the part of the source image a canvas crop shows.
func (f Fill) SourceRect(c geometry.Rect) image.Rectangle {
	return image.Rect(
		int(math.Round((float64(c.L)+f.OffX)/f.SX)),
		int(math.Round((float64(c.T)+f.OffY)/f.SY)),
		int(math.Round((float64(c.R)+f.OffX)/f.SX)),
		int(math.Round((float64(c.B)+f.OffY)/f.SY)),
	)
}

// Slices renders one image per display at its native resolution.
func Slices(src image.Image, ds []geometry.Display, crops []geometry.Rect) ([]*image.RGBA, error) {
	if len(ds) != len(crops) {
		return nil, errors.New("render: one crop per display is required")
	}
	cw, ch := geometry.Canvas(crops)
	b := src.Bounds()
	f, err := FillTransform(b.Dx(), b.Dy(), cw, ch)
	if err != nil {
		return nil, err
	}
	out := make([]*image.RGBA, len(ds))
	type job struct {
		dst *image.RGBA
		m   f64.Aff3
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				Lanczos3.Transform(j.dst, j.m, src, b, draw.Src, nil)
			}
		}()
	}
	// Each slice is cut into bands of rows; bands are independent because
	// Transform works in absolute destination coordinates.
	const band = 48
	for i, d := range ds {
		dst := image.NewRGBA(image.Rect(0, 0, d.ResW, d.ResH))
		out[i] = dst
		m := f.SourceToSlice(crops[i], d, b.Min)
		for y := 0; y < d.ResH; y += band {
			sub := dst.SubImage(image.Rect(0, y, d.ResW, min(y+band, d.ResH))).(*image.RGBA)
			jobs <- job{sub, m}
		}
	}
	close(jobs)
	wg.Wait()
	return out, nil
}

// SourceToSlice is the exact affine map from source pixels to a display's
// slice pixels, so there is no rounding to whole source pixels:
// canvas = source*S - Off, slice = (canvas - crop corner) * native/crop size.
func (f Fill) SourceToSlice(c geometry.Rect, d geometry.Display, srcMin image.Point) f64.Aff3 {
	kx := float64(d.ResW) / float64(c.W())
	ky := float64(d.ResH) / float64(c.H())
	return f64.Aff3{
		f.SX * kx, 0, (-f.OffX-float64(c.L))*kx - f.SX*kx*float64(srcMin.X),
		0, f.SY * ky, (-f.OffY-float64(c.T))*ky - f.SY*ky*float64(srcMin.Y),
	}
}
