// Package tray shows Spanwall's notification-area icon and registers its
// global hotkeys, on one Windows UI thread.
package tray

import (
	"fmt"
	"strings"
)

// Modifier flags for RegisterHotKey.
const (
	modAlt      = 0x1
	modControl  = 0x2
	modShift    = 0x4
	modWin      = 0x8
	modNoRepeat = 0x4000
)

var keyNames = map[string]uint32{
	"pgup": 0x21, "pageup": 0x21, "pgdn": 0x22, "pagedown": 0x22, "end": 0x23, "home": 0x24,
	"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28, "space": 0x20, "insert": 0x2D, "ins": 0x2D,
	"delete": 0x2E, "del": 0x2E, "pause": 0x13, "scrolllock": 0x91,
	",": 0xBC, ".": 0xBE, "-": 0xBD, "=": 0xBB, "[": 0xDB, "]": 0xDD, ";": 0xBA, "/": 0xBF,
}

// ParseHotkey turns "ctrl+alt+pgdn" into RegisterHotKey modifiers and a
// virtual-key code. At least one of ctrl, alt or win is required so normal
// typing is never captured.
func ParseHotkey(s string) (mods, vk uint32, err error) {
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(s, " ", "")), "+")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("hotkey %q needs a modifier and a key, like ctrl+alt+pgdn", s)
	}
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl", "control":
			mods |= modControl
		case "alt":
			mods |= modAlt
		case "shift":
			mods |= modShift
		case "win", "super":
			mods |= modWin
		default:
			return 0, 0, fmt.Errorf("hotkey %q: unknown modifier %q", s, p)
		}
	}
	if mods&(modControl|modAlt|modWin) == 0 {
		return 0, 0, fmt.Errorf("hotkey %q needs ctrl, alt or win", s)
	}
	k := parts[len(parts)-1]
	switch {
	case len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= '0' && k[0] <= '9'):
		vk = uint32(strings.ToUpper(k)[0])
	case len(k) >= 2 && k[0] == 'f':
		var n int
		if _, err := fmt.Sscanf(k[1:], "%d", &n); err == nil && n >= 1 && n <= 24 {
			vk = 0x70 + uint32(n-1)
		}
	default:
		vk = keyNames[k]
	}
	if vk == 0 {
		return 0, 0, fmt.Errorf("hotkey %q: unknown key %q", s, k)
	}
	return mods | modNoRepeat, vk, nil
}
