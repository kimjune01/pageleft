package handler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/kimjune01/pageleft/crawler"
	"github.com/kimjune01/pageleft/platform"
	"github.com/kimjune01/pageleft/search"
)

type searchResult struct {
	URL           string  `json:"url"`
	Title         string  `json:"title"`
	Snippet       string  `json:"snippet"`
	License       string  `json:"license"`
	SemanticScore float64 `json:"semantic_score"`
	RankScore     float64 `json:"rank_score"`
}

type searchResponse struct {
	Query   string         `json:"query"`
	Results []searchResult `json:"results"`
	Total   int            `json:"total"`
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, `{"error":"missing q parameter"}`, http.StatusBadRequest)
		return
	}

	// Crawl-on-demand: if query is a URL, index it before searching
	if strings.HasPrefix(q, "http://") || strings.HasPrefix(q, "https://") {
		if existing, _ := h.db.GetPageByURL(q); existing == nil {
			h.indexURL(q)
		}
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	// Embed query
	queryEmb, err := h.embedder.Embed(q)
	if err != nil {
		http.Error(w, `{"error":"embedding failed"}`, http.StatusInternalServerError)
		return
	}

	// Get all pages
	pages, err := h.db.AllPages()
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	// Search
	results := search.Search(pages, queryEmb, limit)

	// Build response
	resp := searchResponse{
		Query: q,
		Total: len(results),
	}
	for _, r := range results {
		snippet := r.Page.TextContent
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		resp.Results = append(resp.Results, searchResult{
			URL:           r.Page.URL,
			Title:         r.Page.Title,
			Snippet:       snippet,
			License:       r.Page.LicenseType,
			SemanticScore: r.Similarity,
			RankScore:     r.Page.PageRank,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type statsResponse struct {
	Pages int `json:"pages"`
	Links int `json:"links"`
}

// indexURL fetches a URL, checks for a copyleft license, extracts content,
// generates an embedding, and stores the page in the database.
func (h *Handler) indexURL(pageURL string) {
	if !h.robots.IsAllowed(pageURL) {
		log.Printf("crawl-on-demand %s: blocked by robots.txt", pageURL)
		return
	}

	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		log.Printf("crawl-on-demand %s: %v", pageURL, err)
		return
	}
	req.Header.Set("User-Agent", crawler.RobotsUserAgent)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("crawl-on-demand fetch %s: %v", pageURL, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("crawl-on-demand %s: status %d", pageURL, resp.StatusCode)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		log.Printf("crawl-on-demand %s: not HTML (%s)", pageURL, contentType)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		log.Printf("crawl-on-demand read %s: %v", pageURL, err)
		return
	}

	doc, err := html.Parse(strings.NewReader(string(bodyBytes)))
	if err != nil {
		log.Printf("crawl-on-demand parse %s: %v", pageURL, err)
		return
	}

	license := crawler.DetectLicense(doc)
	if license == nil {
		log.Printf("crawl-on-demand %s: no copyleft license", pageURL)
		return
	}

	title := crawler.ExtractTitle(doc)
	text := crawler.ExtractText(doc)
	contentHash := fmt.Sprintf("%x", sha256.Sum256(bodyBytes))

	embInput := title + " " + crawler.First500Words(text)
	emb, err := h.embedder.Embed(embInput)
	if err != nil {
		log.Printf("crawl-on-demand embed %s: %v", pageURL, err)
		emb = nil
	}

	finalURL := resp.Request.URL.String()
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
	if _, err := h.db.InsertPage(page); err != nil {
		log.Printf("crawl-on-demand insert %s: %v", pageURL, err)
		return
	}
	log.Printf("crawl-on-demand indexed %s (%s)", finalURL, license.Type)
}

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	pages, _ := h.db.PageCount()
	links, _ := h.db.LinkCount()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statsResponse{Pages: pages, Links: links})
}

