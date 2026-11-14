// Package sources downloads wallpapers from free image feeds: Bing's image
// of the day, NASA's Astronomy Picture of the Day, Wikimedia Commons'
// picture of the day, subreddits and Unsplash. Only landscape images wide
// enough for the desk are kept.
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// Endpoints are the API roots (variables so tests can use a fake server).
var Endpoints = struct {
	Bing, APOD, Wikimedia, Reddit, Unsplash string
}{
	Bing:      "https://www.bing.com",
	APOD:      "https://api.nasa.gov/planetary/apod",
	Wikimedia: "https://api.wikimedia.org/feed/v1/wikipedia/en/featured",
	Reddit:    "https://www.reddit.com",
	Unsplash:  "https://api.unsplash.com",
}

var client = &http.Client{Timeout: 90 * time.Second}

const userAgent = "Spanwall/1.0 (wallpaper app; https://github.com/martingomezmejia-ctrl/spanwall)"

// Item is one image a source offers.
type Item struct {
	Source string `json:"source"`
	ID     string `json:"id"`  // unique within the source
	URL    string `json:"url"` // full-size image
	Title  string `json:"title,omitempty"`
	Width  int    `json:"width,omitempty"` // 0 when the feed doesn't say
	Height int    `json:"height,omitempty"`
	Ext    string `json:"ext"`
	// Ping is fetched once the image is saved (Unsplash asks for this).
	Ping string `json:"-"`
	auth string
}

// Key is the item's entry in the skip list.
func (it Item) Key() string { return it.Source + ":" + it.ID }

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// FileName is the local name: <source>-<id><ext>.
func (it Item) FileName() string {
	id := strings.Trim(unsafeName.ReplaceAllString(it.ID, "-"), "-")
	if len(id) > 80 {
		id = id[:80]
	}
	ext := strings.ToLower(it.Ext)
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		ext = ".jpg"
	}
	return it.Source + "-" + id + ext
}

func getJSON(ctx context.Context, u string, hdr map[string]string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	for k, val := range hdr {
		req.Header.Set(k, val)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return fmt.Errorf("%s (check the API key, or the site may be blocking requests)", resp.Status)
	case resp.StatusCode == 429:
		return errors.New("rate limited; try again later")
	case resp.StatusCode != 200:
		return errors.New(resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

func extOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ".jpg"
	}
	return strings.ToLower(path.Ext(p.Path))
}

// Bing returns the last eight Bing images of the day in UHD (3840x2160).
func Bing(ctx context.Context) ([]Item, error) {
	var r struct {
		Images []struct {
			StartDate string `json:"startdate"`
			URLBase   string `json:"urlbase"`
			Title     string `json:"title"`
		} `json:"images"`
	}
	if err := getJSON(ctx, Endpoints.Bing+"/HPImageArchive.aspx?format=js&idx=0&n=8&mkt=en-US", nil, &r); err != nil {
		return nil, fmt.Errorf("bing: %w", err)
	}
	var out []Item
	for _, im := range r.Images {
		if !strings.HasPrefix(im.URLBase, "/") {
			continue
		}
		name := im.URLBase
		if i := strings.Index(name, "OHR."); i >= 0 {
			name = name[i+4:]
		}
		if i := strings.Index(name, "_"); i > 0 {
			name = name[:i]
		}
		out = append(out, Item{Source: "bing", ID: im.StartDate + "-" + name, URL: "https://www.bing.com" + im.URLBase + "_UHD.jpg",
			Title: im.Title, Width: 3840, Height: 2160, Ext: ".jpg"})
	}
	return out, nil
}

// APOD returns NASA's pictures of the day from the last days days (images
// only; video days are skipped). key may be "" for NASA's shared demo key,
// which allows only a few requests an hour.
func APOD(ctx context.Context, key string, days int, now time.Time) ([]Item, error) {
	if key == "" {
		key = "DEMO_KEY"
	}
	start := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	var r []struct {
		Date      string `json:"date"`
		MediaType string `json:"media_type"`
		URL       string `json:"url"`
		HDURL     string `json:"hdurl"`
		Title     string `json:"title"`
	}
	u := Endpoints.APOD + "?thumbs=false&start_date=" + start + "&api_key=" + url.QueryEscape(key)
	if err := getJSON(ctx, u, nil, &r); err != nil {
		return nil, fmt.Errorf("nasa apod: %w", err)
	}
	var out []Item
	for _, d := range r {
		src := d.HDURL
		if src == "" {
			src = d.URL
		}
		if d.MediaType != "image" || !strings.HasPrefix(src, "https://") {
			continue
		}
		out = append(out, Item{Source: "apod", ID: d.Date, URL: src, Title: d.Title, Ext: extOf(src)})
	}
	return out, nil
}

// Wikimedia returns Wikimedia Commons' pictures of the day for the last
// days days, at full resolution.
func Wikimedia(ctx context.Context, days int, now time.Time) ([]Item, error) {
	var out []Item
	var firstErr error
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -i)
		var r struct {
			Image struct {
				Title string `json:"title"`
				Image struct {
					Source string `json:"source"`
					Width  int    `json:"width"`
					Height int    `json:"height"`
				} `json:"image"`
			} `json:"image"`
		}
		if err := getJSON(ctx, Endpoints.Wikimedia+d.Format("/2006/01/02"), nil, &r); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("wikimedia: %w", err)
			}
			continue
		}
		im := r.Image.Image
		if !strings.HasPrefix(im.Source, "https://") {
			continue
		}
		out = append(out, Item{Source: "wikimedia", ID: d.Format("2006-01-02"), URL: im.Source,
			Title: strings.TrimPrefix(r.Image.Title, "File:"), Width: im.Width, Height: im.Height, Ext: extOf(im.Source)})
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

var subRe = regexp.MustCompile(`^[A-Za-z0-9_]{2,30}$`)

// Reddit returns the week's top image posts of a subreddit (no NSFW
// posts; direct image links only).
func Reddit(ctx context.Context, sub string) ([]Item, error) {
	sub = strings.TrimPrefix(strings.TrimSpace(sub), "r/")
	if !subRe.MatchString(sub) {
		return nil, fmt.Errorf("reddit: %q is not a subreddit name", sub)
	}
	var r struct {
		Data struct {
			Children []struct {
				Data struct {
					ID      string `json:"id"`
					Title   string `json:"title"`
					URL     string `json:"url"`
					Over18  bool   `json:"over_18"`
					Preview struct {
						Images []struct {
							Source struct {
								Width  int `json:"width"`
								Height int `json:"height"`
							} `json:"source"`
						} `json:"images"`
					} `json:"preview"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := getJSON(ctx, Endpoints.Reddit+"/r/"+sub+"/top.json?t=week&limit=40&raw_json=1", nil, &r); err != nil {
		return nil, fmt.Errorf("reddit r/%s: %w", sub, err)
	}
	var out []Item
	for _, c := range r.Data.Children {
		p := c.Data
		u, err := url.Parse(p.URL)
		if p.Over18 || err != nil || u.Scheme != "https" || (u.Host != "i.redd.it" && u.Host != "i.imgur.com") {
			continue
		}
		ext := extOf(p.URL)
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
			continue
		}
		it := Item{Source: "reddit", ID: p.ID, URL: p.URL, Title: p.Title, Ext: ext}
		if len(p.Preview.Images) > 0 {
			it.Width, it.Height = p.Preview.Images[0].Source.Width, p.Preview.Images[0].Source.Height
		}
		out = append(out, it)
	}
	return out, nil
}

// Unsplash returns n random landscape photos matching query. It needs an
// Unsplash access key (free developer account).
func Unsplash(ctx context.Context, key, query string, n int) ([]Item, error) {
	if key == "" {
		return nil, errors.New("unsplash: add your access key in Settings")
	}
	if query == "" {
		query = "landscape"
	}
	var r []struct {
		ID     string `json:"id"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Desc   string `json:"alt_description"`
		URLs   struct {
			Raw string `json:"raw"`
		} `json:"urls"`
		Links struct {
			DownloadLocation string `json:"download_location"`
		} `json:"links"`
	}
	hdr := map[string]string{"Authorization": "Client-ID " + key, "Accept-Version": "v1"}
	u := fmt.Sprintf("%s/photos/random?count=%d&orientation=landscape&content_filter=high&query=%s", Endpoints.Unsplash, n, url.QueryEscape(query))
	if err := getJSON(ctx, u, hdr, &r); err != nil {
		return nil, fmt.Errorf("unsplash: %w", err)
	}
	var out []Item
	for _, p := range r {
		if !strings.HasPrefix(p.URLs.Raw, "https://") {
			continue
		}
		w := min(p.Width, 7680)
		sep := "?"
		if strings.Contains(p.URLs.Raw, "?") {
			sep = "&"
		}
		h := p.Height
		if p.Width > 0 {
			h = p.Height * w / p.Width
		}
		out = append(out, Item{Source: "unsplash", ID: p.ID, URL: fmt.Sprintf("%s%sfm=jpg&q=90&w=%d", p.URLs.Raw, sep, w),
			Title: p.Desc, Width: w, Height: h, Ext: ".jpg", Ping: p.Links.DownloadLocation, auth: hdr["Authorization"]})
	}
	return out, nil
}

// Save downloads it into dir and returns the file's path. An existing file
// is kept. Anything that isn't an image is refused.
func Save(ctx context.Context, it Item, dir string) (string, error) {
	if !strings.HasPrefix(it.URL, "https://") {
		return "", fmt.Errorf("%s: not an https link", it.Source)
	}
	dst := filepath.Join(dir, it.FileName())
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", it.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s download: %w", it.Source, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%s download: %s", it.Source, resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		return "", fmt.Errorf("%s download: not an image (%s)", it.Source, ct)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 150<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if it.Ping != "" {
		if pr, err := http.NewRequestWithContext(ctx, "GET", it.Ping, nil); err == nil {
			pr.Header.Set("Authorization", it.auth)
			if r, err := client.Do(pr); err == nil {
				r.Body.Close()
			}
		}
	}
	return dst, os.Rename(tmp, dst)
}

// Options says which sources to check.
type Options struct {
	Bing, APOD, Wikimedia, Reddit, Unsplash bool
	Subreddits                              []string
	Query                                   string
	NASAKey, UnsplashKey                    string
	MinWidth                                int
	Skipped                                 map[string]bool // Item.Key() of images rejected before
	Now                                     time.Time
}

// Result is what a run did.
type Result struct {
	Checked    int      `json:"checked"`
	Downloaded []string `json:"downloaded"`
	Rejected   []string `json:"rejected"` // Item.Key()s found too small or portrait (add to the skip list)
	Errors     []string `json:"errors,omitempty"`
}

// Run checks every enabled source and saves new images that are landscape
// and at least MinWidth wide. Images already in dir are left alone.
func Run(ctx context.Context, o Options, dir string) Result {
	if o.MinWidth <= 0 {
		o.MinWidth = 2560
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	var res Result
	var items []Item
	add := func(its []Item, err error) {
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
		items = append(items, its...)
	}
	if o.Bing {
		add(Bing(ctx))
	}
	if o.APOD {
		add(APOD(ctx, o.NASAKey, 7, o.Now))
	}
	if o.Wikimedia {
		add(Wikimedia(ctx, 5, o.Now))
	}
	if o.Reddit {
		for _, s := range o.Subreddits {
			if s = strings.TrimSpace(s); s != "" {
				add(Reddit(ctx, s))
			}
		}
	}
	if o.Unsplash {
		add(Unsplash(ctx, o.UnsplashKey, o.Query, 4))
	}
	r := runItems(ctx, items, dir, o.MinWidth, o.Skipped)
	r.Errors = append(res.Errors, r.Errors...)
	return r
}

// runItems saves the items that fit and aren't skipped or present.
func runItems(ctx context.Context, items []Item, dir string, minWidth int, skipped map[string]bool) Result {
	var res Result
	fits := func(w, h int) bool { return w >= minWidth && w > h }
	for _, it := range items {
		res.Checked++
		if skipped[it.Key()] {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, it.FileName())); err == nil {
			continue
		}
		if it.Width > 0 && it.Height > 0 && !fits(it.Width, it.Height) {
			res.Rejected = append(res.Rejected, it.Key())
			continue
		}
		p, err := Save(ctx, it, dir)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		// Check the real size (feeds don't always say, or say wrong).
		w, h := 0, 0
		if f, err := os.Open(p); err == nil {
			if c, _, err := image.DecodeConfig(f); err == nil {
				w, h = c.Width, c.Height
			}
			f.Close()
		}
		if !fits(w, h) {
			os.Remove(p)
			res.Rejected = append(res.Rejected, it.Key())
			continue
		}
		res.Downloaded = append(res.Downloaded, filepath.Base(p))
	}
	return res
}
