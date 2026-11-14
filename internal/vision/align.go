package vision

import (
	"errors"
	"math"
	"sort"
	"sync"
)

// Screen is what one monitor showed when the photo was taken.
type Screen struct {
	Image *Gray   // the slice on screen, possibly downscaled
	Scale float64 // native monitor pixels per Image pixel
	ResW  int     // native size
	ResH  int
	Prior H // native monitor pixels -> photo pixels, from an earlier calibration
}

// AlignResult is one refined monitor.
type AlignResult struct {
	H   H       // native monitor pixels -> photo pixels
	NCC float64 // match quality, 1 = perfect; below ~0.5 is unreliable
	OK  bool
}

// AlignScreens finds each monitor in a photo of the real wallpaper. It
// starts from the priors, fixes the overall shift and zoom of the new photo
// with a coarse search, then refines every monitor's homography separately
// by matching its on-screen image against the photo (Gauss-Newton with
// gain and bias, robust to windows and icons covering part of the screen).
// photo is the full photo in luminance; priors are in its pixels.
func AlignScreens(photo *Gray, screens []Screen) ([]AlignResult, error) {
	if len(screens) == 0 {
		return nil, errors.New("vision: no screens")
	}
	// Pyramid of the photo: level k is 1/2^k size.
	levels := []*Gray{photo.Blur(1)}
	for len(levels) < 5 && levels[len(levels)-1].W > 160 {
		levels = append(levels, levels[len(levels)-1].Half().Blur(1))
	}
	hs := make([]H, len(screens))
	for i, s := range screens {
		hs[i] = s.Prior
	}

	// 1. Global similarity at the coarsest level.
	top := len(levels) - 1
	f := 1 / math.Pow(2, float64(top))
	g := globalSearch(levels[top], screens, hs, f)
	for i := range hs {
		hs[i] = Mul(g, hs[i])
	}

	// 2. Per monitor refinement, coarse to fine (from 1/8 size down to full;
	// the coarsest level is only used for the global search).
	res := make([]AlignResult, len(screens))
	var wg sync.WaitGroup
	for i, s := range screens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i] = alignOne(levels, top, s, hs[i])
		}()
	}
	wg.Wait()
	return res, nil
}

// alignOne finds one screen: scale-and-shift search, then bounded refinement.
func alignOne(levels []*Gray, top int, s Screen, h0 H) AlignResult {
	// Scale and shift only first: on low-detail images (fog, sky) the
	// full perspective fit can stretch onto a wrong spot that still
	// scores well, so it only fine-tunes near this answer.
	lvS := min(top, 2)
	fS := 1 / math.Pow(2, float64(lvS))
	hl, _ := simSearch(levels[lvS], s, Mul(Scale(fS, fS), h0))
	h := Mul(Scale(1/fS, 1/fS), hl)
	anchor := h
	var score float64
	for lv := lvS; lv >= 0; lv-- {
		fl := 1 / math.Pow(2, float64(lv))
		hl, sc := refine(levels[lv], s, Mul(Scale(fl, fl), h))
		cand := Mul(Scale(1/fl, 1/fl), hl)
		if cornerDrift(anchor, cand, s) <= 0.07 {
			h, score = cand, sc
		} else {
			_, score = refine(levels[lv], s, Mul(Scale(fl, fl), h)) // keep h; score only
		}
	}
	if score == 0 {
		score = nccAt(levels[0], s, h)
	}
	return AlignResult{H: h.Normalized(), NCC: score, OK: score >= 0.5}
}

// globalSearch tries scales and shifts of the whole expected picture (the
// screens' images placed by hs) against the coarse photo and returns the
// best similarity, in full photo pixels.
func globalSearch(p *Gray, screens []Screen, hs []H, f float64) H {
	// Expected image at this level, with a validity mask.
	exp := make([]float32, p.W*p.H)
	valid := make([]bool, p.W*p.H)
	for i, s := range screens {
		inv, err := Mul(Scale(f, f), hs[i]).Inv()
		if err != nil {
			continue
		}
		img := s.Image.Blur(max(1, int(1/(f*s.Scale*0.5))))
		for y := 0; y < p.H; y++ {
			for x := 0; x < p.W; x++ {
				q := inv.Apply(Pt{float64(x), float64(y)})
				if q.X < 0 || q.Y < 0 || q.X >= float64(s.ResW) || q.Y >= float64(s.ResH) {
					continue
				}
				if v, ok := img.At(q.X/s.Scale, q.Y/s.Scale); ok {
					exp[y*p.W+x], valid[y*p.W+x] = v, true
				}
			}
		}
	}
	cx, cy := float64(p.W)/2, float64(p.H)/2
	best, bs, bdx, bdy := -2.0, 1.0, 0.0, 0.0
	maxShift := int(0.14 * float64(p.W))
	a := make([]float32, 0, p.W*p.H)
	b := make([]float32, 0, p.W*p.H)
	for sc := 0.86; sc <= 1.141; sc += 0.02 {
		for dy := -maxShift; dy <= maxShift; dy += 2 {
			for dx := -maxShift; dx <= maxShift; dx += 2 {
				a, b = a[:0], b[:0]
				// Photo pixel (x, y) shows expected pixel at the inverse transform.
				for y := 0; y < p.H; y += 2 {
					for x := 0; x < p.W; x += 2 {
						ex := cx + (float64(x)-cx-float64(dx))/sc
						ey := cy + (float64(y)-cy-float64(dy))/sc
						xi, yi := int(ex), int(ey)
						if xi < 0 || yi < 0 || xi >= p.W || yi >= p.H || !valid[yi*p.W+xi] {
							continue
						}
						a = append(a, exp[yi*p.W+xi])
						b = append(b, p.Pix[y*p.W+x])
					}
				}
				if len(a) < 200 {
					continue
				}
				if c := ncc(a, b); c > best {
					best, bs, bdx, bdy = c, sc, float64(dx), float64(dy)
				}
			}
		}
	}
	// x' = c + s(x - c) + d at this level, expressed in full photo pixels.
	cX, cY, dX, dY := cx/f, cy/f, bdx/f, bdy/f
	return H{bs, 0, cX - bs*cX + dX, 0, bs, cY - bs*cY + dY, 0, 0, 1}
}

// cornerDrift is how far b's screen corners are from a's, as a fraction
// of the screen's diagonal in the photo.
func cornerDrift(a, b H, s Screen) float64 {
	d0 := a.Apply(Pt{0, 0})
	d1 := a.Apply(Pt{float64(s.ResW), float64(s.ResH)})
	diag := math.Hypot(d1.X-d0.X, d1.Y-d0.Y)
	var worst float64
	for _, c := range []Pt{{0, 0}, {float64(s.ResW), 0}, {0, float64(s.ResH)}, {float64(s.ResW), float64(s.ResH)}} {
		p, q := a.Apply(c), b.Apply(c)
		worst = math.Max(worst, math.Hypot(p.X-q.X, p.Y-q.Y))
	}
	return worst / diag
}

// nccAt scores h against the photo using the template's strongest points.
func nccAt(p *Gray, s Screen, h H) float64 {
	samples := pickSamples(s.Image, s, 2500)
	var a, b []float32
	for _, sm := range samples {
		q := h.Apply(Pt{sm.x, sm.y})
		if v, ok := p.At(q.X, q.Y); ok {
			a = append(a, v)
			b = append(b, sm.t)
		}
	}
	if len(a) < 50 || len(a) < len(samples)/3 {
		return -1
	}
	return ncc(a, b)
}

// simSearch tries scales and shifts about the screen's centre (photo side)
// and returns the best, keeping h's perspective shape. h maps native
// monitor pixels to this level's photo pixels.
func simSearch(p *Gray, s Screen, h H) (H, float64) {
	c := h.Apply(Pt{float64(s.ResW) / 2, float64(s.ResH) / 2})
	e := h.Apply(Pt{float64(s.ResW), float64(s.ResH) / 2})
	half := math.Hypot(e.X-c.X, e.Y-c.Y) // half the screen width here
	c2 := h.Apply(Pt{float64(s.ResW)/2 + 100, float64(s.ResH) / 2})
	blur := max(0, int(0.5/(math.Hypot(c2.X-c.X, c2.Y-c.Y)/100*s.Scale)))
	samples := pickSamples(s.Image.Blur(blur), s, 1500)
	if len(samples) < 50 {
		return h, 0
	}
	pts := make([]Pt, len(samples))
	for k, sm := range samples {
		pts[k] = h.Apply(Pt{sm.x, sm.y})
	}
	a := make([]float32, 0, len(samples))
	b := make([]float32, 0, len(samples))
	try := func(sc, dx, dy float64) float64 {
		a, b = a[:0], b[:0]
		for k, q := range pts {
			if v, ok := p.At(c.X+sc*(q.X-c.X)+dx, c.Y+sc*(q.Y-c.Y)+dy); ok {
				a = append(a, v)
				b = append(b, samples[k].t)
			}
		}
		if len(a) < len(samples)/2 {
			return -2
		}
		return ncc(a, b)
	}
	best, bs, bx, by := try(1, 0, 0), 1.0, 0.0, 0.0
	r := 0.3 * half
	// Coarse to fine: a sparse search with a quarter of the samples, then
	// a dense one around the best, then sub-pixel.
	all, allPts := samples, pts
	samples, pts = nil, nil
	for k := 0; k < len(all); k += 4 {
		samples, pts = append(samples, all[k]), append(pts, allPts[k])
	}
	best = try(1, 0, 0)
	for sc := 0.82; sc <= 1.221; sc += 0.03 {
		for dy := -r; dy <= r; dy += 2 {
			for dx := -r; dx <= r; dx += 2 {
				if v := try(sc, dx, dy); v > best {
					best, bs, bx, by = v, sc, dx, dy
				}
			}
		}
	}
	samples, pts = all, allPts
	best = try(bs, bx, by)
	s0, x0, y0 := bs, bx, by
	for sc := s0 - 0.03; sc <= s0+0.0301; sc += 0.01 {
		for dy := y0 - 3; dy <= y0+3; dy++ {
			for dx := x0 - 3; dx <= x0+3; dx++ {
				if v := try(sc, dx, dy); v > best {
					best, bs, bx, by = v, sc, dx, dy
				}
			}
		}
	}
	// Finer pass around the best.
	s0, x0, y0 = bs, bx, by
	for sc := s0 - 0.01; sc <= s0+0.0101; sc += 0.005 {
		for dy := y0 - 1; dy <= y0+1; dy += 0.25 {
			for dx := x0 - 1; dx <= x0+1; dx += 0.25 {
				if v := try(sc, dx, dy); v > best {
					best, bs, bx, by = v, sc, dx, dy
				}
			}
		}
	}
	S := H{bs, 0, c.X - bs*c.X + bx, 0, bs, c.Y - bs*c.Y + by, 0, 0, 1}
	return Mul(S, h), best
}

type sample struct {
	x, y float64 // native monitor pixels
	t    float32 // template value
}

// refine runs robust Gauss-Newton on one monitor at one pyramid level.
// h maps native monitor pixels to this level's photo pixels.
func refine(p *Gray, s Screen, h H) (H, float64) {
	// How many native pixels one photo pixel covers here, to blur the
	// template to a comparable level of detail.
	c := h.Apply(Pt{float64(s.ResW) / 2, float64(s.ResH) / 2})
	c2 := h.Apply(Pt{float64(s.ResW)/2 + 100, float64(s.ResH) / 2})
	photoPerNative := math.Hypot(c2.X-c.X, c2.Y-c.Y) / 100
	blur := max(0, int(0.5/(photoPerNative*s.Scale)))
	tmpl := s.Image.Blur(blur)
	samples := pickSamples(tmpl, s, 2500)
	if len(samples) < 50 {
		return h, 0
	}
	// Optimise in normalized coordinates so the 8 parameters are similar in size.
	ns := Mul(Scale(2/float64(s.ResW), 2/float64(s.ResH)), H{1, 0, -float64(s.ResW) / 2, 0, 1, -float64(s.ResH) / 2, 0, 0, 1})
	np := Mul(Scale(2/float64(p.W), 2/float64(p.W)), H{1, 0, -float64(p.W) / 2, 0, 1, -float64(p.H) / 2, 0, 0, 1})
	nsi, _ := ns.Inv()
	npi, _ := np.Inv()
	hn := Mul(np, Mul(h, nsi)).Normalized()
	gain, bias := 1.0, 0.0
	pxPerN := float64(p.W) / 2 // photo pixels per normalized unit

	// Area of the screen in normalized photo units; a step may not change
	// it by more than 25%, which stops collapse onto plain regions.
	area := func(h H) float64 {
		c := [4]Pt{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}}
		var a float64
		for k := 0; k < 4; k++ {
			p0 := h.Apply(c[k])
			p1 := h.Apply(c[(k+1)%4])
			a += p0.X*p1.Y - p1.X*p0.Y
		}
		return a / 2
	}
	a0 := area(hn)

	// Levenberg-Marquardt: a step is kept only if it improves the match.
	var best H = hn
	bestScore, bestGain, bestBias := -2.0, gain, bias
	var saved [10][11]float64
	haveSaved := false
	lambda := 1e-3
	for iter := 0; iter < 40; iter++ {
		var jtj [10][10]float64
		var jtr [10]float64
		var rs []float64
		type row struct {
			j [10]float64
			r float64
		}
		rows := make([]row, 0, len(samples))
		var as, bs []float32
		for _, sm := range samples {
			q := ns.Apply(Pt{sm.x, sm.y})
			w := hn[6]*q.X + hn[7]*q.Y + 1
			u := (hn[0]*q.X + hn[1]*q.Y + hn[2]) / w
			v := (hn[3]*q.X + hn[4]*q.Y + hn[5]) / w
			pp := npi.Apply(Pt{u, v})
			iv, ok := p.At(pp.X, pp.Y)
			if !ok {
				continue
			}
			gx, gy, ok := p.Grad(pp.X, pp.Y)
			if !ok {
				continue
			}
			// gradient w.r.t. normalized photo coords
			gxn, gyn := float64(gx)*pxPerN, float64(gy)*pxPerN
			var j [10]float64
			j[0], j[1], j[2] = gxn*q.X/w, gxn*q.Y/w, gxn/w
			j[3], j[4], j[5] = gyn*q.X/w, gyn*q.Y/w, gyn/w
			j[6] = -(gxn*u + gyn*v) * q.X / w
			j[7] = -(gxn*u + gyn*v) * q.Y / w
			j[8], j[9] = -float64(sm.t), -1
			r := float64(iv) - (gain*float64(sm.t) + bias)
			rows = append(rows, row{j, r})
			rs = append(rs, math.Abs(r))
			as = append(as, iv)
			bs = append(bs, sm.t)
		}
		score := -2.0
		if len(rows) >= len(samples)/3 && len(rows) >= 50 {
			score = ncc(as, bs)
		}
		ar := area(hn) / a0
		if score <= bestScore || ar < 0.75 || ar > 1.33 {
			// Worse: go back to the best estimate and take a smaller step.
			if !haveSaved {
				break
			}
			hn, gain, bias = best, bestGain, bestBias
			lambda *= 6
			if lambda > 1e4 {
				break
			}
		} else {
			improved := score - bestScore
			best, bestScore, bestGain, bestBias = hn, score, gain, bias
			lambda = math.Max(lambda/3, 1e-6)
			if haveSaved && improved < 1e-5 {
				break
			}
			// Robust weights: Tukey-like cut at 3x the median absolute residual.
			sort.Float64s(rs)
			cut := 3 * math.Max(rs[len(rs)/2], 1)
			for _, rw := range rows {
				a := math.Abs(rw.r)
				if a > cut {
					continue
				}
				q := a / cut
				wt := (1 - q*q) * (1 - q*q)
				for a := 0; a < 10; a++ {
					jtr[a] += wt * rw.j[a] * rw.r
					for b := a; b < 10; b++ {
						jtj[a][b] += wt * rw.j[a] * rw.j[b]
					}
				}
			}
			for a := 0; a < 10; a++ {
				for b := 0; b < 10; b++ {
					if b >= a {
						saved[a][b] = jtj[a][b]
					} else {
						saved[a][b] = jtj[b][a]
					}
				}
				saved[a][10] = -jtr[a]
			}
			haveSaved = true
		}
		m := make([][]float64, 10)
		for a := 0; a < 10; a++ {
			m[a] = make([]float64, 11)
			copy(m[a], saved[a][:])
			m[a][a] *= 1 + lambda
		}
		if err := gauss(m); err != nil {
			break
		}
		for a := 0; a < 8; a++ {
			hn[a] += m[a][10]
		}
		gain += m[8][10]
		bias += m[9][10]
	}
	score := bestScore
	hn = best
	return Mul(npi, Mul(hn, ns)).Normalized(), score
}

// pickSamples chooses template points with strong gradients, spread over
// the screen (best few per cell of a 24x12 grid).
func pickSamples(t *Gray, s Screen, n int) []sample {
	const cw, ch = 24, 12
	type cand struct {
		x, y int
		m    float32
	}
	cells := make([][]cand, cw*ch)
	step := max(1, min(t.W, t.H)/200)
	for y := 2; y < t.H-2; y += step {
		for x := 2; x < t.W-2; x += step {
			gx, gy, _ := t.Grad(float64(x), float64(y))
			m := gx*gx + gy*gy
			if m < 4 {
				continue
			}
			c := (y*ch/t.H)*cw + x*cw/t.W
			cells[c] = append(cells[c], cand{x, y, m})
		}
	}
	per := max(1, n/(cw*ch))
	var out []sample
	for _, cs := range cells {
		sort.Slice(cs, func(i, j int) bool { return cs[i].m > cs[j].m })
		for k := 0; k < len(cs) && k < per; k++ {
			out = append(out, sample{x: float64(cs[k].x) * s.Scale, y: float64(cs[k].y) * s.Scale, t: t.Pix[cs[k].y*t.W+cs[k].x]})
		}
	}
	return out
}
