//go:build windows

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/SkadidleSkadoodle/spanwall/internal/paths"
	"github.com/SkadidleSkadoodle/spanwall/internal/pattern"
	"github.com/SkadidleSkadoodle/spanwall/internal/wallpaper"
)

// Saved is enough to put the wallpaper back exactly as it was.
type Saved struct {
	Position int
	Global   string
	PerMon   map[string]string
}

// SaveWallpaper records the current wallpaper state.
func (s *Session) SaveWallpaper() Saved {
	sv := Saved{PerMon: map[string]string{}}
	sv.Position, _ = s.DW.Position()
	sv.Global, _ = s.DW.Wallpaper("")
	for _, m := range s.Monitors {
		sv.PerMon[m.DevicePath], _ = s.DW.Wallpaper(m.DevicePath)
	}
	return sv
}

// RestoreWallpaper puts back a saved state.
func (s *Session) RestoreWallpaper(sv Saved) error {
	if sv.Position == wallpaper.PosSpan && sv.Global != "" {
		if err := s.DW.SetPosition(wallpaper.PosSpan); err != nil {
			return err
		}
		return s.DW.SetWallpaper("", sv.Global)
	}
	for path, file := range sv.PerMon {
		if file != "" {
			if err := s.DW.SetWallpaper(path, file); err != nil {
				return err
			}
		}
	}
	return s.DW.SetPosition(sv.Position)
}

// ShowIdentify saves the wallpaper, puts a numbered card on each monitor,
// and returns the state to restore afterwards.
func (s *Session) ShowIdentify() (Saved, error) {
	saved := s.SaveWallpaper()
	if _, err := s.ShowIdentifyCards(); err != nil {
		s.RestoreWallpaper(saved)
		return saved, err
	}
	return saved, nil
}

// ShowIdentifyCards puts a numbered card on each monitor (no saving).
func (s *Session) ShowIdentifyCards() (int, error) {
	if len(s.Monitors) == 0 {
		return 0, fmt.Errorf("no attached monitors")
	}
	dir, err := paths.CacheDir("identify")
	if err != nil {
		return 0, err
	}
	files := make([]string, len(s.Monitors))
	stamp := time.Now().Format("150405")
	for i, m := range s.Monitors {
		img := pattern.Identify(m.ResW, m.ResH, i+1, strings.ToUpper(m.Name), []string{
			m.ID,
			fmt.Sprintf("%d x %d px  |  %.0f x %.0f mm  |  %.1f PPI", m.ResW, m.ResH, m.WidthMM, m.HeightMM, m.PPI),
			fmt.Sprintf("Windows puts it at %d, %d (Spanwall ignores this)", m.DesktopX, m.DesktopY),
		})
		files[i] = filepath.Join(dir, fmt.Sprintf("identify-%s-%d.png", stamp, i+1))
		if err := WritePNG(files[i], img); err != nil {
			return 0, err
		}
	}
	if pos, _ := s.DW.Position(); pos == wallpaper.PosSpan {
		if err := s.DW.SetPosition(wallpaper.PosFill); err != nil {
			return 0, err
		}
	}
	for i, m := range s.Monitors {
		if err := s.DW.SetWallpaper(m.DevicePath, files[i]); err != nil {
			return 0, fmt.Errorf("set wallpaper on %s: %w", m.Name, err)
		}
	}
	return len(s.Monitors), nil
}
