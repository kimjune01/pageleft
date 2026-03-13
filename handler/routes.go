package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/kimjune01/pageleft/crawler"
	"github.com/kimjune01/pageleft/platform"
)

type Handler struct {
	db       *platform.DB
	embedder *platform.Embedder
	robots   *crawler.RobotsChecker
}

func New(db *platform.DB, embedder *platform.Embedder) *Handler {
	return &Handler{
		db:       db,
		embedder: embedder,
		robots:   crawler.NewRobotsChecker(&http.Client{Timeout: 10 * time.Second}),
	}
}

func (h *Handler) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.handleRoot)
	mux.HandleFunc("GET /api/search", h.handleSearch)
	mux.HandleFunc("GET /api/stats", h.handleStats)
	mux.HandleFunc("GET /api/frontier", h.handleFrontier)
	mux.HandleFunc("POST /api/contribute/page", h.handleContributePage)
	mux.HandleFunc("GET /api/work/embed", h.handleWorkEmbed)
	mux.HandleFunc("POST /api/contribute/embedding", h.handleContributeEmbedding)
	return mux
}

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	pages, _ := h.db.PageCount()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, `PageLeft — a search engine for copyleft expressions.
%d pages indexed.

Read more: https://www.june.kim/pageleft-manifesto
Source:    https://github.com/kimjune01/pageleft

API
  GET  /api/search?q=<query>        Search by natural language
  GET  /api/frontier                Next URLs to crawl

Try:  curl https://pageleft.cc/api/search?q=open+source+licensing
`, pages)
}
