package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/kimjune01/pageleft/crawler"
	"github.com/kimjune01/pageleft/handler"
	"github.com/kimjune01/pageleft/platform"
)

// stale404Threshold is the number of consecutive 404 observations after which
// a page is considered permanently gone and gets deleted. Cloudflare/CDN
// hiccups happen, so a single 404 isn't enough.
const stale404Threshold = 3

// revalidationAction is the outcome of a single per-page revalidation.
type revalidationAction string

const (
	actionUnchanged revalidationAction = "unchanged" // 304 Not Modified, or 200 with same content_hash
	actionUpdated   revalidationAction = "updated"   // 200 with new content
	actionDeleted   revalidationAction = "deleted"   // 410, persistent 404, or off-domain redirect
	actionTransient revalidationAction = "transient" // 5xx, timeout, DNS failure — leave page alone
)

// revalidatePage issues a conditional GET against a single indexed page and
// applies the appropriate update to the database. Returns the action taken
// and any error encountered. Errors are NOT considered transient — they
// indicate something went wrong with the revalidation logic itself, not the
// remote server. Transient remote failures are reported via actionTransient
// with a nil error.
func revalidatePage(db *platform.DB, client *http.Client, p *platform.Page) (revalidationAction, error) {
	// For forge pages (github.com/owner/repo), the actual content lives at
	// raw.githubusercontent.com. crawler.Resolve returns the rewritten URL.
	// For everything else, FetchURL is empty and we use p.URL as-is.
	fetchURL := p.URL
	if res := crawler.Resolve(p.URL); res.FetchURL != "" {
		fetchURL = res.FetchURL
	}
	req, err := http.NewRequest("GET", fetchURL, nil)
	if err != nil {
		return actionTransient, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", crawler.RobotsUserAgent)
	if p.ETag != "" {
		req.Header.Set("If-None-Match", p.ETag)
	}
	if p.LastModified != "" {
		req.Header.Set("If-Modified-Since", p.LastModified)
	}

	resp, err := client.Do(req)
	if err != nil {
		// Network failure, DNS failure, timeout — transient. Don't penalize.
		log.Printf("  transient: %s — %v", p.URL, err)
		return actionTransient, nil
	}
	defer resp.Body.Close()

	now := time.Now()

	// Off-domain redirect → treat as deletion (mirrors fetchAndVerify logic).
	// Compare against fetchURL, not p.URL — for forge pages, fetchURL is the
	// resolved raw URL (raw.githubusercontent.com) and the response will land
	// there; comparing against p.URL (github.com) would always trip.
	requestedHost := crawler.ExtractDomain(fetchURL)
	finalHost := crawler.ExtractDomain(resp.Request.URL.String())
	if requestedHost != "" && finalHost != "" && requestedHost != finalHost {
		log.Printf("  off-domain redirect: %s → %s", fetchURL, resp.Request.URL.String())
		if err := db.DeletePage(p.ID); err != nil {
			return actionDeleted, fmt.Errorf("delete page: %w", err)
		}
		return actionDeleted, nil
	}

	switch {
	case resp.StatusCode == http.StatusNotModified:
		// 304 → bump last_validated, reset failures, keep validators.
		if err := db.UpdatePageValidators(p.ID, p.ETag, p.LastModified, now, 0); err != nil {
			return actionUnchanged, fmt.Errorf("update validators: %w", err)
		}
		return actionUnchanged, nil

	case resp.StatusCode == http.StatusGone: // 410
		log.Printf("  410 Gone: %s", p.URL)
		if err := db.DeletePage(p.ID); err != nil {
			return actionDeleted, fmt.Errorf("delete page: %w", err)
		}
		return actionDeleted, nil

	case resp.StatusCode == http.StatusNotFound: // 404
		count, err := db.IncrementPageFailures(p.ID, now)
		if err != nil {
			return actionTransient, fmt.Errorf("increment failures: %w", err)
		}
		if count >= stale404Threshold {
			log.Printf("  404 (threshold reached, %d): %s", count, p.URL)
			if err := db.DeletePage(p.ID); err != nil {
				return actionDeleted, fmt.Errorf("delete page: %w", err)
			}
			return actionDeleted, nil
		}
		log.Printf("  404 (%d/%d): %s", count, stale404Threshold, p.URL)
		return actionTransient, nil

	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		log.Printf("  transient %d: %s", resp.StatusCode, p.URL)
		return actionTransient, nil

	case resp.StatusCode == http.StatusOK:
		// Fall through to body handling below.

	default:
		// 4xx other than 404/410 — treat as transient. Often a temporary
		// auth/permission state, not a deletion.
		log.Printf("  unexpected status %d: %s", resp.StatusCode, p.URL)
		return actionTransient, nil
	}

	// 200 OK — read body, check content_type for dispatch, compare hash.
	// Read 20 MiB + 1 byte to detect overflow: if we got the extra byte,
	// the page exceeds our limit and we'd be hashing/storing truncated
	// content. Skip such pages as transient rather than corrupt the index.
	const bodyLimit = 20 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	if err != nil {
		return actionTransient, fmt.Errorf("read body: %w", err)
	}
	if len(body) > bodyLimit {
		log.Printf("  body exceeds %d bytes, skipping: %s", bodyLimit, p.URL)
		return actionTransient, nil
	}

	newHash := fmt.Sprintf("%x", sha256.Sum256(body))
	newETag := resp.Header.Get("ETag")
	newLastModified := resp.Header.Get("Last-Modified")

	if newHash == p.ContentHash {
		// 200 but body unchanged. Just refresh validators.
		if err := db.UpdatePageValidators(p.ID, newETag, newLastModified, now, 0); err != nil {
			return actionUnchanged, fmt.Errorf("update validators: %w", err)
		}
		return actionUnchanged, nil
	}

	// Content changed. Dispatch by content type.
	newText, chunks, err := extractRevalidatedContent(resp.Header.Get("Content-Type"), body)
	if err != nil {
		log.Printf("  skipping %s: %v", p.URL, err)
		return actionTransient, nil
	}

	// Atomic: page row + chunks update in a single transaction.
	if err := db.UpdatePageContent(p.ID, newText, newHash, newETag, newLastModified, now, chunks); err != nil {
		return actionUpdated, fmt.Errorf("update content: %w", err)
	}
	return actionUpdated, nil
}

// extractRevalidatedContent dispatches body parsing by Content-Type,
// returning the text content and chunks ready for storage.
func extractRevalidatedContent(contentType string, body []byte) (string, []platform.Chunk, error) {
	switch {
	case strings.Contains(contentType, "application/pdf"):
		text, rawChunks, _, err := handler.ExtractPDFContent(body)
		if err != nil {
			return "", nil, fmt.Errorf("parse PDF: %w", err)
		}
		return text, paragraphsToChunks(rawChunks), nil
	case strings.Contains(contentType, "text/html"), strings.Contains(contentType, "application/xhtml+xml"):
		doc, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			return "", nil, fmt.Errorf("parse HTML: %w", err)
		}
		return crawler.ExtractText(doc), paragraphsToChunks(crawler.ExtractParagraphs(doc)), nil
	case strings.HasPrefix(contentType, "text/plain"), strings.Contains(contentType, "markdown"):
		text := string(body)
		return text, paragraphsToChunks(handler.SplitTextContent(text)), nil
	default:
		return "", nil, fmt.Errorf("unsupported content type: %s", contentType)
	}
}

func paragraphsToChunks(paragraphs []string) []platform.Chunk {
	chunks := make([]platform.Chunk, 0, len(paragraphs))
	for i, text := range paragraphs {
		chunks = append(chunks, platform.Chunk{Idx: i, Text: text})
	}
	return chunks
}

// cmdPruneStale is the entry point for the `pageleft prune-stale` subcommand.
// It iterates over all pages oldest-validated first, issues a conditional GET
// against each, and applies the resulting action.
func cmdPruneStale(dbPath string) {
	db, err := platform.NewDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	pages, err := db.PagesForRevalidation(0)
	if err != nil {
		log.Fatalf("list pages: %v", err)
	}
	log.Printf("revalidating %d pages", len(pages))

	client := &http.Client{Timeout: 30 * time.Second}
	var unchanged, updated, deleted, transient int

	for _, p := range pages {
		action, err := revalidatePage(db, client, p)
		if err != nil {
			log.Printf("  error revalidating %s: %v", p.URL, err)
		}
		switch action {
		case actionUnchanged:
			unchanged++
		case actionUpdated:
			updated++
		case actionDeleted:
			deleted++
		case actionTransient:
			transient++
		}
		// Be polite — 100ms between requests.
		time.Sleep(100 * time.Millisecond)
	}

	log.Printf("revalidated %d pages: %d unchanged, %d updated, %d deleted, %d transient",
		len(pages), unchanged, updated, deleted, transient)
}
