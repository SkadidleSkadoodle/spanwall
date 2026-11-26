# Notes for Claude

Spanwall: Go, Windows only at runtime (pure Go packages also build and test on
Linux).

## Layout

- `cmd/spanwall` CLI entry. Windows commands in `cmd_windows.go`, stubs in `cmd_other.go`.
- `internal/geometry` canvas math in "PPI normalized" pixels (densest display = 1:1).
  Matches Superpaper exactly, including Python's round-half-to-even (`pyRound`).
- `internal/render` one-pass Lanczos3 resample per display via an exact affine map,
  parallel over row bands. Golden-tested against Superpaper (mean diff < 0.5/255).
- `internal/display` EDID parsing (pure) + Windows enumeration via IDesktopWallpaper
  device paths and `HKLM\SYSTEM\CurrentControlSet\Enum\DISPLAY\<pnp>\<instance>\Device Parameters\EDID`.
- `internal/wallpaper` raw-vtable COM wrapper for IDesktopWallpaper (no go-ole).
- `internal/win` per-monitor DPI awareness + COM init (call first in main, locks the OS thread).
- `internal/pattern` test cards drawn with the 7x13 bitmap font.
- `internal/config` config.json (layouts in mm keyed by stable monitor ID, size overrides,
  last applied), plus a parser for Superpaper's display_systems.dat.
- `internal/imageio` decode JPEG/PNG/WebP/BMP, EXIF orientation, letterbox trimming.
- `internal/preview` draws the layout to scale with slices inside (used to check results).
- `internal/app` the core used by CLI, background app and MCP: Session (fresh monitors +
  config per operation), Apply/preview, layouts and presets, identify save/restore, library.
- `internal/server` background app: HTTP API on 127.0.0.1:47920; every Windows call runs on
  one locked COM thread (`Server.session`). Rejects cross-origin browser requests.
- `internal/launch` starts the background app via an on-demand Task Scheduler task
  ("Spanwall", `conhost --headless bin\spanwall-daemon.exe serve`) so it is never a child of
  Claude's MSIX package. `spanwall start|stop`.
- `internal/mcpserver` stdio MCP server (official Go SDK). Thin wrappers over the HTTP API,
  auto-starting the background app. `internal/client` is the HTTP client.
- `build.ps1` vet, test, build bin\spanwall.exe, copy to spanwall-mcp.exe, generate the
  extension manifest (absolute paths, not committed) and pack bin\Spanwall.mcpb.
- `internal/web/static` browser UI (vanilla JS + SVG editor). Served from disk when the exe
  runs from the repo's bin (edit + reload, no rebuild); embedded otherwise.
- `internal/pattern/calibration.go` live calibration pattern in desk mm; written as BMP
  (~0.27 s per update). Server overlays (identify, calibrate) keep the pre-overlay wallpaper.
- `internal/vdesk` virtual desktops via registry + RegNotifyChangeKeyValue watcher.
  Desktop modes same/individual/panorama; per-desktop slices cached in cache\desktops.
- Monitor watcher: polls GetSystemMetrics signature every 2 s; on change picks the saved
  layout matching the attached monitors (ChooseLayout) and re-applies.
- Stale Explorer (`server/explorer_windows.go`, `win/explorer_windows.go`): after a dock
  reconnect Explorer's IDesktopWallpaper table can stay stale (blank slots, old port paths,
  no RECT for live monitors), so it sees fewer monitors than SM_CMONITORS (`Session.Stale`).
  The watcher waits ~15 s for it to catch up, never spans onto the partial set, and restarts
  Explorer itself only if no File Explorer windows are open and not within the last hour;
  otherwise a tray warning points to "Fix monitors" (`POST /api/fix-explorer`, MCP
  `sw_fix_monitors`). The COM thread recreates the wallpaper object on RPC disconnect
  errors (`wallpaper.Disconnected`), so an Explorer restart doesn't break the app.
- Data: `%USERPROFILE%\.spanwall` (config.json, cache\, inbox\ for uploaded photos,
  spanwall.log). Not AppData: see below.
- `internal/vision` photo calibration, pure Go:
  - `dots.go` dot grid (outer dots 1.6 radii from the edges), blob detection (Otsu),
    colour from the background-subtracted glow ring (phone cameras clip dot centres to
    white), largest-cluster filter (drops status LEDs), grid match via extreme corners.
  - `align.go` wallpaper mode: global similarity search, then per screen a scale+shift
    brute-force search (`simSearch`) and LM refinement limited to 7% corner drift and
    25% area change. Low-detail images (fog) otherwise stretch onto wrong spots that
    score higher than the truth.
  - `solve.go` maps every screen into the anchor's (Dell's) plane: similarity for
    position/scale/roll plus the perspective quad (corners in desk mm).
- `app/photo_windows.go` ShowPhotoPattern, AlignPhoto (pattern/wallpaper/auto), saves the
  prior (per-monitor photo homography normalized by photo width) in config `photo`.
  Wallpaper mode re-predicts missed screens from how the matched ones moved.
- Phase 6:
  - `app/slideshow.go` PickSlides (shuffle avoids recent history, name order wraps,
    previous pops history); `server/slideshow_windows.go` 20 s ticker, `Slideshow.Next`
    RFC3339 in config, first step seeds history with the current wallpaper.
  - Span groups: `Config.Groups` (monitor IDs), `Session.SpanGroups` (ungrouped monitors
    get their own image), apply mode "groups", `spanSlices` renders any subset.
  - `internal/wallhaven` API client (SFW only, key as X-API-Key header, downloads only
    from *.wallhaven.cc, verifies size). Key file `.spanwall\wallhaven.key`, never in
    config or API output.
  - `internal/tray` raw Win32 tray (hidden window, TrackPopupMenu, TaskbarCreated re-add,
    BMP-format icon from `tray.Icon()`) and RegisterHotKey on the same UI thread.
    `server/tray_windows.go` wires menu/hotkeys; restarted when tray/hotkey config changes.
  - Autostart: `launch.SetAutostart` HKCU Run "Spanwall" = conhost --headless spanwall.exe start.
- Phase 7:
  - `vision/curve.go` Curve = homography + cubic correction, fitted jointly (alternating) to the
    matched dots; `SolveCurves` stores a 17x9 `Placement.Mesh` (desk mm) per monitor. Rendering
    uses `render.MeshWarp` (any `render.Mapper`), the live pattern `pattern.CalibrationMap`.
    Wallpaper-mode photos only give homographies, so they produce quads, not meshes.
  - Framing per image, or per image and span group (`app.FrameKey`).
  - `internal/install`: `spanwall install` (copies to %LOCALAPPDATA%\Programs\Spanwall, packs
    Spanwall.mcpb in Go, Run-key autostart, starts the app, opens ?welcome=1); double-clicking the
    exe does the same. `spanwall uninstall [--purge]`. `build.ps1` packs via `pack-extension`.
  - Tray notifications (`Tray.Notify`): sync results, hotkey conflicts, slideshow errors;
    repeated warnings are suppressed.
  - `.github/workflows`: ci (vet/test on Linux and Windows), release (on v* tags: zip with exe).
- `geometry.Placement.Quad`: optional TL,TR,BR,BL desk mm. XMM/YMM/Scale are then its
  bounding box. `CarryQuad` keeps it through editor moves/rescales (editor sends the
  carried quad; `resetShape` drops it). `render.SlicesWarp` renders quad monitors with a
  projective bilinear sampler (supersampled up to 3x3); the live pattern uses
  `pattern.CalibrationMap`.

## Rules

- Monitor identity = EDID maker + product + serial (`EDID.StableID`), never Windows numbering.
- Never depend on the Windows desktop arrangement for wallpaper placement. It is shown for information only.
- Anything that changes the user's wallpaper for a test must save and restore it (`saveWallpaper` / `restoreWallpaper`).
- No personal paths, keys, photos or setup notes in the repo. Runtime data lives in `%USERPROFILE%\.spanwall`; test photos go in `local\`, personal notes in CLAUDE.local.md (both ignored).
- Before committing: `go vet ./...`, `go test ./...`, and for Windows code `GOOS=windows go vet ./...`.

## Findings from the phase 1 spike (Nov 2026, Windows 11 25H2, build 26200)

- Processes launched from the Claude desktop app (MSIX packaged) get `%LOCALAPPDATA%`
  writes redirected to `%LOCALAPPDATA%\Packages\Claude_<id>\LocalCache\Local`. Explorer can't
  see those files, so IDesktopWallpaper.SetWallpaper fails with 0x80070002.
  Phase 3 moved all data to `%USERPROFILE%\.spanwall`, which is never redirected, so the
  background app (outside the package) and CLI/MCP (inside it) share one config. Early
  configs are migrated from AppData automatically.
- Per-monitor SetWallpaper works and needs a position other than Span (we use Fill while
  showing slices, then restore the saved position).
- Virtual desktops: `HKCU\...\Explorer\VirtualDesktops\Desktops\{guid}\Wallpaper` holds ONE
  path per desktop (no per-monitor value). Tested on 5 desktops (two runs,
  registry logged every 300 ms while he switched):
  - Desktops with an EMPTY value show the current per-monitor slices, no flash. Confirmed.
  - Visiting a desktop with a SAVED path makes Windows fade to that image on all monitors,
    replacing the slices everywhere; afterwards the empty desktops show that image too and
    Windows may stamp the path into other desktops' values.
  - SetWallpaper per monitor clears the CURRENT desktop's saved value.
  - Plan: "same on all" = keep every desktop's value empty, set slices once (stable, no
    flash). Individual and panorama = keep values empty and re-apply cached slices on each
    `CurrentVirtualDesktop` change (phase 4 must measure what that looks like).
  - Open: does writing "" to those registry values directly take effect, or must each
    desktop be visited? Test in phase 4.
- Device file sync: stage each sync under a fresh path (`/home/claude/bin/stage.sh` in the
  build session); re-committing the same staged path can deliver a stale copy.

Personal setup notes (the owner's desk, monitors, test results) live in CLAUDE.local.md, which is
not committed.
