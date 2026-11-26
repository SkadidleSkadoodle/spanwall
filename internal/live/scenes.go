package live

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Scenes are ES modules: the built-in ones ship in the UI files under
// live/scenes, the user's live in .spanwall\scenes and win on a name clash.
// Names starting with "_" are helpers, not scenes.

// BuiltinDir is where the built-in scenes are inside the UI files.
const BuiltinDir = "live/scenes"

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)

// ValidName reports whether name can be a scene file name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// SceneInfo describes a scene for lists.
type SceneInfo struct {
	Name        string  `json:"name"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	FPS         int     `json:"fps,omitempty"`     // the scene's own frame rate
	CharMM      float64 `json:"charMM,omitempty"`  // the scene's own character height
	Builtin     bool    `json:"builtin,omitempty"` // ships with Spanwall
	User        bool    `json:"user,omitempty"`    // in the user's scenes folder (overrides a built-in)
}

var (
	titleRE = regexp.MustCompile(`(?m)^\s*name:\s*"([^"]*)"`)
	descRE  = regexp.MustCompile(`(?m)^\s*description:\s*"([^"]*)"`)
	fpsRE   = regexp.MustCompile(`(?m)^\s*fps:\s*(\d+)`)
	cellRE  = regexp.MustCompile(`(?m)^\s*cell:\s*\{\s*h:\s*([\d.]+)`)
	playRE  = regexp.MustCompile(`player\([^,]+,\s*(\{.*\})\)`)
)

func describe(name string, src []byte) SceneInfo {
	si := SceneInfo{Name: name, Title: name}
	if m := titleRE.FindSubmatch(src); m != nil {
		si.Title = string(m[1])
	}
	if m := descRE.FindSubmatch(src); m != nil {
		si.Description = string(m[1])
	}
	if m := fpsRE.FindSubmatch(src); m != nil {
		fmt.Sscan(string(m[1]), &si.FPS)
	}
	if m := cellRE.FindSubmatch(src); m != nil {
		fmt.Sscan(string(m[1]), &si.CharMM)
	}
	if m := playRE.FindSubmatch(src); m != nil { // converted clips keep their options as JSON
		var o struct{ Name, Description string }
		if json.Unmarshal(m[1], &o) == nil {
			if o.Name != "" {
				si.Title = o.Name
			}
			si.Description = o.Description
		}
	}
	return si
}

// List returns every scene, sorted by title.
func List(builtin fs.FS, userDir string) []SceneInfo {
	byName := map[string]*SceneInfo{}
	if ents, err := fs.ReadDir(builtin, BuiltinDir); err == nil {
		for _, e := range ents {
			n, ok := strings.CutSuffix(e.Name(), ".js")
			if !ok || !ValidName(n) {
				continue
			}
			src, _ := fs.ReadFile(builtin, path.Join(BuiltinDir, e.Name()))
			si := describe(n, src)
			si.Builtin = true
			byName[n] = &si
		}
	}
	if ents, err := os.ReadDir(userDir); err == nil {
		for _, e := range ents {
			n, ok := strings.CutSuffix(e.Name(), ".js")
			if !ok || !ValidName(n) {
				continue
			}
			src, _ := os.ReadFile(filepath.Join(userDir, e.Name()))
			si := describe(n, src)
			si.User = true
			si.Builtin = byName[n] != nil
			byName[n] = &si
		}
	}
	out := make([]SceneInfo, 0, len(byName))
	for _, si := range byName {
		out = append(out, *si)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out
}

// Open finds a scene file (or a helper or clip next to it): the user's copy
// first, then the built-in one. version changes whenever the file does.
func Open(builtin fs.FS, userDir, file string) (data []byte, version string, err error) {
	if file != path.Base(file) || strings.Contains(file, "\\") || file == "" || file[0] == '.' {
		return nil, "", fmt.Errorf("bad scene file %q", file)
	}
	up := filepath.Join(userDir, file)
	if fi, err := os.Stat(up); err == nil && !fi.IsDir() {
		b, err := os.ReadFile(up)
		return b, fmt.Sprint(fi.ModTime().UnixMilli()), err
	}
	p := path.Join(BuiltinDir, file)
	b, err := fs.ReadFile(builtin, p)
	if err != nil {
		return nil, "", fmt.Errorf("no scene file %q", file)
	}
	ver := "b"
	if fi, err := fs.Stat(builtin, p); err == nil && !fi.ModTime().IsZero() {
		ver = fmt.Sprint(fi.ModTime().UnixMilli())
	}
	return b, ver, nil
}

// Write saves a user scene. It never touches built-in files.
func Write(userDir, name, code string) error {
	if !ValidName(name) {
		return fmt.Errorf("scene names are lower-case letters, digits, - and _ (got %q)", name)
	}
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("empty scene")
	}
	if len(code) > 2<<20 {
		return fmt.Errorf("scene code is over 2 MB; put big data in a separate file")
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(userDir, name+".js"), []byte(code))
}

// Delete removes a user scene (its .js and any converted clip). Built-in
// scenes can't be deleted; deleting the user's edit of one brings the
// built-in back. It reports whether a built-in of that name remains.
func Delete(builtin fs.FS, userDir, name string) (builtinLeft bool, err error) {
	if !ValidName(name) {
		return false, fmt.Errorf("no scene %q", name)
	}
	_, statErr := fs.Stat(builtin, BuiltinDir+"/"+name+".js")
	builtinLeft = statErr == nil
	removed := false
	for _, f := range []string{name + ".js", name + ".clip.json"} {
		if err := os.Remove(filepath.Join(userDir, f)); err == nil {
			removed = true
		} else if !os.IsNotExist(err) {
			return builtinLeft, err
		}
	}
	if !removed {
		if builtinLeft {
			return true, fmt.Errorf("%s is built in and can't be deleted; ban it to keep it out of the slideshow", name)
		}
		return false, fmt.Errorf("no scene %q", name)
	}
	return builtinLeft, nil
}

// ClipOptions are how a converted clip plays.
type ClipOptions struct {
	Name        string  `json:"name,omitempty"`
	Description string  `json:"description,omitempty"`
	Fit         string  `json:"fit,omitempty"`   // cover (default) or contain
	Color       *int    `json:"color,omitempty"` // tint for monochrome clips
	CharMM      float64 `json:"charMM,omitempty"`
	Background  string  `json:"background,omitempty"`
}

// WriteClip saves a converted clip and the one-line scene that plays it.
func WriteClip(userDir, name string, clip *Clip, opts ClipOptions) error {
	if !ValidName(name) {
		return fmt.Errorf("scene names are lower-case letters, digits, - and _ (got %q)", name)
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(clip)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(userDir, name+".clip.json"), data); err != nil {
		return err
	}
	if opts.Name == "" {
		opts.Name = name
	}
	if opts.Description == "" {
		opts.Description = fmt.Sprintf("Converted clip, %d frames, %dx%d characters.", len(clip.Frames), clip.Cols, clip.Rows)
	}
	o, _ := json.Marshal(opts)
	code := fmt.Sprintf("// Made by Spanwall from a picture, GIF or video (%s).\nimport player from \"./_player.js\";\nexport default player(%q, %s);\n",
		time.Now().Format("2006-01-02"), name+".clip.json", o)
	return writeAtomic(filepath.Join(userDir, name+".js"), []byte(code))
}

func writeAtomic(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
