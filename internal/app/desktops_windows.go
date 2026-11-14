//go:build windows

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/martingomezmejia-ctrl/spanwall/internal/config"
	"github.com/martingomezmejia-ctrl/spanwall/internal/paths"
	"github.com/martingomezmejia-ctrl/spanwall/internal/vdesk"
	"github.com/martingomezmejia-ctrl/spanwall/internal/wallpaper"
)

// Section picks one vertical strip of an image (panorama mode).
type Section struct {
	Index int `json:"index"`
	Count int `json:"count"`
}

// DesktopRequest is what a given virtual desktop should show, or nil if the
// desktop mode is "same" or there is nothing applied yet.
func (s *Session) DesktopRequest(desktopID string) (*ApplyRequest, error) {
	d := s.Cfg.Desktops
	last := s.Cfg.LastApplied
	switch d.Mode {
	case "", "same":
		return nil, nil
	case "individual":
		img := d.Assign[desktopID]
		if img == "" {
			if last == nil {
				return nil, nil
			}
			return &ApplyRequest{Images: last.Images, Mode: last.Mode, Layout: last.Layout}, nil
		}
		return &ApplyRequest{Images: []string{img}, Mode: "span"}, nil
	case "panorama":
		if d.Panorama == "" {
			return nil, fmt.Errorf("panorama mode has no image")
		}
		ds, err := vdesk.List()
		if err != nil {
			return nil, err
		}
		for i, dk := range ds {
			if strings.EqualFold(dk.ID, desktopID) {
				return &ApplyRequest{Images: []string{d.Panorama}, Mode: "span", Section: &Section{Index: i, Count: len(ds)}}, nil
			}
		}
		return nil, fmt.Errorf("desktop %s not found", desktopID)
	}
	return nil, fmt.Errorf("unknown desktop mode %q (same, individual, panorama)", d.Mode)
}

// SetCached renders (or reuses) the slices for req and sets them. Used on
// every desktop switch, so cached slices make the switch a single set call.
func (s *Session) SetCached(req ApplyRequest) (cached bool, err error) {
	files, have, err := s.ensureCached(req)
	if err != nil {
		return false, err
	}
	if pos, _ := s.DW.Position(); pos == wallpaper.PosSpan {
		if err := s.DW.SetPosition(wallpaper.PosFill); err != nil {
			return have, err
		}
	}
	for i, m := range s.Monitors {
		if err := s.DW.SetWallpaper(m.DevicePath, files[i]); err != nil {
			return have, fmt.Errorf("set wallpaper on %s: %w", m.Name, err)
		}
	}
	return have, nil
}

// Prerender fills the cache for req without changing the wallpaper.
func (s *Session) Prerender(req ApplyRequest) error {
	_, _, err := s.ensureCached(req)
	return err
}

// ensureCached returns one slice file per monitor for req, rendering them
// if the cache doesn't have them yet.
func (s *Session) ensureCached(req ApplyRequest) (files []string, cached bool, err error) {
	if req.Mode == "" {
		req.Mode = "span"
	}
	ps, err := s.Placements(req.Layout)
	if err != nil && req.Mode == "span" {
		return nil, false, err
	}
	key := sha256.New()
	fmt.Fprintf(key, "%s|%v|%v|%s|", req.Mode, req.NoTrim, req.Section, s.LayoutName(req.Layout))
	for _, p := range req.Images {
		st, err := os.Stat(p)
		if err != nil {
			return nil, false, fmt.Errorf("image not found: %s", p)
		}
		fmt.Fprintf(key, "%s|%d|%d|%v|", p, st.Size(), st.ModTime().UnixNano(), s.FrameState(p, nil))
		for _, g := range s.SpanGroups() {
			fmt.Fprintf(key, "%v|", s.FrameState(p, s.GroupIDs(g)))
		}
	}
	for i, d := range s.Geometry() {
		fmt.Fprintf(key, "%s|%d|%d|%.2f|%.2f|", d.ID, d.ResW, d.ResH, d.WidthMM, d.HeightMM)
		if ps != nil {
			fmt.Fprintf(key, "%.2f|%.2f|%.4f|%v|%v|", ps[i].XMM, ps[i].YMM, ps[i].S(), ps[i].Quad, ps[i].Mesh)
		}
	}
	h := hex.EncodeToString(key.Sum(nil))[:16]

	dir, err := paths.CacheDir("desktops")
	if err != nil {
		return nil, false, err
	}
	files = make([]string, len(s.Monitors))
	have := true
	for i := range s.Monitors {
		files[i] = filepath.Join(dir, fmt.Sprintf("%s-%d.png", h, i+1))
		if _, err := os.Stat(files[i]); err != nil {
			have = false
		}
	}
	if !have {
		slices, _, _, err := s.render(req, s.Geometry(), 1)
		if err != nil {
			return nil, false, err
		}
		var wg sync.WaitGroup
		errs := make([]error, len(slices))
		for i, sl := range slices {
			wg.Add(1)
			go func() { defer wg.Done(); errs[i] = WritePNG(files[i], sl) }()
		}
		wg.Wait()
		for _, e := range errs {
			if e != nil {
				return nil, false, e
			}
		}
		pruneCache(dir, 60)
	}
	return files, have, nil
}

// SetDesktops changes the desktop mode and assignments.
func (s *Session) SetDesktops(d config.Desktops) error {
	switch d.Mode {
	case "", "same", "individual", "panorama":
	default:
		return fmt.Errorf("unknown desktop mode %q (same, individual, panorama)", d.Mode)
	}
	if d.Mode == "panorama" && d.Panorama == "" {
		return fmt.Errorf("panorama mode needs an image")
	}
	s.Cfg.Desktops = d
	return s.Save()
}

// pruneCache keeps the newest max files in dir.
func pruneCache(dir string, max int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= max {
		return
	}
	type f struct {
		name string
		t    int64
	}
	var fs []f
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			fs = append(fs, f{e.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].t > fs[j].t })
	for _, x := range fs[max:] {
		os.Remove(filepath.Join(dir, x.name))
	}
}
