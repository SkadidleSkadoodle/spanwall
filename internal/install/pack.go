// Package install packs the Claude Desktop extension and installs Spanwall
// for the current user.
package install

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/SkadidleSkadoodle/spanwall/internal/mcpserver"
)

// PackExtension writes Spanwall.mcpb into dir. The extension runs mcpExe
// (an absolute path, outside Claude's package folders, so the background
// app it starts can share files with it).
func PackExtension(dir, mcpExe, version string) (string, error) {
	tools, err := mcpserver.Tools(context.Background())
	if err != nil {
		return "", err
	}
	manifest := map[string]any{
		"manifest_version": "0.3",
		"name":             "spanwall",
		"display_name":     "Spanwall",
		"version":          version,
		"description":      "Span one wallpaper across monitors by their real physical layout. Preview, set and align wallpapers from chat. Talks to the Spanwall background app at 127.0.0.1:47920.",
		"author":           map[string]string{"name": "Spanwall contributors"},
		"repository":       map[string]string{"type": "git", "url": "https://github.com/SkadidleSkadoodle/spanwall"},
		"server": map[string]any{
			"type":        "binary",
			"entry_point": "spanwall-mcp.exe",
			"mcp_config":  map[string]any{"command": mcpExe, "args": []string{"mcp"}},
		},
		"tools":         tools,
		"compatibility": map[string]any{"platforms": []string{"win32"}},
		"keywords":      []string{"wallpaper", "multi-monitor", "spanwall"},
		"license":       "MIT",
	}
	out := filepath.Join(dir, "Spanwall.mcpb")
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("manifest.json")
	if err == nil {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		err = enc.Encode(manifest)
	}
	if err == nil {
		err = addFile(zw, mcpExe, "spanwall-mcp.exe")
	}
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	os.Remove(out)
	return out, os.Rename(tmp, out)
}

func addFile(zw *zip.Writer, path, name string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}
