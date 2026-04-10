package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"

	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kimjune01/pageleft/crawler"
	"github.com/kimjune01/pageleft/platform"
	"github.com/ledongthuc/pdf"

	"golang.org/x/net/html"
)

// handleFrontierReject lets workers report URLs that failed with transient errors
// (404, 403, timeout). Deletes the URLs from the frontier. If a domain appears
// 3+ times in a single batch, learns it as non-permissive.
// POST /api/frontier/reject  [{"url":"...","reason":"..."},...]
func (h *Handler) handleFrontierReject(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	var items []struct {
		URL    string `json:"url"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		http.Error(w, `{"error":"invalid JSON, expected array"}`, http.StatusBadRequest)
		return
	}
	if len(items) == 0 {
		http.Error(w, `{"error":"empty batch"}`, http.StatusBadRequest)
		return
	}
	if len(items) > 200 {
		http.Error(w, `{"error":"max 200 items per batch"}`, http.StatusBadRequest)
		return
	}

	// Count rejections per domain to detect persistent failures.
	// Only HTTP-level failures (status codes, timeouts) count toward domain
	// learning. Client-side skips (binary extension, etc.) are just cleanup.
	domainCounts := make(map[string]int)
	deleted := 0
	for _, item := range items {
		if item.URL == "" {
			continue
		}
		h.db.DeleteFrontierURL(item.URL)
		deleted++
		domain := crawler.ExtractDomain(item.URL)
		if domain == "" {
			continue
		}
		// Only count server-side failures toward domain learning.
		// Binary extensions, client-side skips, etc. say nothing about the domain.
		if strings.Contains(item.Reason, "status ") ||
			strings.Contains(item.Reason, "timeout") ||
			strings.Contains(item.Reason, "fetch failed") {
			domainCounts[domain]++
		}
	}

	// If a domain has 5+ HTTP failures in one batch, learn it as non-permissive.
	// Threshold is intentionally conservative — false positives are permanent.
	learned := 0
	for domain, count := range domainCounts {
		if count >= 5 {
			crawler.LearnNonPermissive("https://" + domain)
			learned++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"deleted":         deleted,
		"domains_learned": learned,
	})
}

// handleFrontier returns frontier URLs for workers to crawl.
// GET /api/frontier?limit=10
func (h *Handler) handleFrontier(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	entries, err := h.db.PopFrontier(limit)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	type frontierURL struct {
		URL      string  `json:"url"`
		Priority float64 `json:"priority"`
	}
	out := make([]frontierURL, len(entries))
	for i, e := range entries {
		out[i] = frontierURL{URL: e.URL, Priority: e.Priority}
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
// A bare {"url":"..."} is enough — the server extracts title, text, chunks,
// and links from the page it already fetches for license verification.
// Workers may still supply rich payloads; server-extracted fields only fill gaps.
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

	// Strip tracking params before fetch. The share-link variants
	// (?share=linkedin, ?share=twitter) trigger off-domain redirects to
	// login pages that pollute the index. We do not call full NormalizeURL
	// here because that would upgrade http→https and break HTTP-only sites.
	sub.URL = platform.StripTrackingParams(sub.URL)

	// Fetch the page, verify copyleft license, and keep the parsed HTML.
	result, err := fetchAndVerify(sub.URL)
	if err != nil {
		log.Printf("license verification failed for %s: %v", sub.URL, err)
		// Only learn non-permissive if the page was fetched successfully but
		// had no copyleft or public domain license. Transient errors (403,
		// 500, timeout) are not evidence — only "no license found" is.
		if strings.Contains(err.Error(), "no copyleft license found") {
			crawler.LearnNonPermissive(sub.URL)
		}
		// Remove from frontier so rejected URLs don't cycle forever.
		h.db.DeleteFrontierURL(sub.URL)
		http.Error(w, fmt.Sprintf(`{"error":"license verification failed: %v"}`, err), http.StatusUnprocessableEntity)
		return
	}

	// Fill in any fields the submitter didn't provide.
	if result.IsPDF {
		if sub.Title == "" {
			sub.Title = result.PDFTitle
		}
		if sub.TextContent == "" {
			sub.TextContent = result.PDFText
		}
	} else {
		if sub.Title == "" {
			sub.Title = crawler.ExtractTitle(result.Doc)
		}
		if sub.TextContent == "" {
			sub.TextContent = crawler.ExtractText(result.Doc)
		}
		if len(sub.Links) == 0 {
			sub.Links = crawler.ExtractLinks(result.Doc, result.FinalURL)
		}
	}
	if sub.ContentHash == "" {
		sub.ContentHash = result.BodyHash
	}

	h.db.LogContribution("crawl", platform.ContributorHash(r.RemoteAddr))
	now := time.Now()
	page := &platform.Page{
		URL:           crawler.CanonicalPageURL(sub.URL),
		Title:         sub.Title,
		TextContent:   sub.TextContent,
		LicenseURL:    result.License.URL,
		LicenseType:   result.License.Type,
		ContentHash:   sub.ContentHash,
		CrawledAt:     now,
		ETag:          result.ETag,
		LastModified:  result.LastModified,
		LastValidated: now,
	}

	pageID, err := h.db.InsertPageWithLinks(page, sub.Links, crawler.ShouldBlockFrontierFrom(sub.URL))
	if err != nil {
		http.Error(w, `{"error":"insert failed"}`, http.StatusInternalServerError)
		return
	}
	h.maybeReindex()

	// Extract paragraphs and insert as chunks (embeddings come from the work queue).
	var paragraphs []string
	if result.IsPDF {
		paragraphs = result.PDFChunks
	} else {
		paragraphs = crawler.ExtractParagraphs(result.Doc)
	}
	chunks := make([]platform.Chunk, 0, len(paragraphs))
	for i, text := range paragraphs {
		chunks = append(chunks, platform.Chunk{
			PageID: pageID,
			Idx:    i,
			Text:   text,
		})
	}
	if len(chunks) > 0 {
		if err := h.db.InsertChunks(pageID, chunks); err != nil {
			log.Printf("insert chunks for %s: %v", sub.URL, err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"accepted": true,
		"page_id":  pageID,
		"license":  result.License.Type,
		"chunks":   len(chunks),
		"next": map[string]string{
			"embed":   "GET /api/work/embed?limit=10 — compute embeddings to make this page searchable",
			"quality": "GET /api/work/quality?limit=10 — review pages for quality scores",
		},
	})
}

// handleEmbed exposes the server's HF-backed embedder as a public endpoint.
// Contributors can embed text without needing their own HF token or local model.
// POST /api/embed  {"text":"..."} or {"texts":["...","..."]}
func (h *Handler) handleEmbed(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		Text  string   `json:"text"`
		Texts []string `json:"texts"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Text == "" && len(req.Texts) == 0 {
		http.Error(w, `{"error":"provide text or texts"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if req.Text != "" {
		vec, err := h.embedder.Embed(req.Text)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"embed failed: %v"}`, err), http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"embedding": vec,
			"dim":       len(vec),
		})
		return
	}

	// Batch mode — cap at 32 texts to limit HF API abuse.
	if len(req.Texts) > 32 {
		http.Error(w, `{"error":"max 32 texts per batch"}`, http.StatusBadRequest)
		return
	}
	vecs, err := h.embedder.EmbedBatch(req.Texts)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"embed failed: %v"}`, err), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"embeddings": vecs,
		"dim":        platform.EmbeddingDim,
	})
}

// handleWorkEmbed returns chunks that need embeddings computed.
// Every item has the same shape: {chunk_id, page_id, text}.
// Pages without chunks are chunked on demand before serving.
// GET /api/work/embed?limit=10
func (h *Handler) handleWorkEmbed(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	type chunkWork struct {
		ChunkID int64  `json:"chunk_id"`
		PageID  int64  `json:"page_id"`
		Text    string `json:"text"`
	}
	type workResponse struct {
		Model string      `json:"model"`
		Dim   int         `json:"dim"`
		Items []chunkWork `json:"items"`
	}

	// Try chunk work items first.
	chunks, err := h.db.ChunksWithoutEmbeddings(limit)
	if err == nil && len(chunks) > 0 {
		items := make([]chunkWork, len(chunks))
		for i, c := range chunks {
			items[i] = chunkWork{
				ChunkID: c.ID,
				PageID:  c.PageID,
				Text:    c.Text,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(workResponse{
			Model: platform.EmbeddingModel,
			Dim:   platform.EmbeddingDim,
			Items: items,
		})
		return
	}

	// No unembedded chunks. Check for pages that were never chunked.
	pages, err := h.db.PagesWithoutChunks(limit)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	// Chunk them from stored text_content and insert.
	var allChunks []chunkWork
	for _, p := range pages {
		paragraphs := SplitTextContent(p.TextContent)
		if len(paragraphs) == 0 {
			continue
		}
		dbChunks := make([]platform.Chunk, len(paragraphs))
		for i, text := range paragraphs {
			dbChunks[i] = platform.Chunk{PageID: p.ID, Idx: i, Text: text}
		}
		if err := h.db.InsertChunks(p.ID, dbChunks); err != nil {
			log.Printf("auto-chunk page %d (%s): %v", p.ID, p.URL, err)
			continue
		}
		// Re-fetch the inserted chunks to get their IDs.
		inserted, err := h.db.ChunksWithoutEmbeddings(len(paragraphs))
		if err != nil {
			continue
		}
		for _, c := range inserted {
			if c.PageID == p.ID {
				allChunks = append(allChunks, chunkWork{
					ChunkID: c.ID,
					PageID:  c.PageID,
					Text:    c.Text,
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workResponse{
		Model: platform.EmbeddingModel,
		Dim:   platform.EmbeddingDim,
		Items: allChunks,
	})
}

// SplitTextContent splits plain text into paragraph-sized chunks.
// Used when a page was stored without HTML parsing (no *html.Node available),
// and by the prune-stale revalidator for text/plain and markdown bodies.
func SplitTextContent(text string) []string {
	var paragraphs []string
	for _, p := range strings.Split(text, "\n") {
		p = strings.TrimSpace(p)
		if len(p) > 20 { // skip blank lines and trivial fragments
			paragraphs = append(paragraphs, p)
		}
	}
	return paragraphs
}

// batchEmbeddingItem is one entry in a batch embedding submission.
type batchEmbeddingItem struct {
	ChunkID   int64     `json:"chunk_id"`
	Embedding []float64 `json:"embedding"`
}

// handleContributeEmbeddings accepts a batch of embeddings in one request.
// POST /api/contribute/embeddings  [{"chunk_id":1,"embedding":[...]}, ...]
func (h *Handler) handleContributeEmbeddings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	var items []batchEmbeddingItem
	if err := json.Unmarshal(body, &items); err != nil {
		http.Error(w, `{"error":"invalid JSON, expected array"}`, http.StatusBadRequest)
		return
	}

	if len(items) == 0 {
		http.Error(w, `{"error":"empty batch"}`, http.StatusBadRequest)
		return
	}
	if len(items) > 100 {
		http.Error(w, `{"error":"max 100 items per batch"}`, http.StatusBadRequest)
		return
	}

	// Reject the entire batch if any item has a null or wrong-dimension embedding.
	// Don't silently skip — make the caller fix their pipeline.
	for i, item := range items {
		if item.ChunkID == 0 {
			msg := fmt.Sprintf(
				`{"error":"item %d has chunk_id 0. You had ONE job: fetch work, embed it, submit it. chunk_id is not optional."}`, i)
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		if len(item.Embedding) == 0 {
			msg := fmt.Sprintf(
				`{"error":"item %d (chunk %d) has a null/empty embedding. Do NOT submit chunks you failed to embed. That defeats the entire purpose of this endpoint."}`,
				i, item.ChunkID)
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		if len(item.Embedding) != platform.EmbeddingDim {
			msg := fmt.Sprintf(
				`{"error":"item %d (chunk %d) has %d dimensions, expected %d. You are submitting embeddings from the wrong model. Use %s."}`,
				i, item.ChunkID, len(item.Embedding), platform.EmbeddingDim, platform.EmbeddingModel)
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
	}

	contributor := platform.ContributorHash(r.RemoteAddr)
	accepted := 0
	completedPages := []int64{}

	for _, item := range items {
		if err := h.db.UpdateChunkEmbedding(item.ChunkID, item.Embedding); err != nil {
			continue
		}
		accepted++
		if pageID, err := h.db.PageIDForChunk(item.ChunkID); err == nil {
			if allDone, err := h.db.AllChunksEmbedded(pageID); err == nil && allDone {
				h.computePageEmbedding(pageID)
				completedPages = append(completedPages, pageID)
			}
		}
	}

	if accepted > 0 {
		h.db.LogContribution("embed", contributor)
		if len(completedPages) > 0 {
			h.invalidateChunkCache()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"accepted":        accepted,
		"total":           len(items),
		"pages_completed": len(completedPages),
	})
}

// computePageEmbedding averages chunk embeddings into a page-level embedding.
func (h *Handler) computePageEmbedding(pageID int64) {
	chunks, err := h.db.ChunkEmbeddingsForPage(pageID)
	if err != nil || len(chunks) == 0 {
		return
	}
	dim := len(chunks[0])
	avg := make([]float64, dim)
	for _, emb := range chunks {
		for i, v := range emb {
			avg[i] += v
		}
	}
	n := float64(len(chunks))
	var norm float64
	for i := range avg {
		avg[i] /= n
		norm += avg[i] * avg[i]
	}
	if norm > 0 {
		scale := 1.0 / math.Sqrt(norm)
		for i := range avg {
			avg[i] *= scale
		}
	}
	h.db.UpdateEmbedding(pageID, avg)
}

// handleWorkQuality returns random pages for quality review.
// GET /api/work/quality?limit=10
func (h *Handler) handleWorkQuality(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	pages, err := h.db.RandomPagesForReview(limit)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	type qualityWork struct {
		PageID  int64  `json:"page_id"`
		URL     string `json:"url"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	items := make([]qualityWork, len(pages))
	for i, p := range pages {
		content := p.TextContent
		if len(content) > 2000 {
			content = content[:2000]
		}
		items[i] = qualityWork{
			PageID:  p.ID,
			URL:     p.URL,
			Title:   p.Title,
			Content: content,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

type qualitySubmission struct {
	PageID int64   `json:"page_id"`
	Score  float64 `json:"score"`
	Model  string  `json:"model"`
}

// handleContributeQuality accepts a quality score from a federated reviewer.
// POST /api/contribute/quality
func (h *Handler) handleContributeQuality(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	var sub qualitySubmission
	if err := json.Unmarshal(body, &sub); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if sub.PageID == 0 {
		http.Error(w, `{"error":"page_id is required"}`, http.StatusBadRequest)
		return
	}
	if sub.Score < 0 || sub.Score > 1 {
		http.Error(w, `{"error":"score must be between 0 and 1"}`, http.StatusBadRequest)
		return
	}

	contributor := platform.ContributorHash(r.RemoteAddr)
	h.db.LogContribution("review", contributor)
	if err := h.db.SubmitQualityScore(sub.PageID, sub.Score, sub.Model, contributor); err != nil {
		http.Error(w, `{"error":"submit failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"accepted": true,
		"next": map[string]string{
			"quality": "GET /api/work/quality?limit=10 — review more pages",
			"embed":   "GET /api/work/embed?limit=10 — embed chunks if any pending",
		},
	})
}

type compilableSubmission struct {
	PageID     int64  `json:"page_id"`
	Compilable bool   `json:"compilable"`
	RepoURL    string `json:"repo_url"`
}

// handleContributeCompilable marks a page as compilable (has reference implementation).
// POST /api/contribute/compilable
func (h *Handler) handleContributeCompilable(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	var sub compilableSubmission
	if err := json.Unmarshal(body, &sub); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if sub.PageID == 0 {
		http.Error(w, `{"error":"page_id is required"}`, http.StatusBadRequest)
		return
	}

	if err := h.db.SetCompilable(sub.PageID, sub.Compilable); err != nil {
		http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"accepted": true})
}

// fetchResult holds the parsed page and license from a verification fetch.
type fetchResult struct {
	License      *crawler.LicenseInfo
	Doc          *html.Node // nil for PDFs
	FinalURL     string
	BodyHash     string
	ETag         string
	LastModified string
	// PDF-only fields
	IsPDF     bool
	PDFTitle  string
	PDFText   string
	PDFChunks []string
}

// fetchAndVerify runs the filter chain (crawler.Resolve), fetches the URL,
// and returns parsed content with a verified license.
func fetchAndVerify(pageURL string) (*fetchResult, error) {
	res := crawler.Resolve(pageURL)
	if res.Action != crawler.Allow {
		return nil, fmt.Errorf("%s", res.Reason)
	}

	// Forge URLs: Resolve already checked license and rewrote to raw README.
	// Fetch the README as markdown, wrap in HTML for the paragraph extractor.
	if res.FetchURL != "" && strings.Contains(res.FetchURL, "raw.githubusercontent.com") ||
		res.FetchURL != "" && strings.Contains(res.FetchURL, "codeberg.org") && strings.Contains(res.FetchURL, "/raw/") {
		return fetchForgeReadme(pageURL, res)
	}

	// Standard fetch (HTML or PDF)
	fetchURL := pageURL
	if res.FetchURL != "" {
		fetchURL = res.FetchURL
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", fetchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", crawler.RobotsUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Off-domain redirect check: if the response landed on a different host
	// than the requested URL, the cached res.License (from the original
	// allowlisted domain) doesn't apply. This catches share-link tracking
	// params like ?share=linkedin that redirect to login pages.
	requestedDomain := crawler.ExtractDomain(fetchURL)
	finalDomain := crawler.ExtractDomain(resp.Request.URL.String())
	if requestedDomain != "" && finalDomain != "" && requestedDomain != finalDomain {
		return nil, fmt.Errorf("redirect off-domain: %s → %s", requestedDomain, finalDomain)
	}

	contentType := resp.Header.Get("Content-Type")
	isPDF := strings.Contains(contentType, "application/pdf")
	isHTML := strings.Contains(contentType, "text/html")

	if !isHTML && !isPDF {
		return nil, fmt.Errorf("unsupported content type: %s", contentType)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	h := fmt.Sprintf("%x", sha256.Sum256(bodyBytes))
	finalURL := resp.Request.URL.String()
	etag := resp.Header.Get("ETag")
	lastModified := resp.Header.Get("Last-Modified")

	if isPDF {
		if res.License == nil {
			return nil, fmt.Errorf("PDF requires domain-level license verification")
		}
		text, chunks, title, err := ExtractPDFContent(bodyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse PDF: %w", err)
		}
		return &fetchResult{
			License: res.License, FinalURL: finalURL, BodyHash: h,
			ETag: etag, LastModified: lastModified,
			IsPDF: true, PDFTitle: title, PDFText: text, PDFChunks: chunks,
		}, nil
	}

	// HTML path
	doc, err := html.Parse(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	license := res.License
	if license == nil {
		license = crawler.DetectLicense(doc)
	}
	if license == nil {
		return nil, fmt.Errorf("no copyleft license found")
	}

	return &fetchResult{
		License: license, Doc: doc, FinalURL: finalURL, BodyHash: h,
		ETag: etag, LastModified: lastModified,
	}, nil
}

// fetchForgeReadme fetches a raw README from a forge and wraps it in HTML.
func fetchForgeReadme(pageURL string, res crawler.Resolution) (*fetchResult, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest("GET", res.FetchURL, nil)
	req.Header.Set("User-Agent", crawler.RobotsUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch README: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("README not found: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read README: %w", err)
	}

	h := fmt.Sprintf("%x", sha256.Sum256(body))

	// Forge pages are stored under the canonical github.com/owner/repo URL,
	// but the README is fetched from raw.githubusercontent.com — a different
	// resource with its own ETag and Last-Modified. Storing the raw README's
	// validators against the page URL would be semantically wrong because
	// Layer 1's conditional GET would target the wrong endpoint.
	//
	// Until Layer 1 decides how to handle forge revalidation (e.g. by storing
	// the fetch URL alongside the page URL, or by always doing unconditional
	// fetches for forge content), we deliberately leave ETag and LastModified
	// empty for forge pages.

	// Wrap markdown lines in <p> tags for the paragraph extractor
	htmlStr := "<html><body><article>"
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		htmlStr += "<p>" + line + "</p>\n"
	}
	htmlStr += "</article></body></html>"

	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("parse README: %w", err)
	}

	return &fetchResult{License: res.License, Doc: doc, FinalURL: pageURL, BodyHash: h}, nil
}

// ExtractPDFContent extracts text from PDF bytes, splits into chunks by page.
func ExtractPDFContent(data []byte) (fullText string, chunks []string, title string, err error) {
	tmpFile, err := os.CreateTemp("", "pageleft-*.pdf")
	if err != nil {
		return "", nil, "", fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := tmpFile.Write(data); err != nil {
		return "", nil, "", fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	f, r, err := pdf.Open(tmpFile.Name())
	if err != nil {
		return "", nil, "", fmt.Errorf("open PDF: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if i == 1 {
			// Use first non-empty line as title
			lines := strings.SplitN(text, "\n", 2)
			title = strings.TrimSpace(lines[0])
		}
		chunks = append(chunks, text)
		buf.WriteString(text)
		buf.WriteString("\n\n")
	}

	return buf.String(), chunks, title, nil
}
