package install

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPackExtension(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "spanwall-mcp.exe")
	os.WriteFile(exe, []byte("MZ"), 0o755)
	out, err := PackExtension(dir, exe, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var m map[string]any
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			r, _ := f.Open()
			json.NewDecoder(r).Decode(&m)
			r.Close()
		}
	}
	if m["version"] != "1.2.3" || len(m["tools"].([]any)) < 20 {
		t.Errorf("manifest %v", m)
	}
	if len(zr.File) != 2 {
		t.Errorf("%d files", len(zr.File))
	}
}
