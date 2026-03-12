package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

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

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	pages, _ := h.db.PageCount()
	links, _ := h.db.LinkCount()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statsResponse{Pages: pages, Links: links})
}

