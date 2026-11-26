package live

import (
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
	"math"
)

// Ramp is the brightness ramp used for conversions, dark to bright.
const Ramp = " .'`^\",:;Il!i><~+_-?][}{1)(|\\/tfjrxnuvczXYUJCLQ0OZmwqpdbkhao*#MW&8%B@$"

// Clip is an ASCII animation converted from a GIF, video or image, played
// by the _player scene.
type Clip struct {
	Cols   int         `json:"cols"`
	Rows   int         `json:"rows"`
	Aspect float64     `json:"aspect"` // cell width / height it was made for
	Frames []ClipFrame `json:"frames"`
}

// ClipFrame is one frame: a row-major string of Cols*Rows characters and,
// when in colour, base64 RGB per cell.
type ClipFrame struct {
	MS    int    `json:"ms"`
	Chars string `json:"c"`
	RGB   string `json:"rgb,omitempty"`
}

// Limits keep a clip small enough to load quickly in the page.
const (
	MaxCols   = 600
	MaxFrames = 900
	maxCells  = 8_000_000 // cols*rows*frames: keeps a clip to tens of MB
)

// Convert turns frames (all the same size) into a clip cols wide. aspect is
// the cell width/height the clip will be shown with (about 0.55).
func Convert(frames []image.Image, delaysMS []int, cols int, aspect float64, colour bool) (*Clip, error) {
	if len(frames) == 0 {
		return nil, errors.New("no frames")
	}
	if aspect <= 0 {
		aspect = 0.55
	}
	b := frames[0].Bounds()
	if b.Dx() < 2 || b.Dy() < 2 {
		return nil, errors.New("image too small")
	}
	cols = min(max(cols, 8), MaxCols, b.Dx())
	rows := max(1, int(math.Round(float64(cols)*float64(b.Dy())/float64(b.Dx())*aspect)))
	if len(frames) > MaxFrames {
		frames, delaysMS = frames[:MaxFrames], delaysMS[:MaxFrames]
	}
	for len(frames)*cols*rows > maxCells && len(frames) > 1 {
		frames, delaysMS = frames[:len(frames)/2], delaysMS[:len(delaysMS)/2]
	}
	clip := &Clip{Cols: cols, Rows: rows, Aspect: aspect}
	ramp := []byte(Ramp)
	for k, f := range frames {
		rgba := toRGBA(f)
		chars := make([]byte, cols*rows)
		var rgb []byte
		if colour {
			rgb = make([]byte, cols*rows*3)
		}
		for r := 0; r < rows; r++ {
			y0, y1 := b.Min.Y+r*b.Dy()/rows, b.Min.Y+(r+1)*b.Dy()/rows
			y1 = max(y1, y0+1)
			for c := 0; c < cols; c++ {
				x0, x1 := b.Min.X+c*b.Dx()/cols, b.Min.X+(c+1)*b.Dx()/cols
				x1 = max(x1, x0+1)
				var sr, sg, sb, sa, n float64
				for y := y0; y < y1; y++ {
					o := rgba.PixOffset(x0, y)
					for x := x0; x < x1; x++ {
						a := float64(rgba.Pix[o+3]) / 255
						sr += float64(rgba.Pix[o]) // premultiplied: transparent counts as black
						sg += float64(rgba.Pix[o+1])
						sb += float64(rgba.Pix[o+2])
						sa += a
						n++
						o += 4
					}
				}
				sr, sg, sb = sr/n, sg/n, sb/n
				lum := (0.2126*sr + 0.7152*sg + 0.0722*sb) / 255
				lum = math.Pow(lum, 0.8) // lift the darks a little: thin glyphs read darker than pixels
				i := r*cols + c
				chars[i] = ramp[min(len(ramp)-1, int(lum*float64(len(ramp))))]
				if colour {
					m := math.Max(sr, math.Max(sg, sb))
					if m > 0 {
						k := 255 / m // full brightness colour; the glyph carries the brightness
						rgb[3*i], rgb[3*i+1], rgb[3*i+2] = u8(sr*k), u8(sg*k), u8(sb*k)
					}
				}
			}
		}
		fr := ClipFrame{MS: 100, Chars: string(chars)}
		if k < len(delaysMS) && delaysMS[k] > 0 {
			fr.MS = delaysMS[k]
		}
		if colour {
			fr.RGB = base64.StdEncoding.EncodeToString(rgb)
		}
		clip.Frames = append(clip.Frames, fr)
	}
	return clip, nil
}

func u8(v float64) byte { return byte(math.Min(255, math.Round(v))) }

func toRGBA(im image.Image) *image.RGBA {
	if r, ok := im.(*image.RGBA); ok {
		return r
	}
	r := image.NewRGBA(im.Bounds())
	draw.Draw(r, r.Bounds(), im, im.Bounds().Min, draw.Src)
	return r
}

// DecodeGIF returns a GIF's fully composited frames and their delays (ms).
func DecodeGIF(rd io.Reader) ([]image.Image, []int, error) {
	g, err := gif.DecodeAll(rd)
	if err != nil {
		return nil, nil, err
	}
	w, h := g.Config.Width, g.Config.Height
	if w == 0 || h == 0 {
		w, h = g.Image[0].Bounds().Dx(), g.Image[0].Bounds().Dy()
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	var frames []image.Image
	var delays []int
	for i, p := range g.Image {
		var prev *image.RGBA
		disp := byte(0)
		if i < len(g.Disposal) {
			disp = g.Disposal[i]
		}
		if disp == gif.DisposalPrevious {
			prev = image.NewRGBA(canvas.Bounds())
			copy(prev.Pix, canvas.Pix)
		}
		draw.Draw(canvas, p.Bounds(), p, p.Bounds().Min, draw.Over)
		snap := image.NewRGBA(canvas.Bounds())
		copy(snap.Pix, canvas.Pix)
		frames = append(frames, snap)
		d := 100
		if i < len(g.Delay) && g.Delay[i] > 1 {
			d = g.Delay[i] * 10
		}
		delays = append(delays, d)
		switch disp {
		case gif.DisposalBackground:
			draw.Draw(canvas, p.Bounds(), image.NewUniform(color.Transparent), image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = prev
		}
		if len(frames) >= MaxFrames {
			break
		}
	}
	return frames, delays, nil
}
