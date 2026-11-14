package imageio

import "image"

// TrimBars returns the image without black letterbox or pillarbox bars
// (rows or columns that are almost entirely near-black), common in movie
// stills. It never trims more than a third of the image per side, and
// returns the image unchanged if no bars are found.
func TrimBars(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dark := func(x, y int) bool {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return r>>8 < 24 && g>>8 < 24 && bl>>8 < 24
	}
	rowDark := func(y int) bool {
		n := 0
		for x := 0; x < w; x += 2 {
			if !dark(x, y) {
				n++
			}
		}
		return n*2 <= w/200 // at most 0.5% lit pixels
	}
	colDark := func(x, y0, y1 int) bool {
		n := 0
		for y := y0; y < y1; y += 2 {
			if !dark(x, y) {
				n++
			}
		}
		return n*2 <= (y1-y0)/200
	}
	top, bottom := 0, h
	for top < h/3 && rowDark(top) {
		top++
	}
	for bottom > h-h/3 && rowDark(bottom-1) {
		bottom--
	}
	left, right := 0, w
	for left < w/3 && colDark(left, top, bottom) {
		left++
	}
	for right > w-w/3 && colDark(right-1, top, bottom) {
		right--
	}
	// Ignore tiny edges (1-2 px of dark border are part of many images).
	if top < 3 {
		top = 0
	}
	if h-bottom < 3 {
		bottom = h
	}
	if left < 3 {
		left = 0
	}
	if w-right < 3 {
		right = w
	}
	if top == 0 && bottom == h && left == 0 && right == w {
		return img
	}
	r := image.Rect(b.Min.X+left, b.Min.Y+top, b.Min.X+right, b.Min.Y+bottom)
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	return img
}
