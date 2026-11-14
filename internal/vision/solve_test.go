package vision

import (
	"math"
	"testing"
)

// Build what the camera would see for known placements, then solve.
func TestSolveRecoversPlacements(t *testing.T) {
	mons := []Monitor{
		{ID: "laptop", ResW: 2560, ResH: 1600, WidthMM: 345, HeightMM: 215},
		{ID: "dell", ResW: 3440, ResH: 1440, WidthMM: 797, HeightMM: 334},
		{ID: "acer", ResW: 3440, ResH: 1440, WidthMM: 797, HeightMM: 334},
	}
	type truth struct{ x, y, s float64 }
	want := []truth{{-357, 47, 1.12}, {0, 0, 1}, {-19, -357.3, 0.97}}
	// Camera: anchor (dell) pixels -> photo, with perspective.
	camA := H{0.31, 0.01, 900, -0.004, 0.30, 1300, 0.000015, 0.000008, 1}
	kax, kay := mons[1].WidthMM/float64(mons[1].ResW), mons[1].HeightMM/float64(mons[1].ResH)
	hs := make([]*H, 3)
	for i, m := range mons {
		kx, ky := m.WidthMM/float64(m.ResW), m.HeightMM/float64(m.ResH)
		// monitor px -> desk mm -> anchor px -> photo
		monToMM := H{kx * want[i].s, 0, want[i].x, 0, ky * want[i].s, want[i].y, 0, 0, 1}
		mmToA := H{1 / kax, 0, 0, 0, 1 / kay, 0, 0, 0, 1} // anchor at (0,0)
		h := Mul(camA, Mul(mmToA, monToMM))
		hs[i] = &h
	}
	got, err := Solve(mons, hs, 1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range got {
		if math.Abs(p.XMM-want[i].x) > 0.05 || math.Abs(p.YMM-want[i].y) > 0.05 || math.Abs(p.Scale-want[i].s) > 1e-4 {
			t.Errorf("%s: got (%.2f, %.2f) x%.4f, want (%.2f, %.2f) x%.4f", p.ID, p.XMM, p.YMM, p.Scale, want[i].x, want[i].y, want[i].s)
		}
		if p.ResidualMM > 0.2 || math.Abs(p.RollDeg) > 0.01 {
			t.Errorf("%s: residual %.3f roll %.3f", p.ID, p.ResidualMM, p.RollDeg)
		}
	}
}
