//go:build windows

package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/config"
	"github.com/SkadidleSkadoodle/spanwall/internal/paths"
	"github.com/SkadidleSkadoodle/spanwall/internal/sources"
	"github.com/SkadidleSkadoodle/spanwall/internal/vdesk"
)

// ---- API keys for the extra sources (kept out of config.json) ---------------------

// secretPath is where a source's key lives, next to the config.
func secretPath(name string) string {
	root, err := paths.Root()
	if err != nil {
		return ""
	}
	return filepath.Join(root, name+".key")
}

func readSecret(name string) string {
	b, err := os.ReadFile(secretPath(name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// writeSecret saves (or with "" removes) a key.
func writeSecret(name, k string) error {
	k = strings.TrimSpace(k)
	if k == "" {
		if err := os.Remove(secretPath(name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if len(k) < 8 || len(k) > 200 || strings.ContainsAny(k, " /\\?&=\r\n") {
		return fmt.Errorf("that doesn't look like an API key")
	}
	return os.WriteFile(secretPath(name), []byte(k), 0o600)
}

// ---- sources -----------------------------------------------------------------------

func subreddits(c *config.Config) []string {
	s := c.Sources.Subreddits
	if strings.TrimSpace(s) == "" {
		s = "WidescreenWallpaper,wallpaper"
	}
	return strings.Split(s, ",")
}

func sourcesOn(c *config.Config) bool {
	o := c.Sources
	return o.Bing || o.APOD || o.Wikimedia || o.Reddit || o.Unsplash
}

// syncSources downloads what's new from the enabled sources.
func (s *Server) syncSources() (*sources.Result, error) {
	s.sourceMu.Lock()
	defer s.sourceMu.Unlock()
	v, err := s.session(func(sess *app.Session) (any, error) {
		c := sess.Cfg
		skip := map[string]bool{}
		for _, k := range c.Sources.Skipped {
			skip[k] = true
		}
		return [2]any{sources.Options{Bing: c.Sources.Bing, APOD: c.Sources.APOD, Wikimedia: c.Sources.Wikimedia,
			Reddit: c.Sources.Reddit, Subreddits: subreddits(c), Unsplash: c.Sources.Unsplash, Query: c.Sources.Query,
			NASAKey: readSecret("nasa"), UnsplashKey: readSecret("unsplash"), MinWidth: c.Sources.MinWidth, Skipped: skip,
			Removed: removedSet(c)}, wallpaperDir(c)}, nil
	})
	if err != nil {
		return nil, err
	}
	in := v.([2]any)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	res := sources.Run(ctx, in[0].(sources.Options), in[1].(string))
	msg := fmt.Sprintf("checked %d, %d new", res.Checked, len(res.Downloaded))
	if len(res.Errors) > 0 {
		msg += fmt.Sprintf(", %d problem(s): %s", len(res.Errors), res.Errors[0])
	}
	s.session(func(sess *app.Session) (any, error) {
		so := &sess.Cfg.Sources
		so.Skipped = append(so.Skipped, res.Rejected...)
		if len(so.Skipped) > 500 {
			so.Skipped = so.Skipped[len(so.Skipped)-500:]
		}
		so.LastRun = time.Now().Format(time.RFC3339)
		so.LastResult = msg
		return nil, sess.Save()
	})
	s.log.Printf("sources: %s", msg)
	if len(res.Downloaded) > 0 {
		s.notify("New wallpapers", fmt.Sprintf("%d new from your sources", len(res.Downloaded)), false)
		s.events.publish("library")
	} else if len(res.Errors) > 0 && res.Checked == 0 {
		s.notify("Wallpaper sources", res.Errors[0], true)
	}
	s.events.publish("config")
	return &res, nil
}

// sourcesLoop checks the sources a few minutes after start, then every 6
// hours (the daily feeds change once a day).
func (s *Server) sourcesLoop() {
	time.Sleep(3 * time.Minute)
	for {
		v, _ := s.session(func(sess *app.Session) (any, error) { return sourcesOn(sess.Cfg), nil })
		if on, _ := v.(bool); on {
			s.syncSources()
		}
		time.Sleep(6 * time.Hour)
	}
}

func (s *Server) sourcesSyncNow(w http.ResponseWriter, r *http.Request) {
	res, err := s.syncSources()
	writeJSON(w, res, err)
}

// ---- day and night ----------------------------------------------------------------

// windowsLight reports whether Windows apps use light mode.
func windowsLight() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err != nil || v != 0
}

// period is "day", "night", or "" when the schedule is off.
func period(c *config.Config, now time.Time) string {
	if !c.Schedule.Enabled {
		return ""
	}
	return modePeriod(c, now)
}

// modePeriod is what the schedule's mode says it is now, on or off.
func modePeriod(c *config.Config, now time.Time) string {
	switch scheduleMode(c) {
	case "theme":
		if windowsLight() {
			return "day"
		}
		return "night"
	case "sun":
		if c.Schedule.Lat == 0 && c.Schedule.Lon == 0 {
			return ""
		}
		return app.SunPeriod(now, c.Schedule.Lat, c.Schedule.Lon)
	}
	return ""
}

func scheduleInfo(c *config.Config) map[string]any {
	now := time.Now()
	out := map[string]any{"enabled": c.Schedule.Enabled, "mode": scheduleMode(c), "lat": c.Schedule.Lat, "lon": c.Schedule.Lon,
		"period": modePeriod(c, now), "dayCount": folderCount(c, "Day"), "nightCount": folderCount(c, "Night")}
	if scheduleMode(c) == "sun" && (c.Schedule.Lat != 0 || c.Schedule.Lon != 0) {
		if rise, set, ok := app.SunTimes(now, c.Schedule.Lat, c.Schedule.Lon); ok {
			out["sunrise"], out["sunset"] = rise.Format("15:04"), set.Format("15:04")
		}
	}
	return out
}

// scheduleMode is "sun" or "theme": sun when a location is set (or chosen).
func scheduleMode(c *config.Config) string {
	switch c.Schedule.Mode {
	case "sun", "theme":
		return c.Schedule.Mode
	}
	if c.Schedule.Lat != 0 || c.Schedule.Lon != 0 {
		return "sun"
	}
	return "theme"
}

func folderCount(c *config.Config, name string) int {
	if i := c.FolderIndex(name); i >= 0 {
		return len(c.Folders[i].Images)
	}
	return 0
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// checkSchedule changes the wallpaper when day turns to night or back.
func (s *Server) checkSchedule() {
	v, _ := s.session(func(sess *app.Session) (any, error) {
		p := period(sess.Cfg, time.Now())
		if p == "" || p == sess.Cfg.Schedule.Current {
			return "", nil
		}
		first := sess.Cfg.Schedule.Current == ""
		sess.Cfg.Schedule.Current = p
		if err := sess.Save(); err != nil {
			return "", err
		}
		if first {
			return "", nil // just started: remember, don't change what's on screen
		}
		return p, nil
	})
	if p, _ := v.(string); p != "" {
		s.mu.Lock()
		busy := s.overlay != nil
		s.mu.Unlock()
		if busy {
			return
		}
		if _, err := s.slideStep(false); err != nil {
			s.log.Printf("schedule (%s): %v", p, err)
		} else {
			s.log.Printf("schedule: switched to %s wallpaper", p)
		}
	}
}

// ---- ratings and folders ----------------------------------------------------------

// rateImage sets an image's rating ("fav", "ban" or "none") and/or puts it
// in or takes it out of a folder. Banning what's on screen moves on to the
// next wallpaper.
func (s *Server) rateImage(name, rating, folder string, in bool) (map[string]any, error) {
	if rating != "" && rating != "fav" && rating != "ban" && rating != "none" {
		return nil, fmt.Errorf("rating is fav, ban or none")
	}
	var onScreen bool
	v, err := s.session(func(sess *app.Session) (any, error) {
		if name == "" || name == "current" {
			la := sess.Cfg.LastApplied
			if la == nil || len(la.Images) == 0 {
				return nil, fmt.Errorf("no wallpaper applied yet")
			}
			name = la.Images[0]
		}
		imgs := []string{name}
		if err := resolveImages(sess.Cfg, imgs); err != nil {
			return nil, err
		}
		k := strings.ToLower(filepath.Base(imgs[0]))
		c := sess.Cfg
		if rating != "" {
			if c.Ratings == nil {
				c.Ratings = map[string]string{}
			}
			if rating == "none" {
				delete(c.Ratings, k)
			} else {
				c.Ratings[k] = rating
			}
		}
		if folder != "" {
			if err := c.SetInFolder(folder, k, in); err != nil {
				return nil, err
			}
		}
		if la := c.LastApplied; rating == "ban" && la != nil {
			for _, p := range la.Images {
				if strings.EqualFold(filepath.Base(p), k) {
					onScreen = true
				}
			}
		}
		return map[string]any{"image": filepath.Base(imgs[0]), "rating": c.Ratings[k], "folders": c.FoldersOf(k)}, sess.Save()
	})
	s.changed("library", err)
	if err != nil {
		return nil, err
	}
	out := v.(map[string]any)
	if onScreen && !s.liveOn() {
		if _, err := s.slideStep(false); err == nil {
			out["skipped"] = true
		}
	}
	return out, nil
}

func (s *Server) rate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image  string `json:"image"` // file name or path; "" or "current" = on screen
		Rating string `json:"rating"`
		Folder string `json:"folder"`
		In     bool   `json:"in"` // with folder: add (true) or remove (false)
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.rateImage(req.Image, req.Rating, req.Folder, req.In)
	writeJSON(w, v, err)
}

func foldersInfo(c *config.Config, lib []app.LibraryImage) []map[string]any {
	have := map[string]bool{}
	for _, im := range lib {
		have[strings.ToLower(im.Name)] = true
	}
	var out []map[string]any
	for _, f := range c.Folders {
		n := 0
		for _, k := range f.Images {
			if have[k] {
				n++
			}
		}
		out = append(out, map[string]any{"name": f.Name, "count": n, "builtin": config.IsBuiltin(f.Name)})
	}
	return out
}

// folders creates, renames or deletes a folder.
func (s *Server) folders(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"` // create, rename, delete
		Name   string `json:"name"`
		To     string `json:"to"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		var err error
		switch req.Action {
		case "create":
			err = sess.Cfg.CreateFolder(req.Name)
		case "rename":
			err = sess.Cfg.RenameFolder(req.Name, req.To)
		case "delete":
			err = sess.Cfg.DeleteFolder(req.Name)
		case "", "list":
		default:
			err = fmt.Errorf("action is create, rename, delete or list")
		}
		if err != nil {
			return nil, err
		}
		lib, _ := app.Library(wallpaperDir(sess.Cfg))
		return foldersInfo(sess.Cfg, lib), sess.Save()
	})
	s.changed("library", err)
	writeJSON(w, v, err)
}

// ---- monitor setups ---------------------------------------------------------------

func profilesInfo(sess *app.Session) []map[string]any {
	cur := sess.ProfileKey()
	var out []map[string]any
	for k, p := range sess.Cfg.Profiles {
		var imgs []string
		for _, i := range p.Images {
			imgs = append(imgs, filepath.Base(i))
		}
		out = append(out, map[string]any{"key": k, "monitors": p.Names, "count": len(strings.Split(k, ",")), "layout": p.Layout,
			"mode": p.Mode, "images": imgs, "updated": p.Updated, "current": k == cur})
	}
	return out
}

func (s *Server) forgetProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Forget string `json:"forget"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		delete(sess.Cfg.Profiles, req.Forget)
		return profilesInfo(sess), sess.Save()
	})
	s.changed("config", err)
	writeJSON(w, v, err)
}

// monitorsChanged is what the monitor watcher does after a dock or undock:
// bring back this setup's remembered layout and wallpaper, else pick a
// fitting layout and re-apply the last wallpaper.
func (s *Server) monitorsChanged(sess *app.Session) (string, error) {
	if !sess.Cfg.AutoSwitchOn() {
		return "auto switch off", nil
	}
	n := len(sess.Monitors)
	p, switched, err := sess.UseProfile()
	if err != nil {
		return "", err
	}
	if p != nil {
		cur, _ := vdesk.Current()
		if req, _ := sess.DesktopRequest(cur); req == nil {
			// Same wallpaper on every desktop: bring back this setup's own.
			imgs := append([]string(nil), p.Images...)
			if resolveImages(sess.Cfg, imgs) == nil {
				if _, err := sess.Apply(app.ApplyRequest{Images: imgs, Mode: p.Mode, Layout: p.Layout}); err == nil {
					vdesk.ClearSavedWallpapers()
					go s.notify("Monitors changed", fmt.Sprintf("%d monitor(s): layout %q and its wallpaper are back", n, p.Layout), false)
					return fmt.Sprintf("profile: layout %q (switched: %v), %s %v", p.Layout, switched, p.Mode, imgs), nil
				}
			}
		}
		return fmt.Sprintf("profile layout %q: %v", p.Layout, s.reapply(sess)), nil
	}
	name, switched, err := sess.ChooseLayout()
	if err != nil {
		return "", err
	}
	err = s.reapply(sess)
	if !sess.LayoutCoversAll() {
		go s.notify("New monitor setup", fmt.Sprintf("%d monitor(s) Spanwall hasn't placed yet. Open Spanwall to arrange them; the wallpaper follows from then on.", n), false)
	} else if switched {
		go s.notify("Monitors changed", fmt.Sprintf("%d monitor(s): switched to layout %q", n, name), false)
	}
	return fmt.Sprintf("layout %q (switched: %v): %v", name, switched, err), nil
}

// removedSet snapshots the removed list for a sources run (it runs outside
// the session).
func removedSet(c *config.Config) func(string) bool {
	m := map[string]bool{}
	for _, r := range c.Removed {
		m[r] = true
	}
	return func(name string) bool { return m[strings.ToLower(filepath.Base(name))] }
}

// ---- removing wallpapers ----------------------------------------------------------

var (
	procSHFileOperation = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")
)

type shFileOp struct {
	Hwnd         uintptr
	Func         uint32
	From         *uint16
	To           *uint16
	Flags        uint16
	AnyAborted   int32
	NameMappings uintptr
	Title        *uint16
}

// recycle moves a file to the Recycle Bin (so a removal can be undone).
func recycle(path string) error {
	const foDelete, fofSilent, fofNoConfirm, fofAllowUndo, fofNoErrorUI = 3, 0x4, 0x10, 0x40, 0x400
	from, err := windows.UTF16FromString(path)
	if err != nil {
		return err
	}
	from = append(from, 0) // the list ends with an extra NUL
	op := shFileOp{Func: foDelete, From: &from[0], Flags: fofSilent | fofNoConfirm | fofAllowUndo | fofNoErrorUI}
	r, _, _ := procSHFileOperation.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return fmt.Errorf("couldn't move %s to the Recycle Bin (error %#x)", filepath.Base(path), r)
	}
	if op.AnyAborted != 0 {
		return fmt.Errorf("moving %s to the Recycle Bin was cancelled", filepath.Base(path))
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s is still there (in use?)", filepath.Base(path))
	}
	return nil
}

// removeImage moves a library image to the Recycle Bin and forgets it.
// Only files directly in the wallpaper folder can be removed.
func (s *Server) removeImage(name string) (map[string]any, error) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		dir := wallpaperDir(sess.Cfg)
		base := filepath.Base(name)
		if base == "." || base == "" || strings.ContainsAny(base, `\/:`) {
			return nil, fmt.Errorf("not a file name: %q", name)
		}
		p := filepath.Join(dir, base)
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("%s isn't in the wallpaper folder", base)
		}
		if err := recycle(p); err != nil {
			return nil, err
		}
		sess.Cfg.ForgetImage(base)
		onScreen := false
		if la := sess.Cfg.LastApplied; la != nil {
			for _, i := range la.Images {
				onScreen = onScreen || strings.EqualFold(filepath.Base(i), base)
			}
		}
		return map[string]any{"removed": base, "recycleBin": true, "wasOnScreen": onScreen}, sess.Save()
	})
	s.changed("library", err)
	if err != nil {
		return nil, err
	}
	s.log.Printf("removed %v (Recycle Bin)", v.(map[string]any)["removed"])
	return v.(map[string]any), nil
}

func (s *Server) removeWallpaper(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"image"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.removeImage(req.Image)
	writeJSON(w, v, err)
}
