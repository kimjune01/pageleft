package handler

import (
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/kimjune01/pageleft/crawler"
	"github.com/kimjune01/pageleft/platform"
	"github.com/kimjune01/pageleft/search"
)

type Handler struct {
	db       *platform.DB
	embedder *platform.Embedder
	robots   *crawler.RobotsChecker
	version  string

	// Chunk cache: loaded once, invalidated when embeddings change.
	chunkMu    sync.RWMutex
	chunkCache []platform.ChunkWithPage
	chunkDirty bool

	// Auto-reindex: track page count at last PageRank computation.
	lastReindexCount int
}

func New(db *platform.DB, embedder *platform.Embedder, version string) *Handler {
	pageCount, _ := db.PageCount()
	h := &Handler{
		db:               db,
		embedder:         embedder,
		robots:           crawler.NewRobotsChecker(&http.Client{Timeout: 10 * time.Second}),
		version:          version,
		chunkDirty:       true, // load on first search
		lastReindexCount: pageCount,
	}
	return h
}

// cachedChunks returns the chunk cache, reloading from DB if dirty.
func (h *Handler) cachedChunks() []platform.ChunkWithPage {
	h.chunkMu.RLock()
	if !h.chunkDirty {
		defer h.chunkMu.RUnlock()
		return h.chunkCache
	}
	h.chunkMu.RUnlock()

	h.chunkMu.Lock()
	defer h.chunkMu.Unlock()
	// Double-check after acquiring write lock
	if !h.chunkDirty {
		return h.chunkCache
	}
	chunks, err := h.db.AllChunksWithPages()
	if err != nil {
		log.Printf("chunk cache reload failed: %v", err)
		return h.chunkCache // return stale
	}
	h.chunkCache = chunks
	h.chunkDirty = false
	log.Printf("chunk cache loaded: %d chunks", len(chunks))
	return h.chunkCache
}

// invalidateChunkCache marks the cache dirty so the next search reloads.
func (h *Handler) invalidateChunkCache() {
	h.chunkMu.Lock()
	h.chunkDirty = true
	h.chunkMu.Unlock()
}

// maybeReindex recomputes PageRank if page count has grown by >5% since last reindex.
// Runs in a goroutine to avoid blocking the request.
func (h *Handler) maybeReindex() {
	if h.lastReindexCount < 10 {
		return // skip during tests and fresh DBs
	}
	current, _ := h.db.PageCount()
	threshold := float64(h.lastReindexCount) * 1.05
	if float64(current) <= threshold {
		return
	}
	h.lastReindexCount = current
	go func() {
		log.Printf("auto-reindex: page count %d > 5%% threshold, recomputing PageRank", current)
		if err := search.ComputePageRank(h.db); err != nil {
			log.Printf("auto-reindex failed: %v", err)
		} else {
			log.Printf("auto-reindex complete: %d pages", current)
		}
	}()
}

func (h *Handler) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.handleRoot)
	mux.HandleFunc("GET /skill.md", h.handleSkill)
	mux.HandleFunc("GET /favicon.ico", h.handleFavicon)
	mux.HandleFunc("GET /api/search", h.handleSearch)
	mux.HandleFunc("GET /api/stats", h.handleStats)
	mux.HandleFunc("GET /api/leaderboard", h.handleLeaderboard)
	mux.HandleFunc("GET /api/frontier", h.handleFrontier)
	mux.HandleFunc("POST /api/contribute/page", h.handleContributePage)
	mux.HandleFunc("GET /api/work/embed", h.handleWorkEmbed)
	mux.HandleFunc("GET /api/work/quality", h.handleWorkQuality)
	mux.HandleFunc("POST /api/contribute/embeddings", h.handleContributeEmbeddings)
	mux.HandleFunc("POST /api/contribute/quality", h.handleContributeQuality)
	mux.HandleFunc("POST /api/contribute/compilable", h.handleContributeCompilable)
	mux.HandleFunc("POST /api/embed", h.handleEmbed)
	mux.HandleFunc("GET /contribute", h.handleContribute)
	mux.HandleFunc("/api/", h.handleAPINotFound)
	return mux
}

// handleAPINotFound is the catch-all for /api/* paths that don't match any
// registered route. It returns a 404 with a hint pointing to the documented
// entry points so agents guessing route names get redirected instead of
// probing blindly.
func (h *Handler) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, `404 not found: %s %s

PageLeft routes:
  GET /            Overview and read API (search, stats, leaderboard)
  GET /contribute  Contribution endpoints (compute, quality, content)

Source: https://github.com/kimjune01/pageleft
`, r.Method, r.URL.Path)
}

//go:embed skill.md
var skillMD []byte

func (h *Handler) handleSkill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(skillMD)
}

func (h *Handler) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text y=".9em" font-size="90">🍄</text></svg>`)
}

func (h *Handler) handleContribute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, `Contributing to PageLeft 🍄

Four ways to contribute. All self-interested.
Read why: https://www.june.kim/why-contribute

Content
  Write a blog post under a copyleft license.
  PageLeft will find it, verify the license, and index it.

Code
  Publish a copyleft blog post explaining what you'd change and why.
  Open a one-line PR linking to it. A coding agent evaluates it
  against the manifesto and implements what aligns.
  https://github.com/kimjune01/pageleft

Compute
  Drain the embedding queue — needs only python3:

    git clone https://github.com/kimjune01/pageleft.git
    cd pageleft && ./drain.sh

  The script claims chunks, embeds via the public API, and batch-submits.
  No local model, no API keys, no dependencies.

  Or do it manually:
    1. curl https://pageleft.cc/api/work/embed?limit=10
    2. curl -X POST https://pageleft.cc/api/embed -d '{"texts":["...","..."]}'
    3. curl -X POST https://pageleft.cc/api/contribute/embeddings -d '[{"chunk_id":N,"embedding":[...]}]'

Quality
  Run a SOTA model against random pages and submit quality scores.
  Each score compounds into a page's ranking weight. No binary eviction, just math.

    1. curl https://pageleft.cc/api/work/quality?limit=10
    2. curl -X POST https://pageleft.cc/api/contribute/quality -d '{"page_id":N,"score":0.8,"model":"gpt-4o"}'

  See why this needs frontier models: https://www.june.kim/slop-detection
`)
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

Contribute: https://pageleft.cc/contribute

Try:  curl https://pageleft.cc/api/search?q=open+source+licensing

Claude Code Plugin
  claude plugin marketplace add kimjune01/pageleft
  claude plugin install pageleft@pageleft
`, pages, chunks)
}
