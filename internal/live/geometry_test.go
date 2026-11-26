package live

import (
	"math"
	"testing"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
)

func TestBuildPlain(t *testing.T) {
	ms := []Monitor{
		{ID: "a", Screen: [4]int{-1000, 0, 1000, 500}, Display: geometry.Display{ResW: 1000, ResH: 500, WidthMM: 400, HeightMM: 200}, Place: geometry.Placement{XMM: 0, YMM: 50}},
		{ID: "b", Screen: [4]int{0, 0, 2000, 1000}, Display: geometry.Display{ResW: 2000, ResH: 1000, WidthMM: 600, HeightMM: 300}, Place: geometry.Placement{XMM: 420, YMM: 0, Scale: 1.5}},
	}
	d := Build(-1000, 0, 3000, 1000, ms)
	if d.Monitors[0].NX != 2 || d.Monitors[0].Px[0] != 0 || d.Monitors[1].Px[0] != 1000 {
		t.Fatalf("window pixels: %+v", d.Monitors)
	}
	// b's bottom-right corner: 420 + 600*1.5, 0 + 300*1.5
	mm := d.Monitors[1].MM
	if mm[len(mm)-2] != 1320 || mm[len(mm)-1] != 450 {
		t.Fatalf("b corner = %v", mm[len(mm)-2:])
	}
	if d.MM != [4]float64{0, 0, 1320, 450} {
		t.Fatalf("desk = %v", d.MM)
	}
}

func TestBuildQuad(t *testing.T) {
	q := []float64{10, 10, 110, 0, 110, 60, 10, 50} // right edge taller: perspective
	ms := []Monitor{{ID: "q", Screen: [4]int{0, 0, 1000, 500}, Display: geometry.Display{ResW: 1000, ResH: 500, WidthMM: 100, HeightMM: 50},
		Place: geometry.Placement{XMM: 10, YMM: 0, Quad: q}}}
	d := Build(0, 0, 1000, 500, ms)
	m := d.Monitors[0]
	if m.NX != 17 || m.NY != 9 {
		t.Fatalf("grid %dx%d", m.NX, m.NY)
	}
	corner := func(i, j int) (float64, float64) { k := 2 * (j*m.NX + i); return m.MM[k], m.MM[k+1] }
	for n, c := range [][2]int{{0, 0}, {16, 0}, {16, 8}, {0, 8}} {
		x, y := corner(c[0], c[1])
		if math.Abs(x-q[2*n]) > 1e-6 || math.Abs(y-q[2*n+1]) > 1e-6 {
			t.Fatalf("corner %d = %v,%v want %v,%v", n, x, y, q[2*n], q[2*n+1])
		}
	}
	if m.Box != [4]float64{10, 0, 100, 60} {
		t.Fatalf("box %v", m.Box)
	}
}
