//go:build windows

// Package launch starts and stops the background app.
//
// The background app must not be a child of the Claude desktop app: Claude
// runs extensions inside its MSIX package and may stop child processes when
// it closes. So Spanwall registers an on-demand Task Scheduler task and asks
// Task Scheduler to start it. The task runs `conhost --headless`, which
// gives the app a console that is never shown.
package launch

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/SkadidleSkadoodle/spanwall/internal/client"
)

// TaskName is the Task Scheduler task.
const TaskName = "Spanwall"

// DaemonExe is the copy of spanwall.exe the background app runs from. A
// separate copy means rebuilding spanwall.exe never fails because the
// background app has it open.
func DaemonExe() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(self), "spanwall-daemon.exe"), nil
}

// Start makes sure the background app is running, starting it if needed.
func Start() error {
	if client.Running() {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	daemon, err := DaemonExe()
	if err != nil {
		return err
	}
	if !strings.EqualFold(self, daemon) {
		// A daemon that just stopped can hold its exe for a moment.
		var err error
		for i := 0; i < 25; i++ {
			if err = copyFile(self, daemon); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("copy daemon exe: %w", err)
		}
	}
	conhost := filepath.Join(os.Getenv("SystemRoot"), "System32", "conhost.exe")
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
$a = New-ScheduledTaskAction -Execute '%s' -Argument '--headless "%s" serve' -WorkingDirectory '%s'
$s = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName '%s' -Action $a -Settings $s -Description 'Spanwall background app (on demand)' -Force | Out-Null
Start-ScheduledTask -TaskName '%s'`, conhost, daemon, filepath.Dir(daemon), TaskName, TaskName)
	if out, err := powershell(script); err != nil {
		return fmt.Errorf("start via Task Scheduler: %v: %s", err, out)
	}
	for i := 0; i < 40; i++ {
		time.Sleep(200 * time.Millisecond)
		if client.Running() {
			return nil
		}
	}
	return fmt.Errorf("the background app didn't answer within 8 s; see %%USERPROFILE%%\\.spanwall\\spanwall.log")
}

// Stop asks the background app to exit.
func Stop() error {
	if !client.Running() {
		return nil
	}
	if err := client.Post("/api/shutdown", map[string]any{}, nil); err != nil {
		return err
	}
	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		if !client.Running() {
			return nil
		}
	}
	return fmt.Errorf("the background app is still running")
}

func powershell(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartCommand starts the background app at sign-in without a console
// window: conhost --headless runs `spanwall.exe start`.
func autostartCommand() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe := filepath.Join(filepath.Dir(self), "spanwall.exe")
	if _, err := os.Stat(exe); err != nil {
		exe = self
	}
	conhost := filepath.Join(os.Getenv("SystemRoot"), "System32", "conhost.exe")
	return fmt.Sprintf(`"%s" --headless "%s" start`, conhost, exe), nil
}

// Autostart reports whether Spanwall starts at sign-in, and the command.
func Autostart() (bool, string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(TaskName)
	return err == nil && v != "", v
}

// SetAutostartExe is SetAutostart for a given spanwall.exe.
func SetAutostartExe(exe string, on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(TaskName); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	conhost := filepath.Join(os.Getenv("SystemRoot"), "System32", "conhost.exe")
	return k.SetStringValue(TaskName, fmt.Sprintf(`"%s" --headless "%s" start`, conhost, exe))
}

// RemoveTask deletes the background app's scheduled task.
func RemoveTask() {
	powershell(fmt.Sprintf("Unregister-ScheduledTask -TaskName '%s' -Confirm:$false -ErrorAction SilentlyContinue", TaskName))
}

// SetAutostart adds or removes the sign-in entry (HKCU Run key, so no admin
// rights are needed and it shows in Task Manager's Startup apps).
func SetAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(TaskName); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	cmd, err := autostartCommand()
	if err != nil {
		return err
	}
	return k.SetStringValue(TaskName, cmd)
}
