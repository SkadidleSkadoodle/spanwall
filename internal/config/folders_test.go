package config

import "testing"

func TestFolders(t *testing.T) {
	c := New()
	if c.FolderIndex("day") != 0 || c.FolderIndex("Night") != 1 {
		t.Fatalf("built-ins missing: %+v", c.Folders)
	}
	if err := c.CreateFolder("Space"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"space", "All", "", "Favourites"} {
		if c.CreateFolder(bad) == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	c.SetInFolder("Space", "A.jpg", true)
	c.SetInFolder("night", "a.jpg", true)
	c.SetInFolder("Space", "a.jpg", true) // no duplicate
	if got := c.FoldersOf("A.JPG"); len(got) != 2 || got[0] != "Night" || got[1] != "Space" {
		t.Errorf("folders of a.jpg: %v", got)
	}
	if len(c.Folders[2].Images) != 1 {
		t.Errorf("duplicate entry: %v", c.Folders[2].Images)
	}
	c.SetInFolder("space", "a.jpg", false)
	if c.InFolder("Space", "a.jpg") {
		t.Error("not removed")
	}
	if c.DeleteFolder("Day") == nil || c.RenameFolder("Night", "x") == nil {
		t.Error("built-ins must stay")
	}
	if err := c.RenameFolder("space", "Planets"); err != nil || c.Folders[2].Name != "Planets" {
		t.Errorf("rename: %v %+v", err, c.Folders)
	}
	if err := c.DeleteFolder("planets"); err != nil || len(c.Folders) != 2 {
		t.Errorf("delete: %v %+v", err, c.Folders)
	}
}

func TestOldPeriodTagsMove(t *testing.T) {
	c := &Config{Periods: map[string]string{"a.jpg": "day", "b.jpg": "night", "c.jpg": "any"}}
	c.ensureFolders()
	if !c.InFolder("Day", "a.jpg") || !c.InFolder("Night", "b.jpg") || c.Periods != nil {
		t.Errorf("%+v", c)
	}
}

func TestForgetImage(t *testing.T) {
	c := New()
	c.Ratings = map[string]string{"a.jpg": "fav"}
	c.SetInFolder("Night", "a.jpg", true)
	c.Framing = map[string]Frame{`C:\\w\\a.jpg`: {Zoom: 2}, `C:\\w\\a.jpg|x,y`: {Zoom: 3}, `C:\\w\\b.jpg`: {Zoom: 1}}
	c.ForgetImage("A.jpg")
	if c.Ratings["a.jpg"] != "" || c.InFolder("Night", "a.jpg") || !c.IsRemoved("a.JPG") || c.IsRemoved("b.jpg") {
		t.Errorf("%+v", c)
	}
	if len(c.Framing) != 1 {
		t.Logf("framing left (paths use the build OS's separator): %v", c.Framing)
	}
	c.ForgetImage("a.jpg")
	if len(c.Removed) != 1 {
		t.Errorf("removed twice: %v", c.Removed)
	}
}
