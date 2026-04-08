package handler

import (
	"encoding/json"
	"fmt"
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
	h := New(db, platform.NewEmbedder(), "test")
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

// minimalPDF returns a valid single-page PDF with the given text content.
func minimalPDF(text string) []byte {
	// Minimal valid PDF with one page containing the given text.
	content := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	contentLen := len(content)

	var buf strings.Builder
	buf.WriteString("%PDF-1.4\n")
	// Object 1: Catalog
	obj1 := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	// Object 2: Pages
	obj2 := buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	// Object 3: Page
	obj3 := buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")
	// Object 4: Content stream
	obj4 := buf.Len()
	buf.WriteString(fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", contentLen, content))
	// Object 5: Font
	obj5 := buf.Len()
	buf.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
	// XRef
	xrefPos := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj1))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj2))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj3))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj4))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \n", obj5))
	buf.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\n")
	buf.WriteString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefPos))
	return []byte(buf.String())
}

func TestContributePage_PDF_AcceptedWithDomainLicense(t *testing.T) {
	pdfBytes := minimalPDF("Game Theory for Copyleft")

	fakeSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(pdfBytes)
	}))
	defer fakeSite.Close()

	// The fake site's host needs to be in the copyleft domain list.
	// Since we can't modify the embedded list, we test against the live
	// fetchAndVerify logic indirectly. Instead, test extractPDFContent directly.
	text, chunks, title, err := extractPDFContent(pdfBytes)
	if err != nil {
		t.Fatalf("extractPDFContent: %v", err)
	}
	if title == "" {
		t.Error("expected non-empty title from PDF")
	}
	if text == "" {
		t.Error("expected non-empty text from PDF")
	}
	if len(chunks) == 0 {
		t.Error("expected at least one chunk from PDF")
	}
	if !strings.Contains(text, "Game Theory") {
		t.Errorf("text should contain 'Game Theory', got: %s", text)
	}
	t.Logf("title=%q chunks=%d text_len=%d", title, len(chunks), len(text))
}

func TestContributePage_PDF_RejectedWithoutDomainLicense(t *testing.T) {
	pdfBytes := minimalPDF("No license here")

	fakeSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(pdfBytes)
	}))
	defer fakeSite.Close()

	h, cleanup := newTestHandler(t)
	defer cleanup()

	body := `{"url":"` + fakeSite.URL + `/test.pdf"}`
	req := httptest.NewRequest("POST", "/api/contribute/page", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 for PDF without domain license", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "PDF requires domain-level license") {
		t.Errorf("unexpected error: %s", rec.Body.String())
	}
}

func TestFrontierReject_DeletesAndLearns(t *testing.T) {
	h, cleanup := newTestHandler(t)
	defer cleanup()

	// Seed frontier: 5 from dead.example.com, 2 from flaky.example.com, 1 binary skip.
	h.db.AddToFrontier("https://dead.example.com/a", 0)
	h.db.AddToFrontier("https://dead.example.com/b", 0)
	h.db.AddToFrontier("https://dead.example.com/c", 0)
	h.db.AddToFrontier("https://dead.example.com/d", 0)
	h.db.AddToFrontier("https://dead.example.com/e", 0)
	h.db.AddToFrontier("https://flaky.example.com/x", 0)
	h.db.AddToFrontier("https://flaky.example.com/y", 0)
	h.db.AddToFrontier("https://imgs.example.com/pic.png", 0)

	// dead.example.com has 5 HTTP failures → should be learned.
	// flaky.example.com has only 2 → should NOT be learned.
	// imgs.example.com has "binary extension" reason → should NOT count toward learning.
	body := `[
		{"url":"https://dead.example.com/a","reason":"status 404"},
		{"url":"https://dead.example.com/b","reason":"status 404"},
		{"url":"https://dead.example.com/c","reason":"status 403"},
		{"url":"https://dead.example.com/d","reason":"fetch failed: timeout"},
		{"url":"https://dead.example.com/e","reason":"status 500"},
		{"url":"https://flaky.example.com/x","reason":"timeout"},
		{"url":"https://flaky.example.com/y","reason":"status 403"},
		{"url":"https://imgs.example.com/pic.png","reason":"binary extension"}
	]`
	req := httptest.NewRequest("POST", "/api/frontier/reject", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if deleted := int(resp["deleted"].(float64)); deleted != 8 {
		t.Errorf("deleted = %d, want 8", deleted)
	}
	if learned := int(resp["domains_learned"].(float64)); learned != 1 {
		t.Errorf("domains_learned = %d, want 1 (only dead.example.com)", learned)
	}

	// Frontier should be empty.
	entries, _ := h.db.PopFrontier(10)
	if len(entries) != 0 {
		t.Errorf("frontier has %d entries, want 0", len(entries))
	}
}

func TestContributePage_CapturesETagAndLastModified(t *testing.T) {
	// Server emits both validators in the response.
	fakeSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("ETag", `"abc-123"`)
		w.Header().Set("Last-Modified", "Mon, 01 Jan 2026 00:00:00 GMT")
		w.Write([]byte(copyleftPage()))
	}))
	defer fakeSite.Close()

	h, cleanup := newTestHandler(t)
	defer cleanup()

	body := `{"url":"` + fakeSite.URL + `/test"}`
	req := httptest.NewRequest("POST", "/api/contribute/page", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	page, err := h.db.GetPageByURL(fakeSite.URL + "/test")
	if err != nil {
		t.Fatalf("get page: %v", err)
	}
	if page.ETag != `"abc-123"` {
		t.Errorf("ETag = %q, want %q", page.ETag, `"abc-123"`)
	}
	if page.LastModified != "Mon, 01 Jan 2026 00:00:00 GMT" {
		t.Errorf("LastModified = %q, want header value", page.LastModified)
	}
	if page.LastValidated.IsZero() {
		t.Error("LastValidated should be set on fresh insert, got zero")
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
