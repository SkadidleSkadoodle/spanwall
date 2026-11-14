package render

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
)

// A warp equal to the rectangular crop must give the same picture as Slices.
func TestWarpMatchesAffineForRectangle(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			src.Set(x, y, color.RGBA{uint8(x / 4), uint8(y / 2), uint8((x + y) / 6), 255})
		}
	}
	ds := []geometry.Display{{ID: "a", ResW: 400, ResH: 200, WidthMM: 400, HeightMM: 200}, {ID: "b", ResW: 400, ResH: 200, WidthMM: 400, HeightMM: 200}}
	crops := []geometry.Rect{{L: 0, T: 0, R: 400, B: 200}, {L: 400, T: 200, R: 800, B: 400}}
	want, err := Slices(src, ds, crops)
	if err != nil {
		t.Fatal(err)
	}
	w := &Warp{1, 0, 400, 0, 1, 200, 0, 0, 1}
	got, err := SlicesWarp(src, ds, crops, []Mapper{nil, w})
	if err != nil {
		t.Fatal(err)
	}
	var diff float64
	for i := range want[1].Pix {
		diff += math.Abs(float64(want[1].Pix[i]) - float64(got[1].Pix[i]))
	}
	if m := diff / float64(len(want[1].Pix)); m > 1.0 {
		t.Errorf("mean difference %.2f", m)
	}
	if &got[0].Pix[0] == nil || got[0].Pix[0] != want[0].Pix[0] {
		t.Errorf("plain slice changed")
	}
}

func TestFrameZoomAndMove(t *testing.T) {
	f, _ := FrameFill(1000, 500, 1000, 500, Frame{Zoom: 2, DX: 0.25})
	// Zoom 2: image is 2000 wide, centred then moved right by 500: x0 = 500-1000+500 = 0.
	if f.SX != 2 || f.OffX != 0 || f.OffY != 250 {
		t.Errorf("%+v", f)
	}
	src := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for i := range src.Pix {
		src.Pix[i] = 200
	}
	ds := []geometry.Display{{ID: "a", ResW: 100, ResH: 50, WidthMM: 100, HeightMM: 50}}
	out, err := SlicesFramed(src, ds, []geometry.Rect{{R: 100, B: 50}}, nil, Frame{Zoom: 1, DX: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	// Moved half a width right: left half black, right half image.
	if l, r := out[0].RGBAAt(10, 25), out[0].RGBAAt(90, 25); l.R != 0 || l.A != 255 || r.R < 190 {
		t.Errorf("left %v right %v", l, r)
	}
}

func TestMeshMatchesWarp(t *testing.T) {
	w := &Warp{1.1, 0.05, 30, -0.02, 0.9, 12, 0, 0, 1} // affine, so a mesh reproduces it exactly
	m := &MeshWarp{NX: 5, NY: 3, W: 400, H: 200}
	for j := 0; j < 3; j++ {
		for i := 0; i < 5; i++ {
			x, y := w.Map(400*float64(i)/4, 200*float64(j)/2)
			m.Pts = append(m.Pts, x, y)
		}
	}
	for _, p := range [][2]float64{{0, 0}, {123, 77}, {399, 199}, {250, 10}} {
		ax, ay := w.Map(p[0], p[1])
		bx, by := m.Map(p[0], p[1])
		if math.Abs(ax-bx) > 1e-6 || math.Abs(ay-by) > 1e-6 {
			t.Errorf("%v: %v,%v vs %v,%v", p, ax, ay, bx, by)
		}
	}
}
