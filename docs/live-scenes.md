# Animated wallpaper scenes

Spanwall can show an animated ASCII scene behind the desktop icons instead of a still
wallpaper. It spans every monitor by their physical layout, using the same calibrated shapes
as still wallpapers, so characters are the same physical size everywhere and line up across
bezels. It pauses itself when windows cover the monitors, when the PC is locked and when the
displays are off.

In the editor, pick **Live** in the Layout tab's layout dropdown. The scene's canvas (1.5 times
the size of your monitors) sits behind the monitors like a spanned image: drag it, zoom it,
turn and mirror it, then Save & apply. The side panel shows a live preview, the scene's
details, and snapshots of every scene to switch between. Every scene is also in **Library > Live wallpapers**, with a
snapshot, favourites and bans; choose the **Live wallpapers** album to let the slideshow
shuffle scenes. The tray ("Animated wallpaper") and Claude (`sw_live`) can turn it on too.
Choosing a still wallpaper turns it off.

## Making your own

Scenes are small JavaScript modules in `%USERPROFILE%\.spanwall\scenes\<name>.js`. Edit one
in the editor ("Edit code"), in any editor (the wallpaper reloads when the file changes), or
ask Claude to write one (`sw_live_scene`). The built-in scenes (plasma, matrix, starfield,
clock, donut, weather, system) are good starting points. **Duplicate** (side panel or
Library card) copies a scene under a new name so you can edit it as your own.

```js
export default {
  name: "Title", description: "One line.",
  fps: 30,                        // frame cap (the Live view can override)
  cell: { h: 4.2, aspect: 0.55 }, // character height in mm, width / height
  background: "#05070a",
  params: {                       // optional settings, shown as controls in the Live view
    speed: { label: "Speed", type: "range", min: 0.1, max: 3, step: 0.1, default: 1 },
    color: { label: "Colour", type: "color", default: "#33ff66" },
    trails: { label: "Trails", type: "toggle", default: true },
    style: { label: "Style", type: "select", options: ["a", "b"], default: "a" },
    city: { label: "City", type: "text", default: "" },
  },
  setup(g) {},                    // optional, once per grid (may be async)
  paramsChanged(g) {},            // optional, after the settings change
  draw(g, t, dt) {},              // fill the grid; t = seconds since start
                                  // return false when nothing changed
};
```

`g` is the whole desk as one character grid:

| | |
|---|---|
| `g.cols`, `g.rows`, `g.n` | grid size |
| `g.cw`, `g.ch` | cell size in mm; `g.x0`, `g.y0`, `g.width`, `g.height` the scene's canvas in mm (1.5 times the monitors; its middle is what the monitors show by default) |
| `g.chars`, `g.fg`, `g.bg` | `Uint32Array`s, index `r * cols + c`: code point, `0xRRGGBB`, background (`0` none, else `0xFF000000 \| colour`; set `g.bgUsed = true` if you write it directly) |
| `g.mask` | `1` where the cell is on a monitor (skip the rest to save time) |
| `g.put(c, r, ch, fg, bg)`, `g.text(c, r, str, fg, bg)`, `g.clear(ch, fg, bg)` | drawing |
| `g.ramp(v)` | brightness 0..1 to a character of `" .:-=+*#%@"` (or your own ramp) |
| `g.rgb`, `g.hsl(h, s, l)` (h in turns), `g.mix`, `g.scale` | colours |
| `g.noise(x, y, z)`, `g.hash(x, y)`, `g.rand(seed)` | smooth noise, hashing, a seeded random function |
| `g.mm(c, r)`, `g.cell(x, y)` | cell centre in mm, cell at a point in mm |
| `g.phase(name, rate)` | time scaled by a rate that may change; use `g.phase("T", g.params.speed)` instead of `t * speed` so a speed slider doesn't jump the scene |
| `g.params`, `g.paramsVersion` | the settings (defaults merged with saved values); the version changes when they do |
| `g.location` | `{lat, lon}` from Spanwall's day and night settings, if set |
| `g.monitors`, `g.main` | each monitor's mm box and cell box (`c0, r0, c1, r1`, centre `cc, cr`); `g.main` is the Windows primary monitor |

Tips: precompute palettes into `Uint32Array`s in `setup`, avoid allocating per cell, and
work in mm (`c * g.cw`, `r * g.ch`) so a pattern is the same size on every monitor. Full
blocks (`█ ▀ ▄ ▌ ▐`) and shades (`░ ▒ ▓`) join without gaps. A scene that throws shows its
error on the wallpaper and in the Live view; the code editor shows the line and jumps to it.

Every scene also gets a **colour theme** (phosphor, amber, ice, mono, synthwave, ocean, blood
or two custom colours) and a **character set** (ASCII, blocks, Braille, katakana, binary, dots,
lines), applied on the GPU and by glyph density, so scenes don't implement them.

Data: `fetch("/api/live/stats")` gives `{cpu, memPercent, memUsed, memTotal, uptimeSec, battery}`;
scenes may also fetch public HTTPS APIs (the weather scene uses Open-Meteo). `./_font.js`
exports `big(g, text, c, r, px, py, color)` for 5x7 block lettering.

**Day and night**: the Live view can switch between a day scene and a night scene, using
Spanwall's sunrise and sunset (or Windows light and dark mode) setting.

## From a GIF, video or picture

**Library > Live wallpapers > + From a GIF, video or picture** (or `sw_live_convert`) turns a GIF, picture or video (videos need
`ffmpeg` on the PATH) into ASCII frames and a one-line scene that plays them.

## Working on scenes without Windows

`go run ./tools/livedev -scenes <folder>` serves the page with a made-up three-monitor desk
at http://127.0.0.1:47930/live/?preview=1&scene=name.
