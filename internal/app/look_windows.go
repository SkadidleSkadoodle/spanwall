//go:build windows

package app

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/martingomezmejia-ctrl/spanwall/internal/paths"
	"github.com/martingomezmejia-ctrl/spanwall/internal/render"
)

var (
	lookMu     sync.Mutex
	lookResult string // what the last accent and lock screen sync did
)

// LookResult reports the last accent and lock screen sync.
func LookResult() string {
	lookMu.Lock()
	defer lookMu.Unlock()
	return lookResult
}

func setLookResult(s string) {
	lookMu.Lock()
	lookResult = time.Now().Format("15:04") + " " + s
	lookMu.Unlock()
}

// syncLook copies the new wallpaper's look to Windows, in the background:
// the accent colour and the lock screen, as the config asks.
func (s *Session) syncLook(slices []*image.RGBA, files []string) {
	opt := s.Cfg.Sync
	if !opt.Accent && !opt.LockScreen {
		return
	}
	// Analyse small copies now (the slices are reused after Apply returns).
	var pix []uint8
	if opt.Accent {
		for _, sl := range slices {
			pix = append(pix, render.Small(sl, 160).Pix...)
		}
	}
	primary := 0
	for i, m := range s.Monitors {
		if m.DesktopX == 0 && m.DesktopY == 0 {
			primary = i
		}
	}
	var lockSrc *image.RGBA
	if opt.LockScreen && primary < len(slices) {
		lockSrc = slices[primary]
	}
	go func() {
		var msgs []string
		if opt.Accent {
			all := &image.RGBA{Pix: pix, Stride: len(pix), Rect: image.Rect(0, 0, len(pix)/4, 1)}
			c := render.AccentColor(all)
			if err := SetAccent(c); err != nil {
				msgs = append(msgs, "accent failed: "+err.Error())
			} else {
				msgs = append(msgs, fmt.Sprintf("accent #%02x%02x%02x", c.R, c.G, c.B))
			}
		}
		if lockSrc != nil {
			if err := setLockScreen(lockSrc); err != nil {
				msgs = append(msgs, "lock screen failed: "+err.Error())
			} else {
				msgs = append(msgs, "lock screen set")
			}
		}
		setLookResult(fmt.Sprint(msgs))
	}()
}

func abgr(c color.RGBA) uint32 { return 0xff<<24 | uint32(c.B)<<16 | uint32(c.G)<<8 | uint32(c.R) }

func shade(c color.RGBA, k float64) color.RGBA {
	f := func(v uint8) uint8 {
		x := float64(v)
		if k >= 0 {
			x += (255 - x) * k
		} else {
			x *= 1 + k
		}
		return uint8(x + 0.5)
	}
	return color.RGBA{f(c.R), f(c.G), f(c.B), 0xff}
}

// SetAccent makes c the Windows accent colour (taskbar, Start, title bars
// and app highlights where accent colours are turned on), the way the
// Settings app stores it, and tells running apps.
func SetAccent(c color.RGBA) error {
	// Lightest to darkest; Windows uses entry 3 as the accent itself.
	pal := []color.RGBA{shade(c, 0.6), shade(c, 0.4), shade(c, 0.2), c, shade(c, -0.2), shade(c, -0.4), shade(c, -0.6), shade(c, -0.75)}
	var palette []byte
	for _, p := range pal {
		palette = append(palette, p.R, p.G, p.B, 0)
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\Accent`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetBinaryValue("AccentPalette", palette); err != nil {
		return err
	}
	k.SetDWordValue("AccentColorMenu", abgr(c))
	k.SetDWordValue("StartColorMenu", abgr(pal[4]))
	if d, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\DWM`, registry.SET_VALUE); err == nil {
		d.SetDWordValue("AccentColor", abgr(c))
		argb := 0xc4<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
		d.SetDWordValue("ColorizationColor", argb)
		d.SetDWordValue("ColorizationAfterglow", argb)
		d.Close()
	}
	// Windows' own "pick an accent from my background" would fight this.
	if d, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\Desktop`, registry.SET_VALUE); err == nil {
		d.SetDWordValue("AutoColorization", 0)
		d.Close()
	}
	broadcastSetting("ImmersiveColorSet")
	return nil
}

var procSendMessageTimeout = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

func broadcastSetting(area string) {
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001a, 0x0002
	p, _ := windows.UTF16PtrFromString(area)
	var res uintptr
	procSendMessageTimeout.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(p)), smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&res)))
}

// lockScript sets the lock screen picture through the Windows Runtime
// LockScreen API (the same one the Settings app uses).
const lockScript = `$ErrorActionPreference='Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$m = [System.WindowsRuntimeSystemExtensions].GetMethods() | ? { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 }
$op = ($m | ? { $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
$act = ($m | ? { $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncAction' })[0]
[Windows.Storage.StorageFile,Windows.Storage,ContentType=WindowsRuntime] | Out-Null
[Windows.System.UserProfile.LockScreen,Windows.System.UserProfile,ContentType=WindowsRuntime] | Out-Null
$t = $op.MakeGenericMethod([Windows.Storage.StorageFile]).Invoke($null, @([Windows.Storage.StorageFile]::GetFileFromPathAsync($env:SPANWALL_LOCK)))
$t.Wait(-1) | Out-Null
$t2 = $act.Invoke($null, @([Windows.System.UserProfile.LockScreen]::SetImageFileAsync($t.Result)))
$t2.Wait(-1) | Out-Null
`

// setLockScreen saves img as a JPEG and makes it the lock screen picture.
func setLockScreen(img *image.RGBA) error {
	dir, err := paths.CacheDir("lockscreen")
	if err != nil {
		return err
	}
	// A new name each time (Windows caches by path); drop the older ones.
	old, _ := filepath.Glob(filepath.Join(dir, "lock-*.jpg"))
	p := filepath.Join(dir, fmt.Sprintf("lock-%d.jpg", time.Now().UnixNano()))
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	err = jpeg.Encode(f, img, &jpeg.Options{Quality: 92})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", lockScript)
	cmd.Env = append(os.Environ(), "SPANWALL_LOCK="+p)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	done := make(chan error, 1)
	var out []byte
	go func() { var e error; out, e = cmd.CombinedOutput(); done <- e }()
	select {
	case err = <-done:
	case <-time.After(60 * time.Second):
		cmd.Process.Kill()
		err = fmt.Errorf("timed out")
	}
	if err != nil {
		os.Remove(p)
		if len(out) > 0 {
			return fmt.Errorf("%v: %s", err, firstLine(string(out)))
		}
		return err
	}
	for _, o := range old {
		os.Remove(o)
	}
	return nil
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' || c == '\r' {
			return s[:i]
		}
	}
	return s
}
