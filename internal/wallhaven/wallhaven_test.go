package wallhaven

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestID(t *testing.T) {
	for in, want := range map[string]string{"https://wallhaven.cc/w/mlzwem": "mlzwem", "x6mlg3": "x6mlg3",
		"wallhaven-x6mlg3.png": "x6mlg3", "https://w.wallhaven.cc/full/x6/wallhaven-x6mlg3.png": "x6mlg3"} {
		if got, err := ID(in); err != nil || got != want {
			t.Errorf("%s: %s %v", in, got, err)
		}
	}
	if _, err := ID("../../etc"); err == nil {
		t.Error("bad id accepted")
	}
}

func TestSearchForcesSFW(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		w.Write([]byte(`{"data":[{"id":"abc123","path":"https://w.wallhaven.cc/full/ab/wallhaven-abc123.png","dimension_x":5120,"dimension_y":1440}],"meta":{"total":1,"last_page":1,"current_page":1}}`))
	}))
	defer srv.Close()
	Base = srv.URL
	r, err := Search(context.Background(), Query{Q: "city", Ratios: "32x9"})
	if err != nil || len(r.Data) != 1 || r.Data[0].Width != 5120 {
		t.Fatalf("%v %+v", err, r)
	}
	if !strings.Contains(got, "purity=100") || !strings.Contains(got, "ratios=32x9") {
		t.Errorf("query %s", got)
	}
}

func TestDownloadRejectsForeignHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"id":"abc123","path":"https://evil.example/x.png"}}`))
	}))
	defer srv.Close()
	Base = srv.URL
	dir := t.TempDir()
	if _, _, err := Download(context.Background(), "abc123", dir); err == nil {
		t.Error("foreign host accepted")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 0 {
		t.Error("file written")
	}
	_ = os.Remove
}

func TestKeyGoesInHeaderNotURL(t *testing.T) {
	var hdr, rawURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr, rawURL = r.Header.Get("X-API-Key"), r.URL.String()
		w.Write([]byte(`{"data":[{"id":7,"label":"Default","public":1,"count":3}]}`))
	}))
	defer srv.Close()
	Base, Key = srv.URL, "secret123"
	defer func() { Key = "" }()
	cs, err := Collections(context.Background(), "")
	if err != nil || len(cs) != 1 || cs[0].Label != "Default" {
		t.Fatalf("%v %v", cs, err)
	}
	if hdr != "secret123" || strings.Contains(rawURL, "secret") {
		t.Errorf("header %q url %q", hdr, rawURL)
	}
}

func TestSyncDownloadsOnlyMissing(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/collections":
			w.Write([]byte(`{"data":[{"id":5,"label":"Default","count":0},{"id":9,"label":"Spanwall","count":2}]}`))
		case r.URL.Path == "/collections/me/9":
			w.Write([]byte(`{"data":[{"id":"aaa111","path":"https://w.wallhaven.cc/full/aa/wallhaven-aaa111.png"},{"id":"bbb222","path":"https://w.wallhaven.cc/full/bb/wallhaven-bbb222.jpg"}],"meta":{"last_page":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	Base, Key = srv.URL, "k"
	defer func() { Key = "" }()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wallhaven-aaa111.png"), []byte("x"), 0o644)
	res, err := SyncCollection(context.Background(), "me", "spanwall", dir)
	if err != nil {
		t.Fatal(err)
	}
	// aaa111 is present; bbb222 can't download from the fake host, so it errors
	// instead of being written anywhere else.
	if res.Total != 2 || res.Present != 1 || len(res.Errors) != 1 {
		t.Errorf("%+v", res)
	}
}
