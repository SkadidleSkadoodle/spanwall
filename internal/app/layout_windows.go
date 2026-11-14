//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/martingomezmejia-ctrl/spanwall/internal/config"
	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
)

// MonitorPlacement is one row of a layout listing.
type MonitorPlacement struct {
	Number   int     `json:"number"`
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Placed   bool    `json:"placed"`
	XMM      float64 `json:"xMM"`
	YMM      float64 `json:"yMM"`
	Scale    float64 `json:"scale"`
	WidthMM  float64 `json:"widthMM"`
	HeightMM float64 `json:"heightMM"`
	// Quad is the perspective shape from photo calibration, if any; Mesh is
	// the finer curved-screen shape (MeshNX x MeshNY points).
	Quad   []float64 `json:"quad,omitempty"`
	Mesh   []float64 `json:"mesh,omitempty"`
	MeshNX int       `json:"meshNX,omitempty"`
	MeshNY int       `json:"meshNY,omitempty"`
}

// LayoutInfo describes a layout for the attached monitors.
type LayoutInfo struct {
	Name     string             `json:"name"`
	Active   string             `json:"active"`
	Saved    []string           `json:"saved"`
	Note     string             `json:"note,omitempty"`
	Monitors []MonitorPlacement `json:"monitors"`
	Units    string             `json:"units"`
}

// Layout returns the named (or active) layout.
func (s *Session) Layout(name string) LayoutInfo {
	name = s.LayoutName(name)
	l := s.Cfg.Layout(name)
	info := LayoutInfo{Name: name, Active: s.Cfg.ActiveLayout, Saved: s.Cfg.LayoutNames(), Note: l.Note,
		Units: "mm on the desk; x grows right, y grows down; position = top-left of the visible area"}
	ds := s.Geometry()
	for i, m := range s.Monitors {
		p, ok := l.Placements[m.ID]
		info.Monitors = append(info.Monitors, MonitorPlacement{Number: i + 1, ID: m.ID, Name: m.Name, Placed: ok,
			XMM: p.XMM, YMM: p.YMM, Scale: p.S(), WidthMM: ds[i].WidthMM, HeightMM: ds[i].HeightMM, Quad: p.Quad, Mesh: p.Mesh, MeshNX: p.MeshNX, MeshNY: p.MeshNY})
	}
	return info
}

// PlaceMonitor sets (relative=false) or nudges (relative=true) a monitor's
// position in a layout, in mm.
func (s *Session) PlaceMonitor(layout, ref string, x, y float64, relative bool) (MonitorPlacement, error) {
	i, err := s.Find(ref)
	if err != nil {
		return MonitorPlacement{}, err
	}
	layout = s.LayoutName(layout)
	l := s.Cfg.Layout(layout)
	m := s.Monitors[i]
	old := l.Placements[m.ID]
	p := old
	p.Quad = nil
	if relative {
		p.XMM += x
		p.YMM += y
	} else {
		p.XMM, p.YMM = x, y
	}
	p = geometry.CarryQuad(old, p)
	l.Placements[m.ID] = p
	if err := s.Save(); err != nil {
		return MonitorPlacement{}, err
	}
	return MonitorPlacement{Number: i + 1, ID: m.ID, Name: m.Name, Placed: true, XMM: p.XMM, YMM: p.YMM,
		WidthMM: m.WidthMM, HeightMM: m.HeightMM}, nil
}

// ImportSuperpaper copies Superpaper's saved arrangement into a layout.
func (s *Session) ImportSuperpaper(layout, file string) (LayoutInfo, error) {
	if file == "" {
		local, _ := os.UserCacheDir()
		file = filepath.Join(local, "Programs", "Superpaper", "superpaper-portable", "display_systems.dat")
	}
	f, err := os.Open(file)
	if err != nil {
		return LayoutInfo{}, fmt.Errorf("open Superpaper settings: %w", err)
	}
	defer f.Close()
	offs, err := config.SuperpaperOffsets(f, len(s.Monitors))
	if err != nil {
		return LayoutInfo{}, err
	}
	// Superpaper orders displays by Windows position, same as s.Monitors.
	ps, err := geometry.PlacementsFromOffsets(s.Geometry(), offs)
	if err != nil {
		return LayoutInfo{}, err
	}
	layout = s.LayoutName(layout)
	l := s.Cfg.Layout(layout)
	l.Placements = map[string]geometry.Placement{}
	for i, m := range s.Monitors {
		l.Placements[m.ID] = ps[i]
	}
	l.Note = "imported from Superpaper"
	if err := s.Save(); err != nil {
		return LayoutInfo{}, err
	}
	return s.Layout(layout), nil
}

// LayoutAction manages saved layouts: "activate", "copy" (from -> name),
// "rename" (from -> name), "delete".
func (s *Session) LayoutAction(action, name, from string) (LayoutInfo, error) {
	if name == "" {
		return LayoutInfo{}, fmt.Errorf("a layout name is required")
	}
	switch action {
	case "activate":
		if _, ok := s.Cfg.Layouts[name]; !ok {
			return LayoutInfo{}, fmt.Errorf("no layout %q (have %v)", name, s.Cfg.LayoutNames())
		}
		s.Cfg.ActiveLayout = name
	case "copy", "rename":
		src, ok := s.Cfg.Layouts[s.LayoutName(from)]
		if !ok {
			return LayoutInfo{}, fmt.Errorf("no layout %q", s.LayoutName(from))
		}
		cp := &config.Layout{Note: src.Note, Placements: map[string]geometry.Placement{}}
		for k, v := range src.Placements {
			cp.Placements[k] = v
		}
		s.Cfg.Layouts[name] = cp
		if action == "rename" && s.LayoutName(from) != name {
			delete(s.Cfg.Layouts, s.LayoutName(from))
			if s.Cfg.ActiveLayout == s.LayoutName(from) {
				s.Cfg.ActiveLayout = name
			}
		}
	case "delete":
		if name == s.Cfg.ActiveLayout {
			return LayoutInfo{}, fmt.Errorf("can't delete the active layout; activate another one first")
		}
		delete(s.Cfg.Layouts, name)
		name = s.Cfg.ActiveLayout
	default:
		return LayoutInfo{}, fmt.Errorf("unknown action %q (activate, copy, rename, delete)", action)
	}
	if err := s.Save(); err != nil {
		return LayoutInfo{}, err
	}
	return s.Layout(name), nil
}

// ChooseLayout keeps the active layout if it has a position for every
// attached monitor; otherwise it activates the best saved layout that does
// (preferring one made for exactly these monitors). It returns the layout in
// use and whether it changed. With no fitting layout, nothing changes.
func (s *Session) ChooseLayout() (string, bool, error) {
	covers := func(l *config.Layout) bool {
		for _, m := range s.Monitors {
			if _, ok := l.Placements[m.ID]; !ok {
				return false
			}
		}
		return true
	}
	active := s.Cfg.ActiveLayout
	exact := func(l *config.Layout) bool { return covers(l) && len(l.Placements) == len(s.Monitors) }
	if l, ok := s.Cfg.Layouts[active]; ok && exact(l) {
		return active, false, nil
	}
	best := ""
	for _, n := range s.Cfg.LayoutNames() {
		if exact(s.Cfg.Layouts[n]) {
			best = n
			break
		}
	}
	if best == "" {
		if l, ok := s.Cfg.Layouts[active]; ok && covers(l) {
			return active, false, nil
		}
		for _, n := range s.Cfg.LayoutNames() {
			if covers(s.Cfg.Layouts[n]) {
				best = n
				break
			}
		}
	}
	if best == "" || best == active {
		return active, false, nil
	}
	s.Cfg.ActiveLayout = best
	return best, true, s.Save()
}
