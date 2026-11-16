//go:build windows

// Package app is Spanwall's core: everything the command line, the
// background app and the MCP server can do, in one place.
package app

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/SkadidleSkadoodle/spanwall/internal/config"
	"github.com/SkadidleSkadoodle/spanwall/internal/display"
	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
	"github.com/SkadidleSkadoodle/spanwall/internal/paths"
	"github.com/SkadidleSkadoodle/spanwall/internal/wallpaper"
	"github.com/SkadidleSkadoodle/spanwall/internal/win"
)

// Session is a fresh view of the monitors and config. Create one per
// operation: monitors can change at any time.
type Session struct {
	DW       *wallpaper.DesktopWallpaper
	Monitors []display.Monitor // attached, ordered by Windows position
	Cfg      *config.Config
	CfgDir   string
}

// OpenCOM initialises COM and DPI awareness on the calling thread (which
// stays locked to it) and creates the wallpaper object.
func OpenCOM() (*wallpaper.DesktopWallpaper, func(), error) {
	cleanup, err := win.Init()
	if err != nil {
		return nil, nil, fmt.Errorf("COM init: %w", err)
	}
	dw, err := wallpaper.New()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return dw, func() { dw.Close(); cleanup() }, nil
}

// NewSession lists the monitors and loads the config.
func NewSession(dw *wallpaper.DesktopWallpaper) (*Session, error) {
	all, err := display.List(dw)
	if err != nil {
		return nil, err
	}
	dir, err := paths.ConfigDir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return &Session{DW: dw, Monitors: AttachedSorted(all), Cfg: cfg, CfgDir: dir}, nil
}

// Save writes the config.
func (s *Session) Save() error { return s.Cfg.Save(s.CfgDir) }

// AttachedSorted keeps attached monitors, ordered by Windows position. This
// is the numbering `spanwall displays` shows and Superpaper uses.
func AttachedSorted(ms []display.Monitor) []display.Monitor {
	var out []display.Monitor
	for _, m := range ms {
		if m.Attached {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DesktopX != out[j].DesktopX {
			return out[i].DesktopX < out[j].DesktopX
		}
		return out[i].DesktopY < out[j].DesktopY
	})
	return out
}

// Geometry returns the monitors' geometry with user size overrides applied.
func (s *Session) Geometry() []geometry.Display {
	out := make([]geometry.Display, len(s.Monitors))
	for i, m := range s.Monitors {
		out[i] = s.Cfg.ApplyOverride(m.Geometry())
	}
	return out
}

// Find resolves a monitor reference: its number, ID, or part of its name,
// ID or brand (case-insensitive).
func (s *Session) Find(ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if n, err := strconv.Atoi(ref); err == nil {
		if n >= 1 && n <= len(s.Monitors) {
			return n - 1, nil
		}
		return 0, fmt.Errorf("no monitor number %d (have %d)", n, len(s.Monitors))
	}
	match := -1
	for i, m := range s.Monitors {
		if m.ID == ref {
			return i, nil
		}
		hay := strings.ToLower(m.Name + " " + m.ID + " " + display.Brand(m.ID))
		if strings.Contains(hay, strings.ToLower(ref)) {
			if match >= 0 {
				return 0, fmt.Errorf("%q matches more than one monitor", ref)
			}
			match = i
		}
	}
	if match < 0 {
		return 0, fmt.Errorf("no monitor matches %q", ref)
	}
	return match, nil
}

// LayoutName returns name, or the active layout if name is empty.
func (s *Session) LayoutName(name string) string {
	if name == "" {
		return s.Cfg.ActiveLayout
	}
	return name
}

// Placements returns the layout positions for the attached monitors, or an
// error naming the ones the layout doesn't have yet.
func (s *Session) Placements(layout string) ([]geometry.Placement, error) {
	layout = s.LayoutName(layout)
	l := s.Cfg.Layout(layout)
	out := make([]geometry.Placement, len(s.Monitors))
	var missing []string
	for i, m := range s.Monitors {
		p, ok := l.Placements[m.ID]
		if !ok {
			missing = append(missing, fmt.Sprintf("%d %s (%s)", i+1, m.Name, m.ID))
		}
		out[i] = p
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("layout %q has no position for: %s; place them with layout set, or import-superpaper",
			layout, strings.Join(missing, ", "))
	}
	return out, nil
}

// WritePNG saves an image quickly (fast compression).
func WritePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
