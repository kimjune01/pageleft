package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/kimjune01/pageleft/platform"
)

// newRevalidateTestDB creates a temp DB for revalidate integration tests.
func newRevalidateTestDB(t *testing.T) *platform.DB {
	t.Helper()
	f, err := os.CreateTemp("", "revalidate-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	db, err := platform.NewDB(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func htmlPage(body string) string {
	return `<html><head><title>Test</title>
<link rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/">
</head><body><article>` + body + `</article></body></html>`
}

func TestRevalidatePage_304NotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return 304 regardless of conditional headers — simulates "page unchanged".
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	db := newRevalidateTestDB(t)
	now := time.Now()
	id, err := db.InsertPage(&platform.Page{
		URL:           srv.URL + "/page",
		TextContent:   "old",
		ContentHash:   "oldhash",
		LicenseURL:    "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:   "CC BY-SA",
		CrawledAt:     now.Add(-24 * time.Hour),
		LastValidated: now.Add(-24 * time.Hour),
		ETag:          `"v1"`,
	})
	if err != nil {
		t.Fatal(err)
	}

	p, _ := db.GetPageByURL(srv.URL + "/page")
	action, err := revalidatePage(db, &http.Client{Timeout: 5 * time.Second}, p)
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if action != actionUnchanged {
		t.Errorf("action = %s, want unchanged", action)
	}

	// last_validated should be bumped.
	got, _ := db.GetPageByURL(srv.URL + "/page")
	if !got.LastValidated.After(now.Add(-1 * time.Hour)) {
		t.Errorf("last_validated not bumped: %v", got.LastValidated)
	}
	if got.ID != id {
		t.Errorf("page ID changed: %d != %d", got.ID, id)
	}
}

func TestRevalidatePage_200ChangedContent(t *testing.T) {
	newBody := htmlPage("<p>This is the new content with enough text to be a real paragraph chunk.</p>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("ETag", `"v2"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(newBody))
	}))
	defer srv.Close()

	db := newRevalidateTestDB(t)
	if _, err := db.InsertPage(&platform.Page{
		URL:           srv.URL + "/page",
		TextContent:   "old content",
		ContentHash:   "oldhash",
		LicenseURL:    "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:   "CC BY-SA",
		CrawledAt:     time.Now().Add(-24 * time.Hour),
		LastValidated: time.Now().Add(-24 * time.Hour),
		ETag:          `"v1"`,
	}); err != nil {
		t.Fatal(err)
	}

	p, _ := db.GetPageByURL(srv.URL + "/page")
	action, err := revalidatePage(db, &http.Client{Timeout: 5 * time.Second}, p)
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if action != actionUpdated {
		t.Errorf("action = %s, want updated", action)
	}

	got, _ := db.GetPageByURL(srv.URL + "/page")
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(newBody)))
	if got.ContentHash != wantHash {
		t.Errorf("ContentHash = %s, want %s", got.ContentHash, wantHash)
	}
	if got.ETag != `"v2"` {
		t.Errorf("ETag = %q, want %q", got.ETag, `"v2"`)
	}
}

func TestRevalidatePage_200UnchangedContent(t *testing.T) {
	body := htmlPage("<p>Same content as before, with enough text to be a paragraph.</p>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	db := newRevalidateTestDB(t)
	if _, err := db.InsertPage(&platform.Page{
		URL:           srv.URL + "/page",
		ContentHash:   hash,
		LicenseURL:    "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:   "CC BY-SA",
		CrawledAt:     time.Now().Add(-24 * time.Hour),
		LastValidated: time.Now().Add(-24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	p, _ := db.GetPageByURL(srv.URL + "/page")
	action, err := revalidatePage(db, &http.Client{Timeout: 5 * time.Second}, p)
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if action != actionUnchanged {
		t.Errorf("action = %s, want unchanged (same content_hash)", action)
	}
}

func TestRevalidatePage_410GoneDeletes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	db := newRevalidateTestDB(t)
	id, _ := db.InsertPage(&platform.Page{
		URL:         srv.URL + "/page",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		CrawledAt:   time.Now(),
	})

	p, _ := db.GetPageByURL(srv.URL + "/page")
	action, err := revalidatePage(db, &http.Client{Timeout: 5 * time.Second}, p)
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if action != actionDeleted {
		t.Errorf("action = %s, want deleted", action)
	}

	// Page should be gone.
	if _, err := db.GetPageByURL(srv.URL + "/page"); err == nil {
		t.Errorf("page %d should be deleted but is still present", id)
	}
}

func TestRevalidatePage_404UnderThreshold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	db := newRevalidateTestDB(t)
	id, _ := db.InsertPage(&platform.Page{
		URL:         srv.URL + "/page",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		CrawledAt:   time.Now(),
	})

	// First 404 → transient, count=1, page survives.
	p, _ := db.GetPageByURL(srv.URL + "/page")
	action, _ := revalidatePage(db, &http.Client{Timeout: 5 * time.Second}, p)
	if action != actionTransient {
		t.Errorf("first 404: action = %s, want transient", action)
	}
	got, _ := db.GetPageByURL(srv.URL + "/page")
	if got.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures after 1st 404 = %d, want 1", got.ConsecutiveFailures)
	}
	if got.ID != id {
		t.Errorf("page should still exist")
	}
}

func TestRevalidatePage_404OverThresholdDeletes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	db := newRevalidateTestDB(t)
	if _, err := db.InsertPage(&platform.Page{
		URL:                 srv.URL + "/page",
		LicenseURL:          "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:         "CC BY-SA",
		CrawledAt:           time.Now(),
		ConsecutiveFailures: stale404Threshold - 1, // one more 404 will tip it over
	}); err != nil {
		t.Fatal(err)
	}

	p, _ := db.GetPageByURL(srv.URL + "/page")
	action, err := revalidatePage(db, &http.Client{Timeout: 5 * time.Second}, p)
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if action != actionDeleted {
		t.Errorf("action = %s, want deleted (threshold reached)", action)
	}
	if _, err := db.GetPageByURL(srv.URL + "/page"); err == nil {
		t.Error("page should be deleted at threshold")
	}
}

func TestRevalidatePage_500Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	db := newRevalidateTestDB(t)
	if _, err := db.InsertPage(&platform.Page{
		URL:                 srv.URL + "/page",
		LicenseURL:          "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:         "CC BY-SA",
		CrawledAt:           time.Now(),
		ConsecutiveFailures: 0,
	}); err != nil {
		t.Fatal(err)
	}

	p, _ := db.GetPageByURL(srv.URL + "/page")
	action, _ := revalidatePage(db, &http.Client{Timeout: 5 * time.Second}, p)
	if action != actionTransient {
		t.Errorf("action = %s, want transient", action)
	}

	// 5xx must NOT increment consecutive_failures.
	got, _ := db.GetPageByURL(srv.URL + "/page")
	if got.ConsecutiveFailures != 0 {
		t.Errorf("5xx incremented failures to %d, should stay at 0", got.ConsecutiveFailures)
	}
}
