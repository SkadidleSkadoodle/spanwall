//go:build windows

// Package win holds process-level Windows setup.
package win

import (
	"runtime"

	"golang.org/x/sys/windows"
)

var (
	user32                             = windows.NewLazySystemDLL("user32.dll")
	procSetProcessDpiAwarenessContext  = user32.NewProc("SetProcessDpiAwarenessContext")
	dpiAwarenessContextPerMonitorAware = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
)

// Init makes the process per-monitor DPI aware, so every size and position
// is in real pixels regardless of each monitor's scaling, and starts COM on
// the calling thread, which stays locked to it. Call first thing in main.
func Init() (cleanup func(), err error) {
	procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAware)
	runtime.LockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil {
		return func() {}, err
	}
	return windows.CoUninitialize, nil
}
