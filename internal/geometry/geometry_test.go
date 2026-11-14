package geometry

import (
	"math"
	"testing"
)

func TestPyRoundMatchesPython(t *testing.T) {
	cases := map[float64]int{0.5: 0, 1.5: 2, 2.5: 2, -0.5: 0, 3.4999: 3, 3.5: 4}
	for in, want := range cases {
		if got := pyRound(in); got != want {
			t.Errorf("pyRound(%v) = %d, want %d", in, got, want)
		}
	}
}

func TestPlacementRoundTrip(t *testing.T) {
	ds := []Display{
		{ID: "a", ResW: 2560, ResH: 1600, WidthMM: 344.6, HeightMM: 215.4},
		{ID: "b", ResW: 3440, ResH: 1440, WidthMM: 797.2, HeightMM: 333.7},
	}
	ps := []Placement{{XMM: -10, YMM: 120}, {XMM: 350, YMM: 0}}
	offs, err := OffsetsFromPlacements(ds, ps)
	if err != nil {
		t.Fatal(err)
	}
	if offs[0][0] != 0 || offs[1][1] != 0 {
		t.Fatalf("layout not anchored at 0,0: %v", offs)
	}
	back, err := PlacementsFromOffsets(ds, offs)
	if err != nil {
		t.Fatal(err)
	}
	// back is relative to the new origin (-10, 0)
	if math.Abs(back[1].XMM-360) > 1e-9 || math.Abs(back[0].YMM-120) > 1e-9 {
		t.Fatalf("round trip mismatch: %v", back)
	}
}

func TestDensestDisplayKeepsNativeSize(t *testing.T) {
	ds := []Display{
		{ID: "laptop", ResW: 2560, ResH: 1600, WidthMM: 344.6, HeightMM: 215.4},
		{ID: "dell", ResW: 3440, ResH: 1440, WidthMM: 797.2, HeightMM: 333.7},
	}
	norm, err := NormResolutions(ds)
	if err != nil {
		t.Fatal(err)
	}
	if norm[0] != [2]int{2560, 1600} {
		t.Fatalf("laptop should keep native size, got %v", norm[0])
	}
	// Same physical width on the canvas: dell canvas width / laptop canvas width
	// must equal the ratio of physical widths.
	gotRatio := float64(norm[1][0]) / float64(norm[0][0])
	wantRatio := 797.2 / 344.6
	if math.Abs(gotRatio-wantRatio) > 0.001 {
		t.Fatalf("physical scale not preserved: %v vs %v", gotRatio, wantRatio)
	}
}
