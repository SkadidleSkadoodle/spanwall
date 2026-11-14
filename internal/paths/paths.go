// Package paths decides where Spanwall keeps its files.
//
// Everything lives in %USERPROFILE%\.spanwall rather than AppData. A process
// started from an MSIX packaged app (such as anything the Claude desktop app
// launches) has its AppData writes silently redirected into the package's
// private storage, so the background app (started outside the package) and
// the command line or MCP server (started inside it) would otherwise see two
// different config files, and Explorer couldn't read the wallpaper files.
// The user profile folder is never redirected.
package paths

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Root returns %USERPROFILE%\.spanwall (or $HOME/.spanwall), created if needed.
// SPANWALL_HOME overrides it, which tests use.
func Root() (string, error) {
	dir := os.Getenv("SPANWALL_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".spanwall")
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// ConfigDir is where config.json lives (the root).
func ConfigDir() (string, error) {
	dir, err := Root()
	if err != nil {
		return "", err
	}
	migrateConfig(dir)
	return dir, nil
}

// CacheDir returns Root\cache\<sub>, created if needed. Rendered slices,
// previews and test cards live here.
func CacheDir(sub string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "cache", sub)
	return dir, os.MkdirAll(dir, 0o755)
}

// LogPath is the background app's log file.
func LogPath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "spanwall.log"), nil
}

// migrateConfig copies a config.json written by early builds (which used
// AppData, possibly redirected into a package's LocalCache) if there is no
// config in the root yet.
func migrateConfig(root string) {
	dst := filepath.Join(root, "config.json")
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		return
	}
	var candidates []string
	if cfg, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(cfg, "spanwall", "config.json"))
	}
	if local, err := os.UserCacheDir(); err == nil {
		m, _ := filepath.Glob(filepath.Join(local, "Packages", "*", "LocalCache", "Roaming", "spanwall", "config.json"))
		candidates = append(candidates, m...)
	}
	for _, c := range candidates {
		if copyFile(c, dst) == nil {
			return
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
