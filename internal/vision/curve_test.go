package vision

import (
	"math"
	"math/rand"
	"testing"
)

// A screen bowed like a 1500R ultrawide: rows of dots become arcs. The
// curve must recover the true map well beyond what a homography can.
func TestCurveFollowsBow(t *testing.T) {
	w, hgt := 3440.0, 1440.0
	h := H{0.3, 0.01, 500, -0.005, 0.29, 300, 0.00001, 0, 1}
	truth := func(p Pt) Pt {
		q := h.Apply(p)
		s := 2*p.X/w - 1
		q.Y -= 18 * s * s    // edges rise (bow)
		q.X += 6 * s * s * s // and spread
		return q
	}
	g := DotGrid(int(w), int(hgt))
	rng := rand.New(rand.NewSource(3))
	var src, dst []Pt
	for _, p := range g.Points {
		q := truth(p)
		q.X += rng.NormFloat64() * 0.4
		q.Y += rng.NormFloat64() * 0.4
		src, dst = append(src, p), append(dst, q)
	}
	hf, _ := FitHomography(src, dst)
	c := FitCurve(hf, src, dst, w, hgt)
	var eh, ec float64
	for _, p := range []Pt{{0, 0}, {w, 0}, {w, hgt}, {0, hgt}, {w / 2, 0}, {w / 2, hgt}} {
		tq, a, b := truth(p), hf.Apply(p), c.Apply(p)
		eh = math.Max(eh, math.Hypot(a.X-tq.X, a.Y-tq.Y))
		ec = math.Max(ec, math.Hypot(b.X-tq.X, b.Y-tq.Y))
	}
	t.Logf("worst edge error: homography %.2f px, curve %.2f px", eh, ec)
	if ec > 1.5 || ec > eh/3 {
		t.Errorf("curve error %.2f px (homography %.2f)", ec, eh)
	}
}
