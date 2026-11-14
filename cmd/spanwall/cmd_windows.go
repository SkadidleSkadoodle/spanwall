//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/martingomezmejia-ctrl/spanwall/internal/app"
	"github.com/martingomezmejia-ctrl/spanwall/internal/display"
	"github.com/martingomezmejia-ctrl/spanwall/internal/install"
	"github.com/martingomezmejia-ctrl/spanwall/internal/launch"
	"github.com/martingomezmejia-ctrl/spanwall/internal/server"
)

// withSession opens COM and a session for one command.
func withSession(fn func(*app.Session) error) error {
	dw, done, err := app.OpenCOM()
	if err != nil {
		return err
	}
	defer done()
	s, err := app.NewSession(dw)
	if err != nil {
		return err
	}
	return fn(s)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdDisplays(args []string) error {
	fs := flag.NewFlagSet("displays", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON (all monitors, including detached)")
	fs.Parse(args)
	dw, done, err := app.OpenCOM()
	if err != nil {
		return err
	}
	defer done()
	ms, err := display.List(dw)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(ms)
	}
	for i, m := range app.AttachedSorted(ms) {
		fmt.Printf("%d  %-16s %-22s %4dx%-4d px  %5.0fx%-4.0f mm  %5.1f PPI  windows at %d,%d\n",
			i+1, m.Name, m.ID, m.ResW, m.ResH, m.WidthMM, m.HeightMM, m.PPI, m.DesktopX, m.DesktopY)
	}
	for _, m := range ms {
		if !m.Attached {
			fmt.Printf("-  %-16s %-22s not attached\n", m.Name, m.ID)
		}
	}
	return nil
}

func cmdIdentify(args []string) error {
	fs := flag.NewFlagSet("identify", flag.ExitOnError)
	secs := fs.Int("seconds", 10, "how long to show the cards")
	stopFile := fs.String("stop-file", "", "restore early as soon as this file exists")
	fs.Parse(args)
	return withSession(func(s *app.Session) error {
		saved, err := s.ShowIdentify()
		if err != nil {
			return err
		}
		fmt.Printf("Showing %d cards for %d s. Ctrl+C restores early.\n", len(s.Monitors), *secs)
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		deadline := time.After(time.Duration(*secs) * time.Second)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
	wait:
		for {
			select {
			case <-deadline:
				break wait
			case <-stop:
				break wait
			case <-tick.C:
				if *stopFile != "" {
					if _, err := os.Stat(*stopFile); err == nil {
						os.Remove(*stopFile)
						break wait
					}
				}
			}
		}
		if err := s.RestoreWallpaper(saved); err != nil {
			return fmt.Errorf("restore: %w", err)
		}
		fmt.Println("Previous wallpaper restored.")
		return nil
	})
}

func cmdLayout(args []string) error {
	fs := flag.NewFlagSet("layout", flag.ExitOnError)
	name := fs.String("layout", "", "layout name (default: the active layout)")
	file := fs.String("file", "", "Superpaper display_systems.dat (import-superpaper only)")
	from := fs.String("from", "", "source layout (copy, rename)")
	fs.Parse(args) // flags may come before the subcommand...
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: spanwall layout [--layout name] show|set|move|import-superpaper|activate|copy|rename|delete ...")
	}
	sub := fs.Arg(0)
	fs.Parse(fs.Args()[1:]) // ...or right after it
	rest := fs.Args()

	return withSession(func(s *app.Session) error {
		switch sub {
		case "show":
			info := s.Layout(*name)
			fmt.Printf("layout %q (active: %q, saved: %v)\n", info.Name, info.Active, info.Saved)
			for _, m := range info.Monitors {
				if m.Placed {
					fmt.Printf("%d  %-16s x %7.1f mm  y %7.1f mm  (%.0f x %.0f mm)\n", m.Number, m.Name, m.XMM, m.YMM, m.WidthMM, m.HeightMM)
				} else {
					fmt.Printf("%d  %-16s no position yet\n", m.Number, m.Name)
				}
			}
			return nil
		case "set", "move":
			if len(rest) != 3 {
				return fmt.Errorf("usage: spanwall layout %s <monitor> <x mm> <y mm>", sub)
			}
			x, err1 := strconv.ParseFloat(rest[1], 64)
			y, err2 := strconv.ParseFloat(rest[2], 64)
			if err1 != nil || err2 != nil {
				return fmt.Errorf("x and y must be numbers in mm")
			}
			p, err := s.PlaceMonitor(*name, rest[0], x, y, sub == "move")
			if err != nil {
				return err
			}
			fmt.Printf("%s now at x %.1f mm, y %.1f mm in layout %q\n", p.Name, p.XMM, p.YMM, s.LayoutName(*name))
			return nil
		case "import-superpaper":
			info, err := s.ImportSuperpaper(*name, *file)
			if err != nil {
				return err
			}
			fmt.Printf("Imported Superpaper's layout into %q\n", info.Name)
			return nil
		case "activate", "copy", "rename", "delete":
			if len(rest) != 1 {
				return fmt.Errorf("usage: spanwall layout %s <name> [--from layout]", sub)
			}
			info, err := s.LayoutAction(sub, rest[0], *from)
			if err != nil {
				return err
			}
			fmt.Printf("ok; active layout %q, saved %v\n", info.Active, info.Saved)
			return nil
		}
		return fmt.Errorf("unknown layout command %q", sub)
	})
}

func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	mode := fs.String("mode", "span", "span or per-display")
	layoutName := fs.String("layout", "", "layout to use (default: the active layout)")
	dryRun := fs.Bool("dry-run", false, "render and write the preview, but don't change the wallpaper")
	noTrim := fs.Bool("no-trim", false, "keep black letterbox bars")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: spanwall apply [--mode span|per-display] [--dry-run] [--no-trim] <image> [more images]")
	}
	var images []string
	for _, p := range fs.Args() {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		images = append(images, abs)
	}
	return withSession(func(s *app.Session) error {
		res, err := s.Apply(app.ApplyRequest{Images: images, Mode: *mode, Layout: *layoutName, DryRun: *dryRun, NoTrim: *noTrim})
		if err != nil {
			return err
		}
		for _, n := range res.Notes {
			fmt.Println("-", n)
		}
		if res.Applied {
			fmt.Printf("Applied %s with layout %q in %d ms (render %d ms). Preview: %s\n", res.Mode, res.Layout, res.TotalMS, res.RenderMS, res.Preview)
		} else {
			fmt.Printf("Dry run in %d ms. Preview: %s\n", res.TotalMS, res.Preview)
		}
		return nil
	})
}

func cmdServe([]string) error { return server.Run() }

func cmdStart([]string) error {
	if err := launch.Start(); err != nil {
		return err
	}
	fmt.Println("Spanwall background app is running at http://" + server.Addr)
	return nil
}

func cmdStop([]string) error {
	if err := launch.Stop(); err != nil {
		return err
	}
	fmt.Println("Spanwall background app stopped.")
	return nil
}

func doubleClicked() bool { return install.LaunchedFromExplorer() }

func cmdInstall(args []string) error {
	pause := install.LaunchedFromExplorer()
	fmt.Printf("Installing Spanwall %s for this user\n", version)
	err := install.Install(strings.TrimSuffix(version, "-dev"), func(s string) { fmt.Println("  " + s) })
	if err == nil {
		fmt.Println("\nDone. Finish setup in the browser page that just opened.")
		fmt.Println("To use Spanwall from Claude, open " + install.Dir() + "\\Spanwall.mcpb (Claude Desktop installs it).")
	}
	if pause {
		if err != nil {
			fmt.Println("\nInstall failed:", err)
		}
		fmt.Print("\nPress Enter to close.")
		fmt.Scanln()
	}
	return err
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "also delete settings in %USERPROFILE%\\.spanwall")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return install.Uninstall(*purge, func(s string) { fmt.Println(s) })
}
