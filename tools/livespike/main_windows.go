//go:build windows

// livespike is a throwaway test for animated wallpaper: one WebView2 window per
// monitor, placed behind the desktop icons, running a worst-case ASCII page.
// It prints how the desktop layer was found and the fps each page reports,
// then removes its windows. Usage: livespike [-seconds 45] [-fps 30]
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/SkadidleSkadoodle/spanwall/internal/win"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

//go:embed page.html
var page []byte

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
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
	pSetWindowPos              = user32.NewProc("SetWindowPos")
	pGetWindow                 = user32.NewProc("GetWindow")
	pGetParent                 = user32.NewProc("GetParent")
	pGetClassNameW             = user32.NewProc("GetClassNameW")
	pDestroyWindow             = user32.NewProc("DestroyWindow")
	pGetMessageW               = user32.NewProc("GetMessageW")
	pTranslateMessage          = user32.NewProc("TranslateMessage")
	pDispatchMessageW          = user32.NewProc("DispatchMessageW")
	pSetTimer                  = user32.NewProc("SetTimer")
	pPostQuitMessage           = user32.NewProc("PostQuitMessage")
	pGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
)

const (
	wsChild, wsVisible, wsClipSiblings, wsClipChildren = 0x40000000, 0x10000000, 0x04000000, 0x02000000
	wsExLayered, wsExToolWindow, wsExNoActivate       = 0x00080000, 0x00000080, 0x08000000
	wsExNoRedirectionBitmap                           = 0x00200000
	gwlExStyle                                        = ^uintptr(19) // -20
	swpNoSize, swpNoMove, swpNoActivate               = 0x1, 0x2, 0x10
	gwHwndNext, gwHwndPrev, gwChild                   = 2, 3, 5
	wmTimer                                           = 0x0113
)

type rect struct{ L, T, R, B int32 }

type monitor struct {
	R       rect
	Primary bool
}

type report struct {
	FPS, MS, W, H, DPR, Vis string
}

var (
	mu      sync.Mutex
	reports = map[string][]report{}
)

func str(p string) uintptr { s, _ := windows.UTF16PtrFromString(p); return uintptr(unsafe.Pointer(s)) }

func className(h uintptr) string {
	b := make([]uint16, 256)
	pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&b[0])), 256)
	return windows.UTF16ToString(b)
}

func winRect(h uintptr) rect { var r rect; pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r))); return r }

func monitors() []monitor {
	var out []monitor
	cb := syscall.NewCallback(func(h, hdc, lprc, lp uintptr) uintptr {
		var mi struct {
			Size        uint32
			Mon, Work   rect
			Flags       uint32
		}
		mi.Size = uint32(unsafe.Sizeof(mi))
		pGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
		out = append(out, monitor{mi.Mon, mi.Flags&1 != 0})
		return 1
	})
	pEnumDisplayMonitors.Call(0, 0, cb, 0)
	sort.Slice(out, func(i, j int) bool { return out[i].R.L < out[j].R.L || out[i].R.L == out[j].R.L && out[i].R.T < out[j].R.T })
	return out
}

// desktopLayer finds where a wallpaper window goes. On 24H2 and later
// ("raised desktop": Progman has WS_EX_NOREDIRECTIONBITMAP) the icons
// (SHELLDLL_DefView) and the wallpaper WorkerW are both children of Progman,
// and our window must be a layered child of Progman between them. Older
// builds use a top-level WorkerW behind the icons.
func desktopLayer() (parent, after uintptr, mode string, info map[string]any, err error) {
	progman, _, _ := pFindWindowW.Call(str("Progman"), 0)
	if progman == 0 {
		return 0, 0, "", nil, fmt.Errorf("no Progman window")
	}
	var res uintptr
	pSendMessageTimeoutW.Call(progman, 0x052C, 0xD, 0x1, 0, 1000, uintptr(unsafe.Pointer(&res)))
	time.Sleep(300 * time.Millisecond)
	ex, _, _ := pGetWindowLongPtrW.Call(progman, gwlExStyle)
	raised := ex&wsExNoRedirectionBitmap != 0
	defView, _, _ := pFindWindowExW.Call(progman, 0, str("SHELLDLL_DefView"), 0)
	worker, _, _ := pFindWindowExW.Call(progman, 0, str("WorkerW"), 0)
	info = map[string]any{"progman": fmt.Sprintf("%#x", progman), "raisedDesktop": raised,
		"defViewInProgman": defView != 0, "workerInProgman": worker != 0, "progmanRect": winRect(progman)}
	if raised && defView != 0 {
		return progman, defView, "raised (24H2+): layered child of Progman under the icons", info, nil
	}
	// legacy: the WorkerW right after the top-level window that holds DefView
	var legacy uintptr
	cb := syscall.NewCallback(func(h, lp uintptr) uintptr {
		if d, _, _ := pFindWindowExW.Call(h, 0, str("SHELLDLL_DefView"), 0); d != 0 {
			legacy, _, _ = pFindWindowExW.Call(0, h, str("WorkerW"), 0)
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	if legacy == 0 {
		return 0, 0, "", info, fmt.Errorf("no desktop layer found")
	}
	return legacy, 1 /* HWND_BOTTOM */, "legacy: child of the WorkerW behind the icons", info, nil
}

func main() {
	seconds := flag.Int("seconds", 45, "how long to run")
	fps := flag.Int("fps", 30, "frame cap")
	flag.Parse()
	done, err := win.Init()
	if err == syscall.Errno(1) { // S_FALSE: COM was already started on this thread, which is fine
		err = nil
	}
	if err != nil {
		fmt.Println("COM init:", err)
		os.Exit(1)
	}
	defer done()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		reports[q.Get("m")] = append(reports[q.Get("m")], report{q.Get("fps"), q.Get("ms"), q.Get("w"), q.Get("h"), q.Get("dpr"), q.Get("vis")})
		mu.Unlock()
	})
	go http.Serve(ln, mux)
	base := "http://" + ln.Addr().String() + "/"

	parent, after, mode, info, err := desktopLayer()
	out := map[string]any{"mode": mode, "shell": info}
	if err != nil {
		out["error"] = err.Error()
		dump(out)
		os.Exit(1)
	}
	pr := winRect(parent)
	mons := monitors()
	vx, _, _ := pGetSystemMetrics.Call(76)
	vw, _, _ := pGetSystemMetrics.Call(78)
	out["monitors"] = mons
	out["parentRect"] = pr

	wndProc := syscall.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
		if msg == wmTimer {
			pPostQuitMessage.Call(0)
			return 0
		}
		r, _, _ := pDefWindowProcW.Call(h, msg, wp, lp)
		return r
	})
	type wndClass struct {
		Size, Style                  uint32
		WndProc                      uintptr
		ClsExtra, WndExtra           int32
		Instance, Icon, Cursor, Bg   uintptr
		MenuName, ClassName          *uint16
		IconSm                       uintptr
	}
	cn, _ := windows.UTF16PtrFromString("SpanwallLiveSpike")
	wc := wndClass{WndProc: wndProc, ClassName: cn}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	raised := after != 1
	udf := filepath.Join(os.Getenv("USERPROFILE"), ".spanwall", "livespike-webview")
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS",
		"--disable-features=CalculateNativeWinOcclusion --disable-renderer-backgrounding --disable-backgrounding-occluded-windows --disable-background-timer-throttling")

	var hwnds []uintptr
	var views []*edge.Chromium
	type placed struct {
		Monitor                        int
		Hwnd, Parent, Above, Below     string
	}
	var place []placed
	for i, m := range mons {
		x, y := m.R.L-pr.L, m.R.T-pr.T
		w, h := m.R.R-m.R.L, m.R.B-m.R.T
		exs := uintptr(wsExToolWindow | wsExNoActivate)
		if raised {
			exs |= wsExLayered
		}
		hw, _, e := pCreateWindowExW.Call(exs, uintptr(unsafe.Pointer(cn)), 0,
			wsChild|wsVisible|wsClipSiblings|wsClipChildren, uintptr(x), uintptr(y), uintptr(w), uintptr(h), parent, 0, 0, 0)
		if hw == 0 {
			out["error"] = fmt.Sprintf("CreateWindowEx monitor %d: %v", i, e)
			dump(out)
			os.Exit(1)
		}
		if raised {
			pSetLayeredWindowAttribute.Call(hw, 0, 255, 2 /* LWA_ALPHA */)
		}
		pSetWindowPos.Call(hw, after, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		cv := edge.NewChromium()
		cv.DataPath = udf
		if !cv.Embed(hw) {
			out["error"] = fmt.Sprintf("WebView2 embed failed on monitor %d", i)
			dump(out)
			os.Exit(1)
		}
		cv.Resize()
		cv.Navigate(fmt.Sprintf("%s?m=%d&x=%d&y=%d&vx=%d&vw=%d&fps=%d", base, i, m.R.L, m.R.T, int32(vx), vw, *fps))
		hwnds = append(hwnds, hw)
		views = append(views, cv)
	}
	if raised { // keep the shell's WorkerW at the bottom, as Lively does
		if wk, _, _ := pFindWindowExW.Call(parent, 0, str("WorkerW"), 0); wk != 0 {
			pSetWindowPos.Call(wk, 1, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		}
	}
	for i, hw := range hwnds {
		p, _, _ := pGetParent.Call(hw)
		a, _, _ := pGetWindow.Call(hw, gwHwndPrev)
		b, _, _ := pGetWindow.Call(hw, gwHwndNext)
		place = append(place, placed{i, fmt.Sprintf("%#x", hw), className(p), className(a), className(b)})
	}
	out["windows"] = place
	// z order of Progman's children, top to bottom
	var z []string
	for c, _, _ := pGetWindow.Call(parent, gwChild); c != 0; c, _, _ = pGetWindow.Call(c, gwHwndNext) {
		z = append(z, className(c))
	}
	out["progmanChildrenTopToBottom"] = z

	pSetTimer.Call(hwnds[0], 1, uintptr(*seconds)*1000, 0)
	var msg [48]byte
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
	}
	for _, hw := range hwnds {
		pDestroyWindow.Call(hw)
	}
	mu.Lock()
	out["reports"] = reports
	mu.Unlock()
	dump(out)
}

func dump(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
