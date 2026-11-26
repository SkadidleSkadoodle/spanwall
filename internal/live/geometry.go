// Package live runs animated wallpapers: a scene fills a character grid laid
// out in desk millimetres, and a page behind the desktop icons draws it
// through each monitor's measured shape, so characters line up across
// bezels the same way spanned images do.
package live

import (
	"math"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
	"github.com/SkadidleSkadoodle/spanwall/internal/render"
	"github.com/SkadidleSkadoodle/spanwall/internal/vision"
)

// Monitor is one attached monitor: where Windows shows it (physical pixels,
// virtual-screen coordinates) and where it sits on the desk.
type Monitor struct {
	ID, Name string
	Screen   [4]int // x, y, w, h
	Display  geometry.Display
	Place    geometry.Placement
}

// Mesh maps a grid of points on a monitor between window pixels (relative to
// the wallpaper window, which covers the virtual screen) and desk mm.
type Mesh struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Screen [4]int     `json:"screen"` // x, y, w, h in virtual-screen pixels
	NX     int        `json:"nx"`
	NY     int        `json:"ny"`
	Px     []float64  `json:"px"`  // x,y pairs, row by row, relative to the window
	MM     []float64  `json:"mm"`  // matching desk mm
	Box    [4]float64 `json:"box"` // mm bounding box x, y, w, h
	// Primary is Windows' main display (the one at 0,0).
	Primary bool `json:"primary,omitempty"`
}

// Desk is everything the page needs to draw.
type Desk struct {
	Window   [4]int     `json:"window"` // virtual screen x, y, w, h
	MM       [4]float64 `json:"mm"`     // union of the monitors' shapes, mm
	Monitors []Mesh     `json:"monitors"`
	// Box is the union of the monitors' layout rectangles (position and
	// apparent size, as the editor draws them): what a scene's canvas is
	// framed against, exactly like a spanned image.
	Box [4]float64 `json:"box"`
	// World is the scene's canvas in mm: WorldScale times Box, so it can be
	// moved, turned and zoomed out behind the monitors.
	World [2]float64 `json:"world"`
}

// WorldScale is how much bigger than the monitors a scene's canvas is. The
// default framing (zoom = WorldScale) shows its middle at 1:1.
const WorldScale = 1.5

// PxToMM returns the map from a monitor's own pixels to desk mm, using the
// same shapes as static wallpapers: the curved-screen mesh, else the
// perspective quad, else the plain rectangle.
func PxToMM(d geometry.Display, p geometry.Placement) func(x, y float64) (float64, float64) {
	w, h := float64(d.ResW), float64(d.ResH)
	if p.HasMesh() {
		m := &render.MeshWarp{NX: p.MeshNX, NY: p.MeshNY, W: w, H: h, Pts: p.Mesh}
		return m.Map
	}
	if p.HasQuad() {
		var dst []vision.Pt
		for k := 0; k < 8; k += 2 {
			dst = append(dst, vision.Pt{X: p.Quad[k], Y: p.Quad[k+1]})
		}
		if hm, err := vision.FitHomography([]vision.Pt{{X: 0, Y: 0}, {X: w, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}, dst); err == nil {
			return func(x, y float64) (float64, float64) { q := hm.Apply(vision.Pt{X: x, Y: y}); return q.X, q.Y }
		}
	}
	sx, sy := d.WidthMM*p.S()/w, d.HeightMM*p.S()/h
	return func(x, y float64) (float64, float64) { return p.XMM + x*sx, p.YMM + y*sy }
}

// Build lays out the desk for a wallpaper window covering the virtual
// screen (vx, vy, vw, vh).
func Build(vx, vy, vw, vh int, ms []Monitor) Desk {
	out := Desk{Window: [4]int{vx, vy, vw, vh}}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, m := range ms {
		f := PxToMM(m.Display, m.Place)
		nx, ny := 2, 2
		if m.Place.HasQuad() || m.Place.HasMesh() {
			nx, ny = 17, 9 // fine enough that straight triangles follow the shape
		}
		sw, sh := float64(m.Screen[2]), float64(m.Screen[3])
		// Windows may report a scaled mode; map screen pixels to native ones.
		kx, ky := float64(m.Display.ResW)/sw, float64(m.Display.ResH)/sh
		me := Mesh{ID: m.ID, Name: m.Name, Screen: m.Screen, NX: nx, NY: ny, Primary: m.Screen[0] == 0 && m.Screen[1] == 0}
		bx0, by0, bx1, by1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for j := 0; j < ny; j++ {
			for i := 0; i < nx; i++ {
				px := sw * float64(i) / float64(nx-1)
				py := sh * float64(j) / float64(ny-1)
				x, y := f(px*kx, py*ky)
				me.Px = append(me.Px, px+float64(m.Screen[0]-vx), py+float64(m.Screen[1]-vy))
				me.MM = append(me.MM, round3(x), round3(y))
				bx0, by0, bx1, by1 = math.Min(bx0, x), math.Min(by0, y), math.Max(bx1, x), math.Max(by1, y)
			}
		}
		me.Box = [4]float64{round3(bx0), round3(by0), round3(bx1 - bx0), round3(by1 - by0)}
		out.Monitors = append(out.Monitors, me)
		minX, minY, maxX, maxY = math.Min(minX, bx0), math.Min(minY, by0), math.Max(maxX, bx1), math.Max(maxY, by1)
	}
	if len(ms) > 0 {
		out.MM = [4]float64{round3(minX), round3(minY), round3(maxX - minX), round3(maxY - minY)}
		bx0, by0, bx1, by1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, m := range ms {
			w, h := m.Display.WidthMM*m.Place.S(), m.Display.HeightMM*m.Place.S()
			bx0, by0 = math.Min(bx0, m.Place.XMM), math.Min(by0, m.Place.YMM)
			bx1, by1 = math.Max(bx1, m.Place.XMM+w), math.Max(by1, m.Place.YMM+h)
		}
		out.Box = [4]float64{round3(bx0), round3(by0), round3(bx1 - bx0), round3(by1 - by0)}
		out.World = [2]float64{round3(out.Box[2] * WorldScale), round3(out.Box[3] * WorldScale)}
	}
	return out
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
