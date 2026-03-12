package crawler

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/kimjune01/pageleft/platform"
)

type Crawler struct {
	db       *platform.DB
	embedder *platform.Embedder
	client   *http.Client
	robots   *RobotsChecker

	rateLimit time.Duration
	maxPages  int

	// Per-host rate limiting
	mu        sync.Mutex
	lastFetch map[string]time.Time
}

func New(db *platform.DB, embedder *platform.Embedder, maxPages int) *Crawler {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	return &Crawler{
		db:        db,
		embedder:  embedder,
		client:    client,
		robots:    NewRobotsChecker(client),
		rateLimit: 2 * time.Second,
		maxPages:  maxPages,
		lastFetch: make(map[string]time.Time),
	}
}

func (c *Crawler) Crawl(seeds []string) error {
	for _, seed := range seeds {
		c.db.AddToFrontier(seed, 0)
	}

	crawled := 0
	for crawled < c.maxPages {
		entries, err := c.db.PopFrontier(10)
		if err != nil {
			return fmt.Errorf("pop frontier: %w", err)
		}
		if len(entries) == 0 {
			log.Println("frontier empty, stopping")
			break
		}

		for _, entry := range entries {
			if crawled >= c.maxPages {
				break
			}

			// Skip if already crawled
			if existing, _ := c.db.GetPageByURL(entry.URL); existing != nil {
				continue
			}

			err := c.crawlPage(entry.URL, entry.Depth)
			if err != nil {
				log.Printf("error crawling %s: %v", entry.URL, err)
				continue
			}
			crawled++
			log.Printf("[%d/%d] crawled %s", crawled, c.maxPages, entry.URL)
		}
	}

	log.Printf("crawl complete: %d pages", crawled)
	return nil
}

func (c *Crawler) crawlPage(pageURL string, depth int) error {
	if !c.robots.IsAllowed(pageURL) {
		return fmt.Errorf("blocked by robots.txt")
	}

	c.rateLimitHost(pageURL)

	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", RobotsUserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		return fmt.Errorf("not HTML: %s", contentType)
	}

	// Read body
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	doc, err := html.Parse(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return fmt.Errorf("parse HTML: %w", err)
	}

	// Resolve final URL (after redirects)
	finalURL := resp.Request.URL.String()

	// Detect license
	license := DetectLicense(doc)
	if license == nil {
		log.Printf("  no copyleft license: %s", finalURL)
		return nil // skip non-copyleft pages
	}
	log.Printf("  license: %s (%s)", license.Type, license.URL)

	// Extract title and text
	title := ExtractTitle(doc)
	text := ExtractText(doc)
	contentHash := fmt.Sprintf("%x", sha256.Sum256(bodyBytes))

	// Generate embedding from title + first 500 words
	embInput := title + " " + First500Words(text)
	emb, err := c.embedder.Embed(embInput)
	if err != nil {
		log.Printf("  embedding error (continuing without): %v", err)
		emb = nil
	}

	// Insert page
	page := &platform.Page{
		URL:         finalURL,
		Title:       title,
		TextContent: text,
		LicenseURL:  license.URL,
		LicenseType: license.Type,
		Embedding:   emb,
		CrawledAt:   time.Now(),
		ContentHash: contentHash,
	}
	pageID, err := c.db.InsertPage(page)
	if err != nil {
		return fmt.Errorf("insert page: %w", err)
	}

	// Extract and process links
	links := extractLinks(doc, resp.Request.URL)
	for _, link := range links {
		// Normalize URL
		normalized := normalizeURL(link.href)
		if normalized == "" || normalized == finalURL {
			continue
		}

		// Check if target exists as a page
		targetPage, _ := c.db.GetPageByURL(normalized)
		if targetPage != nil {
			c.db.InsertLink(pageID, targetPage.ID, link.anchor)
		}

		// Add to frontier if not known
		c.db.AddToFrontier(normalized, depth+1)
	}

	return nil
}

func (c *Crawler) rateLimitHost(pageURL string) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return
	}
	host := u.Host

	c.mu.Lock()
	last, ok := c.lastFetch[host]
	c.lastFetch[host] = time.Now()
	c.mu.Unlock()

	if ok {
		elapsed := time.Since(last)
		if elapsed < c.rateLimit {
			time.Sleep(c.rateLimit - elapsed)
		}
	}
}

type linkInfo struct {
	href   string
	anchor string
}

func extractLinks(n *html.Node, base *url.URL) []linkInfo {
	var links []linkInfo
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := attr(n, "href")
			if href != "" {
				resolved := resolveURL(href, base)
				if resolved != "" {
					anchor := textContent(n)
					links = append(links, linkInfo{href: resolved, anchor: anchor})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return links
}

func resolveURL(href string, base *url.URL) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(u)
	// Only follow http/https
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}
	// Strip fragment
	resolved.Fragment = ""
	return resolved.String()
}

func normalizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	u.Fragment = ""
	// Remove trailing slash for consistency
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = ""
	}
	return u.String()
}

func ExtractTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		return strings.TrimSpace(textContent(n))
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := ExtractTitle(c); t != "" {
			return t
		}
	}
	return ""
}

var skipTags = map[string]bool{
	"script": true, "style": true, "nav": true, "footer": true,
	"header": true, "noscript": true, "svg": true,
}

func ExtractText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && skipTags[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	// Normalize whitespace
	return strings.Join(strings.Fields(sb.String()), " ")
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

func First500Words(text string) string {
	words := strings.Fields(text)
	if len(words) > 500 {
		words = words[:500]
	}
	return strings.Join(words, " ")
}
