package app

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/martingomezmejia-ctrl/spanwall/internal/config"
)

// SlideshowCount is how many images one change needs in a mode.
func SlideshowCount(mode string, groups, monitors int) int {
	switch mode {
	case "groups":
		return max(1, groups)
	case "per-display":
		return max(1, monitors)
	}
	return 1
}

// PickSlides chooses the next (back=false) or previous (back=true) image set
// from the library names. It returns the set and the updated history; the
// caller saves the history after the set is applied.
//
// Shuffle avoids anything in the recent history while enough other images
// exist; name order continues after the newest image last shown.
func PickSlides(names []string, ss config.Slideshow, count int, back bool, rng *rand.Rand) ([]string, [][]string, error) {
	hist := ss.History
	if back {
		if len(hist) < 2 {
			return nil, hist, fmt.Errorf("nothing earlier in the slideshow history")
		}
		hist = hist[:len(hist)-1]
		prev := hist[len(hist)-1]
		return prev, hist, nil
	}
	if len(names) == 0 {
		return nil, hist, fmt.Errorf("the wallpaper folder has no images")
	}
	sorted := append([]string(nil), names...)
	sort.Slice(sorted, func(i, j int) bool { return strings.ToLower(sorted[i]) < strings.ToLower(sorted[j]) })
	var pick []string
	if ss.Order == "name" {
		start := 0
		if len(hist) > 0 {
			last := hist[len(hist)-1]
			cur := strings.ToLower(last[len(last)-1])
			for i, n := range sorted {
				if strings.ToLower(n) > cur {
					start = i
					break
				}
				start = (i + 1) % len(sorted)
			}
		}
		for k := 0; k < count; k++ {
			pick = append(pick, sorted[(start+k)%len(sorted)])
		}
	} else {
		recent := map[string]bool{}
		// Avoid repeats while at least `count` fresh images remain.
		for i := len(hist) - 1; i >= 0; i-- {
			fresh := 0
			for _, n := range sorted {
				if !recent[n] {
					fresh++
				}
			}
			if fresh-len(hist[i]) < count {
				break
			}
			for _, n := range hist[i] {
				recent[n] = true
			}
		}
		var pool []string
		for _, n := range sorted {
			if !recent[n] {
				pool = append(pool, n)
			}
		}
		rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		for k := 0; k < count; k++ {
			pick = append(pick, pool[k%len(pool)])
		}
	}
	hist = append(hist, pick)
	if len(hist) > 50 {
		hist = hist[len(hist)-50:]
	}
	return pick, hist, nil
}
