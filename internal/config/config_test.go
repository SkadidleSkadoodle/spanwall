package config

import (
	"strings"
	"testing"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
)

func TestSuperpaperOffsets(t *testing.T) {
	dat := `[5358080938013782324]
ppi_norm_offsets = 0,1672;2560,2461;2586,0
bezel_mms = 0.0,0.0;0.0,0.0;0.0,0.0
user_diagonal_inches = None

[123]
ppi_norm_offsets = 0,0;100,0
`
	got, err := SuperpaperOffsets(strings.NewReader(dat), 3)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]float64{{0, 1672}, {2560, 2461}, {2586, 0}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("offset %d = %v, want %v", i, got[i], want[i])
		}
	}
	if _, err := SuperpaperOffsets(strings.NewReader(dat), 4); err == nil {
		t.Fatal("expected error for a display count with no saved layout")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := New()
	c.Layout("default").Placements["DEL-1"] = geometry.Placement{XMM: 10, YMM: 20}
	c.Displays["LGD-1"] = &DisplayOverride{WidthMM: 344.5}
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := c2.Layouts["default"].Placements["DEL-1"]; p.XMM != 10 || p.YMM != 20 {
		t.Fatalf("placement lost: %+v", p)
	}
	d := c2.ApplyOverride(geometry.Display{ID: "LGD-1", WidthMM: 345, HeightMM: 215})
	if d.WidthMM != 344.5 || d.HeightMM != 215 {
		t.Fatalf("override not applied: %+v", d)
	}
}
