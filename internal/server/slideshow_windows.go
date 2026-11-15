//go:build windows

package server

import (
	"fmt"
	"math/rand"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/martingomezmejia-ctrl/spanwall/internal/app"
	"github.com/martingomezmejia-ctrl/spanwall/internal/config"
	"github.com/martingomezmejia-ctrl/spanwall/internal/vdesk"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

func interval(ss config.Slideshow) time.Duration {
	if ss.IntervalMin <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(ss.IntervalMin) * time.Minute
}

// slideStep shows the next (or previous) image set.
func (s *Server) slideStep(back bool) (any, error) {
	s.endOverlay(false)
	v, err := s.session(func(sess *app.Session) (any, error) {
		dir := wallpaperDir(sess.Cfg)
		lib, err := app.Library(dir)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(lib))
		for i, im := range lib {
			names[i] = im.Name
		}
		ss := sess.Cfg.Slideshow
		// Start the history with what is on screen now (if it came from the
		// folder), so "previous" can return to it.
		if !back && len(ss.History) == 0 {
			if la := sess.Cfg.LastApplied; la != nil && len(la.Images) > 0 {
				var cur []string
				for _, p := range la.Images {
					if strings.EqualFold(filepath.Dir(p), filepath.Clean(dir)) {
						cur = append(cur, filepath.Base(p))
					}
				}
				if len(cur) == len(la.Images) {
					ss.History = [][]string{cur}
				}
			}
		}
		mode := ss.Mode
		if mode == "" {
			mode = "span"
		}
		count := app.SlideshowCount(mode, len(sess.SpanGroups()), len(sess.Monitors))
		weights := app.SlideWeights(lib, sess.Cfg, period(sess.Cfg, time.Now()))
		if !back {
			any := false
			for _, v := range weights {
				any = any || v > 0
			}
			if !any && ss.Source != "" {
				name := ss.Source
				if name == "fav" {
					name = "Favourites"
				}
				return nil, fmt.Errorf("the %s album has nothing to show yet", name)
			}
		}
		pick, hist, err := app.PickSlides(names, ss, count, back, rng, weights)
		if err != nil {
			return nil, err
		}
		imgs := make([]string, len(pick))
		for i, n := range pick {
			imgs[i] = filepath.Join(dir, n)
		}
		res, err := sess.Apply(app.ApplyRequest{Images: imgs, Mode: mode})
		if err != nil {
			return nil, err
		}
		sess.Cfg.Slideshow.History = hist
		if sess.Cfg.Slideshow.Enabled {
			sess.Cfg.Slideshow.Next = time.Now().Add(interval(ss)).Format(time.RFC3339)
		}
		return map[string]any{"images": pick, "mode": mode, "totalMs": res.TotalMS}, sess.Save()
	})
	if err == nil {
		vdesk.ClearSavedWallpapers()
		go s.prerenderDesktops()
	}
	s.changed("wallpaper", err)
	return v, err
}

// slideshowLoop changes the wallpaper when the slideshow is due.
func (s *Server) slideshowLoop() {
	for range time.Tick(20 * time.Second) {
		s.checkSchedule()
		due, _ := s.session(func(sess *app.Session) (any, error) {
			ss := &sess.Cfg.Slideshow
			if !ss.Enabled {
				return false, nil
			}
			next, err := time.Parse(time.RFC3339, ss.Next)
			if err != nil {
				ss.Next = time.Now().Add(interval(*ss)).Format(time.RFC3339)
				return false, sess.Save()
			}
			return time.Now().After(next), nil
		})
		if due == true {
			s.mu.Lock()
			busy := s.overlay != nil
			s.mu.Unlock()
			if busy {
				continue // don't replace test cards or a pattern; try again later
			}
			if v, err := s.slideStep(false); err != nil {
				s.log.Printf("slideshow: %v", err)
				s.notify("Slideshow couldn't change the wallpaper", err.Error(), true)
				s.session(func(sess *app.Session) (any, error) { // try again next interval, not every 20 s
					sess.Cfg.Slideshow.Next = time.Now().Add(interval(sess.Cfg.Slideshow)).Format(time.RFC3339)
					return nil, sess.Save()
				})
			} else {
				s.log.Printf("slideshow: %v", v)
			}
		}
	}
}

func (s *Server) getSlideshow(w http.ResponseWriter, r *http.Request) {
	v, err := s.session(func(sess *app.Session) (any, error) { return slideshowInfo(sess), nil })
	writeJSON(w, v, err)
}

func slideshowInfo(sess *app.Session) map[string]any {
	ss := sess.Cfg.Slideshow
	var cur []string
	if n := len(ss.History); n > 0 {
		cur = ss.History[n-1]
	}
	mode := ss.Mode
	if mode == "" {
		mode = "span"
	}
	order := ss.Order
	if order == "" {
		order = "shuffle"
	}
	return map[string]any{"enabled": ss.Enabled, "intervalMin": int(interval(ss) / time.Minute), "order": order,
		"mode": mode, "source": ss.Source, "next": ss.Next, "current": cur, "canGoBack": len(ss.History) > 1}
}

func (s *Server) setSlideshow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled     *bool   `json:"enabled"`
		IntervalMin *int    `json:"intervalMin"`
		Order       *string `json:"order"`
		Mode        *string `json:"mode"`
		Source      *string `json:"source"` // "" all, "fav", or a folder name
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		ss := &sess.Cfg.Slideshow
		if req.IntervalMin != nil {
			if *req.IntervalMin < 1 || *req.IntervalMin > 30*24*60 {
				return nil, fmt.Errorf("interval must be 1 minute to 30 days")
			}
			ss.IntervalMin = *req.IntervalMin
		}
		if req.Order != nil {
			if *req.Order != "shuffle" && *req.Order != "name" {
				return nil, fmt.Errorf("order is shuffle or name")
			}
			ss.Order = *req.Order
		}
		if req.Mode != nil {
			if *req.Mode != "span" && *req.Mode != "groups" && *req.Mode != "per-display" {
				return nil, fmt.Errorf("mode is span, groups or per-display")
			}
			ss.Mode = *req.Mode
		}
		if req.Source != nil {
			src := strings.TrimSpace(*req.Source)
			switch {
			case src == "" || strings.EqualFold(src, "all"):
				src = ""
			case strings.EqualFold(src, "fav") || strings.EqualFold(src, "favourites") || strings.EqualFold(src, "favorites"):
				src = "fav"
			default:
				i := sess.Cfg.FolderIndex(src)
				if i < 0 {
					return nil, fmt.Errorf("no album called %q", src)
				}
				src = sess.Cfg.Folders[i].Name
			}
			ss.Source = src
		}
		if req.Enabled != nil {
			ss.Enabled = *req.Enabled
		}
		if ss.Enabled && (req.Enabled != nil || req.IntervalMin != nil) {
			ss.Next = time.Now().Add(interval(*ss)).Format(time.RFC3339)
		}
		if !ss.Enabled {
			ss.Next = ""
		}
		return slideshowInfo(sess), sess.Save()
	})
	s.changed("slideshow", err)
	writeJSON(w, v, err)
}

func (s *Server) slideshowNext(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Back bool `json:"back"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.slideStep(req.Back)
	writeJSON(w, v, err)
}

// toggleSlideshow is for the tray and hotkeys.
func (s *Server) toggleSlideshow() (bool, error) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		ss := &sess.Cfg.Slideshow
		ss.Enabled = !ss.Enabled
		ss.Next = ""
		if ss.Enabled {
			ss.Next = time.Now().Add(interval(*ss)).Format(time.RFC3339)
		}
		return ss.Enabled, sess.Save()
	})
	s.changed("slideshow", err)
	on, _ := v.(bool)
	return on, err
}
