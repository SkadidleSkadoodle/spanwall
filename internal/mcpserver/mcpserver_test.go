package mcpserver

import (
	"context"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolsRegister(t *testing.T) {
	ctx := context.Background()
	s := mcp.NewServer(&mcp.Implementation{Name: "spanwall", Version: "test"}, nil)
	register(s)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema == nil {
			t.Errorf("%s has no input schema", tool.Name)
		}
	}
	sort.Strings(names)
	want := []string{"sw_align_from_photo", "sw_apply_alignment", "sw_calibrate", "sw_config", "sw_desktops", "sw_fix_monitors", "sw_folders", "sw_framing",
		"sw_get_layout", "sw_identify", "sw_layout_presets", "sw_list_displays", "sw_list_wallpapers", "sw_move_display", "sw_photo_pattern",
		"sw_preview", "sw_project_info", "sw_rate", "sw_remove_wallpaper", "sw_set_wallpaper", "sw_slideshow", "sw_sources_sync", "sw_start", "sw_status", "sw_stop", "sw_wallhaven_collections", "sw_wallhaven_download", "sw_wallhaven_search", "sw_wallhaven_sync"}
	if len(names) != len(want) {
		t.Fatalf("tools = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tools = %v", names)
		}
	}
}
