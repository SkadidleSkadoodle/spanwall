//go:build windows

package server

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/martingomezmejia-ctrl/spanwall/internal/app"
	"github.com/martingomezmejia-ctrl/spanwall/internal/vdesk"
)

// photoPattern shows (or removes) the coloured dot grid for a phone photo.
// It stays up for 15 minutes or until a photo is processed.
func (s *Server) photoPattern(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Stop bool `json:"stop"`
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
	err := s.showOverlay("photo", 15*time.Minute, func(sess *app.Session) error { return sess.ShowPhotoPattern() })
	writeJSON(w, map[string]any{"showing": err == nil,
		"tips": "Sit where you normally sit, fit every screen fully in the frame (1x lens if it fits), avoid glare, then send or drop the photo."}, err)
}

// align works out a layout from a photo: JSON {path, mode, layout}, or a
// multipart upload with a "photo" file (and optional mode/layout fields).
// The result is kept as the pending proposal; nothing is saved until
// /api/align/apply.
func (s *Server) align(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path   string `json:"path"`
		Mode   string `json:"mode"`
		Layout string `json:"layout"`
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeJSON(w, nil, err)
			return
		}
		f, hdr, err := r.FormFile("photo")
		if err != nil {
			writeJSON(w, nil, fmt.Errorf("no photo in the upload: %w", err))
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, 64<<20))
		f.Close()
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		if req.Path, err = app.SaveInboxPhoto(data, strings.ToLower(filepath.Ext(hdr.Filename))); err != nil {
			writeJSON(w, nil, err)
			return
		}
		req.Mode, req.Layout = r.FormValue("mode"), r.FormValue("layout")
	} else if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if req.Path == "" {
		writeJSON(w, nil, fmt.Errorf("give a photo path or upload a photo"))
		return
	}
	// If the dot pattern is up, this is a pattern photo; put the wallpaper back.
	s.mu.Lock()
	patternUp := s.overlay != nil && s.overlay.kind == "photo"
	s.mu.Unlock()
	if patternUp && (req.Mode == "" || req.Mode == "auto") {
		req.Mode = "pattern"
	}
	if req.Mode == "wallpaper" || req.Mode == "auto" || req.Mode == "" {
		s.endOverlay(true) // the real wallpaper must be what's on screen
	}
	v, err := s.session(func(sess *app.Session) (any, error) { return sess.AlignPhoto(req.Path, req.Mode, req.Layout) })
	if patternUp {
		s.endOverlay(true)
	}
	if err == nil {
		s.mu.Lock()
		s.proposal = v.(*app.Proposal)
		s.mu.Unlock()
		s.events.publish("proposal")
	}
	writeJSON(w, v, err)
}

// applyAlignment saves the pending proposal into its layout and re-applies
// the wallpaper.
func (s *Server) applyAlignment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	p := s.proposal
	s.mu.Unlock()
	if p == nil {
		writeJSON(w, nil, fmt.Errorf("no photo result to apply; run align first"))
		return
	}
	s.endOverlay(false)
	v, err := s.session(func(sess *app.Session) (any, error) {
		info, err := sess.SaveLayout(p.Layout, p.Placements())
		if err != nil {
			return nil, err
		}
		if sess.Cfg.ActiveLayout != p.Layout {
			if _, err := sess.LayoutAction("activate", p.Layout, ""); err != nil {
				return nil, err
			}
		}
		if err := s.reapply(sess); err != nil {
			return nil, err
		}
		return info, nil
	})
	if err == nil {
		vdesk.ClearSavedWallpapers()
		s.mu.Lock()
		s.proposal = nil
		s.mu.Unlock()
	}
	s.changed("layout", err)
	writeJSON(w, v, err)
}

func (s *Server) getProposal(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	p := s.proposal
	s.mu.Unlock()
	writeJSON(w, map[string]any{"proposal": p}, nil)
}
