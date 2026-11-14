package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirsUnderRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPANWALL_HOME", root)
	c, err := CacheDir("applied")
	if err != nil {
		t.Fatal(err)
	}
	if c != filepath.Join(root, "cache", "applied") {
		t.Fatalf("cache dir = %s", c)
	}
	if _, err := os.Stat(c); err != nil {
		t.Fatal("cache dir not created")
	}
	if d, _ := ConfigDir(); d != root {
		t.Fatalf("config dir = %s", d)
	}
}
