//go:build windows

package app

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SkadidleSkadoodle/spanwall/internal/config"
	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
	"github.com/SkadidleSkadoodle/spanwall/internal/imageio"
	"github.com/SkadidleSkadoodle/spanwall/internal/paths"
	"github.com/SkadidleSkadoodle/spanwall/internal/vision"
	"github.com/SkadidleSkadoodle/spanwall/internal/wallpaper"
)

// ShowPhotoPattern puts the coloured dot grid on every monitor (monitor n
// uses palette colour n), for a phone photo.
func (s *Session) ShowPhotoPattern() error {
	dir, err := paths.CacheDir("calibrate")
	if err != nil {
		return err
	}
	stamp := time.Now().Format("150405.000")
	files := make([]string, len(s.Monitors))
	for i, m := range s.Monitors {
		files[i] = filepath.Join(dir, fmt.Sprintf("dots-%s-%d.bmp", strings.ReplaceAll(stamp, ".", ""), i+1))
		if err := writeBMP(files[i], vision.DrawDots(m.ResW, m.ResH, i)); err != nil {
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
	return nil
}

// MonitorFix is one monitor's photo result.
type MonitorFix struct {
	Number     int     `json:"number"`
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Found      bool    `json:"found"`
	XMM        float64 `json:"xMM"`
	YMM        float64 `json:"yMM"`
	Scale      float64 `json:"scale"`
	RollDeg    float64 `json:"rollDeg"`
	ResidualMM float64 `json:"residualMM"`
	// Perspective shape (corners on the desk, mm). ResidualMM is what a
	// plain rectangle couldn't explain; the shape takes care of it.
	Quad    []float64 `json:"quad,omitempty"`
	Mesh    []float64 `json:"mesh,omitempty"` // curved-screen shape (vision.MeshNX x MeshNY points)
	Quality string    `json:"quality"`        // dots matched, or match score
	MovedMM float64   `json:"movedMM"`        // how far the centre moves vs the current layout
}

// Proposal is a layout worked out from a photo, not yet saved.
type Proposal struct {
	Mode     string       `json:"mode"` // "pattern" or "wallpaper"
	Layout   string       `json:"layout"`
	Anchor   string       `json:"anchor"`
	Monitors []MonitorFix `json:"monitors"`
	Notes    []string     `json:"notes"`
	Overlay  []byte       `json:"overlay"` // JPEG
	Photo    string       `json:"photo"`
	TookMS   int64        `json:"tookMs"`
}

// Placements returns the proposal as layout positions.
func (p *Proposal) Placements() map[string]geometry.Placement {
	out := map[string]geometry.Placement{}
	for _, m := range p.Monitors {
		if m.Found {
			pl := geometry.Placement{XMM: m.XMM, YMM: m.YMM, Scale: m.Scale, Quad: m.Quad}
			if len(m.Mesh) == 2*vision.MeshNX*vision.MeshNY {
				pl.Mesh, pl.MeshNX, pl.MeshNY = m.Mesh, vision.MeshNX, vision.MeshNY
			}
			out[m.ID] = pl
		}
	}
	return out
}

// AlignPhoto works out the layout from a phone photo. mode is "pattern"
// (the dot grid is on screen), "wallpaper" (a real wallpaper is on screen;
// needs an earlier pattern calibration) or "auto" (pattern if dots are
// found, else wallpaper).
func (s *Session) AlignPhoto(path, mode, layout string) (*Proposal, error) {
	start := time.Now()
	layout = s.LayoutName(layout)
	photo, err := imageio.Load(path) // applies the phone's EXIF rotation
	if err != nil {
		return nil, fmt.Errorf("can't read the photo (send it as JPEG or PNG): %w", err)
	}
	pb := photo.Bounds()
	prop := &Proposal{Layout: layout, Photo: path}
	hs := make([]*vision.H, len(s.Monitors))
	curves := make([]*vision.Curve, len(s.Monitors)) // dot photos only
	moved := map[string][9]float64{}                 // re-predicted priors for screens not found
	quality := make([]string, len(s.Monitors))
	var blobs []vision.Blob

	if mode == "" {
		mode = "auto"
	}
	if mode == "pattern" || mode == "auto" {
		blobs = vision.DetectDots(photo)
		found := 0
		for i, m := range s.Monitors {
			var mine []vision.Blob
			for _, b := range blobs {
				if b.Class == i%len(vision.Palette) {
					mine = append(mine, b)
				}
			}
			gm, err := vision.MatchGrid(mine, vision.DotGrid(m.ResW, m.ResH))
			if err != nil {
				quality[i] = err.Error()
				continue
			}
			h := gm.H
			hs[i] = &h
			c := vision.FitCurve(gm.H, gm.Src, gm.Dst, float64(m.ResW), float64(m.ResH))
			curves[i] = &c
			quality[i] = fmt.Sprintf("%d/%d dots, %.1f px fit error (%.1f px flat)", gm.Matched, gm.Expected, vision.CurveRMS(&c, gm.Src, gm.Dst), gm.RMS)

			found++
		}
		if found >= 2 || mode == "pattern" {
			mode = "pattern"
			if found < 2 {
				return nil, fmt.Errorf("found the dot pattern on %d monitor(s); every screen must be fully in the photo (%s)", found, strings.Join(quality, "; "))
			}
		} else {
			mode = "wallpaper"
			hs = make([]*vision.H, len(s.Monitors))
			curves = make([]*vision.Curve, len(s.Monitors))
			blobs = nil
		}
	}
	if mode == "wallpaper" {
		prior := s.Cfg.Photo
		if prior == nil {
			return nil, fmt.Errorf("fixing from a real wallpaper needs one calibration with the dot pattern first")
		}
		// Decode what's on each screen (and the photo) in parallel.
		type loaded struct {
			g   *vision.Gray
			sc  float64
			err error
		}
		shown := make([]loaded, len(s.Monitors))
		var wg sync.WaitGroup
		for i, m := range s.Monitors {
			file, err := s.DW.Wallpaper(m.DevicePath)
			if err != nil || file == "" {
				shown[i].err = fmt.Errorf("can't tell what's on this screen")
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				img, err := imageio.Load(file)
				if err != nil {
					shown[i].err = fmt.Errorf("can't read the on-screen image")
					return
				}
				shown[i].g, shown[i].sc = vision.NewGray(img, 900)
			}()
		}
		pg, back := vision.NewGray(photo, 1600)
		wg.Wait()
		var screens []vision.Screen
		var idx []int
		for i, m := range s.Monitors {
			hp, ok := prior.H[m.ID]
			if !ok {
				quality[i] = "not in the last pattern calibration"
				continue
			}
			if shown[i].err != nil {
				quality[i] = shown[i].err.Error()
				continue
			}
			g, sc := shown[i].g, shown[i].sc
			screens = append(screens, vision.Screen{Image: g, Scale: sc, ResW: m.ResW, ResH: m.ResH,
				Prior: vision.Mul(vision.Scale(float64(pg.W), float64(pg.W)), vision.H(hp))})
			idx = append(idx, i)
		}
		if len(screens) < 2 {
			return nil, fmt.Errorf("need at least two monitors to compare (%s)", strings.Join(quality, "; "))
		}
		res, err := vision.AlignScreens(pg, screens)
		if err != nil {
			return nil, err
		}
		// A photo from a different spot moves the screens relative to the
		// last calibration. Use how the matched screens moved to re-predict
		// the missed ones, then search for those again.
		var src, dst []vision.Pt
		var retry []int
		for k, r := range res {
			if !r.OK {
				retry = append(retry, k)
				continue
			}
			sc := screens[k]
			for _, c := range []vision.Pt{{X: 0, Y: 0}, {X: float64(sc.ResW), Y: 0}, {X: 0, Y: float64(sc.ResH)}, {X: float64(sc.ResW), Y: float64(sc.ResH)}, {X: float64(sc.ResW) / 2, Y: float64(sc.ResH) / 2}} {
				src = append(src, sc.Prior.Apply(c))
				dst = append(dst, r.H.Apply(c))
			}
		}
		if len(retry) > 0 && len(src) >= 10 {
			if t, err := vision.FitHomography(src, dst); err == nil {
				var again []vision.Screen
				for _, k := range retry {
					sc := screens[k]
					sc.Prior = vision.Mul(t, sc.Prior)
					again = append(again, sc)
					// photo-gray px -> photo px / photo width
					moved[s.Monitors[idx[k]].ID] = vision.Mul(vision.Scale(back/float64(pb.Dx()), back/float64(pb.Dx())), sc.Prior).Normalized()
				}
				if res2, err := vision.AlignScreens(pg, again); err == nil {
					for j, k := range retry {
						if res2[j].NCC > res[k].NCC {
							res[k] = res2[j]
						}
					}
				}
			}
		}
		for k, r := range res {
			i := idx[k]
			quality[i] = fmt.Sprintf("match %.2f", r.NCC)
			if !r.OK {
				quality[i] += " (too low: too dark, too plain, or covered by windows)"
				continue
			}
			h := vision.Mul(vision.Scale(back, back), r.H)
			hs[i] = &h
		}
		found := 0
		for _, h := range hs {
			if h != nil {
				found++
			}
		}
		if found < 2 {
			return nil, fmt.Errorf("couldn't match the wallpaper reliably (%s). Show the dot pattern and take the photo again", strings.Join(quality, "; "))
		}
	}
	prop.Mode = mode

	// Anchor: the biggest monitor that was found keeps its current position.
	cur, _ := s.PlacementsFor(layout, nil)
	ds := s.Geometry()
	anchor := -1
	for i, d := range ds {
		if hs[i] != nil && (anchor < 0 || d.WidthMM*d.HeightMM > ds[anchor].WidthMM*ds[anchor].HeightMM+1) {
			anchor = i
		}
	}
	var ax, ay, as float64 = 0, 0, 1
	if cur != nil {
		ax, ay, as = cur[anchor].XMM, cur[anchor].YMM, cur[anchor].S()
	}
	vm := make([]vision.Monitor, len(ds))
	for i, d := range ds {
		vm[i] = vision.Monitor{ID: d.ID, ResW: d.ResW, ResH: d.ResH, WidthMM: d.WidthMM, HeightMM: d.HeightMM}
	}
	sol, err := vision.SolveCurves(vm, hs, curves, anchor, ax, ay)
	if err != nil {
		return nil, err
	}
	prop.Anchor = s.Monitors[anchor].Name
	for i, p := range sol {
		m := s.Monitors[i]
		f := MonitorFix{Number: i + 1, ID: m.ID, Name: m.Name, Found: p.Found, Quality: quality[i]}
		if p.Found {
			// Results are in the anchor's apparent units; keep the anchor's own scale.
			f.XMM = ax + as*(p.XMM-ax)
			f.YMM = ay + as*(p.YMM-ay)
			f.Scale = round4(as * p.Scale)
			f.RollDeg, f.ResidualMM = round4(p.RollDeg), round4(p.ResidualMM*as)
			f.XMM, f.YMM = round1(f.XMM), round1(f.YMM)
			scaleAbout := func(v []float64) []float64 {
				out := make([]float64, len(v))
				for k := 0; k+1 < len(v); k += 2 {
					out[k] = round2(ax + as*(v[k]-ax))
					out[k+1] = round2(ay + as*(v[k+1]-ay))
				}
				return out
			}
			if len(p.Mesh) > 0 {
				f.Mesh = scaleAbout(p.Mesh)
			}
			if len(p.Quad) == 8 {
				f.Quad = scaleAbout(p.Quad)
				// Position and scale become the quad's bounding box, so the
				// editor's rectangle covers the warped slice.
				bx, by, bw, bh := geometry.QuadBox(f.Quad)
				f.XMM, f.YMM = bx, by
				f.Scale = round4(math.Max(bw/ds[i].WidthMM, bh/ds[i].HeightMM))
			}
			if cur != nil {
				c0x, c0y := cur[i].XMM+ds[i].WidthMM*cur[i].S()/2, cur[i].YMM+ds[i].HeightMM*cur[i].S()/2
				c1x, c1y := f.XMM+ds[i].WidthMM*f.Scale/2, f.YMM+ds[i].HeightMM*f.Scale/2
				f.MovedMM = round1(hypot(c1x-c0x, c1y-c0y))
			}
			if abs(p.RollDeg) > 1 {
				prop.Notes = append(prop.Notes, fmt.Sprintf("%s is rotated %.1f° relative to %s; its slice is warped to match", m.Name, p.RollDeg, s.Monitors[anchor].Name))
			}
		} else if cur != nil {
			f.XMM, f.YMM, f.Scale = cur[i].XMM, cur[i].YMM, cur[i].S()
			prop.Notes = append(prop.Notes, m.Name+" wasn't found; its position stays as it is")
		}
		prop.Monitors = append(prop.Monitors, f)
	}
	prop.Notes = append(prop.Notes, fmt.Sprintf("positions are relative to %s, which stays where it is", prop.Anchor))

	// Remember where every monitor was, for the next wallpaper photo.
	pr := &config.PhotoPrior{Width: pb.Dx(), Height: pb.Dy(), H: map[string][9]float64{}, Date: time.Now().Format(time.RFC3339), Mode: mode}
	if s.Cfg.Photo != nil && mode == "wallpaper" {
		for k, v := range s.Cfg.Photo.H {
			pr.H[k] = v
		}
		for k, v := range moved {
			pr.H[k] = v
		}
	}
	for i, h := range hs {
		if h != nil {
			pr.H[s.Monitors[i].ID] = vision.Mul(vision.Scale(1/float64(pb.Dx()), 1/float64(pb.Dx())), *h).Normalized()
		}
	}
	s.Cfg.Photo = pr
	if err := s.Save(); err != nil {
		return nil, err
	}

	// Review picture.
	var outs []vision.Outline
	for i, m := range s.Monitors {
		outs = append(outs, vision.Outline{Label: fmt.Sprintf("%d %s", i+1, m.Name), H: hs[i], W: m.ResW, Hgt: m.ResH, Palette: i})
	}
	ov := vision.Overlay(photo, outs, blobs, 1400)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, ov, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	prop.Overlay = buf.Bytes()
	prop.TookMS = time.Since(start).Milliseconds()
	return prop, nil
}

// SaveInboxPhoto stores an uploaded photo and returns its path.
func SaveInboxPhoto(data []byte, ext string) (string, error) {
	dir := filepath.Join(must(paths.Root()), "inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if ext == "" {
		ext = ".jpg"
	}
	p := filepath.Join(dir, time.Now().Format("20060102-150405")+ext)
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return "", fmt.Errorf("not an image Spanwall can read (JPEG or PNG; iPhone HEIC needs converting): %w", err)
	}
	return p, os.WriteFile(p, data, 0o644)
}

func must(s string, err error) string {
	if err != nil {
		return os.TempDir()
	}
	return s
}

func round2(v float64) float64 { return float64(int64(v*100+sign(v)*0.5)) / 100 }
func round1(v float64) float64 { return float64(int64(v*10+sign(v)*0.5)) / 10 }
func round4(v float64) float64 { return float64(int64(v*10000+sign(v)*0.5)) / 10000 }
func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func hypot(a, b float64) float64 { return math.Hypot(a, b) }
