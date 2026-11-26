package live

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestScenes(t *testing.T) {
	b := fstest.MapFS{
		"live/scenes/plasma.js":  {Data: []byte("export default {\n  name: \"Plasma\",\n  description: \"Waves.\",\n}")},
		"live/scenes/_player.js": {Data: []byte("helper")},
	}
	dir := t.TempDir()
	if err := Write(dir, "mine", "export default {\n  name: \"Mine\",\n draw(){} }"); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, "../x", "x"); err == nil {
		t.Fatal("bad name accepted")
	}
	clip := &Clip{Cols: 2, Rows: 1, Frames: []ClipFrame{{MS: 100, Chars: "ab"}}}
	if err := WriteClip(dir, "cat", clip, ClipOptions{Name: "Cat gif"}); err != nil {
		t.Fatal(err)
	}
	ls := List(b, dir)
	got := []string{}
	for _, s := range ls {
		got = append(got, s.Name+":"+s.Title)
	}
	if strings.Join(got, ",") != "cat:Cat gif,mine:Mine,plasma:Plasma" {
		t.Fatalf("list = %v", got)
	}
	if _, _, err := Open(b, dir, "_player.js"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(b, dir, "../secret"); err == nil {
		t.Fatal("path escape")
	}
	src, ver, err := Open(b, dir, "cat.js")
	if err != nil || ver == "b" || !strings.Contains(string(src), `player("cat.clip.json"`) {
		t.Fatalf("cat.js: %s %s %v", src, ver, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cat.clip.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	bi := fstest.MapFS{BuiltinDir + "/plasma.js": {Data: []byte("x")}}
	if err := Write(dir, "mine", "export default {}"); err != nil {
		t.Fatal(err)
	}
	if left, err := Delete(bi, dir, "mine"); err != nil || left {
		t.Fatalf("delete mine: %v %v", left, err)
	}
	if _, err := Delete(bi, dir, "plasma"); err == nil {
		t.Fatal("deleting a built-in should fail")
	}
	Write(dir, "plasma", "edited")
	if left, err := Delete(bi, dir, "plasma"); err != nil || !left {
		t.Fatalf("revert edit: %v %v", left, err)
	}
	if _, err := Delete(bi, dir, "nothing"); err == nil {
		t.Fatal("missing scene should fail")
	}
}
