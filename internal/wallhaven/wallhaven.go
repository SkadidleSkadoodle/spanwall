// Package wallhaven searches wallhaven.cc and downloads wallpapers at full
// resolution. Only safe-for-work results are requested (no API key).
package wallhaven

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Base is the API root (a variable so tests can point it at a fake server).
var Base = "https://wallhaven.cc/api/v1"

var client = &http.Client{Timeout: 60 * time.Second}

// Query is a search. Empty fields use Wallhaven's defaults.
type Query struct {
	Q          string `json:"q"`
	Categories string `json:"categories"` // general/anime/people flags, e.g. "110"
	Sorting    string `json:"sorting"`    // date_added, relevance, random, views, favorites, toplist
	TopRange   string `json:"topRange"`   // 1d 3d 1w 1M 3M 6M 1y (toplist only)
	AtLeast    string `json:"atleast"`    // e.g. "3840x2160"
	Ratios     string `json:"ratios"`     // e.g. "21x9,32x9"
	Colors     string `json:"colors"`
	Page       int    `json:"page"`
	Seed       string `json:"seed"`
}

// Wallpaper is one result.
type Wallpaper struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Path       string   `json:"path"` // full image
	Resolution string   `json:"resolution"`
	Width      int      `json:"dimension_x"`
	Height     int      `json:"dimension_y"`
	Ratio      string   `json:"ratio"`
	FileSize   int64    `json:"file_size"`
	FileType   string   `json:"file_type"`
	Category   string   `json:"category"`
	Favorites  int      `json:"favorites"`
	Colors     []string `json:"colors"`
	Thumbs     struct {
		Large    string `json:"large"`
		Original string `json:"original"`
		Small    string `json:"small"`
	} `json:"thumbs"`
}

// Result is one page of results.
type Result struct {
	Data []Wallpaper `json:"data"`
	Meta struct {
		CurrentPage int    `json:"current_page"`
		LastPage    int    `json:"last_page"`
		Total       int    `json:"total"`
		Seed        string `json:"seed"`
	} `json:"meta"`
}

// Key is the user's Wallhaven API key, sent as a header (never in URLs) by
// every request when set. It unlocks the user's private collections.
var Key string

func get(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Spanwall (https://github.com/SkadidleSkadoodle/spanwall)")
	if Key != "" {
		req.Header.Set("X-API-Key", Key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("wallhaven: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401:
		return errors.New("wallhaven: the API key was refused; check it in Settings")
	case resp.StatusCode == 429:
		return errors.New("wallhaven: rate limited (45 requests a minute); wait a moment")
	case resp.StatusCode != 200:
		return fmt.Errorf("wallhaven: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// Search runs a query. Purity is always SFW.
func Search(ctx context.Context, q Query) (*Result, error) {
	v := url.Values{"purity": {"100"}}
	add := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	add("q", q.Q)
	add("categories", q.Categories)
	add("sorting", q.Sorting)
	if q.Sorting == "toplist" {
		add("topRange", q.TopRange)
	}
	add("atleast", q.AtLeast)
	add("ratios", q.Ratios)
	add("colors", q.Colors)
	add("seed", q.Seed)
	if q.Page > 1 {
		v.Set("page", fmt.Sprint(q.Page))
	}
	var r Result
	if err := get(ctx, Base+"/search?"+v.Encode(), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9]{4,10}$`)

// ID extracts a wallpaper id from an id or a wallhaven URL.
func ID(s string) (string, error) {
	s = strings.TrimSpace(strings.TrimRight(s, "/"))
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(s, "wallhaven-")
	if j := strings.Index(s, "."); j >= 0 {
		s = s[:j]
	}
	if !idRe.MatchString(s) {
		return "", fmt.Errorf("not a Wallhaven id or link: %q", s)
	}
	return s, nil
}

// Download saves wallpaper id at full resolution into dir as
// wallhaven-<id>.<ext> (the site's own file name and format) and returns
// the path. An existing file is kept.
func Download(ctx context.Context, id, dir string) (string, *Wallpaper, error) {
	var info struct {
		Data Wallpaper `json:"data"`
	}
	if err := get(ctx, Base+"/w/"+id, &info); err != nil {
		return "", nil, err
	}
	w := info.Data
	dst, err := Save(ctx, w, dir)
	return dst, &w, err
}

// FileName is the local name for w: wallhaven-<id>.<ext>, the site's own.
func FileName(w Wallpaper) (string, error) {
	pu, err := url.Parse(w.Path)
	if err != nil || pu.Scheme != "https" || !strings.HasSuffix(pu.Host, "wallhaven.cc") {
		return "", fmt.Errorf("wallhaven: unexpected image link %q", w.Path)
	}
	name := filepath.Base(pu.Path)
	if !strings.HasPrefix(name, "wallhaven-") {
		return "", fmt.Errorf("wallhaven: unexpected file name %q", name)
	}
	return name, nil
}

// Save downloads w's full image into dir (keeping an existing file) and
// returns its path.
func Save(ctx context.Context, w Wallpaper, dir string) (string, error) {
	if w.Path == "" {
		return "", errors.New("wallhaven: no image link for " + w.ID)
	}
	name, err := FileName(w)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", w.Path, nil)
	req.Header.Set("User-Agent", "Spanwall")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("wallhaven download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("wallhaven download: %s", resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && w.FileSize > 0 && n != w.FileSize {
		err = fmt.Errorf("wallhaven download: got %d of %d bytes", n, w.FileSize)
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return dst, os.Rename(tmp, dst)
}

// Collection is one of a user's collections (favorites live in these).
type Collection struct {
	ID     int    `json:"id"`
	Label  string `json:"label"`
	Views  int    `json:"views"`
	Public int    `json:"public"`
	Count  int    `json:"count"`
}

var userRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{2,40}$`)

// Collections lists user's collections: all of them with Key set and no
// user (the key's owner), otherwise the user's public ones.
func Collections(ctx context.Context, user string) ([]Collection, error) {
	u := Base + "/collections"
	if user != "" {
		if !userRe.MatchString(user) {
			return nil, fmt.Errorf("not a Wallhaven username: %q", user)
		}
		u += "/" + url.PathEscape(user)
	} else if Key == "" {
		return nil, errors.New("wallhaven: set your username or API key in Settings")
	}
	var r struct {
		Data []Collection `json:"data"`
	}
	if err := get(ctx, u, &r); err != nil {
		return nil, err
	}
	return r.Data, nil
}

// CollectionWallpapers lists one page of a collection (safe for work only).
func CollectionWallpapers(ctx context.Context, user string, id, page int) (*Result, error) {
	if !userRe.MatchString(user) {
		return nil, fmt.Errorf("not a Wallhaven username: %q", user)
	}
	v := url.Values{"purity": {"100"}}
	if page > 1 {
		v.Set("page", fmt.Sprint(page))
	}
	var r Result
	if err := get(ctx, fmt.Sprintf("%s/collections/%s/%d?%s", Base, url.PathEscape(user), id, v.Encode()), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SyncResult reports a collection sync.
type SyncResult struct {
	Collection string   `json:"collection"`
	Total      int      `json:"total"`
	Downloaded []string `json:"downloaded"`
	Present    int      `json:"present"`
	Removed    int      `json:"removed,omitempty"` // skipped because the user removed them
	Errors     []string `json:"errors,omitempty"`
}

// SyncCollection downloads every wallpaper in the user's collection called
// label (case-insensitive) that isn't in dir yet. Nothing is ever deleted.
// skip (may be nil) names files the user removed; they aren't downloaded
// again.
func SyncCollection(ctx context.Context, user, label, dir string, skip func(name string) bool) (*SyncResult, error) {
	cs, err := Collections(ctx, "")
	if err != nil && user != "" {
		cs, err = Collections(ctx, user)
	}
	if err != nil {
		return nil, err
	}
	var col *Collection
	for i := range cs {
		if strings.EqualFold(cs[i].Label, label) {
			col = &cs[i]
		}
	}
	if col == nil {
		return nil, fmt.Errorf("no Wallhaven collection named %q", label)
	}
	res := &SyncResult{Collection: col.Label}
	for page := 1; ; page++ {
		r, err := CollectionWallpapers(ctx, user, col.ID, page)
		if err != nil {
			return res, err
		}
		for _, w := range r.Data {
			res.Total++
			name, err := FileName(w)
			if err != nil {
				res.Errors = append(res.Errors, err.Error())
				continue
			}
			if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.Size() > 0 {
				res.Present++
				continue
			}
			if skip != nil && skip(name) {
				res.Removed++
				continue
			}
			if _, err := Save(ctx, w, dir); err != nil {
				res.Errors = append(res.Errors, w.ID+": "+err.Error())
				continue
			}
			res.Downloaded = append(res.Downloaded, name)
		}
		if r.Meta.LastPage <= page {
			break
		}
		time.Sleep(1500 * time.Millisecond) // stay well under 45 requests a minute
	}
	return res, nil
}
