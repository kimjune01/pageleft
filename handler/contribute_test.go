package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kimjune01/pageleft/platform"
)

// copyleftPage returns minimal HTML with a copyleft license, title, and content paragraphs.
func copyleftPage() string {
	return `<html>
<head>
	<title>Test Page</title>
	<link rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/">
</head>
<body>
<article>
	<p>This is the first paragraph with enough text to pass the minimum length filter.</p>
	<p>This is the second paragraph also with enough text to be extracted as a chunk.</p>
</article>
</body>
</html>`
}

// newTestHandler creates a handler backed by an in-memory DB and no embedder.
// The returned cleanup function closes the DB.
func newTestHandler(t *testing.T) (*Handler, func()) {
	t.Helper()
	db, err := platform.NewDB(":memory:")
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	h := New(db, platform.NewEmbedder())
	return h, func() { db.Close() }
}

func TestContributePage_BareURL_ExtractsContent(t *testing.T) {
	// Serve a fake copyleft page.
	fakeSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(copyleftPage()))
	}))
	defer fakeSite.Close()

	h, cleanup := newTestHandler(t)
	defer cleanup()

	// Submit bare URL — no title, no text_content.
	body := `{"url":"` + fakeSite.URL + `/test"}`
	req := httptest.NewRequest("POST", "/api/contribute/page", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["accepted"] != true {
		t.Fatalf("expected accepted=true, got %v", resp["accepted"])
	}

	// Verify server extracted title and text.
	pageID := int64(resp["page_id"].(float64))
	if pageID == 0 {
		t.Fatal("expected nonzero page_id")
	}

	page, err := h.db.GetPageByURL(fakeSite.URL + "/test")
	if err != nil {
		t.Fatalf("get page: %v", err)
	}
	if page == nil {
		t.Fatal("page not found in db")
	}
	if page.Title != "Test Page" {
		t.Errorf("title = %q, want %q", page.Title, "Test Page")
	}
	if page.TextContent == "" {
		t.Error("text_content is empty — server-side extraction failed")
	}

	// Verify chunks were created.
	chunks := resp["chunks"].(float64)
	if chunks < 2 {
		t.Errorf("chunks = %v, want >= 2", chunks)
	}

	// Verify next instructions are present.
	next, ok := resp["next"].(map[string]any)
	if !ok {
		t.Fatal("missing next field in response")
	}
	if _, ok := next["embed"]; !ok {
		t.Error("missing next.embed")
	}
	if _, ok := next["quality"]; !ok {
		t.Error("missing next.quality")
	}
}

func TestContributePage_WorkerPayload_PreservesFields(t *testing.T) {
	// Serve a fake copyleft page (server still fetches for license verification).
	fakeSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(copyleftPage()))
	}))
	defer fakeSite.Close()

	h, cleanup := newTestHandler(t)
	defer cleanup()

	// Submit with worker-provided title and text — these should be preserved.
	body := `{"url":"` + fakeSite.URL + `/test","title":"Worker Title","text_content":"Worker extracted this text content for the page."}`
	req := httptest.NewRequest("POST", "/api/contribute/page", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	page, _ := h.db.GetPageByURL(fakeSite.URL + "/test")
	if page == nil {
		t.Fatal("page not found")
	}
	if page.Title != "Worker Title" {
		t.Errorf("title = %q, want %q — server overwrote worker-provided title", page.Title, "Worker Title")
	}
	if page.TextContent != "Worker extracted this text content for the page." {
		t.Errorf("text_content was overwritten — server should preserve worker-provided content")
	}
}

func TestContributePage_NoCopyleft_Rejected(t *testing.T) {
	fakeSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>No License</title></head><body><p>Hello</p></body></html>`))
	}))
	defer fakeSite.Close()

	h, cleanup := newTestHandler(t)
	defer cleanup()

	body := `{"url":"` + fakeSite.URL + `/test"}`
	req := httptest.NewRequest("POST", "/api/contribute/page", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 for non-copyleft page", rec.Code)
	}
}
