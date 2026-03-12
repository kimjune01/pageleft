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
	mux.HandleFunc("GET /api/search", h.handleSearch)
	mux.HandleFunc("GET /api/stats", h.handleStats)
	mux.HandleFunc("GET /api/frontier", h.handleFrontier)
	mux.HandleFunc("POST /api/contribute/page", h.handleContributePage)
	mux.HandleFunc("GET /api/work/embed", h.handleWorkEmbed)
	mux.HandleFunc("POST /api/contribute/embedding", h.handleContributeEmbedding)
	return mux
}
