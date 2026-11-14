package tray

import (
	"image"
	"image/color"
)

// Icon draws Spanwall's tray icon: three screens arranged like a desk, with
// one gradient spanning them.
func Icon() image.Image {
	const n = 32
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	screens := []image.Rectangle{image.Rect(9, 3, 30, 14), image.Rect(9, 16, 30, 28), image.Rect(1, 18, 8, 27)}
	for _, r := range screens {
		for y := r.Min.Y - 1; y <= r.Max.Y; y++ {
			for x := r.Min.X - 1; x <= r.Max.X; x++ {
				if x < 0 || y < 0 || x >= n || y >= n {
					continue
				}
				if x == r.Min.X-1 || y == r.Min.Y-1 || x == r.Max.X || y == r.Max.Y {
					img.SetNRGBA(x, y, color.NRGBA{235, 238, 245, 255}) // frame
					continue
				}
				// One gradient across all screens: violet at the top left to amber at the bottom right.
				t := float64(x+y) / float64(2*n)
				img.SetNRGBA(x, y, color.NRGBA{uint8(90 + 160*t), uint8(80 + 90*t), uint8(220 - 170*t), 255})
			}
		}
	}
	return img
}
