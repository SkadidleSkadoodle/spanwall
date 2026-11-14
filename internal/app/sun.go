package app

import (
	"math"
	"time"
)

// SunTimes returns sunrise and sunset on t's date at lat, lon (degrees,
// east positive), in t's location. ok is false where the sun doesn't rise
// or set that day (polar day or night). Accurate to a minute or two.
func SunTimes(t time.Time, lat, lon float64) (rise, set time.Time, ok bool) {
	rad := math.Pi / 180
	noon := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, t.Location())
	jd := float64(noon.Unix())/86400 + 2440587.5
	n := math.Round(jd - 2451545.0 + 0.0008)
	js := n - lon/360
	m := math.Mod(357.5291+0.98560028*js, 360)
	c := 1.9148*math.Sin(m*rad) + 0.02*math.Sin(2*m*rad) + 0.0003*math.Sin(3*m*rad)
	l := math.Mod(m+c+180+102.9372, 360)
	transit := 2451545.0 + js + 0.0053*math.Sin(m*rad) - 0.0069*math.Sin(2*l*rad)
	dec := math.Asin(math.Sin(l*rad) * math.Sin(23.4397*rad))
	cosW := (math.Sin(-0.833*rad) - math.Sin(lat*rad)*math.Sin(dec)) / (math.Cos(lat*rad) * math.Cos(dec))
	if cosW < -1 || cosW > 1 {
		return time.Time{}, time.Time{}, false
	}
	w := math.Acos(cosW) / rad
	toTime := func(j float64) time.Time {
		return time.Unix(int64(math.Round((j-2440587.5)*86400)), 0).In(t.Location())
	}
	return toTime(transit - w/360), toTime(transit + w/360), true
}

// SunPeriod is "day" between sunrise and sunset at lat, lon, else "night".
func SunPeriod(now time.Time, lat, lon float64) string {
	rise, set, ok := SunTimes(now, lat, lon)
	if !ok {
		// Polar: day if the sun is up at noon (summer), else night.
		if math.Abs(lat) > 60 && (now.Month() >= 4 && now.Month() <= 9) == (lat > 0) {
			return "day"
		}
		return "night"
	}
	if now.After(rise) && now.Before(set) {
		return "day"
	}
	return "night"
}
