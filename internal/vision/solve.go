package vision

import (
	"errors"
	"math"
)

// Monitor is what the solver needs to know about a monitor.
type Monitor struct {
	ID         string
	ResW, ResH int
	WidthMM    float64 // physical visible size
	HeightMM   float64
}

// Placement is a solved position: top-left on the desk in mm, plus how big
// the monitor looks from the camera relative to the anchor (Scale) and its
// tilt in the photo (RollDeg).
type Placement struct {
	ID         string  `json:"id"`
	Found      bool    `json:"found"`
	XMM        float64 `json:"xMM"`
	YMM        float64 `json:"yMM"`
	Scale      float64 `json:"scale"`
	RollDeg    float64 `json:"rollDeg"`
	ResidualMM float64 `json:"residualMM"` // what a flat, unrotated rectangle can't explain (perspective, curve)
	// Quad is where the corners appear on the anchor's plane (TL, TR, BR,
	// BL as x,y mm), which includes tilt and perspective. What it can't
	// capture (screen curvature) shows up as the dot fit error instead.
	Quad []float64 `json:"quad,omitempty"`
	// Mesh is the same for a grid of MeshNX x MeshNY points across the
	// screen (row by row, x,y mm), when a curve was fitted: it follows
	// curved screens, which a quad can't.
	Mesh           []float64 `json:"mesh,omitempty"`
	MeshNX, MeshNY int       `json:"-"`
}

// Mesh size used for curved screens.
const MeshNX, MeshNY = 17, 9

// SolveCurves is Solve with an optional curve per monitor (nil entries use
// the homography only); monitors with a curve also get a Mesh.
func SolveCurves(mons []Monitor, hs []*H, curves []*Curve, anchor int, anchorX, anchorY float64) ([]Placement, error) {
	out, err := Solve(mons, hs, anchor, anchorX, anchorY)
	if err != nil {
		return nil, err
	}
	g, _ := hs[anchor].Inv()
	a := mons[anchor]
	kax, kay := a.WidthMM/float64(a.ResW), a.HeightMM/float64(a.ResH)
	for i, c := range curves {
		if c == nil || hs[i] == nil {
			continue
		}
		m := mons[i]
		var mesh []float64
		for r := 0; r < MeshNY; r++ {
			for k := 0; k < MeshNX; k++ {
				q := Pt{float64(m.ResW) * float64(k) / (MeshNX - 1), float64(m.ResH) * float64(r) / (MeshNY - 1)}
				p := g.Apply(c.Apply(q))
				mesh = append(mesh, anchorX+p.X*kax, anchorY+p.Y*kay)
			}
		}
		out[i].Mesh, out[i].MeshNX, out[i].MeshNY = mesh, MeshNX, MeshNY
		at := func(k, r int) (float64, float64) { j := 2 * (r*MeshNX + k); return mesh[j], mesh[j+1] }
		x0, y0 := at(0, 0)
		x1, y1 := at(MeshNX-1, 0)
		x2, y2 := at(MeshNX-1, MeshNY-1)
		x3, y3 := at(0, MeshNY-1)
		out[i].Quad = []float64{x0, y0, x1, y1, x2, y2, x3, y3}
	}
	return out, nil
}

// Solve places every found monitor relative to the anchor monitor, which
// keeps its current position (anchorX, anchorY) and scale 1. hs[i] maps
// monitor i's pixels to photo pixels (nil if it wasn't found).
//
// Everything is measured in the anchor's plane as seen from the camera, so
// the result is right for the viewpoint the photo was taken from: a monitor
// that is closer to the camera than the anchor gets a scale above 1, which
// makes its slice cover the part of the image it appears to cover.
func Solve(mons []Monitor, hs []*H, anchor int, anchorX, anchorY float64) ([]Placement, error) {
	if anchor < 0 || anchor >= len(mons) || hs[anchor] == nil {
		return nil, errors.New("vision: the anchor monitor wasn't found in the photo")
	}
	a := mons[anchor]
	g, err := hs[anchor].Inv() // photo -> anchor pixels
	if err != nil {
		return nil, err
	}
	kax, kay := a.WidthMM/float64(a.ResW), a.HeightMM/float64(a.ResH) // mm per anchor pixel
	out := make([]Placement, len(mons))
	for i, m := range mons {
		out[i] = Placement{ID: m.ID}
		if hs[i] == nil {
			continue
		}
		kx, ky := m.WidthMM/float64(m.ResW), m.HeightMM/float64(m.ResH)
		var src, dst []Pt
		for r := 0; r <= 8; r++ {
			for c := 0; c <= 16; c++ {
				q := Pt{float64(m.ResW) * float64(c) / 16, float64(m.ResH) * float64(r) / 8}
				p := g.Apply(hs[i].Apply(q))
				src = append(src, Pt{q.X * kx, q.Y * ky})
				dst = append(dst, Pt{anchorX + p.X*kax, anchorY + p.Y*kay})
			}
		}
		s, th, res := similarity(src, dst)
		var cd Pt
		for _, d := range dst {
			cd.X += d.X
			cd.Y += d.Y
		}
		n := float64(len(dst))
		cd.X, cd.Y = cd.X/n, cd.Y/n
		out[i] = Placement{ID: m.ID, Found: true,
			XMM: cd.X - s*m.WidthMM/2, YMM: cd.Y - s*m.HeightMM/2,
			Scale: s, RollDeg: th * 180 / math.Pi, ResidualMM: res}
		if hq, err := FitHomography(src, dst); err == nil {
			for _, c := range []Pt{{0, 0}, {m.WidthMM, 0}, {m.WidthMM, m.HeightMM}, {0, m.HeightMM}} {
				q := hq.Apply(c)
				out[i].Quad = append(out[i].Quad, q.X, q.Y)
			}
		}
	}
	out[anchor].XMM, out[anchor].YMM, out[anchor].Scale, out[anchor].RollDeg = anchorX, anchorY, 1, 0
	out[anchor].Quad = nil
	return out, nil
}

// similarity fits dst ~ s*R(theta)*src + t (least squares) and returns s,
// theta and the RMS residual.
func similarity(src, dst []Pt) (s, theta, rms float64) {
	n := float64(len(src))
	var cs, cd Pt
	for i := range src {
		cs.X += src[i].X
		cs.Y += src[i].Y
		cd.X += dst[i].X
		cd.Y += dst[i].Y
	}
	cs.X, cs.Y, cd.X, cd.Y = cs.X/n, cs.Y/n, cd.X/n, cd.Y/n
	var a, b, ss float64
	for i := range src {
		sx, sy := src[i].X-cs.X, src[i].Y-cs.Y
		dx, dy := dst[i].X-cd.X, dst[i].Y-cd.Y
		a += sx*dx + sy*dy
		b += sx*dy - sy*dx
		ss += sx*sx + sy*sy
	}
	sc, sn := a/ss, b/ss
	s, theta = math.Hypot(sc, sn), math.Atan2(sn, sc)
	var e float64
	for i := range src {
		sx, sy := src[i].X-cs.X, src[i].Y-cs.Y
		px, py := sc*sx-sn*sy+cd.X, sn*sx+sc*sy+cd.Y
		e += (px-dst[i].X)*(px-dst[i].X) + (py-dst[i].Y)*(py-dst[i].Y)
	}
	return s, theta, math.Sqrt(e / n)
}
