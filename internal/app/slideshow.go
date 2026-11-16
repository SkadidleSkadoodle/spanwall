package app

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/SkadidleSkadoodle/spanwall/internal/config"
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
//
// weights (by name; nil = all equal) bias shuffle towards heavier images
// and leave out any with weight 0 (banned, or the wrong time of day).
func PickSlides(names []string, ss config.Slideshow, count int, back bool, rng *rand.Rand, weights map[string]float64) ([]string, [][]string, error) {
	hist := ss.History
	if back {
		if len(hist) < 2 {
			return nil, hist, fmt.Errorf("nothing earlier in the slideshow history")
		}
		hist = hist[:len(hist)-1]
		prev := hist[len(hist)-1]
		return prev, hist, nil
	}
	if weights != nil {
		var keep []string
		for _, n := range names {
			if weights[n] > 0 {
				keep = append(keep, n)
			}
		}
		names = keep
	}
	if len(names) == 0 {
		return nil, hist, fmt.Errorf("the wallpaper folder has no images to show (check banned images)")
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
		// Weighted shuffle: sort by an exponential key with rate = weight,
		// so an image of weight 3 tends to come up three times as often.
		key := make(map[string]float64, len(pool))
		for _, n := range pool {
			w := 1.0
			if weights != nil {
				w = weights[n]
			}
			key[n] = -math.Log(1-rng.Float64()) / w
		}
		sort.SliceStable(pool, func(i, j int) bool { return key[pool[i]] < key[pool[j]] })
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

// Favourite images come up this many times as often in shuffle.
const favouriteWeight = 3

// SlideWeights rates every library image for the slideshow: banned images
// get 0, favourites favouriteWeight, the rest 1. With a day/night period
// ("day" or "night"), only images in the Day or Night folder count (unless
// that folder has none of the library's images, then the period is
// ignored).
func SlideWeights(lib []LibraryImage, c *config.Config, period string) map[string]float64 {
	w := map[string]float64{}
	for _, im := range lib {
		switch c.Ratings[strings.ToLower(im.Name)] {
		case "ban":
			w[im.Name] = 0
		case "fav":
			w[im.Name] = favouriteWeight
		default:
			w[im.Name] = 1
		}
	}
	folder := map[string]string{"day": "Day", "night": "Night"}[period]
	if folder == "" {
		// No day/night: shuffle the chosen album.
		switch src := c.Slideshow.Source; src {
		case "":
		case "fav":
			for _, im := range lib {
				if c.Ratings[strings.ToLower(im.Name)] != "fav" {
					w[im.Name] = 0
				}
			}
		default:
			for _, im := range lib {
				if !c.InFolder(src, im.Name) {
					w[im.Name] = 0
				}
			}
		}
		return w
	}
	timed := map[string]float64{}
	any := false
	for _, im := range lib {
		v := 0.0
		if c.InFolder(folder, im.Name) {
			v = w[im.Name]
		}
		timed[im.Name] = v
		any = any || v > 0
	}
	if !any {
		return w
	}
	return timed
}
