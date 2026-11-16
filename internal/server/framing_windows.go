//go:build windows

package server

import (
	"fmt"
	"image"
	"net/http"
	"os"
	"strings"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/render"
)

// getFraming returns an image's saved zoom and position, plus its size so
// the editor can draw it exactly where the renderer will put it.
func (s *Server) getFraming(w http.ResponseWriter, r *http.Request) {
	img := []string{r.URL.Query().Get("image")}
	group := splitIDs(r.URL.Query().Get("group"))
	v, err := s.session(func(sess *app.Session) (any, error) {
		if err := resolveImages(sess.Cfg, img); err != nil {
			return nil, err
		}
		f, err := os.Open(img[0])
		if err != nil {
			return nil, err
		}
		defer f.Close()
		c, _, err := image.DecodeConfig(f)
		if err != nil {
			return nil, fmt.Errorf("can't read %s: %w", img[0], err)
		}
		fr, saved := sess.SavedFrame(img[0], group)
		auto := !saved && sess.Cfg.SmartFrameOn()
		if auto {
			if fr, err = sess.AutoFrameFor(img[0], group); err != nil {
				return nil, err
			}
		}
		if fr.Zoom == 0 {
			fr.Zoom = 1
		}
		return map[string]any{"image": img[0], "width": c.Width, "height": c.Height, "frame": fr, "saved": saved, "auto": auto}, nil
	})
	writeJSON(w, v, err)
}

func (s *Server) setFraming(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"image"`
		Group string `json:"group"` // span group: comma-separated monitor IDs
		Auto  bool   `json:"auto"`  // forget the saved framing (smart framing decides again)
		render.Frame
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		img := []string{req.Image}
		if err := resolveImages(sess.Cfg, img); err != nil {
			return nil, err
		}
		if req.Auto {
			return map[string]any{"image": img[0], "auto": true}, sess.ClearFrame(img[0], splitIDs(req.Group))
		}
		return map[string]any{"image": img[0], "frame": req.Frame}, sess.SetFrame(img[0], splitIDs(req.Group), req.Frame)
	})
	writeJSON(w, v, err)
}

func splitIDs(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
