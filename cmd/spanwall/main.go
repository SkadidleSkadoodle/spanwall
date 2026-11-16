// Command spanwall spans one wallpaper across monitors using their real
// physical layout, independent of how Windows arranges them.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/SkadidleSkadoodle/spanwall/internal/app"
	"github.com/SkadidleSkadoodle/spanwall/internal/client"
	"github.com/SkadidleSkadoodle/spanwall/internal/install"
	"github.com/SkadidleSkadoodle/spanwall/internal/launch"
	"github.com/SkadidleSkadoodle/spanwall/internal/mcpserver"
)

// version is set by release builds (-ldflags "-X main.version=1.0.0").
var version = "1.2.1"

const usage = `spanwall %s

Wallpaper:
  spanwall apply [--mode span|per-display] [--layout name] [--dry-run] [--no-trim] <image> [images...]
  spanwall identify [--seconds N]      numbered test card on each monitor, then restore

  spanwall next | prev                  next or previous slideshow image (also without the slideshow running)
  spanwall slideshow on|off|status [--every minutes]
  spanwall wallhaven <id or link> [--apply]   download at full resolution into the wallpaper folder

Monitors and layouts (positions in mm; x right, y down; top-left of the visible area):
  spanwall displays [--json]
  spanwall layout show
  spanwall layout set <mon> <x> <y>    <mon> = number, ID, name or brand
  spanwall layout move <mon> <dx> <dy>
  spanwall layout import-superpaper
  spanwall layout activate|copy|rename|delete <name> [--from layout]

Install:
  spanwall install                     install for this user (also: double-click spanwall.exe)
  spanwall uninstall [--purge]         remove (--purge also deletes settings)

Background app (http://127.0.0.1:47920) and Claude:
  spanwall start | stop                start via Task Scheduler / stop it
  spanwall serve                       run the background app in this console
  spanwall mcp                         MCP server on stdio (used by the Claude extension)
  spanwall version
`

func main() {
	app.Version = version
	if len(os.Args) < 2 {
		if doubleClicked() {
			os.Args = append(os.Args, "install")
		} else {
			fmt.Printf(usage, version)
			return
		}
	}
	cmds := map[string]func([]string) error{
		"displays": cmdDisplays,
		"identify": cmdIdentify,
		"layout":   cmdLayout,
		"apply":    cmdApply,
		"serve":    cmdServe,
		"start":    cmdStart,
		"stop":     cmdStop,
		"mcp": func([]string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			return mcpserver.Run(ctx, version)
		},
		"version":   func([]string) error { fmt.Println(version); return nil },
		"next":      func([]string) error { return apiCall("/api/slideshow/next", map[string]any{"back": false}) },
		"prev":      func([]string) error { return apiCall("/api/slideshow/next", map[string]any{"back": true}) },
		"slideshow": cmdSlideshow,
		"install":   cmdInstall,
		"uninstall": cmdUninstall,
		"pack-extension": func(a []string) error {
			if len(a) != 2 {
				return fmt.Errorf("usage: spanwall pack-extension <out dir> <spanwall-mcp.exe>")
			}
			out, err := install.PackExtension(a[0], a[1], strings.TrimSuffix(version, "-dev"))
			if err == nil {
				fmt.Println(out)
			}
			return err
		},
		"wallhaven": cmdWallhaven,
	}
	cmd, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Printf(usage, version)
		os.Exit(2)
	}
	if err := cmd(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "spanwall:", err)
		os.Exit(1)
	}
}

// apiCall posts to the background app (starting it if needed) and prints
// the reply.
func apiCall(path string, body map[string]any) error {
	if !client.Running() {
		if err := launch.Start(); err != nil {
			return err
		}
	}
	var out any
	var err error
	if body == nil {
		err = client.Get(path, &out)
	} else {
		err = client.Post(path, body, &out)
	}
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}

func cmdSlideshow(args []string) error {
	fs := flag.NewFlagSet("slideshow", flag.ContinueOnError)
	every := fs.Int("every", 0, "minutes between changes")
	if len(args) == 0 {
		args = []string{"status"}
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	body := map[string]any{}
	if *every > 0 {
		body["intervalMin"] = *every
	}
	switch args[0] {
	case "status":
		return apiCall("/api/slideshow", nil)
	case "on", "off":
		body["enabled"] = args[0] == "on"
		return apiCall("/api/slideshow", body)
	}
	return fmt.Errorf("usage: spanwall slideshow on|off|status [--every minutes]")
}

func cmdWallhaven(args []string) error {
	fs := flag.NewFlagSet("wallhaven", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "span it across the monitors now")
	var id string
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		id, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if id == "" && fs.NArg() > 0 {
		id = fs.Arg(0)
	}
	if id == "" {
		return fmt.Errorf("usage: spanwall wallhaven <id or link> [--apply]")
	}
	return apiCall("/api/wallhaven/download", map[string]any{"id": id, "apply": *apply})
}
