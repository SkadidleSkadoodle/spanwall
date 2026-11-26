package vision

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"

	"golang.org/x/image/draw"
)

// Palette colours the dots, one colour per monitor (monitor i uses
// Palette[i%len]). Hues are far apart so a phone photo still separates them.
var Palette = []color.RGBA{
	{255, 50, 50, 255},  // red
	{40, 235, 70, 255},  // green
	{60, 120, 255, 255}, // blue
	{255, 220, 40, 255}, // yellow
	{230, 60, 230, 255}, // magenta
	{40, 220, 230, 255}, // cyan
}

var paletteHue = []float64{0, 128, 222, 52, 300, 183}

// Grid is a monitor's dot layout in its own pixels, row by row.
type Grid struct {
	Cols, Rows int
	Points     []Pt
	Radius     float64
}

// DotGrid lays out dots for a monitor: 5 rows, columns to match the aspect
// ratio, inset from the edges so every dot is fully visible.
func DotGrid(resW, resH int) Grid {
	rows := 5
	cols := int(math.Round(float64(rows) * float64(resW) / float64(resH)))
	if cols < 5 {
		cols = 5
	}
	g := Grid{Cols: cols, Rows: rows, Radius: 0.035 * float64(resH)}
	// Outer dots sit close to the edges, so the screen borders next to the
	// bezels are measured rather than extrapolated (which matters most on
	// curved screens).
	in := 1.6 * g.Radius
	x0, x1 := in, float64(resW)-in
	y0, y1 := in, float64(resH)-in
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			g.Points = append(g.Points, Pt{x0 + (x1-x0)*float64(c)/float64(cols-1), y0 + (y1-y0)*float64(r)/float64(rows-1)})
		}
	}
	return g
}

// DrawDots renders the photo calibration pattern for monitor index i.
func DrawDots(resW, resH, i int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, resW, resH))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0, 0, 0, 255}}, image.Point{}, draw.Src)
	g := DotGrid(resW, resH)
	c := Palette[i%len(Palette)]
	r := g.Radius
	for _, p := range g.Points {
		for y := int(p.Y - r - 1); y <= int(p.Y+r+1); y++ {
			for x := int(p.X - r - 1); x <= int(p.X+r+1); x++ {
				d := math.Hypot(float64(x)+0.5-p.X, float64(y)+0.5-p.Y)
				a := math.Max(0, math.Min(1, r-d+0.5)) // 1 px anti-aliased edge
				if a > 0 {
					img.SetRGBA(x, y, color.RGBA{uint8(float64(c.R) * a), uint8(float64(c.G) * a), uint8(float64(c.B) * a), 255})
				}
			}
		}
	}
	return img
}

// Blob is a bright coloured spot found in the photo.
type Blob struct {
	C       Pt // centre, in original photo pixels
	Area    int
	Class   int // palette index, or -1
	R, G, B float64
}

// Downscale returns img scaled so its longer side is at most maxDim, and
// the factor to multiply its coordinates by to get back to img's.
func Downscale(img image.Image, maxDim int) (*image.RGBA, float64) {
	b := img.Bounds()
	s := 1.0
	if m := max(b.Dx(), b.Dy()); m > maxDim {
		s = float64(maxDim) / float64(m)
	}
	w, h := int(float64(b.Dx())*s), int(float64(b.Dy())*s)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(out, out.Bounds(), img, b, draw.Src, nil)
	return out, 1 / s
}

// DetectDots finds bright, saturated, round-ish spots and classifies them
// by palette colour.
func DetectDots(photo image.Image) []Blob {
	img, back := Downscale(photo, 2000)
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	pix := img.Pix
	maxc := make([]uint8, w*h)
	var hist [256]int
	for i := 0; i < w*h; i++ {
		r, g, b := pix[i*4], pix[i*4+1], pix[i*4+2]
		m := max(r, g, b)
		maxc[i] = m
		hist[m]++
	}
	t := max(otsu(hist[:]), 90)
	seen := make([]bool, w*h)
	var blobs []Blob
	stack := make([]int, 0, 1024)
	maxArea := w * h / 200
	for start := 0; start < w*h; start++ {
		if seen[start] || maxc[start] < uint8(t) {
			continue
		}
		var sx, sy, sw, sr, sg, sb float64
		area := 0
		minX, minY, maxX, maxY := w, h, 0, 0
		stack = append(stack[:0], start)
		seen[start] = true
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := i%w, i/w
			v := float64(maxc[i])
			sx += float64(x) * v
			sy += float64(y) * v
			sw += v
			sr += float64(pix[i*4])
			sg += float64(pix[i*4+1])
			sb += float64(pix[i*4+2])
			area++
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
			for _, n := range [4]int{i - 1, i + 1, i - w, i + w} {
				if n < 0 || n >= w*h || seen[n] || maxc[n] < uint8(t) {
					continue
				}
				if (n == i-1 && x == 0) || (n == i+1 && x == w-1) {
					continue
				}
				seen[n] = true
				stack = append(stack, n)
			}
		}
		bw, bh := maxX-minX+1, maxY-minY+1
		if area < 6 || area > maxArea {
			continue
		}
		if float64(area)/float64(bw*bh) < 0.45 || float64(max(bw, bh))/float64(min(bw, bh)) > 3 {
			continue
		}
		n := float64(area)
		cx, cy := sx/sw, sy/sw
		b := Blob{C: Pt{(cx + 0.5) * back, (cy + 0.5) * back}, Area: area, R: sr / n, G: sg / n, B: sb / n}
		// Phone cameras clip the dot centres to white; the colour survives
		// in the glow just outside, so classify by that ring.
		b.Class = classify(b.R, b.G, b.B)
		if rr, rg, rb, ok := ringColour(pix, maxc, w, h, cx, cy, math.Sqrt(n/math.Pi), uint8(t)); ok {
			if c := classify(rr, rg, rb); c >= 0 {
				b.R, b.G, b.B, b.Class = rr, rg, rb, c
			}
		}
		blobs = append(blobs, b)
	}
	return blobs
}

// ringColour is the colour of the glow just outside a dot, minus the
// screen background a little further out. ok is false when there's no glow.
func ringColour(pix []uint8, maxc []uint8, w, h int, cx, cy, r float64, t uint8) (float64, float64, float64, bool) {
	mean := func(r0, r1 float64) (float64, float64, float64, int) {
		var sr, sg, sb float64
		n := 0
		for y := int(cy - r1); y <= int(cy+r1)+1; y++ {
			for x := int(cx - r1); x <= int(cx+r1)+1; x++ {
				if x < 0 || y < 0 || x >= w || y >= h {
					continue
				}
				d := math.Hypot(float64(x)-cx, float64(y)-cy)
				i := y*w + x
				if d < r0 || d > r1 || maxc[i] >= t {
					continue
				}
				sr += float64(pix[i*4])
				sg += float64(pix[i*4+1])
				sb += float64(pix[i*4+2])
				n++
			}
		}
		if n == 0 {
			return 0, 0, 0, 0
		}
		return sr / float64(n), sg / float64(n), sb / float64(n), n
	}
	rr, rg, rb, n := mean(r+0.5, r*1.35+2)
	br, bg, bb, m := mean(r*1.9, r*2.3+2)
	if n < 6 || m < 6 {
		return 0, 0, 0, false
	}
	dr, dg, db := math.Max(rr-br, 0), math.Max(rg-bg, 0), math.Max(rb-bb, 0)
	if math.Max(dr, math.Max(dg, db)) < 12 {
		return 0, 0, 0, false
	}
	return dr, dg, db, true
}

func classify(r, g, b float64) int {
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	if mx < 1 || (mx-mn)/mx < 0.22 {
		return -1 // too grey to tell
	}
	var hue float64
	switch mx {
	case r:
		hue = 60 * math.Mod((g-b)/(mx-mn), 6)
	case g:
		hue = 60 * ((b-r)/(mx-mn) + 2)
	default:
		hue = 60 * ((r-g)/(mx-mn) + 4)
	}
	if hue < 0 {
		hue += 360
	}
	best, bd := -1, 32.0
	for i, ph := range paletteHue {
		d := math.Abs(hue - ph)
		d = math.Min(d, 360-d)
		if d < bd {
			best, bd = i, d
		}
	}
	return best
}

func otsu(hist []int) int {
	total := 0
	var sum float64
	for i, c := range hist {
		total += c
		sum += float64(i * c)
	}
	var sumB, wB, best float64
	t := 0
	for i, c := range hist {
		wB += float64(c)
		if wB == 0 {
			continue
		}
		wF := float64(total) - wB
		if wF == 0 {
			break
		}
		sumB += float64(i * c)
		mB, mF := sumB/wB, (sum-sumB)/wF
		v := wB * wF * (mB - mF) * (mB - mF)
		if v > best {
			best, t = v, i
		}
	}
	return t
}

// GridMatch is one monitor found in the photo.
type GridMatch struct {
	H        H   // monitor pixels -> photo pixels
	Matched  int // dots matched
	Expected int
	RMS      float64 // reprojection error in photo pixels
	// The matched pairs: grid points (monitor pixels) and dots (photo pixels).
	Src, Dst []Pt
}

// dropIsolated keeps the biggest cluster of blobs (dots one grid step
// apart), dropping status LEDs and reflections elsewhere in the photo,
// which would otherwise be picked as grid corners.
func dropIsolated(bs []Blob) []Blob {
	if len(bs) < 4 {
		return bs
	}
	nn := make([]float64, len(bs))
	for i, a := range bs {
		nn[i] = math.Inf(1)
		for j, b := range bs {
			if i != j {
				nn[i] = math.Min(nn[i], math.Hypot(a.C.X-b.C.X, a.C.Y-b.C.Y))
			}
		}
	}
	s := append([]float64(nil), nn...)
	sort.Float64s(s)
	step := s[len(s)/2]
	link := 2.2 * step
	// A lone spot near the grid (a status LED below a screen's corner) is
	// within linking range of the outer dots, but a real dot always has a
	// neighbour about one grid step away. Drop spots without one first, or
	// one becomes a grid corner and the whole fit fails.
	var near []Blob
	for i, b := range bs {
		if nn[i] <= 1.6*step {
			near = append(near, b)
		}
	}
	if len(near) >= 4 {
		bs = near
	}
	// A spot much closer to a dot than the grid step (a power LED right by
	// a corner dot) is not a dot either: of such a pair keep the bigger one.
	var spaced []Blob
	for i, a := range bs {
		keep := true
		for j, b := range bs {
			if i != j && math.Hypot(a.C.X-b.C.X, a.C.Y-b.C.Y) < 0.6*step &&
				(b.Area > a.Area || (b.Area == a.Area && j < i)) {
				keep = false
				break
			}
		}
		if keep {
			spaced = append(spaced, a)
		}
	}
	if len(spaced) >= 4 {
		bs = spaced
	}
	comp := make([]int, len(bs))
	for i := range comp {
		comp[i] = -1
	}
	best, bestN := -1, 0
	for i := range bs {
		if comp[i] >= 0 {
			continue
		}
		comp[i] = i
		stack, n := []int{i}, 0
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			n++
			for j := range bs {
				if comp[j] < 0 && math.Hypot(bs[k].C.X-bs[j].C.X, bs[k].C.Y-bs[j].C.Y) < link {
					comp[j] = i
					stack = append(stack, j)
				}
			}
		}
		if n > bestN {
			best, bestN = i, n
		}
	}
	var out []Blob
	for i, b := range bs {
		if comp[i] == best {
			out = append(out, b)
		}
	}
	return out
}

// MatchGrid fits a monitor's dot grid to the blobs of its colour.
func MatchGrid(blobs []Blob, g Grid) (GridMatch, error) {
	res := GridMatch{Expected: len(g.Points)}
	if len(blobs) < 6 {
		return res, fmt.Errorf("only %d dots found", len(blobs))
	}
	// Drop blobs whose size is far from the typical dot (reflections, LEDs).
	areas := make([]int, len(blobs))
	for i, b := range blobs {
		areas[i] = b.Area
	}
	sort.Ints(areas)
	med := float64(areas[len(areas)/2])
	var bs []Blob
	for _, b := range blobs {
		if a := float64(b.Area); a > med/4 && a < med*4 {
			bs = append(bs, b)
		}
	}
	bs = dropIsolated(bs)
	if len(bs) < 6 {
		return res, fmt.Errorf("only %d consistent dots", len(bs))
	}
	// Corners of the grid are the extreme blobs along the diagonals.
	ext := func(f func(Pt) float64) Pt {
		best := bs[0].C
		for _, b := range bs {
			if f(b.C) > f(best) {
				best = b.C
			}
		}
		return best
	}
	tl := ext(func(p Pt) float64 { return -(p.X + p.Y) })
	br := ext(func(p Pt) float64 { return p.X + p.Y })
	tr := ext(func(p Pt) float64 { return p.X - p.Y })
	bl := ext(func(p Pt) float64 { return p.Y - p.X })
	n := len(g.Points)
	// The extreme blobs are usually the corner dots, but a corner dot the
	// camera washed out (or that classified as another colour) makes the
	// extreme one row or column in. Try those readings too and keep the fit
	// that matches the most dots; a wrong start slips a whole row or column.
	at := func(r, c int) Pt { return g.Points[r*g.Cols+c] }
	R, C := g.Rows, g.Cols
	cands := [4][3]Pt{
		{at(0, 0), at(0, 1), at(1, 0)},
		{at(0, C-1), at(0, C-2), at(1, C-1)},
		{at(R-1, 0), at(R-2, 0), at(R-1, 1)},
		{at(R-1, C-1), at(R-2, C-1), at(R-1, C-2)},
	}
	found := false
	for _, a := range cands[0] {
		for _, b := range cands[1] {
			for _, c := range cands[2] {
				for _, d := range cands[3] {
					h, err := FitHomography([]Pt{a, b, c, d}, []Pt{tl, tr, bl, br})
					if err != nil {
						continue
					}
					m, ok := refineGrid(bs, g, h)
					if !ok {
						continue
					}
					if !found || m.Matched > res.Matched || (m.Matched == res.Matched && m.RMS < res.RMS) {
						m.Expected = res.Expected
						res, found = m, true
					}
				}
			}
		}
	}
	if !found {
		return res, errors.New("grid didn't fit")
	}
	if res.Matched < n*6/10 {
		return res, errors.New("too few dots matched; is the whole screen in the photo?")
	}
	return res, nil
}

// refineGrid matches grid dots to blobs starting from h and refits, a few
// rounds. ok is false when fewer than 6 dots match.
func refineGrid(bs []Blob, g Grid, h H) (GridMatch, bool) {
	var res GridMatch
	var err error
	for iter := 0; iter < 4; iter++ {
		// Distance between neighbouring predicted dots sets the match radius.
		p0, p1, p2 := h.Apply(g.Points[0]), h.Apply(g.Points[1]), h.Apply(g.Points[g.Cols])
		thr := 0.4 * math.Min(math.Hypot(p1.X-p0.X, p1.Y-p0.Y), math.Hypot(p2.X-p0.X, p2.Y-p0.Y))
		used := make([]bool, len(bs))
		var src, dst []Pt
		for _, gp := range g.Points {
			pp := h.Apply(gp)
			bi, bd := -1, thr
			for j, b := range bs {
				if used[j] {
					continue
				}
				if d := math.Hypot(b.C.X-pp.X, b.C.Y-pp.Y); d < bd {
					bi, bd = j, d
				}
			}
			if bi >= 0 {
				used[bi] = true
				src = append(src, gp)
				dst = append(dst, bs[bi].C)
			}
		}
		if len(src) < 6 {
			return res, false
		}
		if h, err = FitHomography(src, dst); err != nil {
			return res, false
		}
		res.H, res.Matched, res.RMS = h, len(src), RMS(h, src, dst)
		res.Src, res.Dst = src, dst
	}
	return res, true
}
