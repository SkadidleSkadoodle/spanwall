//go:build windows

package live

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/SkadidleSkadoodle/spanwall/internal/win"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// The host is a separate process (spanwall live-host) so that the browser
// engine can never take the background app down with it, and so that turning
// the animation off frees everything. It puts one window behind the desktop
// icons, shows the /live page in it, and pauses it whenever nobody can see it.
// Commands arrive on stdin, one per line: reload, hide, show, quit.

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	dwmapi  = windows.NewLazySystemDLL("dwmapi.dll")
	wtsapi  = windows.NewLazySystemDLL("wtsapi32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")

	pFindWindowW               = user32.NewProc("FindWindowW")
	pFindWindowExW             = user32.NewProc("FindWindowExW")
	pSendMessageTimeoutW       = user32.NewProc("SendMessageTimeoutW")
	pGetWindowLongPtrW         = user32.NewProc("GetWindowLongPtrW")
	pGetWindowRect             = user32.NewProc("GetWindowRect")
	pEnumWindows               = user32.NewProc("EnumWindows")
	pEnumDisplayMonitors       = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW           = user32.NewProc("GetMonitorInfoW")
	pRegisterClassExW          = user32.NewProc("RegisterClassExW")
	pCreateWindowExW           = user32.NewProc("CreateWindowExW")
	pDefWindowProcW            = user32.NewProc("DefWindowProcW")
	pSetLayeredWindowAttribute = user32.NewProc("SetLayeredWindowAttributes")
	pGetLayeredWindowAttribute = user32.NewProc("GetLayeredWindowAttributes")
	pSetWindowPos              = user32.NewProc("SetWindowPos")
	pGetWindow                 = user32.NewProc("GetWindow")
	pGetParent                 = user32.NewProc("GetParent")
	pIsWindow                  = user32.NewProc("IsWindow")
	pIsWindowVisible           = user32.NewProc("IsWindowVisible")
	pIsIconic                  = user32.NewProc("IsIconic")
	pShowWindow                = user32.NewProc("ShowWindow")
	pGetClassNameW             = user32.NewProc("GetClassNameW")
	pGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	pGetMessageW               = user32.NewProc("GetMessageW")
	pTranslateMessage          = user32.NewProc("TranslateMessage")
	pDispatchMessageW          = user32.NewProc("DispatchMessageW")
	pPostMessageW              = user32.NewProc("PostMessageW")
	pPostQuitMessage           = user32.NewProc("PostQuitMessage")
	pSetTimer                  = user32.NewProc("SetTimer")
	pRegisterPowerSettingNotif = user32.NewProc("RegisterPowerSettingNotification")
	pSHQueryUserNotification   = shell32.NewProc("SHQueryUserNotificationState")
	pDwmGetWindowAttribute     = dwmapi.NewProc("DwmGetWindowAttribute")
	pWTSRegisterSessionNotif   = wtsapi.NewProc("WTSRegisterSessionNotification")
	pGetStockObject            = gdi32.NewProc("GetStockObject")
)

const (
	wsChild, wsVisible, wsClipSiblings, wsClipChildren, wsPopup  = 0x40000000, 0x10000000, 0x04000000, 0x02000000, 0x80000000
	wsExLayered, wsExToolWindow, wsExNoActivate, wsExTransparent = 0x00080000, 0x00000080, 0x08000000, 0x00000020
	wsExNoRedirectionBitmap                                      = 0x00200000
	gwlExStyle                                                   = ^uintptr(19) // -20
	swpNoSize, swpNoMove, swpNoActivate                          = 0x1, 0x2, 0x10
	gwHwndNext, gwHwndPrev                                       = 2, 3
	wmApp, wmTimer, wmPowerBroadcast, wmWtsSessionChange         = 0x8000, 0x0113, 0x0218, 0x02B1
	swHide, swShowNoActivate                                     = 0, 4
	hwndBottom                                                   = 1
)

type rect struct{ L, T, R, B int32 }

func (r rect) w() int32 { return r.R - r.L }
func (r rect) h() int32 { return r.B - r.T }

func str(p string) uintptr { s, _ := windows.UTF16PtrFromString(p); return uintptr(unsafe.Pointer(s)) }

func className(h uintptr) string {
	b := make([]uint16, 128)
	pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&b[0])), 128)
	return windows.UTF16ToString(b)
}

var pGetWindowTextW = user32.NewProc("GetWindowTextW")

func windowText(h uintptr) string {
	b := make([]uint16, 80)
	pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&b[0])), 80)
	return windows.UTF16ToString(b)
}

func winRect(h uintptr) rect {
	var r rect
	pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r
}

type host struct {
	url                    string
	progman, parent, after uintptr // where the window lives and what it sits under
	raised                 bool
	wnd, msgWnd            uintptr
	view                   *edge.Chromium
	ready, shown           bool
	area                   rect // parent rect the window was sized for

	hiddenByServer, locked, displayOff bool
	visible                            bool
	lastCovered                        string
	allCovered                         bool
	cmds                               chan string
}

// RunHost runs the wallpaper window until told to quit, stdin closes, or the
// desktop goes away (exit code 3: the caller should start a new host).
func RunHost(url string) int {
	log.SetFlags(0)
	log.SetPrefix("live-host: ")
	done, err := win.Init()
	if err != nil && err != syscall.Errno(1) { // S_FALSE: already initialised
		log.Printf("COM: %v", err)
		return 1
	}
	defer done()
	os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "FF000000")
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", strings.Join([]string{
		"--disable-features=CalculateNativeWinOcclusion,msSmartScreenProtection,Translate,OptimizationHints",
		"--disable-renderer-backgrounding", "--disable-backgrounding-occluded-windows", "--disable-background-timer-throttling",
		"--disable-extensions", "--no-first-run", "--disable-sync", "--disable-background-networking", "--renderer-process-limit=1",
	}, " "))

	h := &host{url: url, cmds: make(chan string, 16), visible: true}
	if err := h.findDesktop(); err != nil {
		log.Print(err)
		return 3
	}
	if err := h.createWindows(); err != nil {
		log.Print(err)
		return 3
	}
	dataDir := filepath.Join(os.Getenv("USERPROFILE"), ".spanwall", "webview")
	h.view = edge.NewChromium()
	h.view.DataPath = dataDir
	h.view.MessageCallback = func(m string) {
		if m == "ready" && !h.shown {
			h.shown = true
			pShowWindow.Call(h.wnd, swShowNoActivate)
			h.fixZ()
			log.Printf("showing (%s)", h.mode())
		}
	}
	if !h.view.Embed(h.wnd) {
		log.Print("WebView2 couldn't start (is the WebView2 runtime installed?)")
		return 1
	}
	h.view.Resize()
	h.ready = true
	h.view.Navigate(url)

	go func() { // stdin commands -> UI thread
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			h.cmds <- strings.TrimSpace(sc.Text())
			pPostMessageW.Call(h.msgWnd, wmApp, 0, 0)
		}
		h.cmds <- "quit"
		pPostMessageW.Call(h.msgWnd, wmApp, 0, 0)
	}()
	pSetTimer.Call(h.msgWnd, 1, 1000, 0)

	code := 0
	var msg [48]byte
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
		if exitCode >= 0 {
			code = exitCode
			break
		}
	}
	return code
}

var (
	exitCode = -1
	theHost  *host
)

func (h *host) mode() string {
	if h.raised {
		return "raised desktop, layered child of Progman under the icons"
	}
	return "legacy desktop, child of the WorkerW behind the icons"
}

// findDesktop locates where a wallpaper window goes. On Windows 11 24H2 and
// later ("raised desktop": Progman has WS_EX_NOREDIRECTIONBITMAP) the icons
// (SHELLDLL_DefView) and the shell's wallpaper WorkerW are both children of
// Progman, and our window must be a layered child of Progman between them.
// Older builds use the top-level WorkerW behind the icons.
func (h *host) findDesktop() error {
	pm, _, _ := pFindWindowW.Call(str("Progman"), 0)
	if pm == 0 {
		return fmt.Errorf("no Progman window (Explorer not running?)")
	}
	var res uintptr
	pSendMessageTimeoutW.Call(pm, 0x052C, 0xD, 0x1, 0, 1000, uintptr(unsafe.Pointer(&res)))
	time.Sleep(200 * time.Millisecond)
	h.progman = pm
	ex, _, _ := pGetWindowLongPtrW.Call(pm, gwlExStyle)
	defView, _, _ := pFindWindowExW.Call(pm, 0, str("SHELLDLL_DefView"), 0)
	if ex&wsExNoRedirectionBitmap != 0 && defView != 0 {
		h.raised, h.parent, h.after = true, pm, defView
		return nil
	}
	var worker uintptr
	cb := syscall.NewCallback(func(w, _ uintptr) uintptr {
		if d, _, _ := pFindWindowExW.Call(w, 0, str("SHELLDLL_DefView"), 0); d != 0 {
			worker, _, _ = pFindWindowExW.Call(0, w, str("WorkerW"), 0)
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	if worker == 0 {
		return fmt.Errorf("no desktop layer found")
	}
	h.raised, h.parent, h.after = false, worker, hwndBottom
	return nil
}

func (h *host) createWindows() error {
	theHost = h
	proc := syscall.NewCallback(wndProc)
	black, _, _ := pGetStockObject.Call(4) // BLACK_BRUSH
	type wndClass struct {
		Size, Style                uint32
		WndProc                    uintptr
		ClsExtra, WndExtra         int32
		Instance, Icon, Cursor, Bg uintptr
		MenuName, ClassName        *uint16
		IconSm                     uintptr
	}
	for _, name := range []string{"SpanwallLive", "SpanwallLiveMsg"} {
		cn, _ := windows.UTF16PtrFromString(name)
		wc := wndClass{WndProc: proc, ClassName: cn, Bg: black}
		wc.Size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	}
	h.area = winRect(h.parent)
	ex := uintptr(wsExToolWindow | wsExNoActivate)
	if h.raised {
		ex |= wsExLayered
	}
	w, _, e := pCreateWindowExW.Call(ex, str("SpanwallLive"), str("Spanwall live wallpaper"), wsChild|wsClipSiblings|wsClipChildren,
		0, 0, uintptr(h.area.w()), uintptr(h.area.h()), h.parent, 0, 0, 0)
	if w == 0 {
		return fmt.Errorf("creating the wallpaper window: %v", e)
	}
	h.wnd = w
	if h.raised {
		pSetLayeredWindowAttribute.Call(w, 0, 255, 2) // LWA_ALPHA, fully opaque
	}
	h.fixZ()
	// A hidden top-level window gets the power and session notifications.
	m, _, e := pCreateWindowExW.Call(wsExToolWindow, str("SpanwallLiveMsg"), 0, wsPopup, 0, 0, 0, 0, 0, 0, 0, 0)
	if m == 0 {
		return fmt.Errorf("creating the message window: %v", e)
	}
	h.msgWnd = m
	displayState := windows.GUID{Data1: 0x6fe69556, Data2: 0x704a, Data3: 0x47a0, Data4: [8]byte{0x8f, 0x24, 0xc2, 0x8d, 0x93, 0x6f, 0xda, 0x47}}
	pRegisterPowerSettingNotif.Call(m, uintptr(unsafe.Pointer(&displayState)), 0)
	pWTSRegisterSessionNotif.Call(m, 0)
	return nil
}

// fixZ keeps the window right under the icons and the shell's own wallpaper
// window at the bottom.
func (h *host) fixZ() {
	pSetWindowPos.Call(h.wnd, h.after, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	if h.raised {
		if wk, _, _ := pFindWindowExW.Call(h.parent, 0, str("WorkerW"), 0); wk != 0 {
			if last, _, _ := pGetWindow.Call(h.wnd, gwHwndNext); last != wk {
				pSetWindowPos.Call(wk, hwndBottom, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
			}
		}
	}
}

func wndProc(w, msg, wp, lp uintptr) uintptr {
	h := theHost
	if h != nil && w == h.msgWnd {
		switch msg {
		case wmApp:
			h.commands()
			return 0
		case wmTimer:
			h.tick()
			return 0
		case wmWtsSessionChange:
			switch wp {
			case 7: // WTS_SESSION_LOCK
				h.locked = true
			case 8: // WTS_SESSION_UNLOCK
				h.locked = false
			}
			h.updateVisible()
			return 0
		case wmPowerBroadcast:
			if wp == 0x8013 && lp != 0 { // PBT_POWERSETTINGCHANGE: display on/off/dimmed
				data := powerData(lp)
				h.displayOff = data == 0
				h.updateVisible()
			}
			return 1
		}
	}
	r, _, _ := pDefWindowProcW.Call(w, msg, wp, lp)
	return r
}

// powerData reads POWERBROADCAST_SETTING.Data[0] (after a 16-byte GUID and
// a 4-byte length) from a WM_POWERBROADCAST lParam.
func powerData(lp uintptr) uint32 {
	b := unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&lp))), 24)
	return uint32(b[20]) | uint32(b[21])<<8 | uint32(b[22])<<16 | uint32(b[23])<<24
}

func (h *host) commands() {
	for {
		select {
		case c := <-h.cmds:
			switch c {
			case "reload":
				h.eval("spanwall.reload()")
			case "hide":
				h.hiddenByServer = true
				h.updateVisible()
			case "show":
				h.hiddenByServer = false
				h.updateVisible()
			case "quit":
				exitCode = 0
				pPostQuitMessage.Call(0)
			}
		default:
			return
		}
	}
}

func (h *host) eval(js string) {
	if h.ready {
		h.view.Eval(js)
	}
}

// tick runs every second: checks the window is still on the desktop,
// follows monitor changes and pauses for windows that cover the monitors.
func (h *host) tick() {
	alive, _, _ := pIsWindow.Call(h.wnd)
	par, _, _ := pIsWindow.Call(h.parent)
	if alive == 0 || par == 0 {
		log.Print("the desktop window went away (Explorer restarted?)")
		exitCode = 3
		pPostQuitMessage.Call(0)
		return
	}
	if p, _, _ := pGetParent.Call(h.wnd); p != h.parent {
		log.Print("the wallpaper window lost its place on the desktop")
		exitCode = 3
		pPostQuitMessage.Call(0)
		return
	}
	if a := winRect(h.parent); a != h.area {
		h.area = a
		pSetWindowPos.Call(h.wnd, 0, 0, 0, uintptr(a.w()), uintptr(a.h()), swpNoMove|swpNoActivate|0x4 /* NOZORDER */)
		h.view.Resize()
		h.eval("spanwall.reload()")
	}
	if h.shown {
		if prev, _, _ := pGetWindow.Call(h.wnd, gwHwndPrev); h.raised && prev != h.after {
			h.fixZ()
		}
	}
	mons, covered, why := coverage()
	b, _ := json.Marshal(covered)
	if s := string(b); s != h.lastCovered {
		h.lastCovered = s
		log.Printf("covered: %d of %d monitors %s by %q", len(covered), len(mons), s, why)
		h.eval("spanwall.covered(" + s + ")")
	}
	h.allCovered = len(covered) == len(mons) && len(mons) > 0
	h.updateVisible()
}

func (h *host) updateVisible() {
	v := !(h.hiddenByServer || h.locked || h.displayOff || h.allCovered)
	if v == h.visible || !h.ready {
		return
	}
	h.visible = v
	log.Printf("visible: %v (server hid it %v, locked %v, displays off %v, all covered %v)", v, h.hiddenByServer, h.locked, h.displayOff, h.allCovered)
	if c := h.view.GetController(); c != nil {
		c.PutIsVisible(v) // hidden: the page stops drawing entirely
	}
	if h.hiddenByServer {
		pShowWindow.Call(h.wnd, swHide) // let identify cards or the calibration pattern show through
	} else if h.shown {
		pShowWindow.Call(h.wnd, swShowNoActivate)
	}
}

// ---- coverage ---------------------------------------------------------------------

var browserClasses = map[string]bool{"Chrome_WidgetWin_1": true, "Chrome_WidgetWin_0": true, "MozillaWindowClass": true, "ApplicationFrameWindow": true}

var pGetForegroundWindow = user32.NewProc("GetForegroundWindow")

func foreground() uintptr { w, _, _ := pGetForegroundWindow.Call(); return w }

var shellClasses = map[string]bool{"Progman": true, "WorkerW": true, "Shell_TrayWnd": true, "Shell_SecondaryTrayWnd": true,
	"Windows.UI.Core.CoreWindow": true, "XamlExplorerHostIslandWindow": true, "TopLevelWindowForOverflowXamlIsland": true,
	"SpanwallLive": true, "SpanwallLiveMsg": true, "NotifyIconOverflowWindow": true}

// coverage returns the monitors and those almost entirely hidden by a window
// (a maximised app, a full-screen video, or every monitor during a full-screen
// game), as [x, y, w, h] rects.
func coverage() (mons []rect, covered [][4]int32, why []string) {
	mons, _ = enumMonitors()
	reason := make([]string, len(mons))
	cov := make([]bool, len(mons))
	var qs uint32
	// A full-screen game pauses everything (leave it the GPU). Browsers
	// playing full-screen video report the same D3D full-screen state, so
	// Chromium and Firefox windows only cover their own monitor (below).
	if r, _, _ := pSHQueryUserNotification.Call(uintptr(unsafe.Pointer(&qs))); r == 0 && qs == 3 && !browserClasses[className(foreground())] {
		for i := range cov {
			cov[i], reason[i] = true, "full-screen game "+className(foreground())
		}
	} else {
		self := uint32(os.Getpid())
		ecb := syscall.NewCallback(func(w, _ uintptr) uintptr {
			if v, _, _ := pIsWindowVisible.Call(w); v == 0 {
				return 1
			}
			if ic, _, _ := pIsIconic.Call(w); ic != 0 {
				return 1
			}
			var pid uint32
			pGetWindowThreadProcessId.Call(w, uintptr(unsafe.Pointer(&pid)))
			if pid == self {
				return 1
			}
			r := winRect(w)
			best, frac := -1, 0.0
			for i, m := range mons {
				if cov[i] {
					continue
				}
				ix := min(r.R, m.R) - max(r.L, m.L)
				iy := min(r.B, m.B) - max(r.T, m.T)
				if ix > 0 && iy > 0 {
					if f := float64(ix) * float64(iy) / (float64(m.w()) * float64(m.h())); f > frac {
						best, frac = i, f
					}
				}
			}
			if best < 0 || frac < 0.9 {
				return 1
			}
			var cloaked uint32
			pDwmGetWindowAttribute.Call(w, 14, uintptr(unsafe.Pointer(&cloaked)), 4) // DWMWA_CLOAKED: other desktop, suspended app
			if cloaked != 0 {
				return 1
			}
			ex, _, _ := pGetWindowLongPtrW.Call(w, gwlExStyle)
			if ex&(wsExToolWindow|wsExTransparent) != 0 {
				return 1
			}
			if ex&wsExLayered != 0 { // see-through overlays don't hide the desktop
				var key uint32
				var alpha byte
				var flags uint32
				if ok, _, _ := pGetLayeredWindowAttribute.Call(w, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&alpha)), uintptr(unsafe.Pointer(&flags))); ok != 0 {
					if flags&1 != 0 || (flags&2 != 0 && alpha < 250) { // LWA_COLORKEY or translucent
						return 1
					}
				}
			}
			if shellClasses[className(w)] {
				return 1
			}
			cov[best] = true
			reason[best] = fmt.Sprintf("%s %q %.2f", className(w), windowText(w), frac)
			return 1
		})
		pEnumWindows.Call(ecb, 0)
	}
	for i, m := range mons {
		if cov[i] {
			covered = append(covered, [4]int32{m.L, m.T, m.w(), m.h()})
			why = append(why, reason[i])
		}
	}
	return mons, covered, why
}

// MonitorRects returns every monitor's rectangle (x, y, w, h) in physical
// virtual-screen pixels, as Windows currently shows them.
func MonitorRects() [][4]int {
	mons, _ := enumMonitors()
	out := make([][4]int, len(mons))
	for i, m := range mons {
		out[i] = [4]int{int(m.L), int(m.T), int(m.w()), int(m.h())}
	}
	return out
}

func enumMonitors() ([]rect, error) {
	var mons []rect
	cb := syscall.NewCallback(func(hm, _, _, _ uintptr) uintptr {
		var mi struct {
			Size      uint32
			Mon, Work rect
			Flags     uint32
		}
		mi.Size = uint32(unsafe.Sizeof(mi))
		pGetMonitorInfoW.Call(hm, uintptr(unsafe.Pointer(&mi)))
		mons = append(mons, mi.Mon)
		return 1
	})
	pEnumDisplayMonitors.Call(0, 0, cb, 0)
	return mons, nil
}
