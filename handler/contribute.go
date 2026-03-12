package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kimjune01/pageleft/crawler"
	"github.com/kimjune01/pageleft/platform"

	"golang.org/x/net/html"
)

// handleFrontier returns frontier URLs for workers to crawl.
// GET /api/frontier?limit=10
func (h *Handler) handleFrontier(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	entries, err := h.db.PeekFrontier(limit)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	type frontierURL struct {
		URL   string `json:"url"`
		Depth int    `json:"depth"`
	}
	out := make([]frontierURL, len(entries))
	for i, e := range entries {
		out[i] = frontierURL{URL: e.URL, Depth: e.Depth}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// pageSubmission is the JSON body for POST /api/contribute/page.
type pageSubmission struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	TextContent string   `json:"text_content"`
	LicenseURL  string   `json:"license_url"`
	LicenseType string   `json:"license_type"`
	ContentHash string   `json:"content_hash"`
	Links       []string `json:"links"`
}

// handleContributePage accepts a crawled page from a federated worker.
// POST /api/contribute/page
func (h *Handler) handleContributePage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5*1024*1024))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	var sub pageSubmission
	if err := json.Unmarshal(body, &sub); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if sub.URL == "" {
		http.Error(w, `{"error":"url is required"}`, http.StatusBadRequest)
		return
	}

	// Trust but verify: re-fetch the URL and check for copyleft license
	license, err := verifyLicense(sub.URL)
	if err != nil {
		log.Printf("license verification failed for %s: %v", sub.URL, err)
		http.Error(w, fmt.Sprintf(`{"error":"license verification failed: %v"}`, err), http.StatusUnprocessableEntity)
		return
	}

	page := &platform.Page{
		URL:         sub.URL,
		Title:       sub.Title,
		TextContent: sub.TextContent,
		LicenseURL:  license.URL,
		LicenseType: license.Type,
		ContentHash: sub.ContentHash,
		CrawledAt:   time.Now(),
	}

	pageID, err := h.db.InsertPageWithLinks(page, sub.Links)
	if err != nil {
		http.Error(w, `{"error":"insert failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"accepted": true,
		"page_id":  pageID,
		"license":  license.Type,
	})
}

// handleWorkEmbed returns chunks (or pages) that need embeddings computed.
// GET /api/work/embed?limit=10
func (h *Handler) handleWorkEmbed(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	// Prefer chunk work items
	chunks, err := h.db.ChunksWithoutEmbeddings(limit)
	if err == nil && len(chunks) > 0 {
		type chunkWork struct {
			ChunkID int64  `json:"chunk_id"`
			PageID  int64  `json:"page_id"`
			Text    string `json:"text"`
		}
		out := make([]chunkWork, len(chunks))
		for i, c := range chunks {
			out[i] = chunkWork{
				ChunkID: c.ID,
				PageID:  c.PageID,
				Text:    c.Text,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
		return
	}

	// Fallback to page-level work
	pages, err := h.db.PagesWithoutEmbeddings(limit)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	type embedWork struct {
		PageID      int64  `json:"page_id"`
		Title       string `json:"title"`
		TextContent string `json:"text_content"`
	}
	out := make([]embedWork, len(pages))
	for i, p := range pages {
		text := p.TextContent
		words := strings.Fields(text)
		if len(words) > 500 {
			text = strings.Join(words[:500], " ")
		}
		out[i] = embedWork{
			PageID:      p.ID,
			Title:       p.Title,
			TextContent: text,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// embeddingSubmission is the JSON body for POST /api/contribute/embedding.
type embeddingSubmission struct {
	PageID    int64     `json:"page_id"`
	ChunkID   int64     `json:"chunk_id"`
	Embedding []float64 `json:"embedding"`
}

// handleContributeEmbedding accepts an embedding from a federated worker.
// Supports both chunk_id (new) and page_id (backward compat).
// POST /api/contribute/embedding
func (h *Handler) handleContributeEmbedding(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	var sub embeddingSubmission
	if err := json.Unmarshal(body, &sub); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if len(sub.Embedding) == 0 {
		http.Error(w, `{"error":"embedding is required"}`, http.StatusBadRequest)
		return
	}

	if sub.ChunkID != 0 {
		if err := h.db.UpdateChunkEmbedding(sub.ChunkID, sub.Embedding); err != nil {
			http.Error(w, `{"error":"update chunk failed"}`, http.StatusInternalServerError)
			return
		}
	} else if sub.PageID != 0 {
		if err := h.db.UpdateEmbedding(sub.PageID, sub.Embedding); err != nil {
			http.Error(w, `{"error":"update page failed"}`, http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, `{"error":"chunk_id or page_id is required"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"accepted": true})
}

// verifyLicense fetches a URL and checks for a copyleft license.
func verifyLicense(pageURL string) (*crawler.LicenseInfo, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(pageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		return nil, fmt.Errorf("not HTML: %s", contentType)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	doc, err := html.Parse(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	license := crawler.DetectLicense(doc)
	if license == nil {
		return nil, fmt.Errorf("no copyleft license found")
	}

	return license, nil
}
