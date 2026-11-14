//go:build windows

// Package vdesk reads Windows 11 virtual desktops from the registry and
// watches for desktop switches.
//
// Tested on Windows 11 25H2: each desktop's own wallpaper is a single path in
// HKCU\...\Explorer\VirtualDesktops\Desktops\{guid}\Wallpaper. A desktop with
// an empty value shows the current (per-monitor) wallpaper; switching to one
// with a saved path makes Windows replace the wallpaper everywhere. Spanwall
// therefore keeps those values empty and applies per-desktop slices itself.
package vdesk

import (
	"encoding/binary"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const keyPath = `Software\Microsoft\Windows\CurrentVersion\Explorer\VirtualDesktops`

// Desktop is one virtual desktop.
type Desktop struct {
	Number    int    `json:"number"` // 1-based, in Task View order
	ID        string `json:"id"`     // {GUID}
	Name      string `json:"name,omitempty"`
	Current   bool   `json:"current"`
	Wallpaper string `json:"windowsWallpaper,omitempty"` // Windows' own saved path, if any
}

func guidString(b []byte) string {
	if len(b) < 16 {
		return ""
	}
	return fmt.Sprintf("{%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X}",
		binary.LittleEndian.Uint32(b[0:4]), binary.LittleEndian.Uint16(b[4:6]), binary.LittleEndian.Uint16(b[6:8]),
		b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
}

// Current returns the current desktop's ID.
func Current() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue("CurrentVirtualDesktop")
	if err != nil {
		return "", err
	}
	return guidString(b), nil
}

// List returns the desktops in order.
func List() ([]Desktop, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	ids, _, err := k.GetBinaryValue("VirtualDesktopIDs")
	if err != nil {
		return nil, err
	}
	cur, _ := Current()
	var out []Desktop
	for i := 0; i+16 <= len(ids); i += 16 {
		id := guidString(ids[i : i+16])
		d := Desktop{Number: i/16 + 1, ID: id, Current: strings.EqualFold(id, cur)}
		if dk, err := registry.OpenKey(registry.CURRENT_USER, keyPath+`\Desktops\`+id, registry.QUERY_VALUE); err == nil {
			d.Name, _, _ = dk.GetStringValue("Name")
			d.Wallpaper, _, _ = dk.GetStringValue("Wallpaper")
			dk.Close()
		}
		out = append(out, d)
	}
	return out, nil
}

// ClearSavedWallpapers empties every desktop's own saved wallpaper so
// Windows never swaps Spanwall's slices out on a desktop switch. Returns how
// many values it changed.
func ClearSavedWallpapers() (int, error) {
	ds, err := List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range ds {
		if d.Wallpaper == "" {
			continue
		}
		dk, err := registry.OpenKey(registry.CURRENT_USER, keyPath+`\Desktops\`+d.ID, registry.SET_VALUE)
		if err != nil {
			continue
		}
		if dk.SetStringValue("Wallpaper", "") == nil {
			n++
		}
		dk.Close()
	}
	return n, nil
}

var (
	advapi32                    = windows.NewLazySystemDLL("advapi32.dll")
	procRegNotifyChangeKeyValue = advapi32.NewProc("RegNotifyChangeKeyValue")
)

// Watch calls fn with the new desktop ID every time the current desktop
// changes. It blocks; run it in its own goroutine.
func Watch(fn func(id string)) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.NOTIFY|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	last, _ := Current()
	for {
		// REG_NOTIFY_CHANGE_LAST_SET, synchronous: returns when any value changes.
		r, _, _ := procRegNotifyChangeKeyValue.Call(uintptr(k), 0, 0x4, 0, 0)
		if r != 0 {
			return fmt.Errorf("RegNotifyChangeKeyValue: %d", r)
		}
		if cur, err := Current(); err == nil && !strings.EqualFold(cur, last) {
			last = cur
			fn(cur)
		}
	}
}
