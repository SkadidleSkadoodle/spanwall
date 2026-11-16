// Package config stores Spanwall's settings and layouts as JSON.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
)

// Config is the whole settings file.
type Config struct {
	Version      int                         `json:"version"`
	WallpaperDir string                      `json:"wallpaperDir,omitempty"`
	ActiveLayout string                      `json:"activeLayout"`
	Layouts      map[string]*Layout          `json:"layouts"`
	Displays     map[string]*DisplayOverride `json:"displays,omitempty"`
	LastApplied  *Applied                    `json:"lastApplied,omitempty"`
	AutoSwitch   *bool                       `json:"autoSwitch,omitempty"` // default true
	Desktops     Desktops                    `json:"desktops"`
	Photo        *PhotoPrior                 `json:"photo,omitempty"`
	// Groups are span groups: monitor IDs spanned together in "groups"
	// mode. Monitors in no group get their own image.
	Groups    [][]string        `json:"groups,omitempty"`
	Slideshow Slideshow         `json:"slideshow"`
	Hotkeys   map[string]string `json:"hotkeys,omitempty"` // action -> "ctrl+alt+pgdn"
	Tray      *bool             `json:"tray,omitempty"`    // default true
	// WallhavenUser is the user's Wallhaven username, for their collections.
	// The API key is kept apart, in wallhaven.key next to the config, so it
	// never shows up in /api/config.
	WallhavenUser string `json:"wallhavenUser,omitempty"`
	// WallhavenSync keeps the wallpaper folder in step with one of the
	// user's Wallhaven collections (downloads only; never deletes).
	WallhavenSync WallhavenSync `json:"wallhavenSync"`
	// Framing is how each image (by lower-case file name) is zoomed and
	// moved behind the monitors when spanned.
	Framing map[string]Frame `json:"framing,omitempty"`
	// SmartFrame frames images that have no saved framing automatically,
	// keeping their subject on screens rather than bezels (default true).
	SmartFrame *bool `json:"smartFrame,omitempty"`
	// Profiles remember, per set of attached monitors (sorted IDs joined by
	// ","), the layout and wallpaper last used, so docking and undocking
	// bring each setup back the way it was.
	Profiles map[string]*Profile `json:"profiles,omitempty"`
	// Ratings marks images (lower-case file name) "fav" (shown more often)
	// or "ban" (never shown by the slideshow).
	Ratings map[string]string `json:"ratings,omitempty"`
	// Folders group library images (by lower-case file name); an image can
	// be in any number of them. "Day" and "Night" always exist and feed the
	// day/night schedule.
	Folders []Folder `json:"folders,omitempty"`
	// Removed lists wallpapers (lower-case file names) the user removed from
	// the library, so syncs and sources don't download them again.
	Removed []string `json:"removed,omitempty"`
	// Periods is from an earlier version (day/night tags); Load moves it
	// into the Day and Night folders.
	Periods map[string]string `json:"periods,omitempty"`
	// Schedule switches between day and night images.
	Schedule Schedule `json:"schedule"`
	// Sync copies the wallpaper's look to Windows.
	Sync SyncOptions `json:"sync"`
	// Sources are other places new wallpapers are downloaded from.
	Sources Sources `json:"sources"`
}

// Profile is one monitor setup's layout and last wallpaper.
type Profile struct {
	Layout  string   `json:"layout"`
	Mode    string   `json:"mode"`
	Images  []string `json:"images"`
	Names   []string `json:"names,omitempty"` // monitor names, for showing the setup
	Updated string   `json:"updated,omitempty"`
}

// Schedule shows the Day folder by day and the Night folder at night,
// when Enabled. Mode says when night starts: "sun" (sunrise and sunset at
// Lat, Lon) or "theme" (Windows light or dark mode). Current is the period
// last applied.
type Schedule struct {
	Enabled bool    `json:"enabled"`
	Mode    string  `json:"mode,omitempty"`
	Lat     float64 `json:"lat,omitempty"`
	Lon     float64 `json:"lon,omitempty"`
	Current string  `json:"current,omitempty"`
}

// SyncOptions: Accent sets the Windows accent colour from the wallpaper;
// LockScreen puts the main monitor's slice on the lock screen.
type SyncOptions struct {
	Accent     bool `json:"accent"`
	LockScreen bool `json:"lockScreen"`
}

// Sources configures the extra wallpaper sources. Each new image is saved
// into the wallpaper folder when it is landscape and at least MinWidth wide.
type Sources struct {
	Bing       bool     `json:"bing"`
	APOD       bool     `json:"apod"`
	Wikimedia  bool     `json:"wikimedia"`
	Reddit     bool     `json:"reddit"`
	Subreddits string   `json:"subreddits,omitempty"` // comma-separated, default "WidescreenWallpaper,wallpaper"
	Unsplash   bool     `json:"unsplash"`
	Query      string   `json:"query,omitempty"`    // Unsplash search, default "landscape"
	MinWidth   int      `json:"minWidth,omitempty"` // default 2560
	Skipped    []string `json:"skipped,omitempty"`  // ids checked and rejected (too small, portrait)
	LastRun    string   `json:"lastRun,omitempty"`
	LastResult string   `json:"lastResult,omitempty"`
}

// Folder is a named group of library images.
type Folder struct {
	Name   string   `json:"name"`
	Images []string `json:"images,omitempty"` // lower-case file names
}

// Built-in folders, always present, in this order before the user's own.
var BuiltinFolders = []string{"Day", "Night"}

// IsBuiltin reports whether name is a built-in folder.
func IsBuiltin(name string) bool {
	for _, b := range BuiltinFolders {
		if strings.EqualFold(b, name) {
			return true
		}
	}
	return false
}

// FolderIndex finds a folder by name (case-insensitive), or -1.
func (c *Config) FolderIndex(name string) int {
	for i, f := range c.Folders {
		if strings.EqualFold(f.Name, strings.TrimSpace(name)) {
			return i
		}
	}
	return -1
}

// ensureFolders adds the built-in folders and moves old day/night tags in.
func (c *Config) ensureFolders() {
	for k, b := range BuiltinFolders {
		if i := c.FolderIndex(b); i < 0 {
			c.Folders = append(c.Folders[:min(k, len(c.Folders))], append([]Folder{{Name: b}}, c.Folders[min(k, len(c.Folders)):]...)...)
		}
	}
	for img, p := range c.Periods {
		switch p {
		case "day":
			c.SetInFolder("Day", img, true)
		case "night":
			c.SetInFolder("Night", img, true)
		}
	}
	c.Periods = nil
}

var reservedFolders = []string{"all", "favourites", "favorites", "banned"}

// CreateFolder adds an empty folder.
func (c *Config) CreateFolder(name string) error {
	name = strings.TrimSpace(name)
	if err := validFolderName(name); err != nil {
		return err
	}
	if c.FolderIndex(name) >= 0 {
		return fmt.Errorf("there is already a folder called %q", name)
	}
	c.Folders = append(c.Folders, Folder{Name: name})
	return nil
}

func validFolderName(name string) error {
	if name == "" || len(name) > 40 {
		return fmt.Errorf("a folder name is 1 to 40 characters")
	}
	for _, r := range reservedFolders {
		if strings.EqualFold(r, name) {
			return fmt.Errorf("%q is taken by a built-in tab", name)
		}
	}
	return nil
}

// RenameFolder renames one of the user's folders.
func (c *Config) RenameFolder(from, to string) error {
	i := c.FolderIndex(from)
	if i < 0 {
		return fmt.Errorf("no folder called %q", from)
	}
	if IsBuiltin(from) {
		return fmt.Errorf("%s is built in and can't be renamed", c.Folders[i].Name)
	}
	to = strings.TrimSpace(to)
	if err := validFolderName(to); err != nil {
		return err
	}
	if j := c.FolderIndex(to); j >= 0 && j != i {
		return fmt.Errorf("there is already a folder called %q", to)
	}
	if strings.EqualFold(c.Slideshow.Source, c.Folders[i].Name) {
		c.Slideshow.Source = to
	}
	c.Folders[i].Name = to
	return nil
}

// DeleteFolder removes one of the user's folders (the images stay).
func (c *Config) DeleteFolder(name string) error {
	i := c.FolderIndex(name)
	if i < 0 {
		return fmt.Errorf("no folder called %q", name)
	}
	if IsBuiltin(name) {
		return fmt.Errorf("%s is built in and can't be deleted", c.Folders[i].Name)
	}
	if strings.EqualFold(c.Slideshow.Source, c.Folders[i].Name) {
		c.Slideshow.Source = ""
	}
	c.Folders = append(c.Folders[:i], c.Folders[i+1:]...)
	return nil
}

// SetInFolder adds image (a file name) to the folder, or removes it.
func (c *Config) SetInFolder(folder, image string, in bool) error {
	i := c.FolderIndex(folder)
	if i < 0 {
		return fmt.Errorf("no folder called %q", folder)
	}
	k := strings.ToLower(filepath.Base(image))
	f := &c.Folders[i]
	for j, n := range f.Images {
		if n == k {
			if !in {
				f.Images = append(f.Images[:j], f.Images[j+1:]...)
			}
			return nil
		}
	}
	if in {
		f.Images = append(f.Images, k)
	}
	return nil
}

// InFolder reports whether image (a file name) is in the folder.
func (c *Config) InFolder(folder, image string) bool {
	i := c.FolderIndex(folder)
	if i < 0 {
		return false
	}
	k := strings.ToLower(filepath.Base(image))
	for _, n := range c.Folders[i].Images {
		if n == k {
			return true
		}
	}
	return false
}

// FoldersOf lists the folders image (a file name) is in.
func (c *Config) FoldersOf(image string) []string {
	var out []string
	for _, f := range c.Folders {
		if c.InFolder(f.Name, image) {
			out = append(out, f.Name)
		}
	}
	return out
}

// IsRemoved reports whether the user removed this file name from the
// library.
func (c *Config) IsRemoved(name string) bool {
	k := strings.ToLower(filepath.Base(name))
	for _, r := range c.Removed {
		if r == k {
			return true
		}
	}
	return false
}

// ForgetImage drops everything kept about an image (rating, folders,
// framing) and marks it removed.
func (c *Config) ForgetImage(path string) {
	k := strings.ToLower(filepath.Base(path))
	delete(c.Ratings, k)
	for i := range c.Folders {
		c.SetInFolder(c.Folders[i].Name, k, false)
	}
	for fk := range c.Framing {
		p := fk
		if i := strings.Index(fk, "|"); i >= 0 {
			p = fk[:i]
		}
		if strings.EqualFold(filepath.Base(p), k) {
			delete(c.Framing, fk)
		}
	}
	if !c.IsRemoved(k) {
		c.Removed = append(c.Removed, k)
	}
}

// SmartFrameOn reports whether unframed images are framed automatically.
func (c *Config) SmartFrameOn() bool { return c.SmartFrame == nil || *c.SmartFrame }

// Frame zooms and moves a spanned image: Zoom relative to the default fill,
// DX/DY as fractions of the displayed image size; FlipX/FlipY mirror it.
type Frame struct {
	Zoom   float64 `json:"zoom"`
	DX     float64 `json:"dx"`
	DY     float64 `json:"dy"`
	FlipX  bool    `json:"flipX,omitempty"`
	FlipY  bool    `json:"flipY,omitempty"`
	Rotate float64 `json:"rotate,omitempty"` // degrees clockwise
}

// WallhavenSync is the collection auto-download.
type WallhavenSync struct {
	Enabled    bool   `json:"enabled"`
	Collection string `json:"collection,omitempty"` // default "Spanwall"
	LastRun    string `json:"lastRun,omitempty"`
	LastResult string `json:"lastResult,omitempty"`
}

// Slideshow changes the wallpaper on a timer.
type Slideshow struct {
	Enabled     bool   `json:"enabled"`
	IntervalMin int    `json:"intervalMin,omitempty"` // default 30
	Order       string `json:"order,omitempty"`       // "shuffle" (default) or "name"
	Mode        string `json:"mode,omitempty"`        // "span" (default), "groups" or "per-display"
	// Source is the album shuffled: "" (all wallpapers), "fav"
	// (favourites) or a folder name.
	Source string `json:"source,omitempty"`
	// History holds recently shown image sets (newest last) for "previous"
	// and so shuffle avoids repeats.
	History [][]string `json:"history,omitempty"`
	Next    string     `json:"next,omitempty"` // RFC3339 time of the next change
}

// PhotoPrior is where each monitor appeared in the last successful photo
// calibration. Fixing the layout from a photo of a real wallpaper starts
// from it. Homographies map native monitor pixels to photo pixels divided
// by the photo width (so they don't depend on the photo's resolution).
type PhotoPrior struct {
	Width  int                   `json:"width"`
	Height int                   `json:"height"`
	H      map[string][9]float64 `json:"h"`
	Date   string                `json:"date"`
	Mode   string                `json:"mode"`
}

// Desktops controls what each virtual desktop shows.
type Desktops struct {
	// Mode is "same" (one wallpaper everywhere, the default), "individual"
	// (Assign picks an image per desktop; unassigned ones show the last
	// applied image) or "panorama" (Panorama is split into one vertical strip
	// per desktop, left to right in Task View order).
	Mode     string            `json:"mode,omitempty"`
	Assign   map[string]string `json:"assign,omitempty"` // desktop {GUID} -> image path
	Panorama string            `json:"panorama,omitempty"`
}

// AutoSwitchOn reports whether layouts follow the attached monitors.
func (c *Config) AutoSwitchOn() bool { return c.AutoSwitch == nil || *c.AutoSwitch }

// Layout is where each monitor's active area sits on the desk, in mm.
type Layout struct {
	Placements map[string]geometry.Placement `json:"placements"` // by monitor ID
	Note       string                        `json:"note,omitempty"`
}

// DisplayOverride replaces detected values for one monitor.
type DisplayOverride struct {
	WidthMM  float64 `json:"widthMM,omitempty"`
	HeightMM float64 `json:"heightMM,omitempty"`
}

// Applied records the last wallpaper so it can be re-applied after a
// layout change.
type Applied struct {
	Mode   string   `json:"mode"` // "span", "groups" or "per-display"
	Images []string `json:"images"`
	Layout string   `json:"layout"`
}

const fileName = "config.json"

// New returns an empty config.
func New() *Config {
	c := &Config{Version: 1, ActiveLayout: "default", Layouts: map[string]*Layout{}, Displays: map[string]*DisplayOverride{}}
	c.ensureFolders()
	return c
}

// Load reads dir\config.json, or returns an empty config if there is none.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	c := New()
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.Layouts == nil {
		c.Layouts = map[string]*Layout{}
	}
	if c.Displays == nil {
		c.Displays = map[string]*DisplayOverride{}
	}
	if c.ActiveLayout == "" {
		c.ActiveLayout = "default"
	}
	c.ensureFolders()
	return c, nil
}

// Save writes the config atomically (write to a temp file, then rename).
func (c *Config) Save(dir string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, fileName))
}

// Layout returns the named layout, creating it if missing.
func (c *Config) Layout(name string) *Layout {
	l := c.Layouts[name]
	if l == nil {
		l = &Layout{Placements: map[string]geometry.Placement{}}
		c.Layouts[name] = l
	}
	if l.Placements == nil {
		l.Placements = map[string]geometry.Placement{}
	}
	return l
}

// LayoutNames returns the saved layout names, sorted.
func (c *Config) LayoutNames() []string {
	var out []string
	for n := range c.Layouts {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ApplyOverride swaps in any user-measured size for a display.
func (c *Config) ApplyOverride(d geometry.Display) geometry.Display {
	if o := c.Displays[d.ID]; o != nil {
		if o.WidthMM > 0 {
			d.WidthMM = o.WidthMM
		}
		if o.HeightMM > 0 {
			d.HeightMM = o.HeightMM
		}
	}
	return d
}
