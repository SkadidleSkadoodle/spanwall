//go:build windows

package server

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/config"
	"github.com/SkadidleSkadoodle/spanwall/internal/imageio"
	"github.com/SkadidleSkadoodle/spanwall/internal/live"
	"github.com/SkadidleSkadoodle/spanwall/internal/paths"
	"github.com/SkadidleSkadoodle/spanwall/internal/web"
)

// The animated wallpaper runs in a child process (spanwall live-host) that
// owns a window behind the desktop icons. This file starts and supervises
// it, and serves what its page asks for.

type liveState struct {
	mu       sync.Mutex
	want     bool // should be running (config says enabled)
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	started  time.Time
	crashes  []time.Time
	hidden   bool // an overlay is up
	report   map[string]any
	reportAt time.Time
	lastLog  string
	watchVer string // active scene's file version, for hot reload
	scene    string // active scene (cached from the config by liveSync)
}

func scenesDir() string {
	d, err := paths.ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "scenes")
}

// liveSync starts or stops the host to match the config.
func (s *Server) liveSync() {
	var on bool
	var scene string
	s.session(func(sess *app.Session) (any, error) {
		on, scene = sess.Cfg.Live.Enabled, sess.Cfg.Live.SceneName()
		return nil, nil
	})
	s.live.mu.Lock()
	s.live.want, s.live.scene = on, scene
	running := s.live.cmd != nil
	s.live.mu.Unlock()
	switch {
	case on && !running:
		s.liveStart()
	case !on && running:
		s.liveStop()
	}
}

func (s *Server) liveStart() {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	if s.live.cmd != nil || !s.live.want {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		s.log.Printf("live: %v", err)
		return
	}
	cmd := exec.Command(exe, "live-host", "http://"+Addr+"/live/")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	in, err := cmd.StdinPipe()
	if err != nil {
		s.log.Printf("live: %v", err)
		return
	}
	errp, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		s.log.Printf("live: starting the host: %v", err)
		return
	}
	s.live.cmd, s.live.stdin, s.live.started = cmd, in, time.Now()
	if s.live.hidden {
		fmt.Fprintln(in, "hide")
	}
	s.log.Printf("live: host started (pid %d)", cmd.Process.Pid)
	go func() {
		sc := bufio.NewScanner(errp)
		for sc.Scan() {
			line := sc.Text()
			s.log.Print(line)
			s.live.mu.Lock()
			s.live.lastLog = line
			s.live.mu.Unlock()
		}
	}()
	go s.liveWait(cmd)
}

// liveWait restarts the host when it exits on its own (Explorer restarted,
// or a crash), backing off if it keeps failing.
func (s *Server) liveWait(cmd *exec.Cmd) {
	err := cmd.Wait()
	code := cmd.ProcessState.ExitCode()
	s.live.mu.Lock()
	if s.live.cmd == cmd {
		s.live.cmd, s.live.stdin = nil, nil
	}
	want := s.live.want
	delay := 2 * time.Second
	if want && code != 3 { // 3 = the desktop went away; anything else counts as a crash
		now := time.Now()
		s.live.crashes = append(s.live.crashes, now)
		recent := s.live.crashes[:0]
		for _, t := range s.live.crashes {
			if now.Sub(t) < 10*time.Minute {
				recent = append(recent, t)
			}
		}
		s.live.crashes = recent
		if len(recent) >= 4 {
			delay = 2 * time.Minute
		}
	}
	last := s.live.lastLog
	s.live.mu.Unlock()
	s.log.Printf("live: host exited (code %d, %v)", code, err)
	if !want {
		return
	}
	if delay > time.Minute {
		s.notify("Animated wallpaper keeps stopping", "Retrying in 2 minutes. Last message: "+last, true)
	}
	time.Sleep(delay)
	s.live.mu.Lock()
	still := s.live.want && s.live.cmd == nil
	s.live.mu.Unlock()
	if still {
		s.liveStart()
	}
}

func (s *Server) liveStop() {
	s.live.mu.Lock()
	s.live.want = false
	cmd, in := s.live.cmd, s.live.stdin
	s.live.mu.Unlock()
	if cmd == nil {
		return
	}
	fmt.Fprintln(in, "quit")
	in.Close()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 30; i++ {
			s.live.mu.Lock()
			gone := s.live.cmd != cmd
			s.live.mu.Unlock()
			if gone {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		close(done)
	}()
	<-done
	s.live.mu.Lock()
	if s.live.cmd == cmd {
		cmd.Process.Kill()
	}
	s.live.mu.Unlock()
}

// liveSend passes a command (reload, hide, show) to a running host.
func (s *Server) liveSend(c string) {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	switch c {
	case "hide":
		s.live.hidden = true
	case "show":
		s.live.hidden = false
	}
	if s.live.stdin != nil {
		fmt.Fprintln(s.live.stdin, c)
	}
}

func (s *Server) liveRunning() bool {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	return s.live.cmd != nil
}

// liveWatch reloads the page when the active scene's file changes on disk,
// so a scene can be edited in any editor and seen live.
func (s *Server) liveWatch() {
	for range time.Tick(2 * time.Second) {
		if !s.liveRunning() {
			continue
		}
		s.live.mu.Lock()
		name := s.live.scene
		s.live.mu.Unlock()
		ver := sceneVersion(name)
		s.live.mu.Lock()
		changed := s.live.watchVer != "" && ver != s.live.watchVer
		s.live.watchVer = ver
		s.live.mu.Unlock()
		if changed {
			s.log.Printf("live: scene %s changed on disk, reloading", name)
			s.liveSend("reload")
		}
	}
}

// sceneVersion changes when the scene's file, or its converted clip, does.
func sceneVersion(name string) string {
	_, v, _ := live.Open(web.FS(), scenesDir(), name+".js")
	if fi, err := os.Stat(filepath.Join(scenesDir(), name+".clip.json")); err == nil {
		v += fmt.Sprint(".", fi.ModTime().UnixMilli())
	}
	return v
}

// liveOff turns the animation off because a still wallpaper was chosen.
func (s *Server) liveOff(sess *app.Session) error {
	if !sess.Cfg.Live.Enabled {
		return nil
	}
	sess.Cfg.Live.Enabled = false
	s.live.mu.Lock()
	s.live.want = false
	s.live.mu.Unlock()
	go s.liveSync()
	return sess.Save()
}

// ---- HTTP ---------------------------------------------------------------------------

func (s *Server) liveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /live/scenes/{file}", s.liveSceneFile)
	mux.HandleFunc("GET /api/live", s.getLive)
	mux.HandleFunc("POST /api/live", s.setLive)
	mux.HandleFunc("GET /api/live/runtime", s.liveRuntime)
	mux.HandleFunc("POST /api/live/report", s.liveReport)
	mux.HandleFunc("GET /api/live/scene", s.getScene)
	mux.HandleFunc("POST /api/live/scene", s.setScene)
	mux.HandleFunc("POST /api/live/convert", s.liveConvert)
	mux.HandleFunc("POST /api/live/rate", s.liveRate)
	mux.HandleFunc("GET /api/live/frame", s.getLiveFrame)
	mux.HandleFunc("POST /api/live/frame", s.setLiveFrame)
	mux.HandleFunc("GET /api/live/thumb", s.getLiveThumb)
	mux.HandleFunc("POST /api/live/thumb", s.setLiveThumb)
}

func (s *Server) liveSceneFile(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	data, _, err := live.Open(web.FS(), scenesDir(), file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	ct := "text/javascript; charset=utf-8"
	if strings.HasSuffix(file, ".json") {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

func (s *Server) liveInfo(sess *app.Session) map[string]any {
	l := sess.Cfg.Live
	s.live.mu.Lock()
	rep, at := s.live.report, s.live.reportAt
	running, pid := s.live.cmd != nil, 0
	if running {
		pid = s.live.cmd.Process.Pid
	}
	lastLog := s.live.lastLog
	s.live.mu.Unlock()
	var scenes []map[string]any
	for _, si := range live.List(web.FS(), scenesDir()) {
		ver := sceneVersion(si.Name)
		wver := worldVersion(ver, l.CharMM)
		_, framed := l.Frames[si.Name]
		scenes = append(scenes, map[string]any{"name": si.Name, "title": si.Title, "description": si.Description, "builtin": si.Builtin,
			"user": si.User, "rating": sess.Cfg.Ratings[liveKey(si.Name)], "version": ver, "thumb": thumbFresh(si.Name, ver),
			"worldVersion": wver, "worldThumb": thumbFresh(si.Name+".world", wver), "fps": si.FPS, "charMM": si.CharMM, "framed": framed,
			"file": si.Name + ".js"})
	}
	out := map[string]any{"enabled": l.Enabled, "scene": l.SceneName(), "fps": l.FPS, "charMM": l.CharMM, "font": l.Font,
		"running": running, "pid": pid, "scenes": scenes, "scenesDir": scenesDir(), "lastLog": lastLog,
		"album": sess.Cfg.Slideshow.Source == "live"}
	if rep != nil && time.Since(at) < time.Minute {
		out["page"] = rep
	}
	return out
}

func (s *Server) getLive(w http.ResponseWriter, r *http.Request) {
	v, err := s.session(func(sess *app.Session) (any, error) { return s.liveInfo(sess), nil })
	writeJSON(w, v, err)
}

func (s *Server) setLive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool    `json:"enabled"`
		Scene   *string  `json:"scene"`
		FPS     *int     `json:"fps"`
		CharMM  *float64 `json:"charMM"`
		Font    *string  `json:"font"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	v, err := s.session(func(sess *app.Session) (any, error) {
		l := &sess.Cfg.Live
		if req.Scene != nil {
			name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(*req.Scene)), ".js")
			if _, _, err := live.Open(web.FS(), scenesDir(), name+".js"); err != nil || !live.ValidName(name) {
				return nil, fmt.Errorf("no scene %q (see the scenes list)", *req.Scene)
			}
			l.Scene = name
		}
		if req.FPS != nil {
			l.FPS = min(max(*req.FPS, 0), 60)
		}
		if req.CharMM != nil {
			if *req.CharMM != 0 && (*req.CharMM < 1.5 || *req.CharMM > 40) {
				return nil, errors.New("charMM is the character height in mm: 1.5 to 40, or 0 for the scene's own")
			}
			l.CharMM = *req.CharMM
		}
		if req.Font != nil {
			l.Font = strings.TrimSpace(*req.Font)
		}
		if req.Enabled != nil {
			l.Enabled = *req.Enabled
			if l.Enabled {
				s.endOverlay(false)
			}
		}
		return nil, sess.Save()
	})
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.liveSync()
	s.liveSend("reload")
	s.live.mu.Lock()
	s.live.watchVer = ""
	s.live.mu.Unlock()
	s.events.publish("live")
	v, err = s.session(func(sess *app.Session) (any, error) { return s.liveInfo(sess), nil })
	writeJSON(w, v, err)
}

// liveRuntime is what the page needs: the scene and the desk's geometry.
func (s *Server) liveRuntime(w http.ResponseWriter, r *http.Request) {
	v, err := s.session(func(sess *app.Session) (any, error) {
		l := sess.Cfg.Live
		name := l.SceneName()
		if q := r.URL.Query().Get("scene"); q != "" && live.ValidName(q) {
			name = q
		}
		if _, _, err := live.Open(web.FS(), scenesDir(), name+".js"); err != nil {
			name = "plasma"
		}
		desk, err := liveDesk(sess)
		if err != nil {
			return nil, err
		}
		fr, _ := liveFrame(sess.Cfg.Live, name)
		return map[string]any{"scene": name, "version": sceneVersion(name), "fps": l.FPS, "charMM": l.CharMM, "font": l.Font, "desk": desk, "frame": fr}, nil
	})
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()}, nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, v, nil)
}

// liveDesk lays out the monitors of the active layout for the page.
func liveDesk(sess *app.Session) (live.Desk, error) {
	ps, err := sess.Placements("")
	if err != nil {
		return live.Desk{}, err
	}
	ds := sess.Geometry()
	rects := live.MonitorRects()
	var ms []live.Monitor
	for i, m := range sess.Monitors {
		scr := [4]int{m.DesktopX, m.DesktopY, m.ResW, m.ResH}
		for _, rc := range rects {
			if rc[0] == m.DesktopX && rc[1] == m.DesktopY {
				scr = rc
			}
		}
		ms = append(ms, live.Monitor{ID: m.ID, Name: m.Name, Screen: scr, Display: ds[i], Place: ps[i]})
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].Screen[0] < ms[j].Screen[0] })
	return live.Build(int(metric(76)), int(metric(77)), int(metric(78)), int(metric(79)), ms), nil
}

// liveFrame is a scene's framing: saved, or the default (its canvas's middle
// at 1:1 behind the monitors).
func liveFrame(l config.Live, name string) (config.LiveFrame, bool) {
	if f, ok := l.Frames[name]; ok && f.Zoom > 0 {
		return f, true
	}
	return config.LiveFrame{Zoom: live.WorldScale}, false
}

func (s *Server) getLiveFrame(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("scene")
	v, err := s.session(func(sess *app.Session) (any, error) {
		if name == "" {
			name = sess.Cfg.Live.SceneName()
		}
		if !live.ValidName(name) {
			return nil, fmt.Errorf("bad scene name %q", name)
		}
		desk, err := liveDesk(sess)
		if err != nil {
			return nil, err
		}
		fr, saved := liveFrame(sess.Cfg.Live, name)
		return map[string]any{"scene": name, "width": desk.World[0], "height": desk.World[1], "box": desk.Box, "frame": fr, "saved": saved,
			"default": config.LiveFrame{Zoom: live.WorldScale}}, nil
	})
	writeJSON(w, v, err)
}

func (s *Server) setLiveFrame(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scene string `json:"scene"`
		Reset bool   `json:"reset"`
		config.LiveFrame
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if !live.ValidName(req.Scene) {
		writeJSON(w, nil, fmt.Errorf("bad scene name %q", req.Scene))
		return
	}
	_, err := s.session(func(sess *app.Session) (any, error) {
		l := &sess.Cfg.Live
		if req.Reset {
			delete(l.Frames, req.Scene)
		} else {
			f := req.LiveFrame
			if f.Zoom < 0.05 || f.Zoom > 40 {
				return nil, errors.New("zoom is out of range")
			}
			if l.Frames == nil {
				l.Frames = map[string]config.LiveFrame{}
			}
			l.Frames[req.Scene] = f
		}
		return nil, sess.Save()
	})
	if err == nil {
		s.liveSend("reload")
		s.events.publish("live")
	}
	writeJSON(w, map[string]any{"saved": err == nil}, err)
}

func (s *Server) liveReport(w http.ResponseWriter, r *http.Request) {
	var rep map[string]any
	if err := readJSON(r, &rep); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.live.mu.Lock()
	s.live.report, s.live.reportAt = rep, time.Now()
	s.live.mu.Unlock()
	if e, ok := rep["error"].(string); ok && e != "" {
		s.log.Printf("live: page: %s", e)
	} else if rep["loaded"] == true {
		s.log.Printf("live: page loaded %v (%v of %v monitors visible, %vx%v cells)", rep["scene"], rep["visible"], rep["monitors"], rep["cols"], rep["rows"])
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getScene(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !live.ValidName(name) && !strings.HasPrefix(name, "_") {
		writeJSON(w, nil, fmt.Errorf("bad scene name %q", name))
		return
	}
	data, _, err := live.Open(web.FS(), scenesDir(), name+".js")
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	_, userErr := os.Stat(filepath.Join(scenesDir(), name+".js"))
	writeJSON(w, map[string]any{"name": name, "code": string(data), "user": userErr == nil, "path": filepath.Join(scenesDir(), name+".js")}, nil)
}

func (s *Server) setScene(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Code     string `json:"code"`
		Activate bool   `json:"activate"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	req.Name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(req.Name)), ".js")
	if err := live.Write(scenesDir(), req.Name, req.Code); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.afterSceneWrite(req.Name, req.Activate)
	v, err := s.session(func(sess *app.Session) (any, error) { return s.liveInfo(sess), nil })
	writeJSON(w, v, err)
}

// afterSceneWrite shows a new or changed scene: switches to it if asked,
// reloads the page if it is the one on screen.
func (s *Server) afterSceneWrite(name string, activate bool) {
	s.session(func(sess *app.Session) (any, error) {
		if activate {
			sess.Cfg.Live.Scene, sess.Cfg.Live.Enabled = name, true
			return nil, sess.Save()
		}
		return nil, nil
	})
	s.liveSync()
	s.liveSend("reload")
	s.live.mu.Lock()
	s.live.watchVer = ""
	s.live.mu.Unlock()
	s.events.publish("live")
}

var videoExt = map[string]bool{".mp4": true, ".webm": true, ".mkv": true, ".mov": true, ".avi": true, ".m4v": true}

func (s *Server) liveConvert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string           `json:"path"`
		Name     string           `json:"name"`
		Cols     int              `json:"cols"`
		Mono     bool             `json:"mono"`
		FPS      float64          `json:"fps"`     // videos: frames per second to take (default 15)
		Seconds  float64          `json:"seconds"` // videos: how much to take (default 20)
		Activate bool             `json:"activate"`
		Opts     live.ClipOptions `json:"options"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, nil, err)
		return
	}
	req.Name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(req.Name)), ".js")
	if req.Name == "" {
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path)))
		var b strings.Builder
		for _, c := range base {
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
				b.WriteRune(c)
			} else if c == ' ' {
				b.WriteRune('-')
			}
		}
		req.Name = strings.Trim(b.String(), "-_")
	}
	if !live.ValidName(req.Name) {
		writeJSON(w, nil, fmt.Errorf("give the scene a name: lower-case letters, digits, - and _"))
		return
	}
	if req.Cols <= 0 {
		req.Cols = 240
	}
	frames, delays, err := loadFrames(req.Path, req.FPS, req.Seconds, req.Cols)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	clip, err := live.Convert(frames, delays, req.Cols, 0.55, !req.Mono)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if err := live.WriteClip(scenesDir(), req.Name, clip, req.Opts); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.log.Printf("live: converted %s -> %s (%d frames, %dx%d)", req.Path, req.Name, len(clip.Frames), clip.Cols, clip.Rows)
	s.afterSceneWrite(req.Name, req.Activate)
	writeJSON(w, map[string]any{"scene": req.Name, "frames": len(clip.Frames), "cols": clip.Cols, "rows": clip.Rows}, nil)
}

// loadFrames reads a GIF, a still image, or (with ffmpeg on the PATH) a video.
func loadFrames(p string, fps, seconds float64, cols int) ([]image.Image, []int, error) {
	if p == "" {
		return nil, nil, errors.New("path is required")
	}
	if _, err := os.Stat(p); err != nil {
		return nil, nil, fmt.Errorf("file not found: %s", p)
	}
	ext := strings.ToLower(filepath.Ext(p))
	switch {
	case ext == ".gif":
		f, err := os.Open(p)
		if err != nil {
			return nil, nil, err
		}
		defer f.Close()
		return live.DecodeGIF(f)
	case videoExt[ext]:
		return videoFrames(p, fps, seconds, cols)
	default:
		im, err := imageio.Load(p)
		if err != nil {
			return nil, nil, err
		}
		return []image.Image{im}, []int{1000}, nil
	}
}

func videoFrames(p string, fps, seconds float64, cols int) ([]image.Image, []int, error) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, nil, errors.New("converting videos needs ffmpeg on the PATH (winget install ffmpeg); GIFs and pictures work without it")
	}
	if fps <= 0 {
		fps = 15
	}
	if seconds <= 0 {
		seconds = 20
	}
	tmp, err := os.MkdirTemp("", "spanwall-frames-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	cmd := exec.Command(ff, "-v", "error", "-t", fmt.Sprint(seconds), "-i", p,
		"-vf", fmt.Sprintf("fps=%g,scale=%d:-2", fps, max(64, cols*4)), filepath.Join(tmp, "f%05d.png"))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("ffmpeg: %v %s", err, strings.TrimSpace(string(out)))
	}
	names, _ := filepath.Glob(filepath.Join(tmp, "f*.png"))
	sort.Strings(names)
	var frames []image.Image
	var delays []int
	for _, n := range names {
		im, err := imageio.Load(n)
		if err != nil {
			continue
		}
		frames = append(frames, im)
		delays = append(delays, int(1000/fps))
		if len(frames) >= live.MaxFrames {
			break
		}
	}
	if len(frames) == 0 {
		return nil, nil, errors.New("ffmpeg produced no frames")
	}
	return frames, delays, nil
}

// liveOn reports whether the animation is switched on (as of the last
// liveSync, which every change to it goes through).
func (s *Server) liveOn() bool {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	return s.live.want
}

// toggleLive is for the tray.
func (s *Server) toggleLive() {
	s.session(func(sess *app.Session) (any, error) {
		sess.Cfg.Live.Enabled = !sess.Cfg.Live.Enabled
		return nil, sess.Save()
	})
	s.liveSync()
	s.events.publish("live")
}
