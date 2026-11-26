//go:build windows

package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/win"
)

// Explorer hosts the wallpaper object, and after a dock reconnect its
// monitor table can stay stale: it reports fewer attached monitors than
// Windows shows (or old port paths for them). A span then only reaches some
// monitors. Restarting Explorer rebuilds the table.

// monitorCounts returns how many monitors Explorer sees and Windows shows.
func (s *Server) monitorCounts() (seen, total int, err error) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		return [2]int{len(sess.Monitors), sess.WindowsMonitors}, nil
	})
	if err != nil {
		return 0, 0, err
	}
	c := v.([2]int)
	return c[0], c[1], nil
}

// waitExplorer gives Explorer up to ~15 s to catch up with Windows after a
// monitor change. Explorer usually lags a few seconds behind a dock.
func (s *Server) waitExplorer() (inSync bool, seen, total int) {
	for i := 0; i < 6; i++ {
		if i > 0 {
			time.Sleep(3 * time.Second)
		}
		var err error
		if seen, total, err = s.monitorCounts(); err == nil && seen >= total {
			return true, seen, total
		}
	}
	return false, seen, total
}

// explorerStale handles an Explorer that never caught up. It restarts
// Explorer on its own only when that can't lose anything (no File Explorer
// windows open) and not more than once an hour; otherwise it asks via the
// tray. It never spans onto the partial set of monitors.
func (s *Server) explorerStale(seen, total int) {
	s.log.Printf("explorer is stale: sees %d of %d monitors", seen, total)
	s.fixMu.Lock()
	recent := time.Since(s.lastFix) < time.Hour
	s.fixMu.Unlock()
	if open := win.FolderWindows(); open > 0 || recent {
		s.notify(fmt.Sprintf("Explorer only sees %d of %d monitors", seen, total),
			`The wallpaper can't reach the others until Explorer restarts. Pick "Fix monitors" in Spanwall's tray menu (it closes File Explorer windows).`, true)
		return
	}
	s.notify("Fixing monitors", fmt.Sprintf("Explorer only saw %d of %d monitors, so Spanwall is restarting it.", seen, total), false)
	s.fixMu.Lock()
	s.lastFix = time.Now()
	s.fixMu.Unlock()
	res, err := s.fixExplorer()
	s.log.Printf("explorer fix (auto): %v %v", res, err)
	if err != nil {
		s.notify("Couldn't fix monitors", err.Error(), true)
	}
}

// fixExplorer restarts Explorer, waits for it to see every monitor, then
// puts the right wallpaper back.
func (s *Server) fixExplorer() (map[string]any, error) {
	s.fixMu.Lock()
	defer s.fixMu.Unlock()
	s.endOverlay(false)
	if err := win.RestartExplorer(); err != nil {
		return nil, err
	}
	// The next session call finds the old wallpaper object dead and
	// reconnects (see session).
	ok, seen, total := s.waitExplorer()
	out := map[string]any{"explorerSees": seen, "windowsShows": total, "inSync": ok}
	if !ok {
		return out, fmt.Errorf("Explorer still only sees %d of %d monitors after restarting", seen, total)
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		if sess.Cfg.AutoSwitchOn() {
			return s.monitorsChanged(sess)
		}
		return "re-applied the last wallpaper", s.reapply(sess)
	})
	out["result"] = v
	s.events.publish("monitors")
	return out, err
}

// fixExplorerHandler is POST /api/fix-explorer (tray, editor and Claude).
func (s *Server) fixExplorerHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.fixExplorer()
	s.log.Printf("explorer fix: %v %v", v, err)
	writeJSON(w, v, err)
}
