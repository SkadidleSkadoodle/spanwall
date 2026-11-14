package sources

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pngBytes(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

func TestRunFiltersAndSaves(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/r/WidescreenWallpaper/top.json"):
			fmt.Fprintf(w, `{"data":{"children":[
			 {"data":{"id":"wide","url":"%[1]s/img/wide.png","over_18":false,"preview":{"images":[{"source":{"width":3000,"height":1000}}]}}},
			 {"data":{"id":"tall","url":"%[1]s/img/tall.png","over_18":false,"preview":{"images":[{"source":{"width":1000,"height":3000}}]}}},
			 {"data":{"id":"nsfw","url":"%[1]s/img/wide.png","over_18":true}},
			 {"data":{"id":"liar","url":"%[1]s/img/small.png","over_18":false}}]}}`, ts.URL)
		case r.URL.Path == "/img/wide.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngBytes(3000, 1000))
		case r.URL.Path == "/img/small.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngBytes(800, 400))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	client = ts.Client()
	Endpoints.Reddit = ts.URL
	// The test server's host isn't i.redd.it; rewrite the check by serving
	// through a host alias would need DNS, so test Reddit's filter via the
	// item list and Save/Run via a fake Bing feed instead.
	items, err := Reddit(context.Background(), "WidescreenWallpaper")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("only i.redd.it / i.imgur.com links should pass: %v", items)
	}

	dir := t.TempDir()
	res := runItems(context.Background(), []Item{
		{Source: "test", ID: "wide", URL: ts.URL + "/img/wide.png", Ext: ".png"},
		{Source: "test", ID: "tall", URL: ts.URL + "/img/tall.png", Width: 1000, Height: 3000, Ext: ".png"},
		{Source: "test", ID: "liar", URL: ts.URL + "/img/small.png", Ext: ".png"},
	}, dir, 2560, nil)
	if len(res.Downloaded) != 1 || res.Downloaded[0] != "test-wide.png" {
		t.Errorf("downloaded %v", res.Downloaded)
	}
	if strings.Join(res.Rejected, ",") != "test:tall,test:liar" {
		t.Errorf("rejected %v", res.Rejected)
	}
	if _, err := os.Stat(filepath.Join(dir, "test-liar.png")); err == nil {
		t.Error("the too-small image should have been removed")
	}
}

func TestFeedsParse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/HPImageArchive.aspx":
			fmt.Fprint(w, `{"images":[{"startdate":"20261114","urlbase":"/th?id=OHR.FoxGlacier_EN-US123","title":"Fox"}]}`)
		case r.URL.Path == "/apod":
			fmt.Fprint(w, `[{"date":"2026-11-13","media_type":"video","url":"https://youtube.com/x"},{"date":"2026-11-14","media_type":"image","hdurl":"https://apod.nasa.gov/a.jpg","title":"M31"}]`)
		case strings.HasPrefix(r.URL.Path, "/feat/2026/11/14"):
			fmt.Fprint(w, `{"image":{"title":"File:Lake.jpg","image":{"source":"https://upload.wikimedia.org/Lake.jpg","width":6000,"height":4000}}}`)
		case strings.HasPrefix(r.URL.Path, "/photos/random"):
			if r.Header.Get("Authorization") != "Client-ID k" {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `[{"id":"abc","width":9000,"height":6000,"urls":{"raw":"https://images.unsplash.com/p?ixid=1"},"links":{"download_location":"https://api.unsplash.com/photos/abc/download"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	client = ts.Client()
	Endpoints.Bing, Endpoints.APOD, Endpoints.Wikimedia, Endpoints.Unsplash = ts.URL, ts.URL+"/apod", ts.URL+"/feat", ts.URL
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	b, err := Bing(context.Background())
	if err != nil || len(b) != 1 || b[0].FileName() != "bing-20261114-FoxGlacier.jpg" || !strings.HasSuffix(b[0].URL, "OHR.FoxGlacier_EN-US123_UHD.jpg") {
		t.Errorf("bing %v %v", b, err)
	}
	a, err := APOD(context.Background(), "", 2, now)
	if err != nil || len(a) != 1 || a[0].FileName() != "apod-2026-11-14.jpg" {
		t.Errorf("apod %v %v", a, err)
	}
	wm, err := Wikimedia(context.Background(), 1, now)
	if err != nil || len(wm) != 1 || wm[0].Width != 6000 || wm[0].FileName() != "wikimedia-2026-11-14.jpg" {
		t.Errorf("wikimedia %v %v", wm, err)
	}
	u, err := Unsplash(context.Background(), "k", "", 1)
	if err != nil || len(u) != 1 || u[0].URL != "https://images.unsplash.com/p?ixid=1&fm=jpg&q=90&w=7680" || u[0].Height != 5120 {
		t.Errorf("unsplash %v %v", u, err)
	}
	if _, err := Unsplash(context.Background(), "", "", 1); err == nil {
		t.Error("unsplash without a key should fail")
	}
}
