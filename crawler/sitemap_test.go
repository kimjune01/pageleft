package crawler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractSitemapURLsFromRobots(t *testing.T) {
	body := `User-Agent: *
Disallow: /admin/
Sitemap: https://example.com/sitemap.xml
Sitemap: https://example.com/sitemap-news.xml
`
	urls := ExtractSitemapURLsFromRobots(body)
	if len(urls) != 2 {
		t.Fatalf("expected 2 sitemaps, got %d", len(urls))
	}
	if urls[0] != "https://example.com/sitemap.xml" {
		t.Errorf("unexpected URL: %s", urls[0])
	}
	if urls[1] != "https://example.com/sitemap-news.xml" {
		t.Errorf("unexpected URL: %s", urls[1])
	}
}

func TestFetchSitemapURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/page1</loc></url>
  <url><loc>https://example.com/page2</loc></url>
  <url><loc>https://example.com/page3</loc></url>
</urlset>`))
	}))
	defer srv.Close()

	urls, err := FetchSitemapURLs(srv.Client(), srv.URL+"/sitemap.xml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(urls) != 3 {
		t.Fatalf("expected 3 URLs, got %d", len(urls))
	}
}

func TestFetchSitemapIndex(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/sitemap-index.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>` + srv.URL + `/sitemap1.xml</loc></sitemap>
  <sitemap><loc>` + srv.URL + `/sitemap2.xml</loc></sitemap>
</sitemapindex>`))
	})

	mux.HandleFunc("/sitemap1.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/a</loc></url>
  <url><loc>https://example.com/b</loc></url>
</urlset>`))
	})

	mux.HandleFunc("/sitemap2.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/c</loc></url>
</urlset>`))
	})

	srv = httptest.NewServer(mux)
	defer srv.Close()

	urls, err := FetchSitemapURLs(srv.Client(), srv.URL+"/sitemap-index.xml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(urls) != 3 {
		t.Fatalf("expected 3 URLs, got %d: %v", len(urls), urls)
	}
}
