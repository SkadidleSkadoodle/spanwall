//go:build windows

package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/config"
	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
	"github.com/SkadidleSkadoodle/spanwall/internal/paths"
	"github.com/SkadidleSkadoodle/spanwall/internal/vdesk"
	"github.com/SkadidleSkadoodle/spanwall/internal/wallhaven"
)

var whRatios = []struct {
	name string
	v    float64
}{{"1x1", 1}, {"5x4", 1.25}, {"4x3", 4.0 / 3}, {"3x2", 1.5}, {"16x10", 1.6}, {"16x9", 16.0 / 9}, {"21x9", 21.0 / 9}, {"32x9", 32.0 / 9}, {"48x9", 48.0 / 9}}

// layoutFit describes the spanned layout so searches can ask for images
// that cover it: its aspect ratio, the nearest Wallhaven ratio, and the
// smallest image that keeps the lowest-density monitor sharp.
func layoutFit(sess *app.Session) map[string]any {
	ps, err := sess.Placements("")
	if err != nil {
		return nil
	}
	ds := geometry.Apparent(sess.Geometry(), ps)
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	minPPI := math.Inf(1)
	for i, d := range ds {
		minX, minY = math.Min(minX, ps[i].XMM), math.Min(minY, ps[i].YMM)
		maxX, maxY = math.Max(maxX, ps[i].XMM+d.WidthMM), math.Max(maxY, ps[i].YMM+d.HeightMM)
		minPPI = math.Min(minPPI, d.PPI())
	}
	w, h := maxX-minX, maxY-minY
	r := w / h
	best := whRatios[0]
	for _, c := range whRatios {
		if math.Abs(math.Log(c.v/r)) < math.Abs(math.Log(best.v/r)) {
			best = c
		}
	}
	pw, ph := int(w/25.4*minPPI), int(h/25.4*minPPI)
	return map[string]any{"ratio": math.Round(r*100) / 100, "nearest": best.name,
		"atleast": fmt.Sprintf("%dx%d", pw, ph), "widthMM": math.Round(w), "heightMM": math.Round(h)}
}

func (s *Server) wallhavenSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	query := wallhaven.Query{Q: q.Get("q"), Categories: q.Get("categories"), Sorting: q.Get("sorting"), TopRange: q.Get("topRange"),
		AtLeast: q.Get("atleast"), Ratios: q.Get("ratios"), Colors: q.Get("colors"), Page: page, Seed: q.Get("seed")}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := wallhaven.Search(ctx, query)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	fit, _ := s.session(func(sess *app.Session) (any, error) { return layoutFit(sess), nil })
	writeJSON(w, map[string]any{"results": res.Data, "page": res.Meta.CurrentPage, "lastPage": res.Meta.LastPage,
		"total": res.Meta.Total, "seed": res.Meta.Seed, "layout": fit}, nil)
}

func (s *Server) wallhavenDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"` // id or wallhaven link
		Apply bool   `json:"apply"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	id, err := wallhaven.ID(req.ID)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	dirV, _ := s.session(func(sess *app.Session) (any, error) { return wallpaperDir(sess.Cfg), nil })
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	path, info, err := wallhaven.Download(ctx, id, dirV.(string))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out := map[string]any{"path": path, "resolution": info.Resolution, "fileType": info.FileType, "sizeKB": info.FileSize / 1024}
	s.changed("library", nil)
	if req.Apply {
		s.endOverlay(false)
		v, err := s.session(func(sess *app.Session) (any, error) {
			return sess.Apply(app.ApplyRequest{Images: []string{path}, Mode: "span"})
		})
		if err != nil {
			writeJSON(w, nil, fmt.Errorf("downloaded to %s but applying failed: %w", path, err))
			return
		}
		vdesk.ClearSavedWallpapers()
		s.changed("wallpaper", nil)
		out["applied"] = v
	}
	writeJSON(w, out, nil)
}

func keyPath() string {
	root, err := paths.Root()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "wallhaven.key")
}

// loadWallhavenKey reads the saved API key into the client.
func loadWallhavenKey() {
	if b, err := os.ReadFile(keyPath()); err == nil {
		wallhaven.Key = strings.TrimSpace(string(b))
	}
}

// setWallhavenKey saves (or with "" removes) the API key.
func setWallhavenKey(k string) error {
	k = strings.TrimSpace(k)
	if k == "" {
		wallhaven.Key = ""
		if err := os.Remove(keyPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if len(k) < 16 || len(k) > 128 || strings.ContainsAny(k, " /\\?&=") {
		return fmt.Errorf("that doesn't look like a Wallhaven API key")
	}
	if err := os.WriteFile(keyPath(), []byte(k), 0o600); err != nil {
		return err
	}
	wallhaven.Key = k
	return nil
}

func (s *Server) wallhavenCollections(w http.ResponseWriter, r *http.Request) {
	userV, _ := s.session(func(sess *app.Session) (any, error) { return sess.Cfg.WallhavenUser, nil })
	user := userV.(string)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// With a key, ask for the key owner's own list so private ones show too.
	q := user
	if wallhaven.Key != "" {
		q = ""
	}
	cs, err := wallhaven.Collections(ctx, q)
	writeJSON(w, map[string]any{"user": user, "withKey": wallhaven.Key != "", "collections": cs}, err)
}

func (s *Server) wallhavenCollection(w http.ResponseWriter, r *http.Request) {
	userV, _ := s.session(func(sess *app.Session) (any, error) { return sess.Cfg.WallhavenUser, nil })
	user := userV.(string)
	if user == "" {
		writeJSON(w, nil, fmt.Errorf("set your Wallhaven username in Settings"))
		return
	}
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := wallhaven.CollectionWallpapers(ctx, user, id, page)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]any{"results": res.Data, "page": res.Meta.CurrentPage, "lastPage": res.Meta.LastPage, "total": res.Meta.Total}, nil)
}

func syncLabel(c *config.Config) string {
	if c.WallhavenSync.Collection == "" {
		return "Spanwall"
	}
	return c.WallhavenSync.Collection
}

// syncWallhaven downloads what's new in the synced collection.
func (s *Server) syncWallhaven() (*wallhaven.SyncResult, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	v, _ := s.session(func(sess *app.Session) (any, error) {
		return [3]string{sess.Cfg.WallhavenUser, syncLabel(sess.Cfg), wallpaperDir(sess.Cfg)}, nil
	})
	in := v.([3]string)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	skip := func(name string) bool {
		v, _ := s.session(func(sess *app.Session) (any, error) { return sess.Cfg.IsRemoved(name), nil })
		b, _ := v.(bool)
		return b
	}
	res, err := wallhaven.SyncCollection(ctx, in[0], in[1], in[2], skip)
	msg := ""
	switch {
	case err != nil:
		msg = err.Error()
	default:
		msg = fmt.Sprintf("%d in %s, %d new", res.Total, res.Collection, len(res.Downloaded))
		if len(res.Errors) > 0 {
			msg += fmt.Sprintf(", %d failed (%s)", len(res.Errors), res.Errors[0])
		}
	}
	s.session(func(sess *app.Session) (any, error) {
		sess.Cfg.WallhavenSync.LastRun = time.Now().Format(time.RFC3339)
		sess.Cfg.WallhavenSync.LastResult = msg
		return nil, sess.Save()
	})
	s.log.Printf("wallhaven sync: %s", msg)
	switch {
	case err != nil:
		s.notify("Wallhaven sync failed", msg, true)
	case len(res.Downloaded) > 0:
		s.notify("New wallpapers", fmt.Sprintf("%d new from your %s collection", len(res.Downloaded), res.Collection), false)
	case len(res.Errors) > 0:
		s.notify("Wallhaven sync", msg, true)
	}
	if res != nil && len(res.Downloaded) > 0 {
		s.events.publish("library")
	}
	s.events.publish("config")
	return res, err
}

// wallhavenSyncLoop syncs shortly after start and then every 15 minutes.
func (s *Server) wallhavenSyncLoop() {
	time.Sleep(30 * time.Second)
	for {
		v, _ := s.session(func(sess *app.Session) (any, error) {
			return sess.Cfg.WallhavenSync.Enabled && (wallhaven.Key != "" || sess.Cfg.WallhavenUser != ""), nil
		})
		if on, _ := v.(bool); on {
			s.syncWallhaven()
		}
		time.Sleep(15 * time.Minute)
	}
}

func (s *Server) wallhavenSyncNow(w http.ResponseWriter, r *http.Request) {
	res, err := s.syncWallhaven()
	writeJSON(w, res, err)
}
