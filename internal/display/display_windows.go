//go:build windows

package display

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
	"github.com/SkadidleSkadoodle/spanwall/internal/wallpaper"
)

// Monitor is one display as Spanwall sees it.
type Monitor struct {
	ID         string  `json:"id"`         // stable, from EDID
	Name       string  `json:"name"`       // EDID name, or the PnP code
	DevicePath string  `json:"devicePath"` // what IDesktopWallpaper wants
	Attached   bool    `json:"attached"`
	ResW       int     `json:"resW"` // native pixels
	ResH       int     `json:"resH"`
	DesktopX   int     `json:"desktopX"` // where Windows put it (informational only)
	DesktopY   int     `json:"desktopY"`
	WidthMM    float64 `json:"widthMM"`
	HeightMM   float64 `json:"heightMM"`
	PPI        float64 `json:"ppi"`
	EDIDOK     bool    `json:"edidOK"`
}

// Geometry returns the fields the span math needs.
func (m Monitor) Geometry() geometry.Display {
	return geometry.Display{ID: m.ID, ResW: m.ResW, ResH: m.ResH, WidthMM: m.WidthMM, HeightMM: m.HeightMM}
}

// List returns every monitor Windows has registered, attached or not.
// The process must be per-monitor DPI aware for sizes to be in real pixels.
func List(dw *wallpaper.DesktopWallpaper) ([]Monitor, error) {
	n, err := dw.MonitorCount()
	if err != nil {
		return nil, err
	}
	var out []Monitor
	for i := 0; i < n; i++ {
		path, err := dw.MonitorPath(i)
		if err != nil {
			return nil, err
		}
		m := Monitor{DevicePath: path}
		pnp, instance := splitDevicePath(path)
		m.Name, m.ID = pnp, pnp+"-"+instance

		if raw, err := readEDID(pnp, instance); err == nil {
			if e, err := ParseEDID(raw); err == nil {
				m.EDIDOK = true
				m.ID = e.StableID()
				if e.Name != "" {
					m.Name = e.Name
				}
				m.WidthMM, m.HeightMM = e.SizeMM()
			}
		}
		if r, err := dw.MonitorRect(path); err == nil {
			m.Attached = true
			m.DesktopX, m.DesktopY = int(r.Left), int(r.Top)
			m.ResW, m.ResH = int(r.Right-r.Left), int(r.Bottom-r.Top)
		}
		if m.ResW > 0 && m.WidthMM > 0 {
			// Keep physical orientation consistent with the resolution
			// (rotated monitors), as Superpaper does.
			if (m.ResW > m.ResH) != (m.WidthMM > m.HeightMM) {
				m.WidthMM, m.HeightMM = m.HeightMM, m.WidthMM
			}
			m.PPI = m.Geometry().PPI()
		}
		out = append(out, m)
	}
	return dedupe(out), nil
}

// dedupe drops stale registrations: Windows keeps one entry per port a
// monitor was ever plugged into, all with the same EDID. Keep the attached
// one, or the first one if none is attached.
func dedupe(ms []Monitor) []Monitor {
	best := map[string]int{}
	for i, m := range ms {
		j, seen := best[m.ID]
		if !seen || (m.Attached && !ms[j].Attached) {
			best[m.ID] = i
		}
	}
	var out []Monitor
	for i, m := range ms {
		if best[m.ID] == i {
			out = append(out, m)
		}
	}
	return out
}

// splitDevicePath turns
// \\?\DISPLAY#DELA1F7#5&2c3f9d0&0&UID4352#{e6f07b5f-...}
// into ("DELA1F7", "5&2c3f9d0&0&UID4352").
func splitDevicePath(p string) (pnp, instance string) {
	p = strings.TrimPrefix(p, `\\?\`)
	parts := strings.Split(p, "#")
	if len(parts) >= 3 {
		return parts[1], parts[2]
	}
	return p, ""
}

func readEDID(pnp, instance string) ([]byte, error) {
	key := fmt.Sprintf(`SYSTEM\CurrentControlSet\Enum\DISPLAY\%s\%s\Device Parameters`, pnp, instance)
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	v, _, err := k.GetBinaryValue("EDID")
	return v, err
}
