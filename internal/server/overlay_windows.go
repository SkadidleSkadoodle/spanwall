//go:build windows

package server

import (
	"net/http"
	"time"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
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
// overlay is up yet, and (re)arms the auto-restore timer. ttl 0 means no
// timer: the overlay stays until it is stopped or replaced.
func (s *Server) showOverlay(kind string, ttl time.Duration, show func(*app.Session) error) error {
	_, err := s.session(func(sess *app.Session) (any, error) {
		s.mu.Lock()
		cur := s.overlay
		s.mu.Unlock()
		var saved app.Saved
		if cur != nil {
			saved = cur.saved
			if cur.timer != nil {
				cur.timer.Stop()
			}
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
		if ttl > 0 {
			st.timer = time.AfterFunc(ttl, func() { s.endOverlay(true) })
		}
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
	if st.timer != nil {
		st.timer.Stop()
	}
	if restore {
		s.session(func(sess *app.Session) (any, error) { return nil, sess.RestoreWallpaper(st.saved) })
	}
	s.events.publish("overlay")
}

func (s *Server) identifyHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds int  `json:"seconds"` // 0 = stay until stopped
		Stop    bool `json:"stop"`
		Toggle  bool `json:"toggle"` // stop if the cards are up, else show them until stopped
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if req.Toggle {
		on, err := s.toggleIdentify()
		writeJSON(w, map[string]any{"on": on}, err)
		return
	}
	if req.Stop {
		s.endOverlay(true)
		writeJSON(w, map[string]any{"restored": true}, nil)
		return
	}
	if req.Seconds < 0 || req.Seconds > 300 {
		req.Seconds = 10
	}
	err := s.showIdentify(time.Duration(req.Seconds) * time.Second)
	writeJSON(w, map[string]any{"seconds": req.Seconds, "on": err == nil}, err)
}

// showIdentify puts the numbered cards up (ttl 0 = until stopped).
func (s *Server) showIdentify(ttl time.Duration) error {
	return s.showOverlay("identify", ttl, func(sess *app.Session) error {
		_, err := sess.ShowIdentifyCards()
		return err
	})
}

// toggleIdentify hides the cards if they are up, else shows them until the
// next toggle. It reports whether they are on afterwards.
func (s *Server) toggleIdentify() (bool, error) {
	s.mu.Lock()
	up := s.overlay != nil && s.overlay.kind == "identify"
	s.mu.Unlock()
	if up {
		s.endOverlay(true)
		return false, nil
	}
	return true, s.showIdentify(0)
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
