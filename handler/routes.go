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
	mux.HandleFunc("GET /favicon.ico", h.handleFavicon)
	mux.HandleFunc("GET /api/search", h.handleSearch)
	mux.HandleFunc("GET /api/stats", h.handleStats)
	mux.HandleFunc("GET /api/leaderboard", h.handleLeaderboard)
	mux.HandleFunc("GET /api/frontier", h.handleFrontier)
	mux.HandleFunc("POST /api/contribute/page", h.handleContributePage)
	mux.HandleFunc("GET /api/work/embed", h.handleWorkEmbed)
	mux.HandleFunc("GET /api/work/quality", h.handleWorkQuality)
	mux.HandleFunc("POST /api/contribute/embedding", h.handleContributeEmbedding)
	mux.HandleFunc("POST /api/contribute/quality", h.handleContributeQuality)
	mux.HandleFunc("POST /api/contribute/compilable", h.handleContributeCompilable)
	return mux
}

func (h *Handler) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text y=".9em" font-size="90">🍄</text></svg>`)
}

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	pages, _ := h.db.PageCount()
	chunks, _ := h.db.ChunkCount()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, `PageLeft 🍄 a search engine for copyleft expressions.
%d pages, %d chunks indexed.

Read more: https://www.june.kim/pageleft-manifesto
Source:    https://github.com/kimjune01/pageleft

API
  GET  /api/search?q=<query>          Search by natural language
  GET  /api/stats                     Index stats
  GET  /api/leaderboard               Contributor rankings

Contribute
  GET  /api/frontier                  Claim URLs to crawl
  POST /api/contribute/page           Submit crawled page {"url":"..."}
  GET  /api/work/embed?limit=10       Claim chunks to embed {chunk_id, page_id, text}
  POST /api/contribute/embedding      Submit embedding {chunk_id, embedding}
  GET  /api/work/quality?limit=10     Claim pages to review
  POST /api/contribute/quality        Submit quality score {page_id, score, model}

Try:  curl https://pageleft.cc/api/search?q=open+source+licensing
`, pages, chunks)
}
