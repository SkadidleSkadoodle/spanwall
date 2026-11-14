package tray

import "testing"

func TestParseHotkey(t *testing.T) {
	m, k, err := ParseHotkey("Ctrl+Alt+PgDn")
	if err != nil || m != modControl|modAlt|modNoRepeat || k != 0x22 {
		t.Errorf("%x %x %v", m, k, err)
	}
	if _, k, _ := ParseHotkey("win+shift+f12"); k != 0x7B {
		t.Errorf("f12 %x", k)
	}
	for _, bad := range []string{"pgdn", "shift+a", "ctrl+alt+nope", "hyper+a"} {
		if _, _, err := ParseHotkey(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
