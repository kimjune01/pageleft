package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalPDF returns a valid single-page PDF with the given text content.
func minimalPDF(text string) []byte {
	content := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	contentLen := len(content)

	var buf strings.Builder
	buf.WriteString("%PDF-1.4\n")
	obj1 := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	obj2 := buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	obj3 := buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")
	obj4 := buf.Len()
	buf.WriteString(fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", contentLen, content))
	obj5 := buf.Len()
	buf.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
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

func TestPickPDF(t *testing.T) {
	files := []zenodoFile{
		{Key: "supplement.pdf", Size: 100},
		{Key: "data.csv", Size: 9999},
		{Key: "paper.pdf", Size: 5000},
	}
	files[0].Links.Self = "https://example.org/supplement"
	files[2].Links.Self = "https://example.org/paper"

	f, ok := pickPDF(files, 30<<20)
	if !ok {
		t.Fatal("expected a PDF pick")
	}
	if f.Key != "paper.pdf" {
		t.Errorf("picked %q, want paper.pdf (largest PDF)", f.Key)
	}

	// Oversized PDFs are skipped.
	_, ok = pickPDF([]zenodoFile{{Key: "huge.pdf", Size: 31 << 20}}, 30<<20)
	if ok {
		t.Error("oversized PDF should not be picked")
	}

	// No PDFs at all.
	_, ok = pickPDF([]zenodoFile{{Key: "data.csv", Size: 10}}, 30<<20)
	if ok {
		t.Error("non-PDF files should not be picked")
	}
}

func TestExtractPDFText(t *testing.T) {
	text, err := extractPDFText(minimalPDF("Copyleft mechanisms for preprints"))
	if err != nil {
		t.Fatalf("extractPDFText: %v", err)
	}
	if !strings.Contains(text, "Copyleft mechanisms") {
		t.Errorf("text = %q, want it to contain the PDF content", text)
	}
}

func TestDrip_SubmitsWorkerPayload(t *testing.T) {
	pdfBytes := minimalPDF("Full text lives in the PDF, extracted worker-side.")

	// Fake Zenodo: search endpoint + file download.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/records", func(w http.ResponseWriter, r *http.Request) {
		// One page of results; empty beyond it, like a drained search window.
		if r.URL.Query().Get("page") != "1" {
			json.NewEncoder(w).Encode(map[string]any{
				"hits": map[string]any{"total": 1, "hits": []any{}},
			})
			return
		}
		var fileURL = "http://" + r.Host + "/api/records/42/files/paper.pdf/content"
		resp := map[string]any{
			"hits": map[string]any{
				"total": 1,
				"hits": []map[string]any{{
					"id": 42,
					"metadata": map[string]any{
						"title":        "Verified Auctions",
						"license":      map[string]any{"id": "cc-by-sa-4.0"},
						"access_right": "open",
					},
					"files": []map[string]any{{
						"key":   "paper.pdf",
						"size":  len(pdfBytes),
						"links": map[string]any{"self": fileURL},
					}},
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/records/42/files/paper.pdf/content", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(pdfBytes)
	})
	zenodo := httptest.NewServer(mux)
	defer zenodo.Close()

	// Fake pageleft: capture the contribution.
	var got submission
	pageleft := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/contribute/page" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"accepted": true, "page_id": 1})
	}))
	defer pageleft.Close()

	stateFile := filepath.Join(t.TempDir(), "state")
	cfg := config{
		API:       pageleft.URL,
		ZenodoAPI: zenodo.URL,
		State:     stateFile,
		Licenses:  []string{"cc-by-sa-4.0"},
		YearFrom:  2024,
		YearTo:    2024,
		Max:       10,
	}
	n, err := run(cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if n != 1 {
		t.Fatalf("submitted %d records, want 1", n)
	}

	if got.URL != "https://zenodo.org/records/42" {
		t.Errorf("url = %q, want canonical record URL", got.URL)
	}
	if got.Title != "Verified Auctions" {
		t.Errorf("title = %q, want record metadata title", got.Title)
	}
	if !strings.Contains(got.TextContent, "Full text lives in the PDF") {
		t.Errorf("text_content = %q, want extracted PDF text", got.TextContent)
	}
	if got.LicenseType != "CC BY-SA" {
		t.Errorf("license_type = %q, want CC BY-SA", got.LicenseType)
	}

	// State file records the submission, and a second run skips it.
	state, _ := os.ReadFile(stateFile)
	if !strings.Contains(string(state), "https://zenodo.org/records/42") {
		t.Errorf("state file missing record: %q", state)
	}
	n, err = run(cfg)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if n != 0 {
		t.Errorf("second run submitted %d records, want 0 (state dedup)", n)
	}

	// Dry-run must not mutate state.
	dryCfg := cfg
	dryCfg.State = filepath.Join(t.TempDir(), "dry-state")
	dryCfg.DryRun = true
	n, err = run(dryCfg)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if n != 1 {
		t.Errorf("dry run listed %d records, want 1", n)
	}
	if _, err := os.Stat(dryCfg.State); !os.IsNotExist(err) {
		t.Error("dry run wrote the state file — it must not mutate state")
	}
}
