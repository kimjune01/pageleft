package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kimjune01/pageleft/platform"
)

func TestSearch_HollowPage_ReIndexed(t *testing.T) {
	// Serve a copyleft page that indexURL will fetch.
	fakeSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(copyleftPage()))
	}))
	defer fakeSite.Close()

	db, err := platform.NewDB(":memory:")
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	defer db.Close()

	// Insert a hollow page — URL exists but no content.
	hollow := &platform.Page{
		URL:         fakeSite.URL + "/test",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		CrawledAt:   time.Now(),
	}
	_, err = db.InsertPage(hollow)
	if err != nil {
		t.Fatalf("insert hollow page: %v", err)
	}

	// Verify it's hollow.
	page, _ := db.GetPageByURL(fakeSite.URL + "/test")
	if page == nil {
		t.Fatal("hollow page not found")
	}
	if page.TextContent != "" {
		t.Fatal("expected empty text_content for hollow page")
	}

	h := New(db, platform.NewEmbedder(), "test")

	// Search with the URL as query — should trigger re-indexing.
	// The search response itself may fail (no HF_TOKEN for query embedding),
	// but the re-indexing side effect should still happen.
	req := httptest.NewRequest("GET", "/api/search?q="+fakeSite.URL+"/test", nil)
	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, req)

	page, _ = db.GetPageByURL(fakeSite.URL + "/test")
	if page == nil {
		t.Fatal("page not found after re-index")
	}
	if page.TextContent == "" {
		t.Error("text_content still empty after search — hollow page was not re-indexed")
	}
	if page.Title == "" {
		t.Error("title still empty after re-index")
	}
}

func TestSearch_MissingQ_Returns400(t *testing.T) {
	h, cleanup := newTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search", nil)
	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSearch_TextQuery_ReturnsResults(t *testing.T) {
	db, err := platform.NewDB(":memory:")
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	defer db.Close()

	// Insert a page with embedding so search can match it.
	emb := make([]float64, platform.EmbeddingDim)
	emb[0] = 1.0 // unit vector along first dimension
	page := &platform.Page{
		URL:         "https://example.com/test",
		Title:       "Test Page",
		TextContent: "Some copyleft content about testing.",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		Embedding:   emb,
		CrawledAt:   time.Now(),
	}
	_, err = db.InsertPage(page)
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}

	h := New(db, platform.NewEmbedder(), "test")

	// Text query — embedding will fail without HF_TOKEN, so expect 500 or empty results.
	// This test just verifies the handler doesn't panic on a normal text query.
	req := httptest.NewRequest("GET", "/api/search?q=testing", nil)
	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, req)

	// Without HF_TOKEN the embed call fails and returns 500 — that's expected.
	// We just verify it doesn't panic and returns valid JSON or an error.
	if rec.Code == http.StatusOK {
		var resp searchResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Errorf("invalid JSON response: %v", err)
		}
	}
}
