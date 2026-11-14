// Package vision finds the monitors in a phone photo of the desk and turns
// what it sees into layout positions.
package vision

import (
	"errors"
	"math"
)

// Pt is a 2D point.
type Pt struct{ X, Y float64 }

// H is a 3x3 homography, row major, mapping (x, y, 1) -> (x', y', w').
type H [9]float64

// Identity is the identity homography.
var Identity = H{1, 0, 0, 0, 1, 0, 0, 0, 1}

// Apply maps a point.
func (h H) Apply(p Pt) Pt {
	w := h[6]*p.X + h[7]*p.Y + h[8]
	return Pt{(h[0]*p.X + h[1]*p.Y + h[2]) / w, (h[3]*p.X + h[4]*p.Y + h[5]) / w}
}

// Mul returns a*b (apply b first).
func Mul(a, b H) H {
	var c H
	for r := 0; r < 3; r++ {
		for k := 0; k < 3; k++ {
			c[r*3+k] = a[r*3]*b[k] + a[r*3+1]*b[3+k] + a[r*3+2]*b[6+k]
		}
	}
	return c
}

// Inv returns the inverse.
func (h H) Inv() (H, error) {
	a, b, c, d, e, f, g, i, k := h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7], h[8]
	A := e*k - f*i
	B := -(d*k - f*g)
	C := d*i - e*g
	det := a*A + b*B + c*C
	if math.Abs(det) < 1e-12 {
		return H{}, errors.New("vision: singular homography")
	}
	inv := H{A, -(b*k - c*i), b*f - c*e, B, a*k - c*g, -(a*f - c*d), C, -(a*i - b*g), a*e - b*d}
	for j := range inv {
		inv[j] /= det
	}
	return inv, nil
}

// Normalized scales h so h[8] = 1.
func (h H) Normalized() H {
	if h[8] == 0 {
		return h
	}
	for i := range h {
		h[i] /= h[8]
	}
	return h
}

// Scale returns diag(s, s, 1).
func Scale(sx, sy float64) H { return H{sx, 0, 0, 0, sy, 0, 0, 0, 1} }

// normalizer returns T so T*p has centroid 0 and mean distance sqrt(2)
// (Hartley normalization, which keeps the DLT well conditioned).
func normalizer(ps []Pt) H {
	var cx, cy float64
	for _, p := range ps {
		cx += p.X
		cy += p.Y
	}
	n := float64(len(ps))
	cx, cy = cx/n, cy/n
	var d float64
	for _, p := range ps {
		d += math.Hypot(p.X-cx, p.Y-cy)
	}
	d /= n
	if d == 0 {
		d = 1
	}
	s := math.Sqrt2 / d
	return H{s, 0, -s * cx, 0, s, -s * cy, 0, 0, 1}
}

// FitHomography finds h with h(src[i]) ~ dst[i] by least squares on at
// least 4 correspondences (normalized DLT with h33 = 1).
func FitHomography(src, dst []Pt) (H, error) {
	if len(src) != len(dst) || len(src) < 4 {
		return H{}, errors.New("vision: need at least 4 point pairs")
	}
	ts, td := normalizer(src), normalizer(dst)
	// Normal equations for the 8 unknowns.
	var ata [8][8]float64
	var atb [8]float64
	add := func(row [8]float64, rhs float64) {
		for i := 0; i < 8; i++ {
			atb[i] += row[i] * rhs
			for j := 0; j < 8; j++ {
				ata[i][j] += row[i] * row[j]
			}
		}
	}
	for i := range src {
		s, d := ts.Apply(src[i]), td.Apply(dst[i])
		add([8]float64{s.X, s.Y, 1, 0, 0, 0, -s.X * d.X, -s.Y * d.X}, d.X)
		add([8]float64{0, 0, 0, s.X, s.Y, 1, -s.X * d.Y, -s.Y * d.Y}, d.Y)
	}
	x, err := solve8(ata, atb)
	if err != nil {
		return H{}, err
	}
	hn := H{x[0], x[1], x[2], x[3], x[4], x[5], x[6], x[7], 1}
	tdi, err := td.Inv()
	if err != nil {
		return H{}, err
	}
	return Mul(tdi, Mul(hn, ts)).Normalized(), nil
}

// solve8 solves a dense 8x8 system with partial pivoting.
func solve8(a [8][8]float64, b [8]float64) ([8]float64, error) {
	return solveN(a[:], b[:])
}

func solveN[R ~[8]float64](rows []R, b []float64) ([8]float64, error) {
	n := len(b)
	m := make([][]float64, n)
	for i := range m {
		m[i] = append(append([]float64{}, rows[i][:n]...), b[i])
	}
	var x [8]float64
	if err := gauss(m); err != nil {
		return x, err
	}
	for i := 0; i < n; i++ {
		x[i] = m[i][n]
	}
	return x, nil
}

// gauss solves the augmented n x (n+1) matrix in place (result in the last column).
func gauss(m [][]float64) error {
	n := len(m)
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(m[r][c]) > math.Abs(m[p][c]) {
				p = r
			}
		}
		if math.Abs(m[p][c]) < 1e-12 {
			return errors.New("vision: singular system")
		}
		m[c], m[p] = m[p], m[c]
		for r := 0; r < n; r++ {
			if r == c {
				continue
			}
			f := m[r][c] / m[c][c]
			for k := c; k <= n; k++ {
				m[r][k] -= f * m[c][k]
			}
		}
	}
	for r := 0; r < n; r++ {
		m[r][n] /= m[r][r]
		m[r][r] = 1
	}
	return nil
}

// RMS returns the root mean square reprojection error of h on the pairs.
func RMS(h H, src, dst []Pt) float64 {
	var s float64
	for i := range src {
		p := h.Apply(src[i])
		s += (p.X-dst[i].X)*(p.X-dst[i].X) + (p.Y-dst[i].Y)*(p.Y-dst[i].Y)
	}
	return math.Sqrt(s / float64(len(src)))
}
