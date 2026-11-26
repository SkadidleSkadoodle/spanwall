//go:build windows

package server

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/live"
	"github.com/SkadidleSkadoodle/spanwall/internal/paths"
	"github.com/SkadidleSkadoodle/spanwall/internal/web"
)

// Live scenes in the library: ratings (fav, ban) under "live:<name>" in the
// config's ratings, snapshot pictures made by the editor, and the "live"
// slideshow album, which steps through scenes instead of still images.

func liveKey(name string) string { return "live:" + name }

// liveAlbum reports whether the slideshow plays live scenes.
func (s *Server) liveAlbum() bool {
	v, _ := s.session(func(sess *app.Session) (any, error) { return sess.Cfg.Slideshow.Source == "live", nil })
	on, _ := v.(bool)
	return on
}

// liveStep shows the next (or previous) scene of the live album.
func (s *Server) liveStep(back bool) (any, error) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		ss := sess.Cfg.Slideshow
		var names []string
		weights := map[string]float64{}
		for _, si := range live.List(web.FS(), scenesDir()) {
			names = append(names, si.Name)
			switch sess.Cfg.Ratings[liveKey(si.Name)] {
			case "ban":
				weights[si.Name] = 0
			case "fav":
				weights[si.Name] = 3
			default:
				weights[si.Name] = 1
			}
		}
		if !back && len(ss.History) == 0 && sess.Cfg.Live.Enabled {
			ss.History = [][]string{{sess.Cfg.Live.SceneName()}}
		}
		pick, hist, err := app.PickSlides(names, ss, 1, back, rng, weights)
		if err != nil {
			return nil, err
		}
		if len(pick) == 0 || !live.ValidName(pick[0]) {
			return nil, errors.New("the slideshow history has no scene to go back to")
		}
		l := &sess.Cfg.Live
		l.Scene, l.Enabled = pick[0], true
		sess.Cfg.Slideshow.History = hist
		if sess.Cfg.Slideshow.Enabled {
			sess.Cfg.Slideshow.Next = time.Now().Add(interval(ss)).Format(time.RFC3339)
		}
		return map[string]any{"scene": pick[0]}, sess.Save()
	})
	if err == nil {
		s.liveSync()
		s.liveSend("reload")
		s.live.mu.Lock()
		s.live.watchVer = ""
		s.live.mu.Unlock()
		s.events.publish("live")
	}
	s.changed("slideshow", err)
	return v, err
}

func (s *Server) liveRate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Rating string `json:"rating"` // fav, ban or none
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if !live.ValidName(req.Name) {
		writeJSON(w, nil, fmt.Errorf("bad scene name %q", req.Name))
		return
	}
	if req.Rating != "fav" && req.Rating != "ban" && req.Rating != "none" {
		writeJSON(w, nil, errors.New("rating is fav, ban or none"))
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		c := sess.Cfg
		if c.Ratings == nil {
			c.Ratings = map[string]string{}
		}
		if req.Rating == "none" {
			delete(c.Ratings, liveKey(req.Name))
		} else {
			c.Ratings[liveKey(req.Name)] = req.Rating
		}
		return map[string]any{"name": req.Name, "rating": req.Rating}, sess.Save()
	})
	s.changed("live", err)
	writeJSON(w, v, err)
}

// Snapshots live in the cache: <name>.png and <name>.ver (the scene version
// they were taken of), so an edited scene gets a new one.
func thumbDir() string {
	d, err := paths.CacheDir("live-thumbs")
	if err != nil {
		return ""
	}
	return d
}

func thumbFresh(name, ver string) bool {
	b, err := os.ReadFile(filepath.Join(thumbDir(), name+".ver"))
	return err == nil && string(b) == ver
}

func (s *Server) getLiveThumb(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !live.ValidName(name) {
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	p := filepath.Join(thumbDir(), name+".png")
	if _, err := os.Stat(p); err != nil {
		http.Error(w, "no snapshot yet", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, p)
}

func (s *Server) setLiveThumb(w http.ResponseWriter, r *http.Request) {
	name, ver := r.URL.Query().Get("name"), r.URL.Query().Get("ver")
	if !live.ValidName(name) || ver == "" || strings.ContainsAny(ver, "/\\") {
		writeJSON(w, nil, errors.New("name and ver are required"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		writeJSON(w, nil, errors.New("the snapshot must be a PNG"))
		return
	}
	dir := thumbDir()
	if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0o644); err != nil {
		writeJSON(w, nil, err)
		return
	}
	os.WriteFile(filepath.Join(dir, name+".ver"), []byte(ver), 0o644)
	writeJSON(w, map[string]any{"name": name, "saved": true}, nil)
}
