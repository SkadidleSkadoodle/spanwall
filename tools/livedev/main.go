// Command livedev serves the live wallpaper page with a made-up three-monitor
// desk, for working on scenes in an ordinary browser (any OS):
//
//	go run ./tools/livedev -scenes path\to\scenes
//	open http://127.0.0.1:47930/live/?preview=1&scene=plasma
//
// Without ?preview the page draws as the wallpaper would, at the size of the
// made-up virtual screen (scaled by -scale).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/SkadidleSkadoodle/spanwall/internal/geometry"
	"github.com/SkadidleSkadoodle/spanwall/internal/live"
	"github.com/SkadidleSkadoodle/spanwall/internal/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:47930", "listen address")
	scenes := flag.String("scenes", "", "user scenes folder")
	scale := flag.Float64("scale", 1, "scale the made-up screens' pixels (0.25 = quarter size)")
	flag.Parse()
	k := *scale
	px := func(v int) int { return int(float64(v) * k) }
	// A laptop left of a 34" ultrawide, with a second ultrawide above it,
	// tilted down (a perspective quad), like a typical stacked setup.
	ms := []live.Monitor{
		{ID: "LAP", Name: "Laptop", Screen: [4]int{px(-2560), 0, px(2560), px(1600)},
			Display: geometry.Display{ResW: px(2560), ResH: px(1600), WidthMM: 345, HeightMM: 215}, Place: geometry.Placement{XMM: 0, YMM: 520}},
		{ID: "MID", Name: "Ultrawide", Screen: [4]int{0, 0, px(3440), px(1440)},
			Display: geometry.Display{ResW: px(3440), ResH: px(1440), WidthMM: 800, HeightMM: 335}, Place: geometry.Placement{XMM: 380, YMM: 400}},
		{ID: "TOP", Name: "Upper ultrawide", Screen: [4]int{px(388), px(-1440), px(3440), px(1440)},
			Display: geometry.Display{ResW: px(3440), ResH: px(1440), WidthMM: 800, HeightMM: 335},
			Place:   geometry.Placement{XMM: 400, YMM: 30, Quad: []float64{410, 40, 1190, 30, 1175, 380, 425, 372}}},
	}
	desk := live.Build(px(-2560), px(-1440), px(6388), px(3040), ms)
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(web.FS())))
	mux.HandleFunc("GET /live/scenes/{file}", func(w http.ResponseWriter, r *http.Request) {
		b, _, err := live.Open(web.FS(), *scenes, r.PathValue("file"))
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		if strings.HasSuffix(r.PathValue("file"), ".js") {
			w.Header().Set("Content-Type", "text/javascript")
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/live/runtime", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("scene")
		if name == "" {
			name = "plasma"
		}
		_, ver, _ := live.Open(web.FS(), *scenes, name+".js")
		json.NewEncoder(w).Encode(map[string]any{"scene": name, "version": ver, "desk": desk})
	})
	mux.HandleFunc("POST /api/live/report", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		log.Printf("report: %s", b)
	})
	fmt.Printf("http://%s/live/?preview=1\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
