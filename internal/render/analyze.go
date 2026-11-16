package render

import (
	"image"
	"image/color"
	"math"

	xdraw "golang.org/x/image/draw"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
)

// Small returns src scaled so its long side is at most n pixels (never
// enlarged). Analysis runs on this, not the full image.
func Small(src image.Image, n int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	k := math.Min(1, float64(n)/float64(max(w, h)))
	dw, dh := max(1, int(float64(w)*k+0.5)), max(1, int(float64(h)*k+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	xdraw.ApproxBiLinear.Scale(dst, dst.Rect, src, b, xdraw.Src, nil)
	return dst
}

// MeanLuminance is the average perceived brightness, 0 (black) to 1.
func MeanLuminance(img *image.RGBA) float64 {
	var sum float64
	n := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		sum += 0.2126*float64(img.Pix[i]) + 0.7152*float64(img.Pix[i+1]) + 0.0722*float64(img.Pix[i+2])
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n) / 255
}

// AccentColor picks a representative vivid colour: hues are binned and
// weighted by saturation, and the strongest bin's average colour is returned,
// brightened or darkened into a range that reads well as a Windows accent.
// Mostly grey images give a neutral blue-grey.
func AccentColor(img *image.RGBA) color.RGBA {
	const bins = 24
	var weight [bins]float64
	var rs, gs, bs [bins]float64
	for i := 0; i+3 < len(img.Pix); i += 4 {
		r, g, b := float64(img.Pix[i])/255, float64(img.Pix[i+1])/255, float64(img.Pix[i+2])/255
		h, s, v := rgbToHSV(r, g, b)
		if v < 0.15 || s < 0.2 {
			continue
		}
		w := s * s * v
		k := int(h/360*bins) % bins
		weight[k] += w
		rs[k] += r * w
		gs[k] += g * w
		bs[k] += b * w
	}
	best := 0
	for k := range weight {
		// Smooth over neighbouring bins so a hue split across two bins wins.
		if weight[k]+0.5*(weight[(k+1)%bins]+weight[(k+bins-1)%bins]) >
			weight[best]+0.5*(weight[(best+1)%bins]+weight[(best+bins-1)%bins]) {
			best = k
		}
	}
	total := 0.0
	for _, w := range weight {
		total += w
	}
	if weight[best] == 0 || weight[best] < 0.002*float64(len(img.Pix)/4) {
		return color.RGBA{0x4c, 0x5a, 0x6e, 0xff}
	}
	r, g, b := rs[best]/weight[best], gs[best]/weight[best], bs[best]/weight[best]
	h, s, v := rgbToHSV(r, g, b)
	s = math.Max(s, 0.45)
	v = math.Min(math.Max(v, 0.45), 0.85)
	r, g, b = hsvToRGB(h, s, v)
	return color.RGBA{uint8(r*255 + 0.5), uint8(g*255 + 0.5), uint8(b*255 + 0.5), 0xff}
}

func rgbToHSV(r, g, b float64) (h, s, v float64) {
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	v = mx
	d := mx - mn
	if mx > 0 {
		s = d / mx
	}
	switch {
	case d == 0:
		h = 0
	case mx == r:
		h = 60 * math.Mod((g-b)/d, 6)
	case mx == g:
		h = 60 * ((b-r)/d + 2)
	default:
		h = 60 * ((r-g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	return
}

func hsvToRGB(h, s, v float64) (r, g, b float64) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

// Saliency rates how much each pixel of img stands out (row-major, same
// size as img), 0..1. A pixel stands out when it is textured (local
// contrast) or differs in colour from the nearest of the four border
// strips (a plain backdrop rates low). Dark areas are always background,
// so black space, night sky and letterbox bars never attract the frame.
func Saliency(img *image.RGBA) []float64 {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	n := w * h
	lab := make([][3]float64, n)
	sqL := make([][3]float64, n) // L² in channel 0, for local variance
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := img.PixOffset(img.Rect.Min.X+x, img.Rect.Min.Y+y)
			l, a, b := toLab(img.Pix[o], img.Pix[o+1], img.Pix[o+2])
			lab[y*w+x] = [3]float64{l, a, b}
			sqL[y*w+x][0] = l * l
		}
	}
	r := max(1, min(w, h)/60)
	blur := boxBlur(lab, w, h, r)
	r2 := max(2, min(w, h)/30)
	meanL, meanSq := boxBlur(lab, w, h, r2), boxBlur(sqL, w, h, r2)
	// Mean colour of each border strip (top, bottom, left, right).
	bw, bh := max(1, w/12), max(1, h/12)
	strips := [4]image.Rectangle{image.Rect(0, 0, w, bh), image.Rect(0, h-bh, w, h), image.Rect(0, 0, bw, h), image.Rect(w-bw, 0, w, h)}
	var border [4][3]float64
	for k, rc := range strips {
		cnt := 0.0
		for y := rc.Min.Y; y < rc.Max.Y; y++ {
			for x := rc.Min.X; x < rc.Max.X; x++ {
				for c := 0; c < 3; c++ {
					border[k][c] += blur[y*w+x][c]
				}
				cnt++
			}
		}
		for c := 0; c < 3; c++ {
			border[k][c] /= cnt
		}
	}
	out := make([]float64, n)
	for i := range out {
		c := blur[i]
		d := math.Inf(1)
		for _, m := range border {
			d = math.Min(d, math.Sqrt(sq(c[0]-m[0])+sq(c[1]-m[1])+sq(c[2]-m[2])))
		}
		std := math.Sqrt(math.Max(0, meanSq[i][0]-sq(meanL[i][0])))
		v := math.Max(math.Min(std/12, 1), math.Min(d/40, 1))
		dark := math.Min(math.Max((c[0]-8)/17, 0), 1) // L* 8 = black, 25 = clearly lit
		out[i] = v * dark
	}
	return out
}

func sq(x float64) float64 { return x * x }

// toLab converts sRGB to CIE L*a*b* (D65).
func toLab(r8, g8, b8 uint8) (float64, float64, float64) {
	lin := func(c uint8) float64 {
		v := float64(c) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	r, g, b := lin(r8), lin(g8), lin(b8)
	x := (0.4124*r + 0.3576*g + 0.1805*b) / 0.95047
	y := 0.2126*r + 0.7152*g + 0.0722*b
	z := (0.0193*r + 0.1192*g + 0.9505*b) / 1.08883
	f := func(t float64) float64 {
		if t > 0.008856 {
			return math.Cbrt(t)
		}
		return 7.787*t + 16.0/116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

// boxBlur blurs a w*h field with radius r (two passes, separable).
func boxBlur(in [][3]float64, w, h, r int) [][3]float64 {
	tmp := make([][3]float64, len(in))
	out := make([][3]float64, len(in))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var s [3]float64
			n := 0
			for k := max(0, x-r); k <= min(w-1, x+r); k++ {
				for c := 0; c < 3; c++ {
					s[c] += in[y*w+k][c]
				}
				n++
			}
			for c := 0; c < 3; c++ {
				tmp[y*w+x][c] = s[c] / float64(n)
			}
		}
	}
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			var s [3]float64
			n := 0
			for k := max(0, y-r); k <= min(h-1, y+r); k++ {
				for c := 0; c < 3; c++ {
					s[c] += tmp[k*w+x][c]
				}
				n++
			}
			for c := 0; c < 3; c++ {
				out[y*w+x][c] = s[c] / float64(n)
			}
		}
	}
	return out
}

// AutoFrame picks a zoom and position for src on a cw x ch canvas whose
// visible parts are crops (the screens; everything else is bezel or gap),
// so as much of the image's salient content as possible lands on screens.
// The image always covers the whole canvas (no black), zoom stays at or
// below 1.25, and the default fill wins unless the gain is clear.
func AutoFrame(src image.Image, cw, ch int, crops []geometry.Rect) Frame {
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || cw <= 0 || ch <= 0 || len(crops) == 0 {
		return Frame{}
	}
	small := Small(src, 240)
	sw, sh := small.Rect.Dx(), small.Rect.Dy()
	sal := Saliency(small)
	// Integral image for fast rectangle sums.
	ii := make([]float64, (sw+1)*(sh+1))
	for y := 0; y < sh; y++ {
		row := 0.0
		for x := 0; x < sw; x++ {
			row += sal[y*sw+x]
			ii[(y+1)*(sw+1)+x+1] = ii[y*(sw+1)+x+1] + row
		}
	}
	total := ii[sh*(sw+1)+sw]
	if total <= 0 {
		return Frame{}
	}
	sum := func(x0, y0, x1, y1 float64) float64 { // in small-image pixels
		cx := func(v float64) int { return min(sw, max(0, int(math.Round(v)))) }
		cy := func(v float64) int { return min(sh, max(0, int(math.Round(v)))) }
		a, c := cx(x0), cx(x1)
		bb, d := cy(y0), cy(y1)
		if c <= a || d <= bb {
			return 0
		}
		return ii[d*(sw+1)+c] - ii[bb*(sw+1)+c] - ii[d*(sw+1)+a] + ii[bb*(sw+1)+a]
	}
	base := math.Max(float64(cw)/float64(b.Dx()), float64(ch)/float64(b.Dy()))
	score := func(z, x0, y0 float64) float64 {
		dw, dh := float64(b.Dx())*base*z, float64(b.Dy())*base*z
		kx, ky := float64(sw)/dw, float64(sh)/dh
		on := 0.0
		for _, c := range crops {
			on += sum((float64(c.L)-x0)*kx, (float64(c.T)-y0)*ky, (float64(c.R)-x0)*kx, (float64(c.B)-y0)*ky)
		}
		return on / total
	}
	dflt := score(1, float64(cw)/2-float64(b.Dx())*base/2, float64(ch)/2-float64(b.Dy())*base/2)
	best, bestFr := dflt, Frame{}
	for _, z := range []float64{1, 1.06, 1.12, 1.18, 1.25} {
		dw, dh := float64(b.Dx())*base*z, float64(b.Dy())*base*z
		// x0 in [cw-dw, 0] keeps the canvas covered.
		const steps = 24
		for i := 0; i <= steps; i++ {
			x0 := float64(cw) - dw + (dw-float64(cw))*float64(i)/steps
			for j := 0; j <= steps; j++ {
				y0 := float64(ch) - dh + (dh-float64(ch))*float64(j)/steps
				dx := (x0 - float64(cw)/2 + dw/2) / dw
				dy := (y0 - float64(ch)/2 + dh/2) / dh
				// Small costs for zooming (loses resolution and edges) and
				// for moving away from the centre, so ties keep the default.
				sc := score(z, x0, y0) - 0.25*(z-1) - 0.05*(math.Abs(dx)+math.Abs(dy))
				if sc > best+1e-9 {
					best, bestFr = sc, Frame{Zoom: z, DX: dx, DY: dy}
				}
			}
		}
	}
	if best < dflt+0.03 {
		return Frame{}
	}
	return bestFr
}
