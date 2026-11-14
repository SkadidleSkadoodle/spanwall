package vision

import (
	"math"
	"testing"
)

func TestFitHomographyRecoversKnown(t *testing.T) {
	want := H{0.62, 0.04, 812, -0.02, 0.58, 1290, 0.00002, -0.00001, 1}
	var src, dst []Pt
	for y := 0; y <= 1440; y += 240 {
		for x := 0; x <= 3440; x += 344 {
			p := Pt{float64(x), float64(y)}
			src = append(src, p)
			dst = append(dst, want.Apply(p))
		}
	}
	got, err := FitHomography(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if e := RMS(got, src, dst); e > 1e-6 {
		t.Fatalf("rms %g", e)
	}
	inv, err := got.Inv()
	if err != nil {
		t.Fatal(err)
	}
	p := inv.Apply(want.Apply(Pt{100, 200}))
	if math.Abs(p.X-100) > 1e-6 || math.Abs(p.Y-200) > 1e-6 {
		t.Fatalf("inverse round trip: %v", p)
	}
}
