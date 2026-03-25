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
	Compilable    bool    `json:"compilable"`
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

	// Crawl-on-demand: if query is a URL, index it before searching.
	// Re-index if the page exists but has no content (hollow skeleton from contribute endpoint).
	if strings.HasPrefix(q, "http://") || strings.HasPrefix(q, "https://") {
		existing, _ := h.db.GetPageByURL(q)
		if existing == nil || existing.TextContent == "" {
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

	_, compilableOnly := r.URL.Query()["compiles"]

	// Try chunk-level search first, fall back to page-level
	var results []search.Result

	chunks, chunkErr := h.db.AllChunksWithPages()
	if chunkErr == nil && len(chunks) > 0 {
		pageCount, _ := h.db.PageCount()
		results = search.SearchChunks(chunks, queryEmb, pageCount, limit)
	}

	if len(results) == 0 {
		pages, err := h.db.AllPages()
		if err != nil {
			http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
			return
		}
		results = search.Search(pages, queryEmb, limit)
	}

	if compilableOnly {
		filtered := results[:0]
		for _, r := range results {
			if r.Page.Compilable {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	// Build response
	resp := searchResponse{
		Query: q,
		Total: len(results),
	}
	for _, r := range results {
		snippet := r.Snippet
		if snippet == "" {
			snippet = r.Page.TextContent
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}
		}
		resp.Results = append(resp.Results, searchResult{
			URL:           r.Page.URL,
			Title:         r.Page.Title,
			Snippet:       snippet,
			License:       r.Page.LicenseType,
			Compilable:    r.Page.Compilable,
			SemanticScore: r.Similarity,
			RankScore:     r.Page.PageRank,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type statsResponse struct {
	Pages           int     `json:"pages"`
	Chunks          int     `json:"chunks"`
	Links           int     `json:"links"`
	QualityCoverage float64 `json:"quality_coverage"`
	EmbeddingModel  string  `json:"embedding_model"`
	EmbeddingDim    int     `json:"embedding_dim"`
	Version         string  `json:"version"`
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
	pageID, err := h.db.InsertPage(page)
	if err != nil {
		log.Printf("crawl-on-demand insert %s: %v", pageURL, err)
		return
	}

	// Extract paragraphs and insert as chunks
	paragraphs := crawler.ExtractParagraphs(doc)
	if len(paragraphs) > 0 {
		embeddings, embErr := h.embedder.EmbedBatch(paragraphs)
		if embErr != nil {
			log.Printf("crawl-on-demand chunk embed %s: %v", pageURL, embErr)
			embeddings = make([][]float64, len(paragraphs))
		}
		chunks := make([]platform.Chunk, len(paragraphs))
		for i, text := range paragraphs {
			chunks[i] = platform.Chunk{
				PageID:    pageID,
				Idx:       i,
				Text:      text,
				Embedding: embeddings[i],
			}
		}
		if err := h.db.InsertChunks(pageID, chunks); err != nil {
			log.Printf("crawl-on-demand insert chunks %s: %v", pageURL, err)
		}
	}

	log.Printf("crawl-on-demand indexed %s (%s, %d chunks)", finalURL, license.Type, len(paragraphs))
}

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	pages, _ := h.db.PageCount()
	chunks, _ := h.db.ChunkCount()
	links, _ := h.db.LinkCount()
	// Threshold 1: structural scorer is currently the only reviewer.
	// Raise to 3 when federated quality workers are active.
	qualityCov, _ := h.db.QualityCoverage(1)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statsResponse{
		Pages:           pages,
		Chunks:          chunks,
		Links:           links,
		QualityCoverage: qualityCov,
		EmbeddingModel:  platform.EmbeddingModel,
		EmbeddingDim:    platform.EmbeddingDim,
		Version:         h.version,
	})
}

func (h *Handler) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	ctype := r.URL.Query().Get("type")
	limit := 10
	if s := r.URL.Query().Get("n"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	contributors, _ := h.db.ContributorStats(ctype, limit)

	type leaderboardEntry struct {
		Rank        int    `json:"rank"`
		Contributor string `json:"contributor"`
		Count       int    `json:"count"`
		Mushroom    string `json:"mushroom,omitempty"`
	}
	entries := make([]leaderboardEntry, len(contributors))
	for i, c := range contributors {
		entries[i] = leaderboardEntry{
			Rank:        i + 1,
			Contributor: c.Contributor,
			Count:       c.Count,
		}
		if i == 0 {
			entries[i].Mushroom = "🍄"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

