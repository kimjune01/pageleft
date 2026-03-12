package handler

import (
	"net/http"

	"github.com/kimjune01/pageleft/platform"
)

type Handler struct {
	db       *platform.DB
	embedder *platform.Embedder
}

func New(db *platform.DB, embedder *platform.Embedder) *Handler {
	return &Handler{db: db, embedder: embedder}
}

func (h *Handler) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleRoot)
	mux.HandleFunc("GET /api/search", h.handleSearch)
	mux.HandleFunc("GET /api/stats", h.handleStats)
	mux.HandleFunc("GET /api/frontier", h.handleFrontier)
	mux.HandleFunc("POST /api/contribute/page", h.handleContributePage)
	mux.HandleFunc("GET /api/work/embed", h.handleWorkEmbed)
	mux.HandleFunc("POST /api/contribute/embedding", h.handleContributeEmbedding)
	return mux
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(`PageLeft — a search engine for copyleft ideas.

Read more: https://www.june.kim/pageleft
Source:    https://github.com/kimjune01/pageleft

API
  GET  /api/search?q=<query>        Search by natural language
  GET  /api/stats                   Corpus stats
  GET  /api/frontier                Next URLs to crawl

Try:  curl https://pageleft.cc/api/search?q=open+source+licensing
`))
}
