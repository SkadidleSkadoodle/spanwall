//go:build windows

package server

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/martingomezmejia-ctrl/spanwall/internal/app"
	"github.com/martingomezmejia-ctrl/spanwall/internal/config"
	"github.com/martingomezmejia-ctrl/spanwall/internal/vdesk"
)

var procGetSystemMetrics = windows.NewLazySystemDLL("user32.dll").NewProc("GetSystemMetrics")

func metric(i uintptr) int32 {
	r, _, _ := procGetSystemMetrics.Call(i)
	return int32(r)
}

// monitorSignature changes whenever a monitor is added, removed or moved:
// monitor count plus the virtual screen rectangle. Cheap enough to poll.
func monitorSignature() string {
	// SM_CMONITORS 80, SM_XVIRTUALSCREEN 76, SM_YVIRTUALSCREEN 77, SM_CXVIRTUALSCREEN 78, SM_CYVIRTUALSCREEN 79
	return fmt.Sprint(metric(80), metric(76), metric(77), metric(78), metric(79))
}

// watchMonitors re-applies the wallpaper when the set of monitors changes,
// switching to the saved layout that fits them (when auto switch is on).
func (s *Server) watchMonitors() {
	last := monitorSignature()
	for {
		time.Sleep(2 * time.Second)
		sig := monitorSignature()
		if sig == last {
			continue
		}
		// Let Windows settle (a dock reconnect fires several changes).
		time.Sleep(2 * time.Second)
		if again := monitorSignature(); again != sig {
			continue
		}
		last = sig
		s.endOverlay(false)
		v, err := s.session(func(sess *app.Session) (any, error) {
			if !sess.Cfg.AutoSwitchOn() {
				return "auto switch off", nil
			}
			name, switched, err := sess.ChooseLayout()
			if err != nil {
				return nil, err
			}
			return fmt.Sprintf("layout %q (switched: %v): %v", name, switched, s.reapply(sess)), nil
		})
		s.log.Printf("monitors changed (%s): %v %v", sig, v, err)
		s.events.publish("monitors")
	}
}

// reapply puts the right wallpaper back for the current desktop.
func (s *Server) reapply(sess *app.Session) error {
	cur, _ := vdesk.Current()
	req, err := sess.DesktopRequest(cur)
	if err != nil {
		return err
	}
	if req == nil {
		last := sess.Cfg.LastApplied
		if last == nil {
			return nil
		}
		req = &app.ApplyRequest{Images: last.Images, Mode: last.Mode, Layout: last.Layout}
	}
	_, err = sess.SetCached(*req)
	return err
}

// watchDesktops applies each desktop's own wallpaper on a switch, in the
// individual and panorama desktop modes.
func (s *Server) watchDesktops() {
	err := vdesk.Watch(func(id string) {
		start := time.Now()
		v, err := s.session(func(sess *app.Session) (any, error) {
			req, err := sess.DesktopRequest(id)
			if err != nil || req == nil {
				return "same wallpaper everywhere", err
			}
			cached, err := sess.SetCached(*req)
			return fmt.Sprintf("applied (cached: %v)", cached), err
		})
		if v != "same wallpaper everywhere" || err != nil {
			s.log.Printf("desktop %s: %v %v in %v", id, v, err, time.Since(start).Round(time.Millisecond))
		}
		s.events.publish("desktop")
	})
	s.log.Printf("desktop watcher stopped: %v", err)
}

func (s *Server) getDesktops(w http.ResponseWriter, r *http.Request) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		ds, err := vdesk.List()
		if err != nil {
			return nil, err
		}
		return map[string]any{"mode": desktopMode(sess.Cfg), "assign": sess.Cfg.Desktops.Assign,
			"panorama": sess.Cfg.Desktops.Panorama, "desktops": ds}, nil
	})
	writeJSON(w, v, err)
}

// setDesktops changes the desktop mode, then renders and applies the current
// desktop's wallpaper and pre-renders the others in the background so later
// switches are instant.
func (s *Server) setDesktops(w http.ResponseWriter, r *http.Request) {
	var req config.Desktops
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		if req.Panorama != "" {
			imgs := []string{req.Panorama}
			if err := resolveImages(sess.Cfg, imgs); err != nil {
				return nil, err
			}
			req.Panorama = imgs[0]
		}
		for id, img := range req.Assign {
			imgs := []string{img}
			if err := resolveImages(sess.Cfg, imgs); err != nil {
				return nil, err
			}
			req.Assign[id] = imgs[0]
		}
		if err := sess.SetDesktops(req); err != nil {
			return nil, err
		}
		vdesk.ClearSavedWallpapers()
		return map[string]any{"mode": desktopMode(sess.Cfg)}, s.reapply(sess)
	})
	if err == nil {
		go s.prerenderDesktops()
	}
	s.changed("desktops", err)
	writeJSON(w, v, err)
}

// prerenderDesktops renders every desktop's slices into the cache (without
// setting them), one desktop at a time, so the API stays responsive.
func (s *Server) prerenderDesktops() {
	ds, err := vdesk.List()
	if err != nil {
		return
	}
	for _, d := range ds {
		if d.Current {
			continue
		}
		s.session(func(sess *app.Session) (any, error) {
			req, err := sess.DesktopRequest(d.ID)
			if err != nil || req == nil {
				return nil, err
			}
			return nil, sess.Prerender(*req)
		})
	}
}

// ---- events (server-sent events for open browser tabs) -----------------------

type broker struct {
	mu   sync.Mutex
	subs map[chan string]bool
}

func newBroker() *broker { return &broker{subs: map[chan string]bool{}} }

func (b *broker) publish(what string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for c := range b.subs {
		select {
		case c <- what:
		default: // slow tab: drop, it will refresh on the next event
		}
	}
}

func (b *broker) serve(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	c := make(chan string, 8)
	b.mu.Lock()
	b.subs[c] = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subs, c)
		b.mu.Unlock()
	}()
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case what := <-c:
			fmt.Fprintf(w, "event: changed\ndata: %q\n\n", what)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
