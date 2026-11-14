package render

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
)

func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func TestAutoFrameMovesSubjectOutOfGap(t *testing.T) {
	// Image wider than the canvas: the default fill puts the red blob
	// exactly in the gap between the two screens.
	src := image.NewRGBA(image.Rect(0, 0, 400, 100))
	fillRect(src, src.Rect, color.RGBA{30, 40, 60, 255})
	fillRect(src, image.Rect(185, 30, 215, 70), color.RGBA{230, 40, 30, 255})
	crops := []geometry.Rect{{L: 0, T: 0, R: 130, B: 100}, {L: 170, T: 0, R: 300, B: 100}}
	fr := AutoFrame(src, 300, 100, crops)
	if fr.Identity() {
		t.Fatal("expected a move away from the gap")
	}
	f, _ := FrameFill(400, 100, 300, 100, fr)
	// Blob centre in canvas pixels must land on a screen.
	cx := 200*f.SX - f.OffX
	if cx > 130 && cx < 170 {
		t.Errorf("blob still in the gap at x=%.1f (frame %+v)", cx, fr)
	}
	// The image must still cover the canvas.
	if -f.OffX > 0.5 || -f.OffX+400*f.SX < 299.5 || -f.OffY > 0.5 || -f.OffY+100*f.SY < 99.5 {
		t.Errorf("canvas not covered: %+v", f)
	}
}

func TestAutoFrameKeepsDefaultWhenNothingToGain(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 300, 100))
	fillRect(src, src.Rect, color.RGBA{30, 40, 60, 255})
	fillRect(src, image.Rect(40, 30, 80, 70), color.RGBA{230, 40, 30, 255})
	crops := []geometry.Rect{{L: 0, T: 0, R: 300, B: 100}}
	if fr := AutoFrame(src, 300, 100, crops); !fr.Identity() {
		t.Errorf("same aspect, one screen: want default, got %+v", fr)
	}
}

func TestAccentAndLuminance(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	fillRect(img, img.Rect, color.RGBA{20, 20, 20, 255})
	fillRect(img, image.Rect(0, 0, 50, 20), color.RGBA{20, 90, 220, 255})
	c := AccentColor(img)
	if !(c.B > c.R && c.B > c.G) {
		t.Errorf("expected a blue accent, got %v", c)
	}
	if l := MeanLuminance(img); l <= 0 || l > 0.4 {
		t.Errorf("luminance %v", l)
	}
	grey := image.NewRGBA(image.Rect(0, 0, 10, 10))
	fillRect(grey, grey.Rect, color.RGBA{128, 128, 128, 255})
	if l := MeanLuminance(grey); math.Abs(l-128.0/255) > 0.01 {
		t.Errorf("grey luminance %v", l)
	}
	_ = AccentColor(grey) // neutral fallback, no panic
}

func TestAutoFrameIgnoresPlainBackground(t *testing.T) {
	// A big grey disc on black, taller than the canvas: the default fill
	// shows mostly disc. Smart framing must not slide black space in.
	src := image.NewRGBA(image.Rect(0, 0, 300, 200))
	fillRect(src, src.Rect, color.RGBA{5, 5, 8, 255})
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			if (x-150)*(x-150)+(y-90)*(y-90) < 150*150 {
				src.SetRGBA(x, y, color.RGBA{uint8(120 + (x*7+y*3)%30), 120, 118, 255})
			}
		}
	}
	crops := []geometry.Rect{{L: 0, T: 0, R: 140, B: 100}, {L: 160, T: 0, R: 300, B: 100}}
	fr := AutoFrame(src, 300, 100, crops)
	f, _ := FrameFill(300, 200, 300, 100, fr)
	// Canvas top edge in source rows: the default shows rows 50..150.
	top := f.OffY / f.SY
	if top > 60 {
		t.Errorf("moved towards the black space: top row %.0f (frame %+v)", top, fr)
	}
}
