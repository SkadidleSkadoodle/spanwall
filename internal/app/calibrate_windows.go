//go:build windows

package app

import (
	"bufio"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/bmp"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
	"github.com/SkadidleSkadoodle/spanwall/internal/paths"
	"github.com/SkadidleSkadoodle/spanwall/internal/pattern"
	"github.com/SkadidleSkadoodle/spanwall/internal/render"
	"github.com/SkadidleSkadoodle/spanwall/internal/vision"
	"github.com/SkadidleSkadoodle/spanwall/internal/wallpaper"
)

// PlacementsFor returns placements for the attached monitors, taking any
// unsaved positions from override (keyed by monitor ID) and the rest from
// the layout.
func (s *Session) PlacementsFor(layout string, override map[string]geometry.Placement) ([]geometry.Placement, error) {
	l := s.Cfg.Layout(s.LayoutName(layout))
	out := make([]geometry.Placement, len(s.Monitors))
	var missing []string
	for i, m := range s.Monitors {
		if p, ok := override[m.ID]; ok {
			out[i] = geometry.CarryQuad(l.Placements[m.ID], p)
		} else if p, ok := l.Placements[m.ID]; ok {
			out[i] = p
		} else {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no position for: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// SaveLayout stores several positions at once (the editor's Save).
func (s *Session) SaveLayout(layout string, ps map[string]geometry.Placement) (LayoutInfo, error) {
	layout = s.LayoutName(layout)
	l := s.Cfg.Layout(layout)
	for id, p := range ps {
		sc := 0.0
		if p.Scale > 0 && math.Abs(p.Scale-1) > 1e-4 {
			sc = math.Round(p.Scale*10000) / 10000
		}
		np := geometry.Placement{XMM: math.Round(p.XMM*10) / 10, YMM: math.Round(p.YMM*10) / 10, Scale: sc,
			Quad: p.Quad, Mesh: p.Mesh, MeshNX: p.MeshNX, MeshNY: p.MeshNY, ResetShape: p.ResetShape}
		np = geometry.CarryQuad(l.Placements[id], np)
		l.Placements[id] = np
	}
	l.Note = "edited " + time.Now().Format("2006-01-02 15:04")
	if err := s.Save(); err != nil {
		return LayoutInfo{}, err
	}
	return s.Layout(layout), nil
}

// ShowCalibration puts the physical test pattern on every monitor, using
// the given (possibly unsaved) positions.
func (s *Session) ShowCalibration(layout string, override map[string]geometry.Placement) error {
	ps, err := s.PlacementsFor(layout, override)
	if err != nil {
		return err
	}
	ds := geometry.Apparent(s.Geometry(), ps)
	// Centre the pattern on the layout's bounding box.
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for i, d := range ds {
		minX, minY = math.Min(minX, ps[i].XMM), math.Min(minY, ps[i].YMM)
		maxX, maxY = math.Max(maxX, ps[i].XMM+d.WidthMM), math.Max(maxY, ps[i].YMM+d.HeightMM)
	}
	cx, cy := (minX+maxX)/2, (minY+maxY)/2

	dir, err := paths.CacheDir("calibrate")
	if err != nil {
		return err
	}
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	files := make([]string, len(ds))
	errs := make([]error, len(ds))
	var wg sync.WaitGroup
	for i, d := range ds {
		// Uncompressed BMP: these files live for seconds, so speed beats size.
		files[i] = filepath.Join(dir, fmt.Sprintf("cal-%s-%d.bmp", stamp, i+1))
		wg.Add(1)
		go func() {
			defer wg.Done()
			var img image.Image
			if toMM := shapeMap(d, ps[i]); toMM != nil {
				img = pattern.CalibrationMap(d.ResW, d.ResH, toMM, math.Max(d.WidthMM/float64(d.ResW), d.HeightMM/float64(d.ResH)), cx, cy)
			} else {
				img = pattern.Calibration(d.ResW, d.ResH, ps[i].XMM, ps[i].YMM, d.WidthMM, d.HeightMM, cx, cy)
			}
			errs[i] = writeBMP(files[i], img)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	if pos, _ := s.DW.Position(); pos == wallpaper.PosSpan {
		if err := s.DW.SetPosition(wallpaper.PosFill); err != nil {
			return err
		}
	}
	for i, m := range s.Monitors {
		if err := s.DW.SetWallpaper(m.DevicePath, files[i]); err != nil {
			return fmt.Errorf("set pattern on %s: %w", m.Name, err)
		}
	}
	// Keep only this round's files.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !strings.Contains(e.Name(), stamp) {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	return nil
}

func writeBMP(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if err := bmp.Encode(w, img); err != nil {
		f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// shapeMap maps a photo-shaped monitor's pixels to desk mm (nil for a
// plain rectangle).
func shapeMap(d geometry.Display, p geometry.Placement) func(x, y float64) (float64, float64) {
	w, h := float64(d.ResW), float64(d.ResH)
	if p.HasMesh() {
		m := &render.MeshWarp{NX: p.MeshNX, NY: p.MeshNY, W: w, H: h, Pts: p.Mesh}
		return m.Map
	}
	if !p.HasQuad() {
		return nil
	}
	var dst []vision.Pt
	for k := 0; k < 8; k += 2 {
		dst = append(dst, vision.Pt{X: p.Quad[k], Y: p.Quad[k+1]})
	}
	hm, err := vision.FitHomography([]vision.Pt{{X: 0, Y: 0}, {X: w, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}, dst)
	if err != nil {
		return nil
	}
	return func(x, y float64) (float64, float64) { q := hm.Apply(vision.Pt{X: x, Y: y}); return q.X, q.Y }
}
