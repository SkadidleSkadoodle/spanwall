package live

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"testing"
)

func TestConvert(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 100; x < 200; x++ {
			im.Set(x, y, color.RGBA{200, 0, 0, 255}) // right half red, left half black
		}
	}
	c, err := Convert([]image.Image{im}, nil, 20, 0.5, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.Cols != 20 || c.Rows != 5 {
		t.Fatalf("size %dx%d", c.Cols, c.Rows)
	}
	f := c.Frames[0]
	if f.Chars[0] != ' ' || f.Chars[19] == ' ' {
		t.Fatalf("chars %q", f.Chars[:20])
	}
	rgb, _ := base64.StdEncoding.DecodeString(f.RGB)
	if rgb[3*19] != 255 || rgb[3*19+1] != 0 {
		t.Fatalf("colour %v", rgb[3*19:3*19+3])
	}
}

func TestDecodeGIF(t *testing.T) {
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{}
	for i := 0; i < 3; i++ {
		p := image.NewPaletted(image.Rect(0, 0, 10, 10), pal)
		p.SetColorIndex(i, i, 1)
		g.Image = append(g.Image, p)
		g.Delay = append(g.Delay, 5)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	fr, d, err := DecodeGIF(&buf)
	if err != nil || len(fr) != 3 || d[0] != 50 {
		t.Fatalf("frames %d delays %v err %v", len(fr), d, err)
	}
}
