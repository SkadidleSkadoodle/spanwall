//go:build windows

package server

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/image/draw"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/imageio"
)

// preview renders without applying and returns a JPEG (base64 in JSON) plus
// the notes. With no images it draws the layout outlines.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		app.ApplyRequest
		Width int `json:"width"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if req.Width <= 0 || req.Width > 1600 {
		req.Width = 1200
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		if err := resolveImages(sess.Cfg, req.Images); err != nil {
			return nil, err
		}
		img, notes, err := sess.Preview(req.ApplyRequest, req.Width)
		if err != nil {
			return nil, err
		}
		jpg, err := encodeJPEG(img, req.Width, 82)
		if err != nil {
			return nil, err
		}
		return map[string]any{"jpeg": jpg, "notes": notes}, nil
	})
	writeJSON(w, v, err)
}

// thumb serves a downscaled, letterbox-trimmed JPEG of an image, for the
// editor (which draws the wallpaper across the layout itself) and the
// library grid. ?image=<path or name>&w=<width>&trim=0|1
func (s *Server) thumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	width, _ := strconv.Atoi(q.Get("w"))
	if width <= 0 || width > 2400 {
		width = 480
	}
	trim := q.Get("trim") != "0"
	name := q.Get("image")
	var path string
	_, err := s.session(func(sess *app.Session) (any, error) {
		if name == "" && sess.Cfg.LastApplied != nil && len(sess.Cfg.LastApplied.Images) > 0 {
			name = sess.Cfg.LastApplied.Images[0]
		}
		imgs := []string{name}
		if err := resolveImages(sess.Cfg, imgs); err != nil {
			return nil, err
		}
		path = imgs[0]
		return nil, nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	key := fmt.Sprintf("%s|%d|%d|%d|%v", path, st.Size(), st.ModTime().UnixNano(), width, trim)
	jpg, ok := s.thumbs.get(key)
	if !ok {
		img, err := imageio.Load(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if trim {
			img = imageio.TrimBars(img)
		}
		if jpg, err = encodeJPEG(img, width, 85); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.thumbs.put(key, jpg)
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "max-age=300")
	w.Write(jpg)
}

func encodeJPEG(img image.Image, width, quality int) ([]byte, error) {
	b := img.Bounds()
	if b.Dx() > width {
		h := b.Dy() * width / b.Dx()
		dst := image.NewRGBA(image.Rect(0, 0, width, h))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes(), err
}

// thumbCache keeps the last few encoded thumbnails.
type thumbCache struct {
	mu   sync.Mutex
	max  int
	data map[string][]byte
	used map[string]time.Time
}

func newThumbCache(max int) *thumbCache {
	return &thumbCache{max: max, data: map[string][]byte{}, used: map[string]time.Time{}}
}

func (c *thumbCache) get(k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.data[k]
	if ok {
		c.used[k] = time.Now()
	}
	return b, ok
}

func (c *thumbCache) put(k string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) >= c.max {
		oldest, ot := "", time.Now()
		for kk, t := range c.used {
			if t.Before(ot) {
				oldest, ot = kk, t
			}
		}
		delete(c.data, oldest)
		delete(c.used, oldest)
	}
	c.data[k], c.used[k] = b, time.Now()
}
