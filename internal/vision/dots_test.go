package vision

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"
)

// fakePhoto paints monitors (pattern images seen through homographies) into
// a dim photo, with brightness loss, colour cast and noise like a phone shot.
func fakePhoto(w, h int, screens []image.Image, hs []H) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	inv := make([]H, len(hs))
	for i := range hs {
		inv[i], _ = hs[i].Inv()
	}
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{22, 20, 26, 255}
			for i, s := range screens {
				p := inv[i].Apply(Pt{float64(x) + 0.5, float64(y) + 0.5})
				b := s.Bounds()
				if p.X >= 0 && p.Y >= 0 && p.X < float64(b.Dx()) && p.Y < float64(b.Dy()) {
					r, g, bl, _ := s.At(int(p.X), int(p.Y)).RGBA()
					c = color.RGBA{uint8(18 + float64(r>>8)*0.8), uint8(16 + float64(g>>8)*0.78), uint8(22 + float64(bl>>8)*0.85), 255}
				}
			}
			n := uint8(rng.Intn(8))
			c.R, c.G, c.B = sat(c.R, n), sat(c.G, n), sat(c.B, n)
			out.SetRGBA(x, y, c)
		}
	}
	return out
}

func sat(v, n uint8) uint8 {
	if int(v)+int(n) > 255 {
		return 255
	}
	return v + n
}

func TestDetectAndMatchThreeMonitors(t *testing.T) {
	type mon struct{ w, h int }
	mons := []mon{{2560, 1600}, {3440, 1440}, {3440, 1440}}
	// Perspective views, roughly like a three-monitor desk seen from the chair.
	hs := []H{
		{0.17, 0.004, 120, -0.01, 0.16, 1500, 0.00002, 0, 1},
		{0.33, 0, 900, 0.003, 0.31, 1300, 0, 0.00001, 1},
		{0.35, -0.004, 840, 0, 0.33, 700, 0, -0.00001, 1},
	}
	var screens []image.Image
	for i, m := range mons {
		screens = append(screens, DrawDots(m.w, m.h, i))
	}
	photo := fakePhoto(2400, 1800, screens, hs)
	blobs := DetectDots(photo)
	for i, m := range mons {
		var mine []Blob
		for _, b := range blobs {
			if b.Class == i {
				mine = append(mine, b)
			}
		}
		g := DotGrid(m.w, m.h)
		gm, err := MatchGrid(mine, g)
		if err != nil {
			t.Fatalf("monitor %d: %v (%d blobs of its colour, %d total)", i, err, len(mine), len(blobs))
		}
		if gm.Matched != len(g.Points) {
			t.Errorf("monitor %d matched %d of %d", i, gm.Matched, len(g.Points))
		}
		// The fitted homography must land the screen corners within ~1.5 photo px.
		for _, c := range []Pt{{0, 0}, {float64(m.w), 0}, {0, float64(m.h)}, {float64(m.w), float64(m.h)}} {
			a, b := gm.H.Apply(c), hs[i].Apply(c)
			if d := math.Hypot(a.X-b.X, a.Y-b.Y); d > 1.5 {
				t.Errorf("monitor %d corner %v off by %.2f px", i, c, d)
			}
		}
		t.Logf("monitor %d: %d/%d dots, rms %.2f px", i, gm.Matched, gm.Expected, gm.RMS)
	}
}

// A washed-out corner dot plus status LEDs near the corners must not throw
// the grid off (photo of Nov 20: Dell LED below its corner, Acer power LED
// above its corner, Acer corner dot not classified).
func TestMatchGridLEDsAndMissingCorner(t *testing.T) {
	g := DotGrid(3440, 1440)
	h := H{0.3, 0.01, 600, -0.012, 0.29, 250, 0.00001, 0.000004, 1}
	var bs []Blob
	n := len(g.Points)
	for i, p := range g.Points {
		if i == n-1 {
			continue // bottom-right dot missing
		}
		bs = append(bs, Blob{C: h.Apply(p), Area: 1000})
	}
	step := h.Apply(g.Points[1]).X - h.Apply(g.Points[0]).X
	br, tl := h.Apply(g.Points[n-1]), h.Apply(g.Points[0])
	bs = append(bs,
		Blob{C: Pt{br.X + 0.8*step, br.Y + 1.2*step}, Area: 400}, // LED below the corner
		Blob{C: Pt{tl.X + 0.1*step, tl.Y - 0.5*step}, Area: 300}) // power LED by the corner
	gm, err := MatchGrid(bs, g)
	if err != nil {
		t.Fatal(err)
	}
	if gm.Matched != n-1 || gm.RMS > 0.01 {
		t.Fatalf("matched %d of %d, rms %.3f", gm.Matched, n, gm.RMS)
	}
}
