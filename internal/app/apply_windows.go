//go:build windows

package app

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/martingomezmejia-ctrl/spanwall/internal/config"
	"github.com/martingomezmejia-ctrl/spanwall/internal/geometry"
	"github.com/martingomezmejia-ctrl/spanwall/internal/imageio"
	"github.com/martingomezmejia-ctrl/spanwall/internal/paths"
	"github.com/martingomezmejia-ctrl/spanwall/internal/preview"
	"github.com/martingomezmejia-ctrl/spanwall/internal/render"
	"github.com/martingomezmejia-ctrl/spanwall/internal/vision"
	"github.com/martingomezmejia-ctrl/spanwall/internal/wallpaper"
)

// ApplyRequest describes a wallpaper to render and (unless DryRun) set.
type ApplyRequest struct {
	Images []string `json:"images"`           // absolute paths
	Mode   string   `json:"mode,omitempty"`   // "span" (default) or "per-display"
	Layout string   `json:"layout,omitempty"` // default: active layout
	DryRun bool     `json:"dryRun,omitempty"` // render and preview only
	NoTrim bool     `json:"noTrim,omitempty"` // keep black letterbox bars
	// Section picks one vertical strip of the image (panorama desktops).
	Section *Section `json:"section,omitempty"`
}

// ApplyResult reports what happened.
type ApplyResult struct {
	Mode     string   `json:"mode"`
	Layout   string   `json:"layout"`
	Applied  bool     `json:"applied"`
	Slices   []string `json:"slices"`
	Preview  string   `json:"preview,omitempty"`
	RenderMS int64    `json:"renderMs"`
	TotalMS  int64    `json:"totalMs"`
	Notes    []string `json:"notes,omitempty"`
}

// Apply renders the slices, writes a preview, and sets them on the monitors.
func (s *Session) Apply(req ApplyRequest) (*ApplyResult, error) {
	start := time.Now()
	if len(req.Images) == 0 {
		return nil, fmt.Errorf("no image given")
	}
	if req.Mode == "" {
		req.Mode = "span"
	}
	if len(s.Monitors) == 0 {
		return nil, fmt.Errorf("no attached monitors")
	}
	res := &ApplyResult{Mode: req.Mode, Layout: s.LayoutName(req.Layout)}
	ds := s.Geometry()

	slices, ps, notes, err := s.render(req, ds, 1)
	if err != nil {
		return nil, err
	}
	res.Notes = notes
	res.RenderMS = time.Since(start).Milliseconds()

	dir, err := paths.CacheDir("applied")
	if err != nil {
		return nil, err
	}
	stamp := time.Now().Format("20060102-150405.000")
	stamp = strings.ReplaceAll(stamp, ".", "")
	prefix := "slice"
	if req.DryRun {
		prefix = "dry"
	}
	// A new name every time: Windows caches wallpapers by path.
	res.Slices = make([]string, len(slices))
	errs := make([]error, len(slices))
	var wg sync.WaitGroup
	for i, sl := range slices {
		res.Slices[i] = filepath.Join(dir, fmt.Sprintf("%s-%s-%d.png", prefix, stamp, i+1))
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = WritePNG(res.Slices[i], sl)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	if ps != nil {
		imgs := make([]image.Image, len(slices))
		for i := range slices {
			imgs[i] = slices[i]
		}
		res.Preview = filepath.Join(dir, "preview.png")
		if err := WritePNG(res.Preview, preview.Render(geometry.Apparent(ds, ps), ps, imgs, 1600)); err != nil {
			return nil, err
		}
	}
	if req.DryRun {
		res.TotalMS = time.Since(start).Milliseconds()
		return res, nil
	}

	if err := setJPEGQuality100(); err != nil {
		res.Notes = append(res.Notes, "could not set JPEGImportQuality: "+err.Error())
	}
	if err := s.DW.SetPosition(wallpaper.PosFill); err != nil {
		return nil, err
	}
	for i, m := range s.Monitors {
		if err := s.DW.SetWallpaper(m.DevicePath, res.Slices[i]); err != nil {
			return nil, fmt.Errorf("set wallpaper on %s: %w", m.Name, err)
		}
	}
	res.Applied = true
	cleanOld(dir, stamp)
	s.Cfg.LastApplied = &config.Applied{Mode: req.Mode, Images: req.Images, Layout: res.Layout}
	s.rememberProfile(res.Layout, req.Mode, req.Images)
	s.syncLook(slices, res.Slices)
	if err := s.Save(); err != nil {
		return nil, err
	}
	res.TotalMS = time.Since(start).Milliseconds()
	return res, nil
}

// render makes one slice per attached monitor. It also returns the layout
// placements (nil if the layout is incomplete in per-display mode) and notes
// about trimming and resolution.
func (s *Session) render(req ApplyRequest, ds []geometry.Display, scale float64) ([]*image.RGBA, []geometry.Placement, []string, error) {
	var notes []string
	full := ds
	if scale != 1 {
		// Same physical sizes, fewer pixels: PPI ratios (and so the layout
		// math) are unchanged, only the output is smaller.
		ds = make([]geometry.Display, len(full))
		for i, d := range full {
			d.ResW = max(1, int(float64(d.ResW)*scale))
			d.ResH = max(1, int(float64(d.ResH)*scale))
			ds[i] = d
		}
	}
	load := func(path string) (image.Image, error) {
		src, err := imageio.Load(path)
		if err != nil {
			return nil, err
		}
		if !req.NoTrim {
			before := src.Bounds()
			if src = imageio.TrimBars(src); src.Bounds() != before {
				notes = append(notes, fmt.Sprintf("trimmed black bars from %s: %dx%d -> %dx%d",
					filepath.Base(path), before.Dx(), before.Dy(), src.Bounds().Dx(), src.Bounds().Dy()))
			}
		}
		if sec := req.Section; sec != nil && sec.Count > 1 {
			b := src.Bounds()
			x0 := b.Min.X + b.Dx()*sec.Index/sec.Count
			x1 := b.Min.X + b.Dx()*(sec.Index+1)/sec.Count
			if sub, ok := src.(interface {
				SubImage(image.Rectangle) image.Image
			}); ok {
				src = sub.SubImage(image.Rect(x0, b.Min.Y, x1, b.Max.Y))
				notes = append(notes, fmt.Sprintf("panorama strip %d of %d", sec.Index+1, sec.Count))
			}
		}
		return src, nil
	}

	switch req.Mode {
	case "span":
		if len(req.Images) != 1 {
			return nil, nil, nil, fmt.Errorf("span mode takes exactly one image")
		}
		ps, err := s.Placements(req.Layout)
		if err != nil {
			return nil, nil, nil, err
		}
		src, err := load(req.Images[0])
		if err != nil {
			return nil, nil, nil, err
		}
		fr, saved := s.SavedFrame(req.Images[0], nil)
		slices, n, err := s.spanSlices(src, ds, full, ps, nil, fr, !saved && s.Cfg.SmartFrameOn())
		if err != nil {
			return nil, nil, nil, err
		}
		return slices, ps, append(notes, n...), nil

	case "groups":
		ps, err := s.Placements(req.Layout)
		if err != nil {
			return nil, nil, nil, err
		}
		groups := s.SpanGroups()
		if len(req.Images) != len(groups) && len(req.Images) != 1 {
			return nil, nil, nil, fmt.Errorf("span groups take one image per group (%d), or one for all", len(groups))
		}
		slices := make([]*image.RGBA, len(ds))
		cache := map[string]image.Image{}
		for g, idx := range groups {
			path := req.Images[min(g, len(req.Images)-1)]
			src := cache[path]
			if src == nil {
				if src, err = load(path); err != nil {
					return nil, nil, nil, err
				}
				cache[path] = src
			}
			fr, saved := s.SavedFrame(path, s.GroupIDs(idx))
			auto := !saved && s.Cfg.SmartFrameOn()
			if len(idx) == 1 {
				d := ds[idx[0]]
				if auto {
					fr = render.AutoFrame(src, d.ResW, d.ResH, []geometry.Rect{{R: d.ResW, B: d.ResH}})
				}
				one, err := render.SlicesFramed(src, []geometry.Display{d}, []geometry.Rect{{R: d.ResW, B: d.ResH}}, nil, fr)
				if err != nil {
					return nil, nil, nil, err
				}
				slices[idx[0]] = one[0]
				continue
			}
			sub, n, err := s.spanSlices(src, ds, full, ps, idx, fr, auto)
			if err != nil {
				return nil, nil, nil, err
			}
			notes = append(notes, n...)
			for k, i := range idx {
				slices[i] = sub[k]
			}
		}
		return slices, ps, notes, nil

	case "per-display":
		if len(req.Images) != len(s.Monitors) && len(req.Images) != 1 {
			return nil, nil, nil, fmt.Errorf("per-display mode takes one image, or one per monitor (%d)", len(s.Monitors))
		}
		ps, _ := s.Placements(req.Layout) // only for the preview
		cache := map[string]image.Image{}
		var slices []*image.RGBA
		for i, d := range ds {
			path := req.Images[min(i, len(req.Images)-1)]
			src := cache[path]
			if src == nil {
				var err error
				if src, err = load(path); err != nil {
					return nil, nil, nil, err
				}
				cache[path] = src
			}
			one, err := render.Slices(src, []geometry.Display{d}, []geometry.Rect{{R: d.ResW, B: d.ResH}})
			if err != nil {
				return nil, nil, nil, err
			}
			slices = append(slices, one[0])
		}
		return slices, ps, notes, nil
	}
	return nil, nil, nil, fmt.Errorf("unknown mode %q (span, groups, per-display)", req.Mode)
}

// spanGeometry picks the monitors in idx (all when nil) and lays them out:
// their displays (apparent sizes), placements and canvas crops.
func spanGeometry(ds []geometry.Display, ps []geometry.Placement, idx []int) ([]int, []geometry.Display, []geometry.Placement, []geometry.Rect, error) {
	if idx == nil {
		idx = make([]int, len(ds))
		for i := range idx {
			idx[i] = i
		}
	}
	sd, sp := make([]geometry.Display, len(idx)), make([]geometry.Placement, len(idx))
	for k, i := range idx {
		sd[k], sp[k] = ds[i], ps[i]
	}
	sd = geometry.Apparent(sd, sp)
	offs, err := geometry.OffsetsFromPlacements(sd, sp)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	crops, err := geometry.Crops(sd, offs, nil)
	return idx, sd, sp, crops, err
}

// spanSlices spans src across the monitors in idx (all when idx is nil),
// using their layout positions, and returns their slices in idx order.
// With auto, fr is replaced by an automatic framing (render.AutoFrame).
func (s *Session) spanSlices(src image.Image, ds, full []geometry.Display, ps []geometry.Placement, idx []int, fr render.Frame, auto bool) ([]*image.RGBA, []string, error) {
	idx, sd, sp, crops, err := spanGeometry(ds, ps, idx)
	if err != nil {
		return nil, nil, err
	}
	sf := make([]geometry.Display, len(idx))
	for k, i := range idx {
		sf[k] = full[i]
	}
	sf = geometry.Apparent(sf, sp)
	if auto {
		cw, ch := geometry.Canvas(crops)
		fr = render.AutoFrame(src, cw, ch, crops)
	}
	slices, err := render.SlicesFramed(src, sd, crops, warpsFor(sd, sp), fr)
	if err != nil {
		return nil, nil, err
	}
	var notes []string
	cw, ch := geometry.Canvas(crops)
	b := src.Bounds()
	f, _ := render.FrameFill(b.Dx(), b.Dy(), cw, ch, fr)
	srcPPI := geometry.MaxPPI(sd) / f.SX // scale cancels: both PPI and canvas shrink with it
	notes = append(notes, fmt.Sprintf("source %dx%d covers the layout at %.0f PPI", b.Dx(), b.Dy(), srcPPI))
	if auto && !fr.Identity() {
		notes = append(notes, fmt.Sprintf("smart framing: zoom %.2f, moved %.0f%% across and %.0f%% down", max(fr.Zoom, 1), fr.DX*100, fr.DY*100))
	}
	for k, d := range sf {
		if d.PPI() > srcPPI*1.05 {
			notes = append(notes, fmt.Sprintf("%s is %.0f PPI, so its slice is upscaled %.1fx", s.Monitors[idx[k]].Name, d.PPI(), d.PPI()/srcPPI))
		}
	}
	return slices, notes, nil
}

// SpanGroups returns the monitors (indices) spanned together. Monitors in
// no configured group each form their own group, in monitor order. With no
// groups configured, everything is one group.
func (s *Session) SpanGroups() [][]int {
	if len(s.Cfg.Groups) == 0 {
		all := make([]int, len(s.Monitors))
		for i := range all {
			all[i] = i
		}
		return [][]int{all}
	}
	used := make([]bool, len(s.Monitors))
	var out [][]int
	for _, g := range s.Cfg.Groups {
		var idx []int
		for _, id := range g {
			for i, m := range s.Monitors {
				if m.ID == id && !used[i] {
					idx = append(idx, i)
					used[i] = true
				}
			}
		}
		if len(idx) > 0 {
			out = append(out, idx)
		}
	}
	for i := range s.Monitors {
		if !used[i] {
			out = append(out, []int{i})
		}
	}
	return out
}

// Preview renders at reduced resolution, entirely in memory, and returns a
// picture of the monitors at their physical positions. Nothing is written
// or applied. widthPx is the picture's width.
func (s *Session) Preview(req ApplyRequest, widthPx int) (image.Image, []string, error) {
	if len(req.Images) == 0 {
		img, err := s.LayoutPreview(req.Layout)
		return img, []string{"layout outlines only (no image given)"}, err
	}
	if req.Mode == "" {
		req.Mode = "span"
	}
	ds := s.Geometry()
	slices, ps, notes, err := s.render(req, ds, 0.25)
	if err != nil {
		return nil, nil, err
	}
	if ps == nil {
		return nil, nil, fmt.Errorf("no preview: layout %q is missing monitors", s.LayoutName(req.Layout))
	}
	imgs := make([]image.Image, len(slices))
	for i := range slices {
		imgs[i] = slices[i]
	}
	return preview.Render(geometry.Apparent(ds, ps), ps, imgs, widthPx), notes, nil
}

// LayoutPreview draws the layout without an image: just the monitor outlines.
func (s *Session) LayoutPreview(layout string) (image.Image, error) {
	ps, err := s.Placements(layout)
	if err != nil {
		return nil, err
	}
	return preview.Render(geometry.Apparent(s.Geometry(), ps), ps, nil, 1600), nil
}

func setJPEGQuality100() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Control Panel\Desktop`, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("JPEGImportQuality"); err == nil && v == 100 {
		return nil
	}
	return k.SetDWordValue("JPEGImportQuality", 100)
}

// cleanOld removes slices from earlier applies and dry runs, keeping the
// current apply.
func cleanOld(dir, keepStamp string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".png") && n != "preview.png" && !strings.Contains(n, "-"+keepStamp+"-") {
			os.Remove(filepath.Join(dir, n))
		}
	}
}

// warpsFor maps each photo-shaped monitor's slice pixels to canvas pixels
// (nil for plain rectangles), using the same mm -> canvas scale as
// geometry.OffsetsFromPlacements. A mesh (curved screens) wins over a quad.
func warpsFor(ds []geometry.Display, ps []geometry.Placement) []render.Mapper {
	ppmm := geometry.MaxPPI(ds) / 25.4
	minX, minY := math.Inf(1), math.Inf(1)
	for _, p := range ps {
		minX, minY = math.Min(minX, p.XMM), math.Min(minY, p.YMM)
	}
	out := make([]render.Mapper, len(ds))
	for i, p := range ps {
		w, h := float64(ds[i].ResW), float64(ds[i].ResH)
		switch {
		case p.HasMesh():
			m := &render.MeshWarp{NX: p.MeshNX, NY: p.MeshNY, W: w, H: h, Pts: make([]float64, len(p.Mesh))}
			for k := 0; k < len(p.Mesh); k += 2 {
				m.Pts[k], m.Pts[k+1] = (p.Mesh[k]-minX)*ppmm, (p.Mesh[k+1]-minY)*ppmm
			}
			out[i] = m
		case p.HasQuad():
			var dst []vision.Pt
			for k := 0; k < 8; k += 2 {
				dst = append(dst, vision.Pt{X: (p.Quad[k] - minX) * ppmm, Y: (p.Quad[k+1] - minY) * ppmm})
			}
			hm, err := vision.FitHomography([]vision.Pt{{X: 0, Y: 0}, {X: w, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}, dst)
			if err != nil {
				continue
			}
			wp := render.Warp(hm)
			out[i] = &wp
		}
	}
	return out
}

// FrameKey names an image's framing: the file name, plus the monitor IDs
// of a span group (framing is relative to what the image spans).
func FrameKey(path string, groupIDs []string) string {
	k := strings.ToLower(filepath.Base(path))
	if len(groupIDs) > 0 {
		k += "|" + strings.Join(groupIDs, ",")
	}
	return k
}

// GroupIDs lists the monitor IDs of the monitors at idx.
func (s *Session) GroupIDs(idx []int) []string {
	ids := make([]string, len(idx))
	for k, i := range idx {
		ids[k] = s.Monitors[i].ID
	}
	return ids
}

// SavedFrame is the saved framing of an image (spanned across all
// monitors, or in the span group groupIDs) and whether there is one.
func (s *Session) SavedFrame(path string, groupIDs []string) (render.Frame, bool) {
	f, ok := s.Cfg.Framing[FrameKey(path, groupIDs)]
	if !ok {
		return render.Frame{}, false
	}
	return render.Frame{Zoom: f.Zoom, DX: f.DX, DY: f.DY, FlipX: f.FlipX, FlipY: f.FlipY, Rotate: f.Rotate}, true
}

// FrameState describes how an image is framed, for cache keys: the saved
// frame, or "auto" when smart framing decides.
func (s *Session) FrameState(path string, groupIDs []string) string {
	if fr, ok := s.SavedFrame(path, groupIDs); ok {
		return fmt.Sprint(fr)
	}
	if s.Cfg.SmartFrameOn() {
		return "auto"
	}
	return "fill"
}

// AutoFrameFor works out the smart framing of an image spanned across all
// monitors, or across the span group groupIDs, exactly as applying it
// would.
func (s *Session) AutoFrameFor(path string, groupIDs []string) (render.Frame, error) {
	src, err := imageio.Load(path)
	if err != nil {
		return render.Frame{}, err
	}
	src = imageio.TrimBars(src)
	ds := s.Geometry()
	var idx []int
	for _, id := range groupIDs {
		for i, m := range s.Monitors {
			if m.ID == id {
				idx = append(idx, i)
			}
		}
	}
	if len(idx) == 1 {
		d := ds[idx[0]]
		return render.AutoFrame(src, d.ResW, d.ResH, []geometry.Rect{{R: d.ResW, B: d.ResH}}), nil
	}
	ps, err := s.Placements("")
	if err != nil {
		return render.Frame{}, err
	}
	_, _, _, crops, err := spanGeometry(ds, ps, idx)
	if err != nil {
		return render.Frame{}, err
	}
	cw, ch := geometry.Canvas(crops)
	return render.AutoFrame(src, cw, ch, crops), nil
}

// ClearFrame forgets an image's saved framing, so smart framing (or the
// plain fill, with smart framing off) decides again.
func (s *Session) ClearFrame(path string, groupIDs []string) error {
	delete(s.Cfg.Framing, FrameKey(path, groupIDs))
	return s.Save()
}

// SetFrame saves an image's framing, for a span group when groupIDs is
// set. An identity frame is saved too: it means "plain fill, no smart
// framing" for this image.
func (s *Session) SetFrame(path string, groupIDs []string, fr render.Frame) error {
	k := FrameKey(path, groupIDs)
	{
		if fr.Zoom == 0 {
			fr.Zoom = 1
		}
		if fr.Zoom < 0.25 || fr.Zoom > 10 || math.Abs(fr.DX) > 5 || math.Abs(fr.DY) > 5 {
			return fmt.Errorf("framing out of range (zoom 0.25 to 10)")
		}
		if s.Cfg.Framing == nil {
			s.Cfg.Framing = map[string]config.Frame{}
		}
		// Keep the angle in (-180, 180].
		fr.Rotate = math.Mod(fr.Rotate, 360)
		if fr.Rotate > 180 {
			fr.Rotate -= 360
		} else if fr.Rotate <= -180 {
			fr.Rotate += 360
		}
		s.Cfg.Framing[k] = config.Frame{Zoom: fr.Zoom, DX: fr.DX, DY: fr.DY, FlipX: fr.FlipX, FlipY: fr.FlipY, Rotate: fr.Rotate}
	}
	return s.Save()
}
