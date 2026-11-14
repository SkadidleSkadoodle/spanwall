//go:build windows

package server

import (
	"net/http"
	"time"

	"github.com/martingomezmejia-ctrl/spanwall/internal/app"
	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
)

// An overlay is something temporary on the screens (identify cards or the
// calibration pattern). The wallpaper from before the first overlay is kept,
// so any number of calibration updates still restore the real wallpaper.
type overlayState struct {
	kind  string // "identify" or "calibrate"
	saved app.Saved
	timer *time.Timer
}

// showOverlay runs show on the COM thread, saving the wallpaper first if no
// overlay is up yet, and (re)arms the auto-restore timer.
func (s *Server) showOverlay(kind string, ttl time.Duration, show func(*app.Session) error) error {
	_, err := s.session(func(sess *app.Session) (any, error) {
		s.mu.Lock()
		cur := s.overlay
		s.mu.Unlock()
		var saved app.Saved
		if cur != nil {
			saved = cur.saved
			cur.timer.Stop()
		} else {
			saved = sess.SaveWallpaper()
		}
		if err := show(sess); err != nil {
			if cur == nil {
				sess.RestoreWallpaper(saved)
			}
			return nil, err
		}
		st := &overlayState{kind: kind, saved: saved}
		st.timer = time.AfterFunc(ttl, func() { s.endOverlay(true) })
		s.mu.Lock()
		s.overlay = st
		s.mu.Unlock()
		return nil, nil
	})
	if err == nil {
		s.events.publish("overlay")
	}
	return err
}

// endOverlay removes the overlay; restore puts the saved wallpaper back
// (false when something new is being applied anyway).
func (s *Server) endOverlay(restore bool) {
	s.mu.Lock()
	st := s.overlay
	s.overlay = nil
	s.mu.Unlock()
	if st == nil {
		return
	}
	st.timer.Stop()
	if restore {
		s.session(func(sess *app.Session) (any, error) { return nil, sess.RestoreWallpaper(st.saved) })
	}
	s.events.publish("overlay")
}

func (s *Server) identifyHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds int  `json:"seconds"`
		Stop    bool `json:"stop"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if req.Stop {
		s.endOverlay(true)
		writeJSON(w, map[string]any{"restored": true}, nil)
		return
	}
	if req.Seconds <= 0 || req.Seconds > 300 {
		req.Seconds = 10
	}
	err := s.showOverlay("identify", time.Duration(req.Seconds)*time.Second, func(sess *app.Session) error {
		_, err := sess.ShowIdentifyCards()
		return err
	})
	writeJSON(w, map[string]any{"seconds": req.Seconds}, err)
}

// calibrateHandler shows (or updates) the physical test pattern for the
// given unsaved positions. It restores the wallpaper after 10 minutes
// without updates, or on stop.
func (s *Server) calibrateHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Layout     string                        `json:"layout"`
		Placements map[string]geometry.Placement `json:"placements"`
		Stop       bool                          `json:"stop"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if req.Stop {
		s.endOverlay(true)
		writeJSON(w, map[string]any{"restored": true}, nil)
		return
	}
	start := time.Now()
	err := s.showOverlay("calibrate", 10*time.Minute, func(sess *app.Session) error {
		return sess.ShowCalibration(req.Layout, req.Placements)
	})
	writeJSON(w, map[string]any{"ms": time.Since(start).Milliseconds()}, err)
}
