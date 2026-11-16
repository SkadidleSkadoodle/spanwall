//go:build windows

package app

import (
	"sort"
	"strings"
	"time"

	"github.com/SkadidleSkadoodle/spanwall/internal/config"
)

// ProfileKey names the set of attached monitors: their IDs, sorted.
func (s *Session) ProfileKey() string {
	ids := make([]string, len(s.Monitors))
	for i, m := range s.Monitors {
		ids[i] = m.ID
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// rememberProfile records the layout and wallpaper used with the monitors
// attached now.
func (s *Session) rememberProfile(layout, mode string, images []string) {
	if s.Cfg.Profiles == nil {
		s.Cfg.Profiles = map[string]*config.Profile{}
	}
	names := make([]string, len(s.Monitors))
	for i, m := range s.Monitors {
		names[i] = m.Name
	}
	s.Cfg.Profiles[s.ProfileKey()] = &config.Profile{Layout: layout, Mode: mode, Images: images, Names: names,
		Updated: time.Now().Format(time.RFC3339)}
}

// UseProfile switches to the layout remembered for the attached monitors,
// if there is one and it still has every monitor. It returns the profile
// (nil if none) and whether the active layout changed.
func (s *Session) UseProfile() (*config.Profile, bool, error) {
	p := s.Cfg.Profiles[s.ProfileKey()]
	if p == nil {
		return nil, false, nil
	}
	l, ok := s.Cfg.Layouts[p.Layout]
	if !ok {
		return nil, false, nil
	}
	for _, m := range s.Monitors {
		if _, ok := l.Placements[m.ID]; !ok {
			return nil, false, nil
		}
	}
	if s.Cfg.ActiveLayout == p.Layout {
		return p, false, nil
	}
	s.Cfg.ActiveLayout = p.Layout
	return p, true, s.Save()
}

// LayoutCoversAll reports whether some saved layout places every attached
// monitor.
func (s *Session) LayoutCoversAll() bool {
	for _, l := range s.Cfg.Layouts {
		all := true
		for _, m := range s.Monitors {
			if _, ok := l.Placements[m.ID]; !ok {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
