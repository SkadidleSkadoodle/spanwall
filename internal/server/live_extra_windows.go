//go:build windows

package server

import (
	"net/http"
	"sync"
	"time"
	"unsafe"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"golang.org/x/sys/windows"
)

// ---- day and night scenes ------------------------------------------------------

// liveScheduleStep shows the day or night scene when the period changes.
// It reports whether the schedule is in charge (the live slideshow then
// waits).
func (s *Server) liveScheduleStep() bool {
	var switched string
	v, _ := s.session(func(sess *app.Session) (any, error) {
		l := &sess.Cfg.Live
		if !l.Enabled || !l.Schedule.Enabled {
			return false, nil
		}
		want := map[string]string{"day": l.Schedule.Day, "night": l.Schedule.Night}[modePeriod(sess.Cfg, time.Now())]
		if want == "" || want == l.SceneName() {
			return true, nil
		}
		l.Scene, switched = want, want
		return true, sess.Save()
	})
	if switched != "" {
		s.log.Printf("live: day and night schedule switched to %s", switched)
		s.liveSync()
		s.liveSend("reload")
		s.events.publish("live")
	}
	on, _ := v.(bool)
	return on
}

// ---- system numbers for data scenes ------------------------------------------------

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
	procGetSystemPowerStatus = kernel32.NewProc("GetSystemPowerStatus")

	statsMu                    sync.Mutex
	lastIdle, lastKern, lastUs uint64
	lastCPU                    float64
	lastAt                     time.Time
)

func ft(lo, hi uint32) uint64 { return uint64(hi)<<32 | uint64(lo) }

// cpuPercent is the machine's CPU use since the last call (at least 0.5 s
// apart; quicker calls get the last value).
func cpuPercent() float64 {
	statsMu.Lock()
	defer statsMu.Unlock()
	if time.Since(lastAt) < 500*time.Millisecond {
		return lastCPU
	}
	var idle, kern, user windows.Filetime
	if r, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kern)), uintptr(unsafe.Pointer(&user))); r == 0 {
		return lastCPU
	}
	i, k, u := ft(idle.LowDateTime, idle.HighDateTime), ft(kern.LowDateTime, kern.HighDateTime), ft(user.LowDateTime, user.HighDateTime)
	if !lastAt.IsZero() {
		total := (k - lastKern) + (u - lastUs) // kernel time includes idle
		if total > 0 {
			lastCPU = 100 * float64(total-(i-lastIdle)) / float64(total)
		}
	}
	lastIdle, lastKern, lastUs, lastAt = i, k, u, time.Now()
	return lastCPU
}

// liveStats is what system scenes show: CPU, memory, uptime and battery.
func (s *Server) liveStats(w http.ResponseWriter, r *http.Request) {
	var mem struct {
		Length                                   uint32
		MemoryLoad                               uint32
		TotalPhys, AvailPhys, TotalPage, AvailPg uint64
		TotalVirtual, AvailVirtual, AvailExt     uint64
	}
	mem.Length = uint32(unsafe.Sizeof(mem))
	procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mem)))
	up, _, _ := procGetTickCount64.Call()
	var ps struct {
		ACLine, Flag, Percent, Saver byte
		Life, Full                   uint32
	}
	procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&ps)))
	out := map[string]any{"cpu": cpuPercent(), "memUsed": float64(mem.TotalPhys-mem.AvailPhys) / (1 << 30), "memTotal": float64(mem.TotalPhys) / (1 << 30),
		"memPercent": mem.MemoryLoad, "uptimeSec": uint64(up) / 1000, "time": time.Now().Format(time.RFC3339)}
	if ps.Flag != 128 && ps.Percent <= 100 { // 128: no battery
		out["battery"] = map[string]any{"percent": ps.Percent, "plugged": ps.ACLine == 1}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out, nil)
}
