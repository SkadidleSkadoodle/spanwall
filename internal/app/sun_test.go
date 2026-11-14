package app

import (
	"testing"
	"time"
)

func TestSunTimesToronto(t *testing.T) {
	loc := time.FixedZone("EDT", -4*3600)
	rise, set, ok := SunTimes(time.Date(2026, 6, 21, 9, 0, 0, 0, loc), 43.65, -79.38)
	if !ok {
		t.Fatal("no sunrise")
	}
	// Published: about 5:36 and 21:03 EDT.
	want := func(got time.Time, h, m int) {
		w := time.Date(2026, 6, 21, h, m, 0, 0, loc)
		if d := got.Sub(w); d < -5*time.Minute || d > 5*time.Minute {
			t.Errorf("got %v, want about %v", got.Format("15:04"), w.Format("15:04"))
		}
	}
	want(rise, 5, 36)
	want(set, 21, 3)
	if p := SunPeriod(time.Date(2026, 6, 21, 23, 0, 0, 0, loc), 43.65, -79.38); p != "night" {
		t.Errorf("23:00 is %s", p)
	}
	if p := SunPeriod(time.Date(2026, 6, 21, 13, 0, 0, 0, loc), 43.65, -79.38); p != "day" {
		t.Errorf("13:00 is %s", p)
	}
}
