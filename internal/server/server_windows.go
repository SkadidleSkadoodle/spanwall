//go:build windows

// Package server is the background app: a small HTTP API on localhost that
// the browser UI and the MCP server both use, plus watchers that keep the
// wallpaper right when monitors or virtual desktops change.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/martingomezmejia-ctrl/spanwall/internal/app"
	"github.com/martingomezmejia-ctrl/spanwall/internal/config"
	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
	"github.com/martingomezmejia-ctrl/spanwall/internal/launch"
	"github.com/martingomezmejia-ctrl/spanwall/internal/paths"
	"github.com/martingomezmejia-ctrl/spanwall/internal/tray"
	"github.com/martingomezmejia-ctrl/spanwall/internal/vdesk"
	"github.com/martingomezmejia-ctrl/spanwall/internal/wallhaven"
	"github.com/martingomezmejia-ctrl/spanwall/internal/wallpaper"
	"github.com/martingomezmejia-ctrl/spanwall/internal/web"
)

// Addr is where the background app listens. Localhost only.
const Addr = "127.0.0.1:47920"

// Server owns the COM thread and the HTTP API.
type Server struct {
	work    chan func(*wallpaper.DesktopWallpaper)
	log     *log.Logger
	started time.Time
	http    *http.Server
	events  *broker

	mu      sync.Mutex
	overlay *overlayState // identify cards or calibration pattern on screen

	thumbs   *thumbCache
	proposal *app.Proposal // last photo result, waiting to be applied
	tray     *tray.Tray
	syncMu   sync.Mutex // one Wallhaven sync at a time
	sourceMu sync.Mutex // one sources run at a time
	lastWarn string     // last warning notified (to avoid repeats)
}

// Run serves until shutdown. It returns an error if another instance is
// already listening.
func Run() error {
	ln, err := net.Listen("tcp", Addr)
	if err != nil {
		return fmt.Errorf("another Spanwall is probably running (%v)", err)
	}
	logPath, _ := paths.LogPath()
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		lf = os.Stderr
	}
	s := &Server{
		work:    make(chan func(*wallpaper.DesktopWallpaper)),
		log:     log.New(io.MultiWriter(lf, os.Stderr), "", log.LstdFlags),
		started: time.Now(),
		events:  newBroker(),
		thumbs:  newThumbCache(6),
	}
	ready := make(chan error)
	go s.comThread(ready)
	if err := <-ready; err != nil {
		return err
	}
	if n, err := vdesk.ClearSavedWallpapers(); err == nil && n > 0 {
		s.log.Printf("cleared %d per-desktop saved wallpapers", n)
	}
	go s.watchMonitors()
	go s.watchDesktops()
	go s.slideshowLoop()
	loadWallhavenKey()
	go s.wallhavenSyncLoop()
	go s.sourcesLoop()
	s.startTray()

	s.http = &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	s.log.Printf("spanwall %s serving on http://%s (pid %d)", app.Version, Addr, os.Getpid())
	err = s.http.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// comThread runs every Windows call on one locked OS thread.
func (s *Server) comThread(ready chan<- error) {
	dw, done, err := app.OpenCOM()
	ready <- err
	if err != nil {
		return
	}
	defer done()
	for f := range s.work {
		f(dw)
	}
}

// session runs fn on the COM thread with a fresh session.
func (s *Server) session(fn func(*app.Session) (any, error)) (any, error) {
	type result struct {
		v   any
		err error
	}
	ch := make(chan result, 1)
	s.work <- func(dw *wallpaper.DesktopWallpaper) {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{nil, fmt.Errorf("internal error: %v", r)}
			}
		}()
		sess, err := app.NewSession(dw)
		if err != nil {
			ch <- result{nil, err}
			return
		}
		v, err := fn(sess)
		ch <- result{v, err}
		// Rendering briefly needs a few hundred MB; hand it back to Windows
		// right away so the idle app stays small.
		debug.FreeOSMemory()
	}
	r := <-ch
	return r.v, r.err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", web.Handler())
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/events", s.events.serve)
	mux.HandleFunc("GET /api/displays", s.displays)
	mux.HandleFunc("GET /api/layout", s.getLayout)
	mux.HandleFunc("POST /api/layout/place", s.placeMonitor)
	mux.HandleFunc("POST /api/layout/save", s.saveLayout)
	mux.HandleFunc("POST /api/layout/import-superpaper", s.importSuperpaper)
	mux.HandleFunc("POST /api/layout/action", s.layoutAction)
	mux.HandleFunc("POST /api/apply", s.apply)
	mux.HandleFunc("POST /api/preview", s.preview)
	mux.HandleFunc("GET /api/thumb", s.thumb)
	mux.HandleFunc("POST /api/identify", s.identifyHandler)
	mux.HandleFunc("POST /api/calibrate", s.calibrateHandler)
	mux.HandleFunc("POST /api/photo-pattern", s.photoPattern)
	mux.HandleFunc("POST /api/align", s.align)
	mux.HandleFunc("GET /api/align", s.getProposal)
	mux.HandleFunc("POST /api/align/apply", s.applyAlignment)
	mux.HandleFunc("GET /api/wallpapers", s.wallpapers)
	mux.HandleFunc("GET /api/framing", s.getFraming)
	mux.HandleFunc("POST /api/framing", s.setFraming)
	mux.HandleFunc("GET /api/slideshow", s.getSlideshow)
	mux.HandleFunc("POST /api/slideshow", s.setSlideshow)
	mux.HandleFunc("POST /api/slideshow/next", s.slideshowNext)
	mux.HandleFunc("GET /api/wallhaven/search", s.wallhavenSearch)
	mux.HandleFunc("POST /api/wallhaven/download", s.wallhavenDownload)
	mux.HandleFunc("GET /api/wallhaven/collections", s.wallhavenCollections)
	mux.HandleFunc("GET /api/wallhaven/collection", s.wallhavenCollection)
	mux.HandleFunc("POST /api/wallhaven/sync", s.wallhavenSyncNow)
	mux.HandleFunc("POST /api/sources/sync", s.sourcesSyncNow)
	mux.HandleFunc("POST /api/rate", s.rate)
	mux.HandleFunc("POST /api/folders", s.folders)
	mux.HandleFunc("POST /api/profiles", s.forgetProfile)
	mux.HandleFunc("GET /api/desktops", s.getDesktops)
	mux.HandleFunc("POST /api/desktops", s.setDesktops)
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("POST /api/config", s.setConfig)
	mux.HandleFunc("POST /api/shutdown", s.shutdown)
	return s.guard(mux)
}

// guard rejects requests from web pages on other origins (a page you visit
// could otherwise call localhost) and logs API calls.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+Addr && o != "http://localhost:47920" {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		if len(r.URL.Path) > 4 && r.URL.Path[:5] == "/api/" && r.URL.Path != "/api/events" && r.URL.Path != "/api/thumb" {
			s.log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil && err != io.EOF {
		return fmt.Errorf("bad request body: %w", err)
	}
	return nil
}

// ---- basic handlers -----------------------------------------------------------

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		s.mu.Lock()
		ov := ""
		if s.overlay != nil {
			ov = s.overlay.kind
		}
		hasProposal := s.proposal != nil
		s.mu.Unlock()
		return map[string]any{
			"version":       app.Version,
			"pid":           os.Getpid(),
			"uptime":        time.Since(s.started).Round(time.Second).String(),
			"monitors":      len(sess.Monitors),
			"activeLayout":  sess.Cfg.ActiveLayout,
			"layouts":       sess.Cfg.LayoutNames(),
			"lastApplied":   sess.Cfg.LastApplied,
			"wallpaperDir":  wallpaperDir(sess.Cfg),
			"configDir":     sess.CfgDir,
			"autoSwitch":    sess.Cfg.AutoSwitchOn(),
			"desktopMode":   desktopMode(sess.Cfg),
			"overlay":       ov,
			"proposal":      hasProposal,
			"photoPrior":    sess.Cfg.Photo != nil,
			"slideshow":     slideshowInfo(sess),
			"groups":        groupNames(sess),
			"autostart":     autostartOn(),
			"tray":          sess.Cfg.Tray == nil || *sess.Cfg.Tray,
			"hotkeys":       hotkeysFor(sess.Cfg),
			"layoutFit":     layoutFit(sess),
			"extension":     extensionPath(),
			"wallhavenUser": sess.Cfg.WallhavenUser,
			"wallhavenKey":  wallhaven.Key != "",
			"wallhavenSync": map[string]any{"enabled": sess.Cfg.WallhavenSync.Enabled, "collection": syncLabel(sess.Cfg),
				"lastRun": sess.Cfg.WallhavenSync.LastRun, "lastResult": sess.Cfg.WallhavenSync.LastResult},
			"smartFrame": sess.Cfg.SmartFrameOn(),
			"sync":       map[string]any{"accent": sess.Cfg.Sync.Accent, "lockScreen": sess.Cfg.Sync.LockScreen, "last": app.LookResult()},
			"schedule":   scheduleInfo(sess.Cfg),
			"profiles":   profilesInfo(sess),
			"sources": map[string]any{"bing": sess.Cfg.Sources.Bing, "apod": sess.Cfg.Sources.APOD, "wikimedia": sess.Cfg.Sources.Wikimedia,
				"reddit": sess.Cfg.Sources.Reddit, "subreddits": strings.Join(subreddits(sess.Cfg), ","), "unsplash": sess.Cfg.Sources.Unsplash,
				"query": nonEmpty(sess.Cfg.Sources.Query, "landscape"), "minWidth": minWidth(sess.Cfg), "nasaKey": readSecret("nasa") != "",
				"unsplashKey": readSecret("unsplash") != "", "lastRun": sess.Cfg.Sources.LastRun, "lastResult": sess.Cfg.Sources.LastResult},
		}, nil
	})
	writeJSON(w, v, err)
}

func desktopMode(c *config.Config) string {
	if c.Desktops.Mode == "" {
		return "same"
	}
	return c.Desktops.Mode
}

func (s *Server) displays(w http.ResponseWriter, r *http.Request) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		type row struct {
			Number int     `json:"number"`
			ID     string  `json:"id"`
			Name   string  `json:"name"`
			ResW   int     `json:"resW"`
			ResH   int     `json:"resH"`
			WMM    float64 `json:"widthMM"`
			HMM    float64 `json:"heightMM"`
			PPI    float64 `json:"ppi"`
			WinX   int     `json:"windowsX"`
			WinY   int     `json:"windowsY"`
		}
		ds := sess.Geometry()
		var out []row
		for i, m := range sess.Monitors {
			out = append(out, row{i + 1, m.ID, m.Name, m.ResW, m.ResH, ds[i].WidthMM, ds[i].HeightMM, ds[i].PPI(), m.DesktopX, m.DesktopY})
		}
		return out, nil
	})
	writeJSON(w, v, err)
}

func (s *Server) getLayout(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	v, err := s.session(func(sess *app.Session) (any, error) { return sess.Layout(name), nil })
	writeJSON(w, v, err)
}

func (s *Server) placeMonitor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Layout   string  `json:"layout"`
		Monitor  string  `json:"monitor"`
		X        float64 `json:"x"`
		Y        float64 `json:"y"`
		Relative bool    `json:"relative"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		return sess.PlaceMonitor(req.Layout, req.Monitor, req.X, req.Y, req.Relative)
	})
	s.changed("layout", err)
	writeJSON(w, v, err)
}

func (s *Server) saveLayout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Layout     string                        `json:"layout"`
		Placements map[string]geometry.Placement `json:"placements"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) { return sess.SaveLayout(req.Layout, req.Placements) })
	s.changed("layout", err)
	writeJSON(w, v, err)
}

func (s *Server) importSuperpaper(w http.ResponseWriter, r *http.Request) {
	var req struct{ Layout, File string }
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) { return sess.ImportSuperpaper(req.Layout, req.File) })
	s.changed("layout", err)
	writeJSON(w, v, err)
}

func (s *Server) layoutAction(w http.ResponseWriter, r *http.Request) {
	var req struct{ Action, Name, From string }
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) { return sess.LayoutAction(req.Action, req.Name, req.From) })
	s.changed("layout", err)
	writeJSON(w, v, err)
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	var req app.ApplyRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.endOverlay(false) // a new wallpaper replaces any test cards or pattern
	v, err := s.session(func(sess *app.Session) (any, error) {
		if err := resolveImages(sess.Cfg, req.Images); err != nil {
			return nil, err
		}
		return sess.Apply(req)
	})
	if err == nil {
		vdesk.ClearSavedWallpapers()
	}
	s.changed("wallpaper", err)
	writeJSON(w, v, err)
}

func (s *Server) wallpapers(w http.ResponseWriter, r *http.Request) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		dir := wallpaperDir(sess.Cfg)
		imgs, err := app.Library(dir)
		if err != nil {
			return nil, err
		}
		type row struct {
			app.LibraryImage
			Rating  string   `json:"rating,omitempty"`
			Folders []string `json:"folders"`
		}
		out := make([]row, len(imgs))
		for i, im := range imgs {
			out[i] = row{im, sess.Cfg.Ratings[strings.ToLower(im.Name)], sess.Cfg.FoldersOf(im.Name)}
		}
		return map[string]any{"dir": dir, "images": out, "folders": foldersInfo(sess.Cfg, imgs)}, nil
	})
	writeJSON(w, v, err)
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	v, err := s.session(func(sess *app.Session) (any, error) { return sess.Cfg, nil })
	writeJSON(w, v, err)
}

func (s *Server) setConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WallpaperDir *string `json:"wallpaperDir"`
		AutoSwitch   *bool   `json:"autoSwitch"`
		Monitor      string  `json:"monitor"` // with widthMM/heightMM: size override
		WidthMM      float64 `json:"widthMM"`
		HeightMM     float64 `json:"heightMM"`
		ClearSize    bool    `json:"clearSize"`
		// Span groups as monitor numbers, IDs or names; [] clears them.
		Groups    *[][]string        `json:"groups"`
		Autostart *bool              `json:"autostart"`
		Tray      *bool              `json:"tray"`
		Hotkeys   *map[string]string `json:"hotkeys"`
		// Wallhaven account: username for collections, API key (stored in
		// its own file, write only; "" removes it).
		WallhavenUser *string `json:"wallhavenUser"`
		WallhavenKey  *string `json:"wallhavenKey"`
		// Collection auto-download.
		WallhavenSync     *bool   `json:"wallhavenSync"`
		WallhavenSyncName *string `json:"wallhavenSyncCollection"`
		SmartFrame        *bool   `json:"smartFrame"`
		Accent            *bool   `json:"accent"`
		LockScreen        *bool   `json:"lockScreen"`
		Schedule          *struct {
			Enabled *bool    `json:"enabled"`
			Mode    string   `json:"mode"`
			Lat     *float64 `json:"lat"`
			Lon     *float64 `json:"lon"`
		} `json:"schedule"`
		Sources *struct {
			Bing       *bool   `json:"bing"`
			APOD       *bool   `json:"apod"`
			Wikimedia  *bool   `json:"wikimedia"`
			Reddit     *bool   `json:"reddit"`
			Subreddits *string `json:"subreddits"`
			Unsplash   *bool   `json:"unsplash"`
			Query      *string `json:"query"`
			MinWidth   *int    `json:"minWidth"`
		} `json:"sources"`
		NASAKey     *string `json:"nasaKey"`
		UnsplashKey *string `json:"unsplashKey"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	switchSchedule := false
	v, err := s.session(func(sess *app.Session) (any, error) {
		if req.WallpaperDir != nil {
			if st, err := os.Stat(*req.WallpaperDir); err != nil || !st.IsDir() {
				return nil, fmt.Errorf("not a folder: %s", *req.WallpaperDir)
			}
			sess.Cfg.WallpaperDir = *req.WallpaperDir
		}
		if req.AutoSwitch != nil {
			sess.Cfg.AutoSwitch = req.AutoSwitch
		}
		if req.Groups != nil {
			var gs [][]string
			seen := map[string]bool{}
			for _, g := range *req.Groups {
				var ids []string
				for _, ref := range g {
					i, err := sess.Find(ref)
					if err != nil {
						return nil, err
					}
					id := sess.Monitors[i].ID
					if seen[id] {
						return nil, fmt.Errorf("%s is in two span groups", sess.Monitors[i].Name)
					}
					seen[id] = true
					ids = append(ids, id)
				}
				if len(ids) > 0 {
					gs = append(gs, ids)
				}
			}
			sess.Cfg.Groups = gs
		}
		if req.Autostart != nil {
			if err := launch.SetAutostart(*req.Autostart); err != nil {
				return nil, err
			}
		}
		if req.Tray != nil {
			sess.Cfg.Tray = req.Tray
		}
		if req.WallhavenUser != nil {
			sess.Cfg.WallhavenUser = strings.TrimSpace(*req.WallhavenUser)
		}
		if req.WallhavenSync != nil {
			sess.Cfg.WallhavenSync.Enabled = *req.WallhavenSync
		}
		if req.WallhavenSyncName != nil {
			sess.Cfg.WallhavenSync.Collection = strings.TrimSpace(*req.WallhavenSyncName)
		}
		if req.WallhavenKey != nil {
			if err := setWallhavenKey(*req.WallhavenKey); err != nil {
				return nil, err
			}
		}
		if req.NASAKey != nil {
			if err := writeSecret("nasa", *req.NASAKey); err != nil {
				return nil, err
			}
		}
		if req.UnsplashKey != nil {
			if err := writeSecret("unsplash", *req.UnsplashKey); err != nil {
				return nil, err
			}
		}
		if req.SmartFrame != nil {
			sess.Cfg.SmartFrame = req.SmartFrame
		}
		if req.Accent != nil {
			sess.Cfg.Sync.Accent = *req.Accent
		}
		if req.LockScreen != nil {
			sess.Cfg.Sync.LockScreen = *req.LockScreen
		}
		if sc := req.Schedule; sc != nil {
			c := &sess.Cfg.Schedule
			if sc.Lat != nil && sc.Lon != nil {
				if *sc.Lat < -90 || *sc.Lat > 90 || *sc.Lon < -180 || *sc.Lon > 180 {
					return nil, fmt.Errorf("latitude is -90 to 90 and longitude -180 to 180")
				}
				c.Lat, c.Lon = *sc.Lat, *sc.Lon
			}
			switch sc.Mode {
			case "":
			case "theme":
				c.Mode = "theme"
			case "sun":
				if c.Lat == 0 && c.Lon == 0 {
					return nil, fmt.Errorf("sunrise and sunset need your latitude and longitude")
				}
				c.Mode = "sun"
			default:
				return nil, fmt.Errorf("schedule mode is sun or theme")
			}
			if sc.Enabled != nil {
				if *sc.Enabled && !c.Enabled {
					switchSchedule = true
				}
				c.Enabled = *sc.Enabled
			}
			c.Current = period(sess.Cfg, time.Now())
		}
		if so := req.Sources; so != nil {
			c := &sess.Cfg.Sources
			set := func(dst *bool, v *bool) {
				if v != nil {
					*dst = *v
				}
			}
			set(&c.Bing, so.Bing)
			set(&c.APOD, so.APOD)
			set(&c.Wikimedia, so.Wikimedia)
			set(&c.Reddit, so.Reddit)
			set(&c.Unsplash, so.Unsplash)
			if so.Subreddits != nil {
				c.Subreddits = strings.ReplaceAll(strings.TrimSpace(*so.Subreddits), " ", "")
			}
			if so.Query != nil {
				c.Query = strings.TrimSpace(*so.Query)
			}
			if so.MinWidth != nil {
				if *so.MinWidth < 640 || *so.MinWidth > 20000 {
					return nil, fmt.Errorf("minimum width is 640 to 20000 px")
				}
				c.MinWidth = *so.MinWidth
				c.Skipped = nil // re-check earlier rejects against the new limit
			}
		}
		if req.Hotkeys != nil {
			for action, combo := range *req.Hotkeys {
				if !validAction(action) {
					return nil, fmt.Errorf("unknown hotkey action %q (next, previous, pause, open, favorite, ban)", action)
				}
				if combo != "" {
					if _, _, err := tray.ParseHotkey(combo); err != nil {
						return nil, err
					}
				}
			}
			sess.Cfg.Hotkeys = *req.Hotkeys
		}
		if req.Monitor != "" {
			i, err := sess.Find(req.Monitor)
			if err != nil {
				return nil, err
			}
			id := sess.Monitors[i].ID
			if req.ClearSize {
				delete(sess.Cfg.Displays, id)
			} else {
				sess.Cfg.Displays[id] = &config.DisplayOverride{WidthMM: req.WidthMM, HeightMM: req.HeightMM}
			}
		}
		return sess.Cfg, sess.Save()
	})
	s.changed("config", err)
	if err == nil && (req.Tray != nil || req.Hotkeys != nil) {
		s.restartTray()
	}
	if err == nil && req.WallhavenSync != nil && *req.WallhavenSync {
		go s.syncWallhaven()
	}
	if err == nil && req.Sources != nil {
		go s.syncSources()
	}
	if err == nil && switchSchedule {
		go s.slideStep(false) // show the Day or Night folder right away
	}
	writeJSON(w, v, err)
}

func (s *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"stopping": true}, nil)
	go s.stop()
}

// changed tells open browser tabs that something changed (only on success).
func (s *Server) changed(what string, err error) {
	if err == nil {
		s.events.publish(what)
	}
}

func wallpaperDir(c *config.Config) string {
	if c.WallpaperDir != "" {
		return c.WallpaperDir
	}
	return app.DefaultWallpaperDir()
}

// resolveImages turns bare file names into paths in the wallpaper folder and
// checks every file exists.
func resolveImages(c *config.Config, images []string) error {
	for i, p := range images {
		if !filepath.IsAbs(p) {
			p = filepath.Join(wallpaperDir(c), p)
		}
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("image not found: %s", p)
		}
		images[i] = p
	}
	return nil
}

func autostartOn() bool { on, _ := launch.Autostart(); return on }

// groupNames lists the span groups as monitor numbers (1-based), including
// the implicit one-monitor groups.
func groupNames(sess *app.Session) [][]int {
	var out [][]int
	for _, g := range sess.SpanGroups() {
		var n []int
		for _, i := range g {
			n = append(n, i+1)
		}
		out = append(out, n)
	}
	return out
}

// extensionPath is the Claude extension package next to this exe, if built.
func extensionPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(exe), "Spanwall.mcpb")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func minWidth(c *config.Config) int {
	if c.Sources.MinWidth <= 0 {
		return 2560
	}
	return c.Sources.MinWidth
}
