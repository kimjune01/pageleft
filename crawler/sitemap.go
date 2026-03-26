package crawler

import (
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// SitemapURL represents a <url> entry in a sitemap.
type SitemapURL struct {
	Loc string `xml:"loc"`
}

// Sitemap represents a <urlset> sitemap.
type Sitemap struct {
	URLs []SitemapURL `xml:"url"`
}

// SitemapIndex represents a <sitemapindex> with nested sitemaps.
type SitemapIndex struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

// FetchSitemapURLs fetches and parses a sitemap (or sitemap index),
// returning all discovered page URLs. Follows sitemap indexes one level deep.
func FetchSitemapURLs(client *http.Client, sitemapURL string) ([]string, error) {
	body, err := fetchXML(client, sitemapURL)
	if err != nil {
		return nil, err
	}

	// Try as sitemap index first
	var idx SitemapIndex
	if err := xml.Unmarshal(body, &idx); err == nil && len(idx.Sitemaps) > 0 {
		var allURLs []string
		for _, sm := range idx.Sitemaps {
			urls, err := FetchSitemapURLs(client, sm.Loc)
			if err != nil {
				log.Printf("sitemap index entry failed %s: %v", sm.Loc, err)
				continue
			}
			allURLs = append(allURLs, urls...)
		}
		return allURLs, nil
	}

	// Try as regular sitemap
	var sitemap Sitemap
	if err := xml.Unmarshal(body, &sitemap); err != nil {
		return nil, fmt.Errorf("parse sitemap XML: %w", err)
	}

	urls := make([]string, 0, len(sitemap.URLs))
	for _, u := range sitemap.URLs {
		if u.Loc != "" {
			urls = append(urls, u.Loc)
		}
	}
	return urls, nil
}

func fetchXML(client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", RobotsUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d fetching %s", resp.StatusCode, rawURL)
	}

	// Handle gzip (common for .xml.gz sitemaps)
	var reader io.Reader = resp.Body
	if strings.HasSuffix(rawURL, ".gz") || resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	// 10MB limit for sitemaps
	return io.ReadAll(io.LimitReader(reader, 10*1024*1024))
}

// ExtractSitemapURLsFromRobots parses Sitemap: directives from robots.txt content.
func ExtractSitemapURLsFromRobots(robotsBody string) []string {
	var sitemaps []string
	for _, line := range strings.Split(robotsBody, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(strings.ToLower(parts[0])) == "sitemap" {
			loc := strings.TrimSpace(parts[1])
			// Rejoin if the URL was split on the colon in "https:"
			if strings.HasPrefix(strings.ToLower(loc), "//") || !strings.Contains(loc, "://") {
				// Likely split "https" from "//example.com/sitemap.xml"
				loc = "https:" + loc
			}
			if loc != "" {
				sitemaps = append(sitemaps, loc)
			}
		}
	}
	return sitemaps
}

// DiscoverSitemapURLs tries to find sitemap URLs for a given origin.
// Checks robots.txt first, falls back to /sitemap.xml.
func DiscoverSitemapURLs(client *http.Client, origin string) []string {
	// Try robots.txt
	resp, err := client.Get(origin + "/robots.txt")
	if err == nil && resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		resp.Body.Close()
		sitemaps := ExtractSitemapURLsFromRobots(string(body))
		if len(sitemaps) > 0 {
			return sitemaps
		}
	} else if resp != nil {
		resp.Body.Close()
	}

	// Fallback: try /sitemap.xml directly
	u, err := url.Parse(origin)
	if err != nil {
		return nil
	}
	u.Path = "/sitemap.xml"
	return []string{u.String()}
}
