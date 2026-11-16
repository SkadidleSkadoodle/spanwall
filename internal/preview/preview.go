// Package preview draws the monitors at their physical positions with
// their slices inside, so a person (or Claude) can check a result without
// looking at the real screens.
package preview

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
)

var (
	bg      = color.RGBA{0x0e, 0x10, 0x14, 0xff}
	outline = color.RGBA{0x4c, 0x9a, 0xff, 0xff}
)

// Render draws the layout to scale. widthPx is the output width; the
// height follows from the layout. slices may be nil to draw outlines only.
func Render(ds []geometry.Display, ps []geometry.Placement, slices []image.Image, widthPx int) *image.RGBA {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for i, d := range ds {
		minX = math.Min(minX, ps[i].XMM)
		minY = math.Min(minY, ps[i].YMM)
		maxX = math.Max(maxX, ps[i].XMM+d.WidthMM)
		maxY = math.Max(maxY, ps[i].YMM+d.HeightMM)
	}
	margin := 20.0 // mm
	spanW := maxX - minX + 2*margin
	spanH := maxY - minY + 2*margin
	s := float64(widthPx) / spanW // px per mm
	out := image.NewRGBA(image.Rect(0, 0, widthPx, int(math.Ceil(spanH*s))))
	draw.Draw(out, out.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	for i, d := range ds {
		r := image.Rect(
			int(math.Round((ps[i].XMM-minX+margin)*s)),
			int(math.Round((ps[i].YMM-minY+margin)*s)),
			int(math.Round((ps[i].XMM-minX+margin+d.WidthMM)*s)),
			int(math.Round((ps[i].YMM-minY+margin+d.HeightMM)*s)),
		)
		if i < len(slices) && slices[i] != nil {
			draw.ApproxBiLinear.Scale(out, r, slices[i], slices[i].Bounds(), draw.Src, nil)
		}
		strokeRect(out, r.Inset(-2), 2, outline)
	}
	return out
}

func strokeRect(img *image.RGBA, r image.Rectangle, t int, c color.Color) {
	u := &image.Uniform{c}
	draw.Draw(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+t), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(r.Min.X, r.Max.Y-t, r.Max.X, r.Max.Y), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+t, r.Max.Y), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(r.Max.X-t, r.Min.Y, r.Max.X, r.Max.Y), u, image.Point{}, draw.Src)
}
