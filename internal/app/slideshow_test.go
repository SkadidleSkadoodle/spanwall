package app

import (
	"math/rand"
	"testing"

	"github.com/SkadidleSkadoodle/spanwall/internal/config"
)

func TestShuffleAvoidsRecentAndGoesBack(t *testing.T) {
	names := []string{"a.png", "b.png", "c.png", "d.png", "e.png"}
	ss := config.Slideshow{}
	rng := rand.New(rand.NewSource(1))
	seen := map[string]int{}
	for i := 0; i < 5; i++ {
		pick, hist, err := PickSlides(names, ss, 1, false, rng, nil)
		if err != nil {
			t.Fatal(err)
		}
		ss.History = hist
		seen[pick[0]]++
	}
	if len(seen) != 5 {
		t.Errorf("5 changes should show 5 different images, got %v", seen)
	}
	prev, hist, err := PickSlides(names, ss, 1, true, rng, nil)
	if err != nil || prev[0] != ss.History[3][0] || len(hist) != 4 {
		t.Errorf("back: %v %v %v", prev, len(hist), err)
	}
}

func TestNameOrderWraps(t *testing.T) {
	names := []string{"b.png", "a.png", "c.png"}
	ss := config.Slideshow{Order: "name", History: [][]string{{"c.png"}}}
	pick, _, _ := PickSlides(names, ss, 2, false, rand.New(rand.NewSource(1)), nil)
	if pick[0] != "a.png" || pick[1] != "b.png" {
		t.Errorf("got %v", pick)
	}
}

func TestWeightsBanFavouriteAndFolders(t *testing.T) {
	lib := []LibraryImage{{Name: "Day.jpg"}, {Name: "night.jpg"}, {Name: "fav.jpg"}, {Name: "ban.jpg"}}
	c := config.New()
	c.Ratings = map[string]string{"fav.jpg": "fav", "ban.jpg": "ban"}
	c.SetInFolder("Day", "day.jpg", true)
	c.SetInFolder("Night", "night.jpg", true)
	c.SetInFolder("Night", "fav.jpg", true)
	c.SetInFolder("Night", "ban.jpg", true)
	w := SlideWeights(lib, c, "")
	if w["ban.jpg"] != 0 || w["fav.jpg"] != favouriteWeight || w["Day.jpg"] != 1 {
		t.Errorf("no period: %v", w)
	}
	w = SlideWeights(lib, c, "night")
	if w["Day.jpg"] != 0 || w["night.jpg"] != 1 || w["fav.jpg"] != favouriteWeight || w["ban.jpg"] != 0 {
		t.Errorf("night: %v", w)
	}
	names := []string{"Day.jpg", "night.jpg", "fav.jpg", "ban.jpg"}
	rng := rand.New(rand.NewSource(2))
	count := map[string]int{}
	for i := 0; i < 3000; i++ {
		p, _, err := PickSlides(names, config.Slideshow{}, 1, false, rng, w)
		if err != nil {
			t.Fatal(err)
		}
		count[p[0]]++
	}
	if count["Day.jpg"] != 0 || count["ban.jpg"] != 0 {
		t.Errorf("excluded images shown: %v", count)
	}
	if r := float64(count["fav.jpg"]) / float64(count["night.jpg"]); r < 2.3 || r > 3.8 {
		t.Errorf("favourite should come up about 3x as often: %v", count)
	}
	// An empty Day folder means day ignores the folders.
	c.SetInFolder("Day", "day.jpg", false)
	if w := SlideWeights(lib, c, "day"); w["night.jpg"] != 1 {
		t.Errorf("empty Day folder: %v", w)
	}
}

func TestAlbumSource(t *testing.T) {
	lib := []LibraryImage{{Name: "a.jpg"}, {Name: "b.jpg"}, {Name: "c.jpg"}}
	c := config.New()
	c.CreateFolder("Space")
	c.SetInFolder("Space", "b.jpg", true)
	c.SetInFolder("Space", "c.jpg", true)
	c.Ratings = map[string]string{"c.jpg": "ban", "a.jpg": "fav"}
	c.Slideshow.Source = "space"
	w := SlideWeights(lib, c, "")
	if w["a.jpg"] != 0 || w["b.jpg"] != 1 || w["c.jpg"] != 0 {
		t.Errorf("album Space: %v", w)
	}
	c.Slideshow.Source = "fav"
	if w := SlideWeights(lib, c, ""); w["a.jpg"] == 0 || w["b.jpg"] != 0 {
		t.Errorf("favourites: %v", w)
	}
	c.RenameFolder("Space", "Planets")
	c.Slideshow.Source = "Planets"
	c.DeleteFolder("planets")
	if c.Slideshow.Source != "" {
		t.Errorf("deleting the album should fall back to all: %q", c.Slideshow.Source)
	}
}
