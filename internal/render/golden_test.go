package render

import (
	"encoding/json"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
)

// Golden data comes from tools/golden/superpaper_ref.py, which runs
// Superpaper's own span math.
type goldenFile struct {
	Displays []geometry.Display `json:"displays"`
	Offsets  [][2]float64       `json:"offsets"`
	Cases    []struct {
		Name            string       `json:"name"`
		Image           string       `json:"image"`
		Manual          [][2]float64 `json:"manual"`
		NormResolutions [][2]int     `json:"normResolutions"`
		Crops           [][4]int     `json:"crops"`
		Canvas          [2]int       `json:"canvas"`
		Slices          []string     `json:"slices"`
	} `json:"cases"`
}

const goldenDir = "../../testdata"

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDir, "golden.json"))
	if err != nil {
		t.Fatalf("read golden.json: %v (run tools/golden/superpaper_ref.py)", err)
	}
	var g goldenFile
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func loadPNG(t *testing.T, name string) image.Image {
	t.Helper()
	f, err := os.Open(filepath.Join(goldenDir, "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestGoldenGeometry: crops and canvas must match Superpaper exactly.
func TestGoldenGeometry(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Cases {
		norm, err := geometry.NormResolutions(g.Displays)
		if err != nil {
			t.Fatal(err)
		}
		for i := range norm {
			if norm[i] != c.NormResolutions[i] {
				t.Errorf("%s: norm res %d = %v, want %v", c.Name, i, norm[i], c.NormResolutions[i])
			}
		}
		crops, err := geometry.Crops(g.Displays, g.Offsets, c.Manual)
		if err != nil {
			t.Fatal(err)
		}
		for i, cr := range crops {
			want := c.Crops[i]
			if [4]int{cr.L, cr.T, cr.R, cr.B} != want {
				t.Errorf("%s: crop %d = %v, want %v", c.Name, i, cr, want)
			}
		}
		if w, h := geometry.Canvas(crops); [2]int{w, h} != c.Canvas {
			t.Errorf("%s: canvas = %dx%d, want %v", c.Name, w, h, c.Canvas)
		}
	}
}

// TestGoldenSlices: rendered slices must be visually identical to
// Superpaper's. Pixels can't match exactly because Spanwall resamples once
// where Superpaper resamples twice, so this checks the average difference
// and that no region is shifted (which would show up as a large max error
// along the grid lines).
func TestGoldenSlices(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Cases {
		src := loadPNG(t, c.Image)
		crops, err := geometry.Crops(g.Displays, g.Offsets, c.Manual)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Slices(src, g.Displays, crops)
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range c.Slices {
			want := loadPNG(t, name)
			mean, p99 := diff(got[i], want)
			t.Logf("%s slice %d: mean diff %.2f, 99th percentile %.1f (0-255)", c.Name, i, mean, p99)
			if mean > 0.5 || p99 > 2 {
				t.Errorf("%s slice %d differs from Superpaper: mean %.2f, p99 %.1f", c.Name, i, mean, p99)
			}
		}
	}
}

func diff(a, b image.Image) (mean, p99 float64) {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return math.Inf(1), math.Inf(1)
	}
	var hist [256 * 3]int
	total, n := 0.0, 0
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
			r1, g1, b1, _ := a.At(ab.Min.X+x, ab.Min.Y+y).RGBA()
			r2, g2, b2, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			for _, d := range []int{absDiff(r1, r2), absDiff(g1, g2), absDiff(b1, b2)} {
				total += float64(d)
				hist[d]++
				n++
			}
		}
	}
	cut, acc := int(float64(n)*0.99), 0
	for v, cnt := range hist {
		acc += cnt
		if acc >= cut {
			p99 = float64(v)
			break
		}
	}
	return total / float64(n), p99
}

func absDiff(a, b uint32) int {
	x, y := int(a>>8), int(b>>8)
	if x > y {
		return x - y
	}
	return y - x
}
