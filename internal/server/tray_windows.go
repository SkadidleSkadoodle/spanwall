//go:build windows

package server

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/config"
	"github.com/SkadidleSkadoodle/spanwall/internal/tray"
)

// Hotkey actions and their defaults.
var (
	hotkeyActions  = []string{"next", "previous", "pause", "open", "favorite", "ban"}
	defaultHotkeys = map[string]string{"next": "ctrl+alt+pgdn", "previous": "ctrl+alt+pgup", "pause": "ctrl+alt+end"}
)

func validAction(a string) bool {
	for _, x := range hotkeyActions {
		if x == a {
			return true
		}
	}
	return false
}

// hotkeysFor is the effective hotkey per action ("" = none).
func hotkeysFor(c *config.Config) map[string]string {
	out := map[string]string{}
	for _, a := range hotkeyActions {
		out[a] = defaultHotkeys[a]
		if v, ok := c.Hotkeys[a]; ok {
			out[a] = v
		}
	}
	return out
}

const (
	menuOpen = iota + 1
	menuNext
	menuPrev
	menuSlideshow
	menuIdentify
	menuFavorite
	menuBan
	menuFixMonitors
	menuLive
	menuQuit
)

// startTray shows the tray icon and registers the hotkeys from the config.
func (s *Server) startTray() {
	v, err := s.session(func(sess *app.Session) (any, error) {
		return [2]any{sess.Cfg.Tray == nil || *sess.Cfg.Tray, hotkeysFor(sess.Cfg)}, nil
	})
	if err != nil {
		s.log.Printf("tray: %v", err)
		return
	}
	cfg := v.([2]any)
	keys := map[int]string{}
	for i, a := range hotkeyActions {
		if k := cfg[1].(map[string]string)[a]; k != "" {
			keys[100+i] = k
		}
	}
	t, err := tray.Start(tray.Options{
		ShowIcon: cfg[0].(bool),
		Tip:      "Spanwall",
		Icon:     tray.Icon(),
		Menu:     s.trayMenu,
		OnClick:  openEditor,
		OnMenu:   s.trayAction,
		Hotkeys:  keys,
		OnHotkey: func(id int) {
			if a := id - 100; a >= 0 && a < len(hotkeyActions) {
				s.hotkey(hotkeyActions[a])
			}
		},
		OnHotkeyError: func(id int, combo string, err error) {
			s.log.Printf("hotkey: %v", err)
			go func() {
				time.Sleep(2 * time.Second) // let the icon appear first
				s.notify("Hotkey not available", fmt.Sprintf("%s is used by another app. Pick another in Spanwall's Settings.", combo), true)
			}()
		},
	})
	if err != nil {
		s.log.Printf("tray: %v", err)
		return
	}
	s.mu.Lock()
	s.tray = t
	s.mu.Unlock()
}

func (s *Server) stopTray() {
	s.mu.Lock()
	t := s.tray
	s.tray = nil
	s.mu.Unlock()
	t.Stop()
}

func (s *Server) restartTray() {
	s.stopTray()
	s.startTray()
}

func (s *Server) trayMenu() []tray.Item {
	on, liveOn := false, false
	if v, err := s.session(func(sess *app.Session) (any, error) { return sess.Cfg.Slideshow.Enabled, nil }); err == nil {
		on = v.(bool)
	}
	liveOn = s.liveOn()
	return []tray.Item{
		{ID: menuOpen, Label: "Open Spanwall", Default: true},
		{Separator: true},
		{ID: menuNext, Label: "Next wallpaper"},
		{ID: menuPrev, Label: "Previous wallpaper"},
		{ID: menuSlideshow, Label: "Slideshow", Checked: on},
		{ID: menuLive, Label: "Animated wallpaper", Checked: liveOn},
		{ID: menuIdentify, Label: "Identify monitors"},
		{ID: menuFixMonitors, Label: "Fix monitors (restarts Explorer)"},
		{Separator: true},
		{ID: menuFavorite, Label: "Favourite this wallpaper"},
		{ID: menuBan, Label: "Never show this wallpaper again"},
		{Separator: true},
		{ID: menuQuit, Label: "Quit Spanwall"},
	}
}

func (s *Server) trayAction(id int) {
	switch id {
	case menuOpen:
		openEditor()
	case menuNext:
		s.hotkey("next")
	case menuPrev:
		s.hotkey("previous")
	case menuSlideshow:
		s.hotkey("pause")
	case menuIdentify:
		if _, err := s.toggleIdentify(); err != nil {
			s.log.Printf("identify: %v", err)
		}
	case menuFavorite:
		s.hotkey("favorite")
	case menuBan:
		s.hotkey("ban")
	case menuFixMonitors:
		go func() {
			res, err := s.fixExplorer()
			s.log.Printf("explorer fix (tray): %v %v", res, err)
			if err != nil {
				s.notify("Couldn't fix monitors", err.Error(), true)
			} else {
				s.notify("Monitors fixed", fmt.Sprintf("Explorer sees all %v monitors again", res["windowsShows"]), false)
			}
		}()
	case menuLive:
		go s.toggleLive()
	case menuQuit:
		s.stop()
	}
}

func (s *Server) hotkey(action string) {
	var err error
	switch action {
	case "next", "previous":
		if _, err = s.slideStep(action == "previous"); err == nil {
			s.session(func(sess *app.Session) (any, error) { return nil, s.liveOff(sess) })
		}
	case "pause":
		var on bool
		on, err = s.toggleSlideshow()
		s.log.Printf("slideshow on: %v", on)
		if err == nil {
			if on {
				s.notify("Slideshow on", "The wallpaper changes on schedule. Your pause hotkey or the tray menu stops it.", false)
			} else {
				s.notify("Slideshow paused", "Your pause hotkey or the tray menu starts it again.", false)
			}
		}
	case "open":
		openEditor()
	case "favorite", "ban":
		var r map[string]any
		rating := map[string]string{"favorite": "fav", "ban": "ban"}[action]
		if r, err = s.rateImage("current", rating, "", false); err == nil {
			if action == "favorite" {
				s.notify("Favourite", fmt.Sprintf("%v comes up more often in the slideshow", r["image"]), false)
			} else {
				s.notify("Won't show again", fmt.Sprintf("%v is banned from the slideshow (undo in the Library)", r["image"]), false)
			}
		}
	}
	if err != nil {
		s.log.Printf("%s: %v", action, err)
		s.notify("Spanwall", err.Error(), true)
	}
}

func openEditor() {
	windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr("http://"+Addr), nil, nil, 1)
}

// stop shuts the background app down (API shutdown and the tray's Quit).
func (s *Server) stop() {
	s.liveStop()
	s.endOverlay(true)
	s.stopTray()
	time.Sleep(100 * time.Millisecond)
	s.http.Close()
}

// notify shows a tray notification. Repeats of the same warning are
// dropped, so a problem that persists (say, no network) notifies once.
func (s *Server) notify(title, msg string, warn bool) {
	s.mu.Lock()
	t := s.tray
	if warn && s.lastWarn == title+msg {
		s.mu.Unlock()
		return
	}
	if warn {
		s.lastWarn = title + msg
	} else {
		s.lastWarn = ""
	}
	s.mu.Unlock()
	t.Notify(title, msg, warn)
}
