package vision

import "math"

// Curve is a monitor-to-photo map for screens that aren't flat: a
// homography plus a smooth cubic correction, fitted to the matched dots.
// Curved monitors (and slight lens distortion) bend straight rows of dots
// into arcs that a homography alone can't follow.
type Curve struct {
	H      H
	W, Hgt float64    // monitor pixels
	CX, CY [7]float64 // correction coefficients (photo pixels)
}

func curveBasis(s, t float64) [7]float64 {
	return [7]float64{s * s, s * t, t * t, s * s * s, s * s * t, s * t * t, t * t * t}
}

// Apply maps monitor pixels to photo pixels.
func (c *Curve) Apply(p Pt) Pt {
	q := c.H.Apply(p)
	b := curveBasis(2*p.X/c.W-1, 2*p.Y/c.Hgt-1)
	for k := range b {
		q.X += c.CX[k] * b[k]
		q.Y += c.CY[k] * b[k]
	}
	return q
}

// CurveLambda damps the correction (ridge strength per point).
var CurveLambda = 0.005

// FitCurve fits homography and correction together (alternating: the
// homography to the dots minus the correction, then the correction to what
// the homography leaves), so the homography doesn't bend to absorb the bow.
func FitCurve(h H, src, dst []Pt, w, hgt float64) Curve {
	c := Curve{H: h, W: w, Hgt: hgt}
	if len(src) < 14 {
		return c
	}
	for it := 0; it < 12; it++ {
		c = fitCorrection(c.H, src, dst, w, hgt)
		adj := make([]Pt, len(dst))
		for i := range dst {
			b := curveBasis(2*src[i].X/w-1, 2*src[i].Y/hgt-1)
			adj[i] = dst[i]
			for k := range b {
				adj[i].X -= c.CX[k] * b[k]
				adj[i].Y -= c.CY[k] * b[k]
			}
		}
		hn, err := FitHomography(src, adj)
		if err != nil {
			break
		}
		c.H = hn
	}
	return fitCorrection(c.H, src, dst, w, hgt)
}

// fitCorrection fits the correction to what h leaves over at the matched
// points; CurveLambda damps it (ridge) so a few noisy dots can't bend it.
func fitCorrection(h H, src, dst []Pt, w, hgt float64) Curve {
	c := Curve{H: h, W: w, Hgt: hgt}
	if len(src) < 14 {
		return c
	}
	var ata [7][7]float64
	var bx, by [7]float64
	for i := range src {
		p := h.Apply(src[i])
		rx, ry := dst[i].X-p.X, dst[i].Y-p.Y
		b := curveBasis(2*src[i].X/w-1, 2*src[i].Y/hgt-1)
		for a := 0; a < 7; a++ {
			bx[a] += b[a] * rx
			by[a] += b[a] * ry
			for k := 0; k < 7; k++ {
				ata[a][k] += b[a] * b[k]
			}
		}
	}
	lambda := CurveLambda * float64(len(src))
	solve := func(rhs [7]float64) [7]float64 {
		m := make([][]float64, 7)
		for a := 0; a < 7; a++ {
			m[a] = make([]float64, 8)
			copy(m[a], ata[a][:])
			m[a][a] += lambda
			m[a][7] = rhs[a]
		}
		var out [7]float64
		if gauss(m) != nil {
			return out
		}
		for a := 0; a < 7; a++ {
			out[a] = m[a][7]
		}
		return out
	}
	c.CX, c.CY = solve(bx), solve(by)
	return c
}

// CurveRMS is the reprojection error of c at the pairs.
func CurveRMS(c *Curve, src, dst []Pt) float64 {
	var s float64
	for i := range src {
		q := c.Apply(src[i])
		s += (q.X-dst[i].X)*(q.X-dst[i].X) + (q.Y-dst[i].Y)*(q.Y-dst[i].Y)
	}
	return math.Sqrt(s / float64(len(src)))
}
