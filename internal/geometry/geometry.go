// Package geometry turns a physical monitor layout into crop rectangles on a
// shared "PPI normalized" canvas, the same model Superpaper uses for its
// advanced span mode.
//
// The canvas is measured in pixels of the densest display. A display with a
// lower pixel density covers more canvas pixels than its native resolution,
// so one image spanned across displays keeps the same physical scale on all
// of them.
//
// Ported from Superpaper (MIT, see NOTICE): Display.compute_ppi,
// DisplaySystem.compute_ppinorm_resolutions, get_ppi_norm_crops and
// compute_working_canvas.
package geometry

import (
	"errors"
	"math"
)

// Display is the part of a monitor the geometry needs.
type Display struct {
	ID       string  `json:"id"`
	ResW     int     `json:"resW"`     // native pixels
	ResH     int     `json:"resH"`     // native pixels
	WidthMM  float64 `json:"widthMM"`  // physical active area
	HeightMM float64 `json:"heightMM"` // physical active area
}

// PPI is horizontal pixels per inch, like Superpaper's compute_ppi.
func (d Display) PPI() float64 {
	if d.WidthMM <= 0 {
		return 0
	}
	return float64(d.ResW) / d.WidthMM * 25.4
}

// Rect is a crop on the canvas: left, top inclusive; right, bottom exclusive.
type Rect struct {
	L, T, R, B int
}

func (r Rect) W() int { return r.R - r.L }
func (r Rect) H() int { return r.B - r.T }

// Placement is where a display's active area sits on the desk, in mm,
// measured from any fixed origin. Only relative positions matter.
type Placement struct {
	XMM float64 `json:"xMM"`
	YMM float64 `json:"yMM"`
	// Scale is how big the monitor looks from where you sit, relative to its
	// physical size (photo calibration measures it; 0 or 1 = as measured).
	// A monitor closer to you than the others looks bigger, so its slice
	// should cover more of the image.
	Scale float64 `json:"scale,omitempty"`
	// Quad, when set, is where the monitor's corners appear on the desk
	// plane from your seat (TL, TR, BR, BL as x,y mm pairs), measured by
	// photo calibration. It captures tilt and perspective that a rectangle
	// can't; the slice is warped to it. XMM/YMM/Scale then describe its
	// bounding box.
	Quad []float64 `json:"quad,omitempty"`
	// Mesh is a finer shape (MeshNX x MeshNY points, row by row, x,y mm)
	// for curved screens, measured from the dot photo. Rendering uses it in
	// place of the quad; Quad then holds its corners.
	Mesh   []float64 `json:"mesh,omitempty"`
	MeshNX int       `json:"meshNX,omitempty"`
	MeshNY int       `json:"meshNY,omitempty"`
	// ResetShape asks a save to drop the quad (editor only; never stored).
	ResetShape bool `json:"resetShape,omitempty"`
}

// HasMesh reports whether p has a curved-screen mesh.
func (p Placement) HasMesh() bool {
	return p.MeshNX >= 2 && p.MeshNY >= 2 && len(p.Mesh) == 2*p.MeshNX*p.MeshNY
}

// HasQuad reports whether p has a perspective shape.
func (p Placement) HasQuad() bool { return len(p.Quad) == 8 }

// QuadBox is the bounding box of a quad.
func QuadBox(q []float64) (x, y, w, h float64) {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for i := 0; i < 8; i += 2 {
		minX, maxX = math.Min(minX, q[i]), math.Max(maxX, q[i])
		minY, maxY = math.Min(minY, q[i+1]), math.Max(maxY, q[i+1])
	}
	return minX, minY, maxX - minX, maxY - minY
}

// CarryQuad keeps old's measured shape (quad and mesh) when a monitor is
// moved or rescaled in the editor (which only edits position and scale):
// the shape follows the new top-left and scale. A shape already in next is
// kept; ResetShape drops both.
func CarryQuad(old, next Placement) Placement {
	if next.ResetShape {
		next.Quad, next.Mesh, next.MeshNX, next.MeshNY, next.ResetShape = nil, nil, 0, 0, false
		return next
	}
	k := next.S() / old.S()
	tr := func(v []float64) []float64 {
		q := make([]float64, len(v))
		for i := 0; i+1 < len(v); i += 2 {
			q[i] = next.XMM + k*(v[i]-old.XMM)
			q[i+1] = next.YMM + k*(v[i+1]-old.YMM)
		}
		return q
	}
	if !next.HasMesh() && old.HasMesh() {
		next.Mesh, next.MeshNX, next.MeshNY = tr(old.Mesh), old.MeshNX, old.MeshNY
		if old.HasQuad() {
			next.Quad = tr(old.Quad)
		}
	} else if !next.HasQuad() && old.HasQuad() {
		next.Quad = tr(old.Quad)
	}
	return next
}

// S returns the effective scale (1 when unset).
func (p Placement) S() float64 {
	if p.Scale <= 0 {
		return 1
	}
	return p.Scale
}

// Apparent returns the displays with each physical size multiplied by its
// placement's scale: the size each monitor appears to have.
func Apparent(ds []Display, ps []Placement) []Display {
	out := make([]Display, len(ds))
	for i, d := range ds {
		if i < len(ps) {
			d.WidthMM *= ps[i].S()
			d.HeightMM *= ps[i].S()
		}
		out[i] = d
	}
	return out
}

// pyRound matches Python 3's round(): halves go to the even neighbour.
// Superpaper relies on it, so the golden tests need it too.
func pyRound(x float64) int { return int(math.RoundToEven(x)) }

// MaxPPI returns the highest pixel density in the system.
func MaxPPI(ds []Display) float64 {
	m := 0.0
	for _, d := range ds {
		if p := d.PPI(); p > m {
			m = p
		}
	}
	return m
}

// NormResolutions is each display's size on the canvas.
func NormResolutions(ds []Display) ([][2]int, error) {
	maxPPI := MaxPPI(ds)
	if maxPPI == 0 {
		return nil, errors.New("geometry: no display has a physical size")
	}
	out := make([][2]int, len(ds))
	for i, d := range ds {
		if d.PPI() == 0 {
			return nil, errors.New("geometry: display " + d.ID + " has no physical size")
		}
		rel := d.PPI() / maxPPI
		out[i] = [2]int{pyRound(float64(d.ResW) / rel), pyRound(float64(d.ResH) / rel)}
	}
	return out, nil
}

// Crops places each display on the canvas. offsets are canvas pixels (the
// format Superpaper stores as ppi_norm_offsets, bezels already included);
// manual are extra per-display shifts in canvas pixels and may be nil.
func Crops(ds []Display, offsets [][2]float64, manual [][2]float64) ([]Rect, error) {
	if len(offsets) != len(ds) {
		return nil, errors.New("geometry: one offset per display is required")
	}
	norm, err := NormResolutions(ds)
	if err != nil {
		return nil, err
	}
	crops := make([]Rect, len(ds))
	for i := range ds {
		var m [2]float64
		if i < len(manual) {
			m = manual[i]
		}
		l := pyRound(offsets[i][0] + m[0])
		t := pyRound(offsets[i][1] + m[1])
		crops[i] = Rect{L: l, T: t, R: l + norm[i][0], B: t + norm[i][1]}
	}
	return crops, nil
}

// OffsetsFromPlacements converts a layout in mm to canvas pixel offsets,
// shifted so the top-left most display edge sits at (0, 0).
func OffsetsFromPlacements(ds []Display, ps []Placement) ([][2]float64, error) {
	if len(ps) != len(ds) {
		return nil, errors.New("geometry: one placement per display is required")
	}
	maxPPI := MaxPPI(ds)
	if maxPPI == 0 {
		return nil, errors.New("geometry: no display has a physical size")
	}
	ppmm := maxPPI / 25.4
	minX, minY := math.Inf(1), math.Inf(1)
	for _, p := range ps {
		minX = math.Min(minX, p.XMM)
		minY = math.Min(minY, p.YMM)
	}
	out := make([][2]float64, len(ps))
	for i, p := range ps {
		out[i] = [2]float64{(p.XMM - minX) * ppmm, (p.YMM - minY) * ppmm}
	}
	return out, nil
}

// PlacementsFromOffsets is the inverse of OffsetsFromPlacements, used to
// import a Superpaper layout.
func PlacementsFromOffsets(ds []Display, offsets [][2]float64) ([]Placement, error) {
	maxPPI := MaxPPI(ds)
	if maxPPI == 0 {
		return nil, errors.New("geometry: no display has a physical size")
	}
	ppmm := maxPPI / 25.4
	out := make([]Placement, len(offsets))
	for i, o := range offsets {
		out[i] = Placement{XMM: o[0] / ppmm, YMM: o[1] / ppmm}
	}
	return out, nil
}

// Canvas is the working canvas size, like Superpaper's
// compute_working_canvas: from (0, 0) to the furthest right and bottom edge.
func Canvas(crops []Rect) (w, h int) {
	for _, c := range crops {
		if c.R > w {
			w = c.R
		}
		if c.B > h {
			h = c.B
		}
	}
	return w, h
}
