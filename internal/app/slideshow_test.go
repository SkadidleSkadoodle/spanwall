package app

import (
	"math/rand"
	"testing"

	"github.com/martingomezmejia-ctrl/spanwall/internal/config"
)

func TestShuffleAvoidsRecentAndGoesBack(t *testing.T) {
	names := []string{"a.png", "b.png", "c.png", "d.png", "e.png"}
	ss := config.Slideshow{}
	rng := rand.New(rand.NewSource(1))
	seen := map[string]int{}
	for i := 0; i < 5; i++ {
		pick, hist, err := PickSlides(names, ss, 1, false, rng)
		if err != nil {
			t.Fatal(err)
		}
		ss.History = hist
		seen[pick[0]]++
	}
	if len(seen) != 5 {
		t.Errorf("5 changes should show 5 different images, got %v", seen)
	}
	prev, hist, err := PickSlides(names, ss, 1, true, rng)
	if err != nil || prev[0] != ss.History[3][0] || len(hist) != 4 {
		t.Errorf("back: %v %v %v", prev, len(hist), err)
	}
}

func TestNameOrderWraps(t *testing.T) {
	names := []string{"b.png", "a.png", "c.png"}
	ss := config.Slideshow{Order: "name", History: [][]string{{"c.png"}}}
	pick, _, _ := PickSlides(names, ss, 2, false, rand.New(rand.NewSource(1)))
	if pick[0] != "a.png" || pick[1] != "b.png" {
		t.Errorf("got %v", pick)
	}
}
