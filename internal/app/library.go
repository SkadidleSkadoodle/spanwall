package app

import (
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// Version is set by main.
var Version = "dev"

// LibraryImage is one image in the wallpaper folder.
type LibraryImage struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Ratio    float64 `json:"ratio"`
	SizeKB   int64   `json:"sizeKB"`
	Modified string  `json:"modified"`
}

var imageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true}

// Library lists the images in dir, newest first. It reads only image
// headers, so it is fast even for large folders.
func Library(dir string) ([]LibraryImage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []LibraryImage
	for _, e := range entries {
		if e.IsDir() || !imageExt[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		img := LibraryImage{Name: e.Name(), Path: p, SizeKB: info.Size() / 1024, Modified: info.ModTime().Format("2006-01-02 15:04")}
		if f, err := os.Open(p); err == nil {
			if c, _, err := image.DecodeConfig(f); err == nil {
				img.Width, img.Height = c.Width, c.Height
				if c.Height > 0 {
					img.Ratio = float64(int(float64(c.Width)/float64(c.Height)*100)) / 100
				}
			}
			f.Close()
		}
		out = append(out, img)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out, nil
}

// DefaultWallpaperDir is used until the config names one.
func DefaultWallpaperDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Pictures", "Wallpapers")
}
