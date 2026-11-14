//go:build windows

package tray

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pRegisterHotKey           = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	pCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	pAppendMenuW              = user32.NewProc("AppendMenuW")
	pTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	pDestroyMenu              = user32.NewProc("DestroyMenu")
	pSetMenuDefaultItem       = user32.NewProc("SetMenuDefaultItem")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	pCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	pDestroyIcon              = user32.NewProc("DestroyIcon")
	pShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	pGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy     = 0x0002
	wmNull        = 0x0000
	wmHotkey      = 0x0312
	wmApp         = 0x8000
	wmTray        = wmApp + 1
	wmStop        = wmApp + 2
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifInfo   = 0x10
	niifInfo  = 1
	niifWarn  = 2
	nifMsg    = 1
	nifIcon   = 2
	nifTip    = 4

	mfString    = 0x0
	mfChecked   = 0x8
	mfGrayed    = 0x1
	mfSeparator = 0x800

	tpmRightButton = 0x2
	tpmNoNotify    = 0x80
	tpmReturnCmd   = 0x100
)

// Item is one context-menu entry.
type Item struct {
	ID        int
	Label     string
	Checked   bool
	Disabled  bool
	Separator bool
	Default   bool // bold; what a left click does
}

// Options configure the tray. Callbacks run on their own goroutines.
type Options struct {
	ShowIcon bool
	Tip      string
	Icon     image.Image // drawn at 32x32
	Menu     func() []Item
	OnClick  func()
	OnMenu   func(id int)
	Hotkeys  map[int]string // id -> "ctrl+alt+pgdn"
	OnHotkey func(id int)
	// OnHotkeyError reports a hotkey Windows refused (usually because
	// another app already uses it).
	OnHotkeyError func(id int, combo string, err error)
}

// Tray is a running tray icon and hotkey set.
type Tray struct {
	o    Options
	hwnd uintptr
	icon uintptr
	ids  []int
	done chan struct{}
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      [2]int32
	private uint32
}

var (
	classOnce      sync.Once
	classErr       error
	className      = windows.StringToUTF16Ptr("SpanwallTray")
	taskbarCreated uintptr
	mu             sync.Mutex
	cur            *Tray // the one running tray (callbacks need it)
)

func registerClass() error {
	classOnce.Do(func() {
		inst, _, _ := pGetModuleHandleW.Call(0)
		wc := wndClassEx{WndProc: syscall.NewCallback(wndProc), Instance: inst, ClassName: className}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = fmt.Errorf("tray: RegisterClassEx: %w", err)
		}
		taskbarCreated, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("TaskbarCreated"))))
	})
	return classErr
}

// Start creates the hidden window, the icon and the hotkeys on a new
// locked OS thread and returns once they exist.
func Start(o Options) (*Tray, error) {
	mu.Lock()
	if cur != nil {
		mu.Unlock()
		return nil, errors.New("tray: already running")
	}
	t := &Tray{o: o, done: make(chan struct{})}
	cur = t
	mu.Unlock()
	ready := make(chan error, 1)
	go t.run(ready)
	if err := <-ready; err != nil {
		mu.Lock()
		cur = nil
		mu.Unlock()
		return nil, err
	}
	return t, nil
}

// Stop removes the icon and hotkeys and ends the UI thread.
func (t *Tray) Stop() {
	if t == nil {
		return
	}
	pPostMessageW.Call(t.hwnd, wmStop, 0, 0)
	<-t.done
	mu.Lock()
	if cur == t {
		cur = nil
	}
	mu.Unlock()
}

func (t *Tray) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)
	if err := registerClass(); err != nil {
		ready <- err
		return
	}
	inst, _, _ := pGetModuleHandleW.Call(0)
	hwnd, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Spanwall"))),
		0, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("tray: CreateWindowEx: %w", err)
		return
	}
	t.hwnd = hwnd
	for id, combo := range t.o.Hotkeys {
		mods, vk, err := ParseHotkey(combo)
		if err == nil {
			if r, _, e := pRegisterHotKey.Call(hwnd, uintptr(id), uintptr(mods), uintptr(vk)); r == 0 {
				err = fmt.Errorf("Windows refused %s (another app may use it): %v", combo, e)
			} else {
				t.ids = append(t.ids, id)
			}
		}
		if err != nil && t.o.OnHotkeyError != nil {
			go t.o.OnHotkeyError(id, combo, err)
		}
	}
	if t.o.ShowIcon {
		t.icon = makeIcon(t.o.Icon)
		t.addIcon()
	}
	ready <- nil
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	if t.icon != 0 {
		pDestroyIcon.Call(t.icon)
	}
}

func (t *Tray) nid() *notifyIconData {
	n := &notifyIconData{HWnd: t.hwnd, UID: 1, UFlags: nifMsg | nifIcon | nifTip, UCallbackMessage: wmTray, HIcon: t.icon}
	n.CbSize = uint32(unsafe.Sizeof(*n))
	tip, _ := windows.UTF16FromString(t.o.Tip)
	copy(n.SzTip[:len(n.SzTip)-1], tip)
	return n
}

func (t *Tray) addIcon() { pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(t.nid()))) }

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	mu.Lock()
	t := cur
	mu.Unlock()
	if t == nil || t.hwnd != hwnd {
		r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
		return r
	}
	switch {
	case m == wmTray:
		switch lp & 0xffff {
		case wmLButtonUp:
			if t.o.OnClick != nil {
				go t.o.OnClick()
			}
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		}
		return 0
	case m == wmHotkey:
		if t.o.OnHotkey != nil {
			go t.o.OnHotkey(int(wp))
		}
		return 0
	case m == taskbarCreated && taskbarCreated != 0:
		if t.o.ShowIcon {
			t.addIcon() // Explorer restarted
		}
		return 0
	case m == wmStop:
		pDestroyWindow.Call(hwnd)
		return 0
	case m == wmDestroy:
		if t.o.ShowIcon {
			pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(t.nid())))
		}
		for _, id := range t.ids {
			pUnregisterHotKey.Call(hwnd, uintptr(id))
		}
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
	return r
}

func (t *Tray) showMenu() {
	if t.o.Menu == nil {
		return
	}
	hm, _, _ := pCreatePopupMenu.Call()
	if hm == 0 {
		return
	}
	defer pDestroyMenu.Call(hm)
	for _, it := range t.o.Menu() {
		if it.Separator {
			pAppendMenuW.Call(hm, mfSeparator, 0, 0)
			continue
		}
		f := uintptr(mfString)
		if it.Checked {
			f |= mfChecked
		}
		if it.Disabled {
			f |= mfGrayed
		}
		pAppendMenuW.Call(hm, f, uintptr(it.ID), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(it.Label))))
		if it.Default {
			pSetMenuDefaultItem.Call(hm, uintptr(it.ID), 0)
		}
	}
	var pt [2]int32
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// The menu only closes on an outside click if our window is foreground.
	pSetForegroundWindow.Call(t.hwnd)
	id, _, _ := pTrackPopupMenu.Call(hm, tpmRightButton|tpmNoNotify|tpmReturnCmd, uintptr(pt[0]), uintptr(pt[1]), 0, t.hwnd, 0)
	pPostMessageW.Call(t.hwnd, wmNull, 0, 0)
	if id != 0 && t.o.OnMenu != nil {
		go t.o.OnMenu(int(id))
	}
}

// makeIcon builds a 32x32 icon from img through a BMP-format icon resource
// (32-bit BGRA with alpha, then an all-zero AND mask).
func makeIcon(img image.Image) uintptr {
	if img == nil {
		return 0
	}
	const n = 32
	b := img.Bounds()
	data := make([]byte, 40+n*n*4+n*4)
	binary.LittleEndian.PutUint32(data[0:], 40)
	binary.LittleEndian.PutUint32(data[4:], n)
	binary.LittleEndian.PutUint32(data[8:], 2*n) // colour + mask
	binary.LittleEndian.PutUint16(data[12:], 1)
	binary.LittleEndian.PutUint16(data[14:], 32)
	binary.LittleEndian.PutUint32(data[20:], n*n*4+n*4)
	o := 40
	for y := n - 1; y >= 0; y-- { // bottom-up
		for x := 0; x < n; x++ {
			r, g, bl, a := img.At(b.Min.X+x*b.Dx()/n, b.Min.Y+y*b.Dy()/n).RGBA()
			data[o], data[o+1], data[o+2], data[o+3] = byte(bl>>8), byte(g>>8), byte(r>>8), byte(a>>8)
			o += 4
		}
	}
	h, _, _ := pCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, n, n, 0)
	return h
}

// Notify shows a notification from the tray icon (a Windows toast on
// Windows 10 and 11). warn picks the warning icon.
func (t *Tray) Notify(title, msg string, warn bool) {
	if t == nil || !t.o.ShowIcon {
		return
	}
	n := t.nid()
	n.UFlags = nifInfo
	ti, _ := windows.UTF16FromString(title)
	mi, _ := windows.UTF16FromString(msg)
	copy(n.SzInfoTitle[:len(n.SzInfoTitle)-1], ti)
	copy(n.SzInfo[:len(n.SzInfo)-1], mi)
	n.DwInfoFlags = niifInfo
	if warn {
		n.DwInfoFlags = niifWarn
	}
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(n)))
}
