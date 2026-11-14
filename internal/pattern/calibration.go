package pattern

import (
	"image"
	"math"
	"runtime"
	"sync"
)

// Calibration draws one monitor's piece of a test pattern defined in desk
// millimetres. Because the pattern lives in physical space, its lines only
// look continuous across the bezels when the layout matches the desk:
//   - a 20 mm grid (100 mm lines brighter) shows vertical and horizontal offsets
//   - two families of 45 degree diagonals show both at once, and are easy to
//     follow across a gap
//   - circles around the layout centre show scale errors (a wrongly sized
//     monitor bends them)
//
// resW, resH are the monitor's pixels; xMM, yMM its top-left on the desk;
// wMM, hMM its visible size; cx, cy the pattern centre in desk mm.
func Calibration(resW, resH int, xMM, yMM, wMM, hMM, cx, cy float64) *image.RGBA {
	kx, ky := wMM/float64(resW), hMM/float64(resH) // mm per pixel
	return CalibrationMap(resW, resH, func(x, y float64) (float64, float64) {
		return xMM + x*kx, yMM + y*ky
	}, math.Max(kx, ky), cx, cy)
}

// CalibrationMap draws the pattern with any pixel -> desk mm map (used for
// monitors with a photo-measured perspective shape). mmPerPx sets the line
// width.
func CalibrationMap(resW, resH int, toMM func(x, y float64) (float64, float64), mmPerPx, cx, cy float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, resW, resH))
	half := mmPerPx * 0.75 // at least ~1.5 px wide lines

	rows := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for y := range rows {
				o := img.PixOffset(0, y)
				for x := 0; x < resW; x++ {
					mx, my := toMM(float64(x)+0.5, float64(y)+0.5)
					r, g, b := calibColor(mx, my, cx, cy, half)
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = r, g, b, 255
					o += 4
				}
			}
		}()
	}
	for y := 0; y < resH; y++ {
		rows <- y
	}
	close(rows)
	wg.Wait()
	return img
}

// distance from v to the nearest multiple of step
func near(v, step float64) float64 {
	m := math.Mod(v, step)
	if m < 0 {
		m += step
	}
	return math.Min(m, step-m)
}

func calibColor(x, y, cx, cy, half float64) (uint8, uint8, uint8) {
	dx, dy := x-cx, y-cy
	// diagonals, 150 mm apart, 2 mm wide
	if near((dx-dy)/math.Sqrt2, 150/math.Sqrt2) < 1+half {
		return 0x2e, 0xd3, 0xe6 // cyan, falling to the right
	}
	if near((dx+dy)/math.Sqrt2, 150/math.Sqrt2) < 1+half {
		return 0xff, 0x9f, 0x1c // orange, rising to the right
	}
	// circles every 80 mm
	if near(math.Hypot(dx, dy), 80) < 0.75+half {
		return 0xe0, 0x4f, 0xd8 // magenta
	}
	// grid
	if near(dx, 100) < 0.5+half || near(dy, 100) < 0.5+half {
		return 0x9a, 0xa4, 0xb6
	}
	if near(dx, 20) < half || near(dy, 20) < half {
		return 0x3a, 0x40, 0x4c
	}
	// centre cross
	if (math.Abs(dx) < 1.5+half && math.Abs(dy) < 25) || (math.Abs(dy) < 1.5+half && math.Abs(dx) < 25) {
		return 0xff, 0xff, 0xff
	}
	return 0x10, 0x13, 0x18
}
