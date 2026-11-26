//go:build windows

package win

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procEnumWindows      = user32.NewProc("EnumWindows")
	procGetClassNameW    = user32.NewProc("GetClassNameW")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
)

// MonitorCount is how many monitors Windows is showing the desktop on
// (SM_CMONITORS). Explorer's wallpaper object should report the same number
// of attached monitors; when it reports fewer, its monitor table is stale.
func MonitorCount() int {
	r, _, _ := procGetSystemMetrics.Call(80)
	return int(int32(r))
}

// One callback for the life of the process: windows.NewCallback slots are
// never freed.
var (
	folderMu    sync.Mutex
	folderCount int
	folderCB    = windows.NewCallback(func(h, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(h); v == 0 {
			return 1
		}
		var buf [64]uint16
		procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if windows.UTF16ToString(buf[:]) == "CabinetWClass" {
			folderCount++
		}
		return 1
	})
)

// FolderWindows counts open File Explorer windows. Restarting Explorer
// closes them, so Spanwall only restarts it on its own when this is zero.
func FolderWindows() int {
	folderMu.Lock()
	defer folderMu.Unlock()
	folderCount = 0
	procEnumWindows.Call(folderCB, 0)
	return folderCount
}

// explorerPIDs lists explorer.exe processes in this user's session.
func explorerPIDs() ([]uint32, error) {
	var mySession uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &mySession); err != nil {
		return nil, err
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []uint32
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if !strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "explorer.exe") {
			continue
		}
		var s uint32
		if windows.ProcessIdToSessionId(pe.ProcessID, &s) == nil && s == mySession {
			out = append(out, pe.ProcessID)
		}
	}
	return out, nil
}

// RestartExplorer ends Explorer and starts it again, which rebuilds its
// monitor table. Open File Explorer windows close; the taskbar comes back.
func RestartExplorer() error {
	pids, err := explorerPIDs()
	if err != nil {
		return fmt.Errorf("find explorer: %w", err)
	}
	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			return fmt.Errorf("open explorer %d: %w", pid, err)
		}
		windows.TerminateProcess(h, 1)
		windows.WaitForSingleObject(h, 5000)
		windows.CloseHandle(h)
	}
	// Winlogon sometimes restarts the shell by itself; give it a moment so
	// we don't start a second one (which would just open a folder window).
	for i := 0; i < 12; i++ {
		time.Sleep(250 * time.Millisecond)
		if p, _ := explorerPIDs(); len(p) > 0 {
			return waitDesktop()
		}
	}
	exe := filepath.Join(os.Getenv("WINDIR"), "explorer.exe")
	// Break away from Task Scheduler's job, so quitting Spanwall can never
	// take the shell down with it. Fall back if the job forbids breakaway.
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x01000000} // CREATE_BREAKAWAY_FROM_JOB
	if err := cmd.Start(); err != nil {
		cmd = exec.Command(exe)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start explorer: %w", err)
		}
	}
	cmd.Process.Release()
	return waitDesktop()
}

// waitDesktop gives the new Explorer time to register its wallpaper object.
func waitDesktop() error {
	time.Sleep(4 * time.Second)
	return nil
}
