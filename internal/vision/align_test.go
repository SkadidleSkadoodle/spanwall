package vision

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"
)

// wallpaperLike makes a detailed, smooth test image (gradients, soft blobs,
// a few hard edges), like a painted wallpaper.
func wallpaperLike(w, h int, seed int64) *image.RGBA {
	rng := rand.New(rand.NewSource(seed))
	type blob struct{ x, y, r, v float64 }
	var bs []blob
	for i := 0; i < 40; i++ {
		bs = append(bs, blob{rng.Float64() * float64(w), rng.Float64() * float64(h), (0.03 + rng.Float64()*0.12) * float64(h), rng.Float64()*160 - 60})
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 60 + 50*math.Sin(float64(x)/float64(w)*7+float64(seed)) + 30*math.Cos(float64(y)/float64(h)*5)
			for _, b := range bs {
				d := math.Hypot(float64(x)-b.x, float64(y)-b.y)
				if d < b.r {
					v += b.v * (1 - d/b.r)
				}
			}
			if (x/97+y/61)%7 == 0 {
				v += 40
			}
			v = math.Max(0, math.Min(255, v))
			img.SetRGBA(x, y, color.RGBA{uint8(v), uint8(v * 0.8), uint8(255 - v*0.6), 255})
		}
	}
	return img
}

func TestAlignScreensRecoversFromOffPrior(t *testing.T) {
	type mon struct{ w, h int }
	mons := []mon{{2560, 1600}, {3440, 1440}, {3440, 1440}}
	truth := []H{
		{0.17, 0.004, 120, -0.01, 0.16, 1500, 0.00002, 0, 1},
		{0.33, 0, 900, 0.003, 0.31, 1300, 0, 0.00001, 1},
		{0.35, -0.004, 840, 0, 0.33, 700, 0, -0.00001, 1},
	}
	var imgs []image.Image
	for i, m := range mons {
		imgs = append(imgs, wallpaperLike(m.w, m.h, int64(i+3)))
	}
	photo := fakePhoto(2400, 1800, imgs, truth)
	pg, back := NewGray(photo, 1600)
	toG := Scale(1/back, 1/back)
	// The new photo was taken a bit further left and closer: the prior is
	// off by 3% zoom and ~70 px.
	shift := H{1.03, 0, -60, 0, 1.03, 40, 0, 0, 1}
	var screens []Screen
	for i, m := range mons {
		g, sb := NewGray(imgs[i], 900)
		prior, _ := shift.Inv()
		screens = append(screens, Screen{Image: g, Scale: sb, ResW: m.w, ResH: m.h, Prior: Mul(toG, Mul(prior, truth[i]))})
	}
	res, err := AlignScreens(pg, screens)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range mons {
		want := Mul(toG, truth[i])
		worst := 0.0
		for _, c := range []Pt{{0, 0}, {float64(m.w), 0}, {0, float64(m.h)}, {float64(m.w), float64(m.h)}} {
			a, b := res[i].H.Apply(c), want.Apply(c)
			worst = math.Max(worst, math.Hypot(a.X-b.X, a.Y-b.Y))
		}
		t.Logf("monitor %d: ncc %.3f, worst corner error %.2f px (photo is %dx%d)", i, res[i].NCC, worst, pg.W, pg.H)
		if !res[i].OK || worst > 2 {
			t.Errorf("monitor %d not recovered: ncc %.3f, corner error %.2f px", i, res[i].NCC, worst)
		}
	}
}
