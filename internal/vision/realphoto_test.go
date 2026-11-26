package vision

import (
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// TestRealPhotos runs dot matching on calibration photos in local\ (not in
// the repo); it is skipped when they aren't there.
func TestRealPhotos(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "local", "dots-*.jpg"))
	if len(files) == 0 {
		t.Skip("no photos in local")
	}
	// Laptop 2560x1600, Dell 3440x1440, Acer 3440x1440 (palette order).
	mons := [][2]int{{2560, 1600}, {3440, 1440}, {3440, 1440}}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(fh)
		fh.Close()
		if err != nil {
			t.Fatal(err)
		}
		blobs := DetectDots(img)
		for i, m := range mons {
			var mine []Blob
			for _, b := range blobs {
				if b.Class == i {
					mine = append(mine, b)
				}
			}
			g := DotGrid(m[0], m[1])
			gm, err := MatchGrid(mine, g)
			t.Logf("%s monitor %d: %d blobs, matched %d/%d, rms %.1f px, err %v", filepath.Base(f), i, len(mine), gm.Matched, gm.Expected, gm.RMS, err)
		}
	}
}
