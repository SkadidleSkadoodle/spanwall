// Package config stores Spanwall's settings and layouts as JSON.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
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
}

// Frame zooms and moves a spanned image: Zoom relative to the default fill,
// DX/DY as fractions of the displayed image size.
type Frame struct {
	Zoom float64 `json:"zoom"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
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
	return &Config{Version: 1, ActiveLayout: "default", Layouts: map[string]*Layout{}, Displays: map[string]*DisplayOverride{}}
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
