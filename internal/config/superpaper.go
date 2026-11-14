package config

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// SuperpaperOffsets reads the ppi_norm_offsets lists from Superpaper's
// display_systems.dat (an INI file with one section per display system).
// Offsets are in Superpaper's canvas pixels (densest display = 1:1) and
// already include bezels. Only sections with exactly n displays are
// returned; the last one in the file is the most recently saved.
func SuperpaperOffsets(r io.Reader, n int) ([][2]float64, error) {
	var found [][2]float64
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "ppi_norm_offsets" {
			continue
		}
		offs, err := parsePairs(strings.TrimSpace(val))
		if err != nil {
			return nil, err
		}
		if len(offs) == n {
			found = offs
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, errors.New("superpaper: no saved layout for this number of displays")
	}
	return found, nil
}

func parsePairs(s string) ([][2]float64, error) {
	var out [][2]float64
	for _, p := range strings.Split(s, ";") {
		xs, ys, ok := strings.Cut(p, ",")
		if !ok {
			return nil, errors.New("superpaper: bad offset " + p)
		}
		x, err1 := strconv.ParseFloat(strings.TrimSpace(xs), 64)
		y, err2 := strconv.ParseFloat(strings.TrimSpace(ys), 64)
		if err1 != nil || err2 != nil {
			return nil, errors.New("superpaper: bad offset " + p)
		}
		out = append(out, [2]float64{x, y})
	}
	return out, nil
}
