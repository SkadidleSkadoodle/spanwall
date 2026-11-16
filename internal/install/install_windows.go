//go:build windows

package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/SkadidleSkadoodle/spanwall/internal/client"
	"github.com/SkadidleSkadoodle/spanwall/internal/launch"
)

// Dir is where Spanwall installs for the current user.
func Dir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Spanwall")
}

// Install copies the running exe to Dir, packs the Claude extension, turns
// on start at sign-in, starts the background app and opens the welcome
// page. log gets one line per step.
func Install(version string, log func(string)) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exe := filepath.Join(dir, "spanwall.exe")
	mcp := filepath.Join(dir, "spanwall-mcp.exe")
	if client.Running() {
		log("Stopping the running background app")
		if err := launch.Stop(); err != nil {
			return err
		}
	}
	if !strings.EqualFold(self, exe) {
		if err := copyRetry(self, exe); err != nil {
			return fmt.Errorf("copy to %s: %w", exe, err)
		}
	}
	log("Installed " + exe)
	if err := copyRetry(exe, mcp); err != nil {
		log("Claude extension binary is in use: turn the Spanwall extension off in Claude Desktop (Settings > Extensions) and run install again to update it")
	}
	pkg, err := PackExtension(dir, mcp, version)
	if err != nil {
		return err
	}
	log("Claude extension: " + pkg)
	if err := launch.SetAutostartExe(exe, true); err != nil {
		return err
	}
	log("Starts when you sign in (turn off in Settings)")
	cmd := exec.Command(exe, "start")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start: %v: %s", err, out)
	}
	log("Background app running at http://127.0.0.1:47920")
	open("http://127.0.0.1:47920/?welcome=1")
	return nil
}

// Uninstall stops Spanwall, removes start at sign-in and the scheduled
// task, and deletes the program folder (after this process exits). Settings
// in %USERPROFILE%\.spanwall are kept unless purge is set.
func Uninstall(purge bool, log func(string)) error {
	if client.Running() {
		if err := launch.Stop(); err != nil {
			return err
		}
	}
	log("Stopped the background app")
	if err := launch.SetAutostartExe("", false); err != nil {
		return err
	}
	launch.RemoveTask()
	log("Removed start at sign-in and the scheduled task")
	targets := []string{Dir()}
	if purge {
		home, _ := os.UserHomeDir()
		targets = append(targets, filepath.Join(home, ".spanwall"))
	}
	// This exe may live in the folder being removed: delete after we exit.
	var del []string
	for _, t := range targets {
		del = append(del, fmt.Sprintf(`rmdir /s /q "%s"`, t))
	}
	// Raw command line: Go's argument quoting would mangle the quoted paths
	// for cmd. ping is the delay because timeout needs a console.
	c := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true,
		CmdLine: `cmd.exe /c "ping -n 4 127.0.0.1 >nul & ` + strings.Join(del, " & ") + `"`}
	if err := c.Start(); err != nil {
		return err
	}
	log("Removing " + strings.Join(targets, " and "))
	log("If Claude Desktop has the Spanwall extension, remove it in Settings > Extensions")
	return nil
}

func copyRetry(src, dst string) error {
	var err error
	for i := 0; i < 15; i++ {
		if err = copyFile(src, dst); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func open(target string) {
	windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(target), nil, nil, 1)
}

// LaunchedFromExplorer reports whether this console was created just for
// this process (double-click), as opposed to a terminal.
func LaunchedFromExplorer() bool {
	ids := make([]uint32, 4)
	r, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	return r == 1
}

var procGetConsoleProcessList = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")
