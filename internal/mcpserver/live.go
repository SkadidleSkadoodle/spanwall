package mcpserver

import (
	"fmt"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SceneGuide is the scene-writing reference given to Claude with sw_live_scene.
const SceneGuide = `Scenes are ES modules (plain JavaScript, no imports needed) that fill a character grid laid out in desk mm across every monitor; Spanwall draws it on the GPU through each monitor's calibrated shape, so characters line up across bezels. Template:

export default {
  name: "Title", description: "One line.",
  fps: 30,                       // frame cap (the user can override)
  cell: { h: 4.2, aspect: 0.55 }, // character height in mm, width/height
  background: "#05070a",
  params: {                      // optional settings, shown as controls in the editor and saved per scene
    speed: { label: "Speed", type: "range", min: 0.1, max: 3, step: 0.1, default: 1 },
    color: { label: "Colour", type: "color", default: "#33ff66" },
    trails: { label: "Trails", type: "toggle", default: true },
    style: { label: "Style", type: "select", options: ["a", "b"], default: "a" },
    city: { label: "City", type: "text", default: "" },
  },
  setup(g) {},                   // optional, once per grid (may be async)
  draw(g, t, dt) {},             // fill the grid; t = seconds since start. Return false when nothing changed (skips the GPU upload)
  paramsChanged(g) {},           // optional: settings changed (g.params updated, g.paramsVersion bumped)
};

g: cols, rows, n, cw, ch (cell mm), x0, y0, width, height (desk mm). Typed arrays indexed i = r*cols + c: g.chars (Uint32 code points; 32 = blank), g.fg (0xRRGGBB), g.bg (0 = none, else 0xFF000000|0xRRGGBB; set g.bgUsed = true if you write it directly), g.mask (1 where the cell is on a monitor: skip the rest to save time).
Helpers: g.put(c, r, ch, fg, bg), g.text(c, r, str, fg, bg), g.clear(ch=32, fg, bg), g.ramp(v0to1, chars=" .:-=+*#%@") -> code point, g.rgb(r,g,b), g.hsl(hTurns, s, l), g.mix(a, b, t), g.scale(color, k), g.noise(x, y, z) (smooth 0..1), g.hash(x, y) (0..1), g.rand(seed) -> () => 0..1, g.mm(c, r) -> [x, y] mm, g.cell(xmm, ymm) -> [c, r].
g.phase(name, rate): use for anything driven by a speed setting (e.g. const T = g.phase("T", g.params.speed)) instead of t * speed, so moving the slider changes the pace without jumping the scene; call once per frame per name.
g.params: the settings (defaults merged with saved values); g.paramsVersion changes when they do (rebuild palettes then). Colour themes and character sets are applied by Spanwall to any scene (params _theme, _charset), so scenes don't implement them.
g.width/g.height are the scene's canvas (1.5x the monitors' box; the user frames it like an image, so draw over the whole canvas and centre on g.width/2, g.height/2 or g.main).
g.monitors: [{id, name, primary, x, y, w, h (canvas mm box), c0, r0, c1, r1 (cell box), cc, cr (centre cell)}]; g.main is the Windows primary monitor: centre focal things (clocks, logos) on it. g.preview is true in the editor. g.location is {lat, lon} from Spanwall's day and night settings, if set.
Helpers in ./_font.js: import { big, textWidth } from "./_font.js"; big(g, "12:30", c, r, px, py, color) draws 5x7 block digits/letters. Data: fetch("/api/live/stats") gives {cpu, memPercent, memUsed, memTotal (GB), uptimeSec, battery?}; scenes may fetch public HTTPS APIs (e.g. Open-Meteo).
Performance: one draw per frame for the whole desk (~100k cells); avoid per-cell allocations and closures, precompute palettes into Uint32Arrays in setup, use mm-based maths (c*g.cw, r*g.ch) so patterns are the same physical size on every monitor. Full blocks (█ ▀ ▄ ▌ ▐) and shades (░ ▒ ▓) join seamlessly; any Unicode character works. Keep everything deterministic in t where you can.
A scene that throws shows its error on screen and in sw_live status (page.error); fix and rewrite it.`

func registerLive(s *mcp.Server) {
	type liveIn struct {
		Action string         `json:"action,omitempty" jsonschema:"status (default), on, off, set, rate, settings, duplicate or schedule"`
		Rating string         `json:"rating,omitempty" jsonschema:"rate: fav, ban or none for the scene (favourites come up more in the live slideshow album, banned never)"`
		Params map[string]any `json:"params,omitempty" jsonschema:"settings: the scene's own settings by key, plus _theme (phosphor, amber, ice, mono, synthwave, ocean, blood, custom with _tintA/_tintB hex), _charset (ascii, blocks, braille, katakana, binary, dots, lines); empty resets"`
		Name   string         `json:"name,omitempty" jsonschema:"duplicate: the copy's name"`
		Day    string         `json:"day,omitempty" jsonschema:"schedule: the day scene"`
		Night  string         `json:"night,omitempty" jsonschema:"schedule: the night scene"`
		On     *bool          `json:"on,omitempty" jsonschema:"schedule: switch day and night scenes on or off"`
		Scene  string         `json:"scene,omitempty" jsonschema:"scene name (with on or set); see the scenes list in status"`
		FPS    *int           `json:"fps,omitempty" jsonschema:"frame cap 1-60, 0 = the scene's own (with set or on)"`
		CharMM *float64       `json:"charMM,omitempty" jsonschema:"character height in mm, 0 = the scene's own (with set or on)"`
	}
	add(s, "sw_live", "Animated ASCII wallpaper behind the desktop icons, spanning every monitor by the physical layout: status (scenes list, whether it runs, the page's fps and errors), on, off, or set the scene, frame rate and character size. It pauses itself whenever windows cover the monitors. Choosing a still wallpaper turns it off. The slideshow album \"live\" (sw_slideshow source=live) shuffles scenes; rate scenes fav or ban like wallpapers. Each scene has settings (its own, plus colour theme and character set), day and night scenes can switch automatically, and duplicate copies a scene to edit as the user's own.",
		func(in liveIn) (*mcp.CallToolResult, error) {
			body := map[string]any{}
			if in.Scene != "" {
				body["scene"] = in.Scene
			}
			if in.FPS != nil {
				body["fps"] = *in.FPS
			}
			if in.CharMM != nil {
				body["charMM"] = *in.CharMM
			}
			switch in.Action {
			case "", "status":
				return getJSON("/api/live")
			case "on", "off":
				body["enabled"] = in.Action == "on"
				return postJSON("/api/live", body)
			case "set":
				return postJSON("/api/live", body)
			case "rate":
				return postJSON("/api/live/rate", map[string]any{"name": in.Scene, "rating": in.Rating})
			case "settings":
				return postJSON("/api/live/params", map[string]any{"scene": in.Scene, "params": in.Params})
			case "duplicate":
				return postJSON("/api/live/duplicate", map[string]any{"from": in.Scene, "name": in.Name})
			case "schedule":
				on := in.On == nil || *in.On
				return postJSON("/api/live", map[string]any{"schedule": map[string]any{"enabled": on, "day": in.Day, "night": in.Night}})
			}
			return nil, fmt.Errorf("unknown action %q", in.Action)
		})

	type sceneIn struct {
		Action   string `json:"action,omitempty" jsonschema:"read (default), write, or delete (removes one of the user's scenes with its snapshots and settings; deleting an edited built-in brings the built-in back)"`
		Name     string `json:"name" jsonschema:"scene name: lower-case letters, digits, - and _"`
		Code     string `json:"code,omitempty" jsonschema:"the whole scene module (write)"`
		Activate bool   `json:"activate,omitempty" jsonschema:"write: switch the wallpaper to this scene and turn animation on"`
	}
	add(s, "sw_live_scene", "Read, write or delete an animated wallpaper scene (JavaScript). Writing saves it in the user's scenes folder (a built-in scene of the same name is overridden, not changed) and reloads it if it is on screen. Read a built-in scene first for a working example.\n\n"+SceneGuide,
		func(in sceneIn) (*mcp.CallToolResult, error) {
			if in.Action == "delete" {
				return postJSON("/api/live/delete", map[string]any{"name": in.Name})
			}
			if in.Action == "write" {
				return postJSON("/api/live/scene", map[string]any{"name": in.Name, "code": in.Code, "activate": in.Activate})
			}
			return getJSON("/api/live/scene?name=" + url.QueryEscape(in.Name))
		})

	type convIn struct {
		Path     string  `json:"path" jsonschema:"a GIF, picture, or video (videos need ffmpeg on the PATH)"`
		Name     string  `json:"name,omitempty" jsonschema:"scene name; default from the file name"`
		Cols     int     `json:"cols,omitempty" jsonschema:"width in characters (default 240, max 600)"`
		Mono     bool    `json:"mono,omitempty" jsonschema:"one colour instead of the image's colours"`
		FPS      float64 `json:"fps,omitempty" jsonschema:"videos: frames per second to take (default 15)"`
		Seconds  float64 `json:"seconds,omitempty" jsonschema:"videos: seconds to take (default 20)"`
		Fit      string  `json:"fit,omitempty" jsonschema:"cover (fill every monitor, default) or contain (show all of it)"`
		Activate bool    `json:"activate,omitempty" jsonschema:"show it on the desktop right away"`
	}
	add(s, "sw_live_convert", "Turn a GIF, video or picture into ASCII art frames and save it as an animated wallpaper scene.",
		func(in convIn) (*mcp.CallToolResult, error) {
			return postJSON("/api/live/convert", map[string]any{"path": in.Path, "name": in.Name, "cols": in.Cols, "mono": in.Mono,
				"fps": in.FPS, "seconds": in.Seconds, "activate": in.Activate, "options": map[string]any{"fit": in.Fit}})
		})
}
