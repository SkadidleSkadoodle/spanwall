//go:build windows

// Package wallpaper wraps the Windows IDesktopWallpaper COM interface, which
// can set a different image on each monitor.
package wallpaper

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Position values from DESKTOP_WALLPAPER_POSITION.
const (
	PosCenter  = 0
	PosTile    = 1
	PosStretch = 2
	PosFit     = 3
	PosFill    = 4
	PosSpan    = 5
)

var (
	clsidDesktopWallpaper = windows.GUID{Data1: 0xC2CF3110, Data2: 0x460E, Data3: 0x4FC1, Data4: [8]byte{0xB9, 0xD0, 0x8A, 0x1C, 0x0C, 0x9C, 0xC4, 0xBD}}
	iidIDesktopWallpaper  = windows.GUID{Data1: 0xB92B56A9, Data2: 0x8B55, Data3: 0x4E14, Data4: [8]byte{0x9A, 0x89, 0x01, 0x99, 0xBB, 0xB6, 0xF9, 0x3B}}

	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
)

// vtable slots, in the order declared in shobjidl_core.h
const (
	vRelease                   = 2
	vSetWallpaper              = 3
	vGetWallpaper              = 4
	vGetMonitorDevicePathAt    = 5
	vGetMonitorDevicePathCount = 6
	vGetMonitorRECT            = 7
	vSetPosition               = 10
	vGetPosition               = 11
)

// ErrNotAttached is returned for a monitor Windows knows but isn't showing.
var ErrNotAttached = errors.New("wallpaper: monitor not attached")

// DesktopWallpaper is a live IDesktopWallpaper. Use it from one OS thread
// that has called windows.CoInitializeEx.
type DesktopWallpaper struct{ ptr unsafe.Pointer }

// Rect is a monitor rectangle in physical desktop pixels.
type Rect struct{ Left, Top, Right, Bottom int32 }

// New creates the COM object.
func New() (*DesktopWallpaper, error) {
	var p unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidDesktopWallpaper)), 0,
		windows.CLSCTX_LOCAL_SERVER|windows.CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&iidIDesktopWallpaper)),
		uintptr(unsafe.Pointer(&p)),
	)
	if int32(hr) < 0 {
		return nil, fmt.Errorf("wallpaper: CoCreateInstance: HRESULT 0x%08X", uint32(hr))
	}
	return &DesktopWallpaper{ptr: p}, nil
}

// HRESULTError is a failed COM call.
type HRESULTError uint32

func (e HRESULTError) Error() string { return fmt.Sprintf("wallpaper: HRESULT 0x%08X", uint32(e)) }

// Disconnected reports whether err means the wallpaper object's server
// (Explorer) went away, for example after Explorer restarted. The object is
// dead then and must be created again.
func Disconnected(err error) bool {
	var h HRESULTError
	if !errors.As(err, &h) {
		return false
	}
	switch uint32(h) {
	case 0x800706BA, // RPC_S_SERVER_UNAVAILABLE
		0x800706BE, // RPC_S_CALL_FAILED
		0x80010108: // RPC_E_DISCONNECTED
		return true
	}
	return false
}

func (d *DesktopWallpaper) call(slot int, args ...uintptr) (uint32, error) {
	vtbl := *(**[19]uintptr)(d.ptr)
	hr, _, _ := syscall.SyscallN(vtbl[slot], append([]uintptr{uintptr(d.ptr)}, args...)...)
	if int32(hr) < 0 {
		return uint32(hr), HRESULTError(hr)
	}
	return uint32(hr), nil
}

// Close releases the COM object.
func (d *DesktopWallpaper) Close() {
	if d.ptr != nil {
		d.call(vRelease)
		d.ptr = nil
	}
}

func optStr(s string) (uintptr, error) {
	if s == "" {
		return 0, nil
	}
	p, err := windows.UTF16PtrFromString(s)
	return uintptr(unsafe.Pointer(p)), err
}

func takeCoString(p *uint16) string {
	if p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	windows.CoTaskMemFree(unsafe.Pointer(p))
	return s
}

// MonitorCount is the number of monitors Windows has registered.
func (d *DesktopWallpaper) MonitorCount() (int, error) {
	var n uint32
	_, err := d.call(vGetMonitorDevicePathCount, uintptr(unsafe.Pointer(&n)))
	return int(n), err
}

// MonitorPath returns the device path used to address a monitor.
func (d *DesktopWallpaper) MonitorPath(i int) (string, error) {
	var p *uint16
	if _, err := d.call(vGetMonitorDevicePathAt, uintptr(i), uintptr(unsafe.Pointer(&p))); err != nil {
		return "", err
	}
	return takeCoString(p), nil
}

// MonitorRect returns where Windows has the monitor on the desktop.
func (d *DesktopWallpaper) MonitorRect(path string) (Rect, error) {
	var r Rect
	id, err := optStr(path)
	if err != nil {
		return r, err
	}
	hr, err := d.call(vGetMonitorRECT, id, uintptr(unsafe.Pointer(&r)))
	if err != nil {
		return r, ErrNotAttached
	}
	if hr == 1 || r.Right <= r.Left { // S_FALSE
		return r, ErrNotAttached
	}
	return r, nil
}

// Wallpaper returns the image on one monitor ("" path = all monitors; that
// returns S_FALSE and an empty string when monitors differ).
func (d *DesktopWallpaper) Wallpaper(path string) (string, error) {
	id, err := optStr(path)
	if err != nil {
		return "", err
	}
	var p *uint16
	if _, err := d.call(vGetWallpaper, id, uintptr(unsafe.Pointer(&p))); err != nil {
		return "", err
	}
	return takeCoString(p), nil
}

// SetWallpaper sets the image on one monitor ("" path = all monitors).
func (d *DesktopWallpaper) SetWallpaper(path, file string) error {
	id, err := optStr(path)
	if err != nil {
		return err
	}
	f, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return err
	}
	_, err = d.call(vSetWallpaper, id, uintptr(unsafe.Pointer(f)))
	return err
}

// Position returns how wallpapers are fitted.
func (d *DesktopWallpaper) Position() (int, error) {
	var p int32
	_, err := d.call(vGetPosition, uintptr(unsafe.Pointer(&p)))
	return int(p), err
}

// SetPosition sets how wallpapers are fitted. Per-monitor images need any
// position except PosSpan.
func (d *DesktopWallpaper) SetPosition(pos int) error {
	_, err := d.call(vSetPosition, uintptr(pos))
	return err
}
