package imageio

import (
	"image"
	"image/color"
	"testing"
)

// A 3x2 image with a red pixel at the top-left corner.
func marker() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	return img
}

func TestOrient(t *testing.T) {
	// Where the original top-left pixel must end up, and the output size.
	cases := []struct {
		o          int
		w, h, x, y int
	}{
		{2, 3, 2, 2, 0},
		{3, 3, 2, 2, 1},
		{4, 3, 2, 0, 1},
		{5, 2, 3, 0, 0},
		{6, 2, 3, 1, 0},
		{7, 2, 3, 1, 2},
		{8, 2, 3, 0, 2},
	}
	for _, c := range cases {
		out := Orient(marker(), c.o)
		if out.Bounds().Dx() != c.w || out.Bounds().Dy() != c.h {
			t.Errorf("o=%d: size %v, want %dx%d", c.o, out.Bounds().Size(), c.w, c.h)
			continue
		}
		if r, _, _, _ := out.At(c.x, c.y).RGBA(); r>>8 != 255 {
			t.Errorf("o=%d: marker not at (%d,%d)", c.o, c.x, c.y)
		}
	}
}

func TestJPEGOrientation(t *testing.T) {
	// SOI, APP1 Exif with a little-endian IFD0 holding orientation 6, then SOS.
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, // header, IFD0 at 8
		1, 0, // 1 entry
		0x12, 0x01, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, // tag 0x0112 SHORT count 1 value 6
		0, 0, 0, 0}
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	size := len(app1) + 2
	b := []byte{0xFF, 0xD8, 0xFF, 0xE1, byte(size >> 8), byte(size)}
	b = append(b, app1...)
	b = append(b, 0xFF, 0xDA)
	if got := jpegOrientation(b); got != 6 {
		t.Fatalf("orientation = %d, want 6", got)
	}
	if got := jpegOrientation([]byte{0xFF, 0xD8, 0xFF, 0xDA}); got != 0 {
		t.Fatalf("no exif: got %d", got)
	}
}

func TestTrimBars(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 60))
	for y := 10; y < 50; y++ { // content rows 10..49, black bars above and below
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{120, 80, 40, 255})
		}
	}
	got := TrimBars(img).Bounds()
	if got != image.Rect(0, 10, 100, 50) {
		t.Fatalf("trimmed to %v", got)
	}
	// A dark but detailed image must not be trimmed.
	night := image.NewRGBA(image.Rect(0, 0, 100, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 100; x += 7 {
			night.Set(x, y, color.RGBA{200, 200, 90, 255})
		}
	}
	if TrimBars(night).Bounds() != night.Bounds() {
		t.Fatal("trimmed an image without bars")
	}
}
