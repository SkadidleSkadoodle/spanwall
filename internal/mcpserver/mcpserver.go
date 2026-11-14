// Package mcpserver exposes Spanwall to Claude over MCP (stdio). Every tool
// is a thin call to the background app's HTTP API, which it starts on
// demand.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/image/draw"

	"github.com/martingomezmejia-ctrl/spanwall/internal/client"
	"github.com/martingomezmejia-ctrl/spanwall/internal/imageio"
	"github.com/martingomezmejia-ctrl/spanwall/internal/launch"
)

// Run serves MCP on stdin/stdout until the client disconnects.
func Run(ctx context.Context, version string) error {
	s := mcp.NewServer(&mcp.Implementation{Name: "spanwall", Title: "Spanwall", Version: version}, &mcp.ServerOptions{
		Instructions: "Spanwall spans one wallpaper across the user's monitors using their physical layout in mm " +
			"(x grows right, y grows down, position = top-left of each monitor's visible area). Moving a monitor UP " +
			"in the layout moves the image on it DOWN. Always call sw_preview and look at it before sw_set_wallpaper " +
			"when changing images or layouts. Bare file names refer to the wallpaper folder (see sw_status).",
	})
	register(s)
	return s.Run(ctx, &mcp.StdioTransport{})
}

func register(s *mcp.Server) {
	add(s, "sw_status", "Is the background app running; version, monitors, active layout, last applied wallpaper, wallpaper folder.",
		func(in struct{}) (*mcp.CallToolResult, error) { return getJSON("/api/status") })

	add(s, "sw_start", "Start the Spanwall background app if it isn't running (other tools also do this automatically).",
		func(in struct{}) (*mcp.CallToolResult, error) {
			if err := launch.Start(); err != nil {
				return nil, err
			}
			return getJSON("/api/status")
		})

	add(s, "sw_stop", "Stop the background app (needed before rebuilding it).",
		func(in struct{}) (*mcp.CallToolResult, error) {
			if err := launch.Stop(); err != nil {
				return nil, err
			}
			return text("stopped"), nil
		})

	add(s, "sw_project_info", "Where Spanwall's code lives and how to rebuild and reload it.",
		func(in struct{}) (*mcp.CallToolResult, error) {
			exe, _ := os.Executable()
			bin := filepath.Dir(exe)
			repo := filepath.Dir(bin)
			return jsonResult(map[string]any{
				"repo":      repo,
				"github":    "martingomezmejia-ctrl/spanwall (private)",
				"docs":      filepath.Join(repo, "CLAUDE.md"),
				"build":     filepath.Join(repo, "build.ps1") + "  (stops the background app, builds, packs the extension)",
				"reload":    "After rebuilding spanwall-mcp.exe, toggle the Spanwall extension off and on in Claude Desktop > Settings > Extensions.",
				"api":       client.Base,
				"dataDir":   "%USERPROFILE%\\.spanwall (config.json, cache, spanwall.log)",
				"mcpBinary": exe,
			})
		})

	add(s, "sw_list_displays", "Attached monitors: number, stable ID, name, resolution, physical size in mm, PPI, and where Windows puts them (informational only).",
		func(in struct{}) (*mcp.CallToolResult, error) { return getJSON("/api/displays") })

	type layoutIn struct {
		Layout string `json:"layout,omitempty" jsonschema:"layout name; empty means the active layout"`
	}
	add(s, "sw_get_layout", "Each monitor's position in mm in a layout, plus the saved layout names.",
		func(in layoutIn) (*mcp.CallToolResult, error) {
			return getJSON("/api/layout?name=" + urlQuery(in.Layout))
		})

	type moveIn struct {
		Monitor  string  `json:"monitor" jsonschema:"monitor number, ID, name or brand (e.g. 1, dell, acer)"`
		X        float64 `json:"x" jsonschema:"x in mm (or dx when relative)"`
		Y        float64 `json:"y" jsonschema:"y in mm (or dy when relative); positive is down"`
		Relative bool    `json:"relative,omitempty" jsonschema:"true to nudge by x,y instead of setting the position"`
		Layout   string  `json:"layout,omitempty" jsonschema:"layout name; empty means the active layout"`
	}
	add(s, "sw_move_display", "Set or nudge a monitor's position in a layout (mm). Doesn't re-apply the wallpaper; call sw_set_wallpaper after.",
		func(in moveIn) (*mcp.CallToolResult, error) {
			return postJSON("/api/layout/place", map[string]any{"layout": in.Layout, "monitor": in.Monitor, "x": in.X, "y": in.Y, "relative": in.Relative})
		})

	type presetIn struct {
		Action string `json:"action" jsonschema:"list, activate, copy, rename, delete or import-superpaper"`
		Name   string `json:"name,omitempty" jsonschema:"target layout name"`
		From   string `json:"from,omitempty" jsonschema:"source layout for copy and rename; empty means the active layout"`
	}
	add(s, "sw_layout_presets", "Manage saved layouts: list, activate, copy, rename, delete, or import Superpaper's arrangement into a layout.",
		func(in presetIn) (*mcp.CallToolResult, error) {
			switch in.Action {
			case "list", "":
				return getJSON("/api/layout")
			case "import-superpaper":
				return postJSON("/api/layout/import-superpaper", map[string]any{"layout": in.Name})
			default:
				return postJSON("/api/layout/action", map[string]any{"action": in.Action, "name": in.Name, "from": in.From})
			}
		})

	type identifyIn struct {
		Seconds int  `json:"seconds,omitempty" jsonschema:"how long to show the cards, default 10, max 300"`
		Stop    bool `json:"stop,omitempty" jsonschema:"true to end early and restore the wallpaper"`
	}
	add(s, "sw_identify", "Show a numbered test card on each monitor, then restore the wallpaper automatically.",
		func(in identifyIn) (*mcp.CallToolResult, error) {
			return postJSON("/api/identify", map[string]any{"seconds": in.Seconds, "stop": in.Stop})
		})

	type calIn struct {
		Stop   bool   `json:"stop,omitempty" jsonschema:"true to remove the pattern and restore the wallpaper"`
		Layout string `json:"layout,omitempty" jsonschema:"layout name; empty means the active layout"`
	}
	add(s, "sw_calibrate", "Show the physical test pattern (grid, diagonals, circles in desk mm) on every monitor using the saved layout, so the user can see misalignment across bezels. Auto-restores after 10 minutes; stop=true restores now. The browser editor drives this live while dragging.",
		func(in calIn) (*mcp.CallToolResult, error) {
			return postJSON("/api/calibrate", map[string]any{"stop": in.Stop, "layout": in.Layout})
		})

	type desktopsIn struct {
		Mode     string            `json:"mode,omitempty" jsonschema:"same, individual or panorama; empty just reads the current settings"`
		Assign   map[string]string `json:"assign,omitempty" jsonschema:"individual mode: desktop {GUID} (from a read) -> image path or file name"`
		Panorama string            `json:"panorama,omitempty" jsonschema:"panorama mode: the wide image split across desktops"`
	}
	add(s, "sw_desktops", "Virtual desktop wallpapers: read the desktops and mode, or set the mode (same everywhere, individual per desktop, or one panorama split across desktops).",
		func(in desktopsIn) (*mcp.CallToolResult, error) {
			if in.Mode == "" {
				return getJSON("/api/desktops")
			}
			return postJSON("/api/desktops", map[string]any{"mode": in.Mode, "assign": in.Assign, "panorama": in.Panorama})
		})

	type patIn struct {
		Stop bool `json:"stop,omitempty" jsonschema:"true to remove the dot pattern and restore the wallpaper"`
	}
	add(s, "sw_photo_pattern", "Show the coloured dot grid on every monitor (monitor 1 red, 2 green, 3 blue) so the user can take a phone photo from his seat for photo calibration. Stays up 15 minutes or until a photo is processed.",
		func(in patIn) (*mcp.CallToolResult, error) {
			return postJSON("/api/photo-pattern", map[string]any{"stop": in.Stop})
		})

	type alignIn struct {
		Path   string `json:"path" jsonschema:"photo on the user's PC (save chat photos to Documents\\spanwall\\local first)"`
		Mode   string `json:"mode,omitempty" jsonschema:"auto (default), pattern (dot grid on screen) or wallpaper (a real wallpaper on screen; needs one pattern calibration first)"`
		Layout string `json:"layout,omitempty" jsonschema:"layout to update; empty means the active layout"`
		Apply  bool   `json:"apply,omitempty" jsonschema:"save the result into the layout and re-apply the wallpaper; leave false to review first"`
	}
	add(s, "sw_align_from_photo", "Work out the monitor layout from a phone photo taken from the user's seat. Returns the photo with each detected screen outlined, the proposed positions (mm), apparent scales, residuals and match quality. With apply=false (default) nothing is saved: check the picture and numbers, then call again with apply=true or use sw_apply_alignment.",
		func(in alignIn) (*mcp.CallToolResult, error) {
			var p struct {
				Mode     string           `json:"mode"`
				Layout   string           `json:"layout"`
				Anchor   string           `json:"anchor"`
				Monitors []map[string]any `json:"monitors"`
				Notes    []string         `json:"notes"`
				Overlay  []byte           `json:"overlay"`
				TookMS   int64            `json:"tookMs"`
			}
			if err := callStarting(func() error {
				return client.Post("/api/align", map[string]any{"path": in.Path, "mode": in.Mode, "layout": in.Layout}, &p)
			}); err != nil {
				return nil, err
			}
			summary, _ := json.MarshalIndent(map[string]any{"mode": p.Mode, "layout": p.Layout, "anchor": p.Anchor, "monitors": p.Monitors, "notes": p.Notes, "tookMs": p.TookMS}, "", "  ")
			content := []mcp.Content{&mcp.ImageContent{Data: p.Overlay, MIMEType: "image/jpeg"}, &mcp.TextContent{Text: string(summary)}}
			if in.Apply {
				var info any
				if err := client.Post("/api/align/apply", map[string]any{}, &info); err != nil {
					return nil, err
				}
				content = append(content, &mcp.TextContent{Text: "Applied: layout saved and wallpaper re-applied."})
			} else {
				content = append(content, &mcp.TextContent{Text: "Not saved yet. Call sw_apply_alignment to save and re-apply."})
			}
			return &mcp.CallToolResult{Content: content}, nil
		})

	add(s, "sw_apply_alignment", "Save the last photo calibration result into its layout and re-apply the wallpaper.",
		func(in struct{}) (*mcp.CallToolResult, error) { return postJSON("/api/align/apply", map[string]any{}) })

	add(s, "sw_list_wallpapers", "Images in the wallpaper folder with resolution and aspect ratio, newest first.",
		func(in struct{}) (*mcp.CallToolResult, error) { return getJSON("/api/wallpapers") })

	type imagesIn struct {
		Images []string `json:"images,omitempty" jsonschema:"image paths, or file names in the wallpaper folder; span takes one, per-display takes one or one per monitor; empty in sw_preview draws the layout outlines"`
		Mode   string   `json:"mode,omitempty" jsonschema:"span (default), groups (span groups from sw_config, one image per group) or per-display"`
		Layout string   `json:"layout,omitempty" jsonschema:"layout name; empty means the active layout"`
		NoTrim bool     `json:"noTrim,omitempty" jsonschema:"keep black letterbox bars"`
	}
	add(s, "sw_preview", "Render without applying and return a picture of the monitors at their physical positions with the image inside, plus notes on trimming and resolution.",
		func(in imagesIn) (*mcp.CallToolResult, error) {
			return previewResult(in.Images, in.Mode, in.Layout, in.NoTrim, nil)
		})

	add(s, "sw_set_wallpaper", "Render and set the wallpaper on every monitor (one slice per monitor). Returns timings, notes and a preview picture.",
		func(in imagesIn) (*mcp.CallToolResult, error) {
			var res map[string]any
			if err := callStarting(func() error {
				return client.Post("/api/apply", map[string]any{"images": in.Images, "mode": in.Mode, "layout": in.Layout, "noTrim": in.NoTrim}, &res)
			}); err != nil {
				return nil, err
			}
			b, _ := json.MarshalIndent(res, "", "  ")
			content := []mcp.Content{&mcp.TextContent{Text: string(b)}}
			if p, _ := res["preview"].(string); p != "" {
				if jpg, err := jpegFromFile(p, 1200); err == nil {
					content = append(content, &mcp.ImageContent{Data: jpg, MIMEType: "image/jpeg"})
				}
			}
			return &mcp.CallToolResult{Content: content}, nil
		})

	type slideIn struct {
		Action      string `json:"action,omitempty" jsonschema:"status (default), next, previous, start, stop or set"`
		IntervalMin int    `json:"intervalMin,omitempty" jsonschema:"minutes between changes (with set or start)"`
		Order       string `json:"order,omitempty" jsonschema:"shuffle or name (with set or start)"`
		Mode        string `json:"mode,omitempty" jsonschema:"span, groups or per-display (with set or start)"`
	}
	add(s, "sw_slideshow", "Slideshow from the wallpaper folder: status, next, previous (from history), start, stop, or set interval/order/mode.",
		func(in slideIn) (*mcp.CallToolResult, error) {
			body := map[string]any{}
			if in.IntervalMin > 0 {
				body["intervalMin"] = in.IntervalMin
			}
			if in.Order != "" {
				body["order"] = in.Order
			}
			if in.Mode != "" {
				body["mode"] = in.Mode
			}
			switch in.Action {
			case "", "status":
				return getJSON("/api/slideshow")
			case "next", "previous":
				return postJSON("/api/slideshow/next", map[string]any{"back": in.Action == "previous"})
			case "start", "stop":
				body["enabled"] = in.Action == "start"
				return postJSON("/api/slideshow", body)
			case "set":
				return postJSON("/api/slideshow", body)
			}
			return nil, fmt.Errorf("unknown action %q", in.Action)
		})

	type whSearchIn struct {
		Query      string `json:"query,omitempty" jsonschema:"search terms or tags (Wallhaven syntax, e.g. +city -anime)"`
		Sorting    string `json:"sorting,omitempty" jsonschema:"relevance, toplist, random, date_added, views or favorites"`
		TopRange   string `json:"topRange,omitempty" jsonschema:"1d 3d 1w 1M 3M 6M 1y, for toplist"`
		Ratios     string `json:"ratios,omitempty" jsonschema:"e.g. 16x9,21x9; the result's layout field suggests one"`
		AtLeast    string `json:"atleast,omitempty" jsonschema:"minimum resolution, e.g. 3840x2160"`
		Categories string `json:"categories,omitempty" jsonschema:"general/anime/people flags, e.g. 100 for general only"`
		Page       int    `json:"page,omitempty"`
	}
	add(s, "sw_wallhaven_search", "Search wallhaven.cc (safe for work only). Returns 24 results per page with id, resolution, ratio, size, thumbnail and full URL, plus the layout's aspect ratio and the smallest image size that stays sharp on it.",
		func(in whSearchIn) (*mcp.CallToolResult, error) {
			v := url.Values{}
			for k, val := range map[string]string{"q": in.Query, "sorting": in.Sorting, "topRange": in.TopRange, "ratios": in.Ratios,
				"atleast": in.AtLeast, "categories": in.Categories} {
				if val != "" {
					v.Set(k, val)
				}
			}
			if in.Page > 1 {
				v.Set("page", fmt.Sprint(in.Page))
			}
			var res struct {
				Results []struct {
					ID         string `json:"id"`
					URL        string `json:"url"`
					Resolution string `json:"resolution"`
					Ratio      string `json:"ratio"`
					FileSize   int64  `json:"file_size"`
					FileType   string `json:"file_type"`
					Favorites  int    `json:"favorites"`
					Thumbs     struct {
						Large string `json:"large"`
					} `json:"thumbs"`
				} `json:"results"`
				Page     int `json:"page"`
				LastPage int `json:"lastPage"`
				Total    int `json:"total"`
				Layout   any `json:"layout"`
			}
			if err := callStarting(func() error { return client.Get("/api/wallhaven/search?"+v.Encode(), &res) }); err != nil {
				return nil, err
			}
			b, _ := json.MarshalIndent(res, "", "  ")
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
		})

	type whDownloadIn struct {
		ID    string `json:"id" jsonschema:"Wallhaven id or link, e.g. x6mlg3 or https://wallhaven.cc/w/x6mlg3"`
		Apply bool   `json:"apply,omitempty" jsonschema:"also span it across the monitors now"`
	}
	add(s, "sw_wallhaven_download", "Download a Wallhaven wallpaper at full resolution, in its original format, into the wallpaper folder (as wallhaven-<id>.<ext>); optionally apply it.",
		func(in whDownloadIn) (*mcp.CallToolResult, error) {
			return postJSON("/api/wallhaven/download", map[string]any{"id": in.ID, "apply": in.Apply})
		})

	type collIn struct {
		ID   int `json:"id,omitempty" jsonschema:"collection id; omit to list the user's collections"`
		Page int `json:"page,omitempty"`
	}
	add(s, "sw_wallhaven_collections", "The user's Wallhaven collections (their favorites): without id, list them; with id, list the wallpapers in one (24 per page). Download with sw_wallhaven_download.",
		func(in collIn) (*mcp.CallToolResult, error) {
			if in.ID == 0 {
				return getJSON("/api/wallhaven/collections")
			}
			return getJSON(fmt.Sprintf("/api/wallhaven/collection?id=%d&page=%d", in.ID, max(1, in.Page)))
		})

	add(s, "sw_wallhaven_sync", "Download anything new in the user's synced Wallhaven collection (default \"Spanwall\") into the wallpaper folder now. Never deletes.",
		func(in struct{}) (*mcp.CallToolResult, error) {
			return postJSON("/api/wallhaven/sync", map[string]any{})
		})

	type frameIn struct {
		Image string   `json:"image" jsonschema:"image path or file name in the wallpaper folder"`
		Group []string `json:"group,omitempty" jsonschema:"for span groups: the group's monitor IDs (from sw_status groups and sw_list_displays); empty = spanned across all monitors"`
		Zoom  float64  `json:"zoom,omitempty" jsonschema:"1 = default fill; 2 = twice as big (0.25 to 10)"`
		DX    float64  `json:"dx,omitempty" jsonschema:"move the image right by this fraction of its displayed width (negative = left)"`
		DY    float64  `json:"dy,omitempty" jsonschema:"move the image down by this fraction of its displayed height (negative = up)"`
		Set   bool     `json:"set,omitempty" jsonschema:"true to save zoom/dx/dy (all zero or zoom 1 resets); false just reads"`
	}
	add(s, "sw_framing", "Read or set how an image is zoomed and moved behind the monitors (the editor's Move image). Saved per image (and per span group). Re-apply with sw_set_wallpaper to see it.",
		func(in frameIn) (*mcp.CallToolResult, error) {
			group := strings.Join(in.Group, ",")
			if !in.Set {
				return getJSON("/api/framing?image=" + url.QueryEscape(in.Image) + "&group=" + url.QueryEscape(group))
			}
			z := in.Zoom
			if z == 0 {
				z = 1
			}
			return postJSON("/api/framing", map[string]any{"image": in.Image, "group": group, "zoom": z, "dx": in.DX, "dy": in.DY})
		})

	type configIn struct {
		WallpaperDir  string            `json:"wallpaperDir,omitempty" jsonschema:"folder that holds wallpapers"`
		Monitor       string            `json:"monitor,omitempty" jsonschema:"monitor to override the physical size of"`
		WidthMM       float64           `json:"widthMM,omitempty" jsonschema:"measured visible width in mm"`
		HeightMM      float64           `json:"heightMM,omitempty" jsonschema:"measured visible height in mm"`
		Groups        [][]string        `json:"groups,omitempty" jsonschema:"span groups: lists of monitors (number, ID or name) spanned together in groups mode; [[]] clears"`
		Autostart     *bool             `json:"autostart,omitempty" jsonschema:"start Spanwall at sign-in"`
		Tray          *bool             `json:"tray,omitempty" jsonschema:"show the tray icon"`
		Hotkeys       map[string]string `json:"hotkeys,omitempty" jsonschema:"action (next, previous, pause, open) to combo like ctrl+alt+pgdn; empty string disables"`
		WallhavenUser string            `json:"wallhavenUser,omitempty" jsonschema:"the user's Wallhaven username (the API key is entered by the user in Settings, never here)"`
		ClearSize     bool              `json:"clearSize,omitempty" jsonschema:"remove the size override for monitor"`
	}
	add(s, "sw_config", "Read the config, or change: wallpaper folder, a monitor's measured size, span groups, start at sign-in, tray icon, hotkeys. With no arguments it just reads.",
		func(in configIn) (*mcp.CallToolResult, error) {
			if in.WallpaperDir == "" && in.Monitor == "" && in.Groups == nil && in.Autostart == nil && in.Tray == nil && in.Hotkeys == nil && in.WallhavenUser == "" {
				return getJSON("/api/config")
			}
			body := map[string]any{"monitor": in.Monitor, "widthMM": in.WidthMM, "heightMM": in.HeightMM, "clearSize": in.ClearSize}
			if in.WallpaperDir != "" {
				body["wallpaperDir"] = in.WallpaperDir
			}
			if in.Groups != nil {
				body["groups"] = in.Groups
			}
			if in.Autostart != nil {
				body["autostart"] = *in.Autostart
			}
			if in.Tray != nil {
				body["tray"] = *in.Tray
			}
			if in.Hotkeys != nil {
				body["hotkeys"] = in.Hotkeys
			}
			if in.WallhavenUser != "" {
				body["wallhavenUser"] = in.WallhavenUser
			}
			return postJSON("/api/config", body)
		})
}

// add registers a tool whose handler returns a result or an error; errors
// become tool errors Claude can read.
func add[In any](s *mcp.Server, name, desc string, h func(In) (*mcp.CallToolResult, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc},
		func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			res, err := h(in)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
			}
			return res, nil, nil
		})
}

// callStarting runs fn, starting the background app once if it isn't up.
func callStarting(fn func() error) error {
	err := fn()
	if err != nil && errors.Is(err, client.ErrNotRunning) {
		if serr := launch.Start(); serr != nil {
			return fmt.Errorf("%v; starting it failed: %v", err, serr)
		}
		err = fn()
	}
	return err
}

func getJSON(path string) (*mcp.CallToolResult, error) {
	var v any
	if err := callStarting(func() error { return client.Get(path, &v) }); err != nil {
		return nil, err
	}
	return jsonResult(v)
}

func postJSON(path string, body any) (*mcp.CallToolResult, error) {
	var v any
	if err := callStarting(func() error { return client.Post(path, body, &v) }); err != nil {
		return nil, err
	}
	return jsonResult(v)
}

func previewResult(images []string, mode, layout string, noTrim bool, first mcp.Content) (*mcp.CallToolResult, error) {
	var res struct {
		JPEG  []byte   `json:"jpeg"`
		Notes []string `json:"notes"`
	}
	if err := callStarting(func() error {
		return client.Post("/api/preview", map[string]any{"images": images, "mode": mode, "layout": layout, "noTrim": noTrim, "width": 1200}, &res)
	}); err != nil {
		return nil, err
	}
	var content []mcp.Content
	if first != nil {
		content = append(content, first)
	}
	content = append(content,
		&mcp.ImageContent{Data: res.JPEG, MIMEType: "image/jpeg"},
		&mcp.TextContent{Text: "Notes:\n- " + strings.Join(res.Notes, "\n- ")})
	return &mcp.CallToolResult{Content: content}, nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return text(string(b)), nil
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func urlQuery(s string) string { return url.QueryEscape(s) }

// jpegFromFile loads an image written by the background app (same machine)
// and returns a downscaled JPEG small enough to send to Claude.
func jpegFromFile(path string, width int) ([]byte, error) {
	img, err := imageio.Load(path)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() > width {
		dst := image.NewRGBA(image.Rect(0, 0, width, b.Dy()*width/b.Dx()))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82})
	return buf.Bytes(), err
}

// ToolInfo names a tool for the extension manifest.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Tools lists the registered tools (for the extension manifest).
func Tools(ctx context.Context) ([]ToolInfo, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: "spanwall", Version: "list"}, nil)
	register(s)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		return nil, err
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "list", Version: "list"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		return nil, err
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ToolInfo, len(res.Tools))
	for i, t := range res.Tools {
		d := t.Description
		if j := strings.IndexAny(d, ".("); j > 20 {
			d = d[:j] // first sentence is enough for the manifest
		}
		out[i] = ToolInfo{t.Name, d}
	}
	return out, nil
}
