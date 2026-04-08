package platform

import (
	"math"
	"os"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	f, err := os.CreateTemp("", "pageleft-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := NewDB(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertTestPage(t *testing.T, db *DB) int64 {
	t.Helper()
	page := &Page{
		URL:         "https://example.com/test",
		Title:       "Test Page",
		TextContent: "Some content",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		ContentHash: "abc123",
	}
	id, err := db.InsertPageWithLinks(page, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func getQuality(t *testing.T, db *DB, pageID int64) float64 {
	t.Helper()
	var q float64
	err := db.conn.QueryRow("SELECT quality FROM pages WHERE id = ?", pageID).Scan(&q)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestQualityGeometricMean(t *testing.T) {
	db := testDB(t)
	pageID := insertTestPage(t, db)

	// Initial quality is 1.0
	q := getQuality(t, db, pageID)
	if q != 1.0 {
		t.Fatalf("initial quality: got %f, want 1.0", q)
	}

	// One review of 0.9 → quality = 0.9
	err := db.SubmitQualityScore(pageID, 0.9, "test-model", "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	q = getQuality(t, db, pageID)
	if math.Abs(q-0.9) > 0.001 {
		t.Fatalf("after one 0.9 review: got %f, want 0.9", q)
	}

	// Second review of 0.9 → geometric mean = 0.9 (not 0.81)
	err = db.SubmitQualityScore(pageID, 0.9, "test-model", "worker-2")
	if err != nil {
		t.Fatal(err)
	}
	q = getQuality(t, db, pageID)
	if math.Abs(q-0.9) > 0.001 {
		t.Fatalf("after two 0.9 reviews: got %f, want 0.9 (geometric mean)", q)
	}

	// Third review of 0.6 → geometric mean of (0.9, 0.9, 0.6) = (0.9*0.9*0.6)^(1/3) ≈ 0.783
	expected := math.Pow(0.9*0.9*0.6, 1.0/3.0)
	err = db.SubmitQualityScore(pageID, 0.6, "test-model", "worker-3")
	if err != nil {
		t.Fatal(err)
	}
	q = getQuality(t, db, pageID)
	if math.Abs(q-expected) > 0.001 {
		t.Fatalf("after (0.9, 0.9, 0.6): got %f, want %f", q, expected)
	}
}

func TestQualityDuplicateReviewRejected(t *testing.T) {
	db := testDB(t)
	pageID := insertTestPage(t, db)

	err := db.SubmitQualityScore(pageID, 0.8, "model-a", "worker-1")
	if err != nil {
		t.Fatal(err)
	}

	// Same contributor, same page → rejected
	err = db.SubmitQualityScore(pageID, 0.9, "model-b", "worker-1")
	if err == nil {
		t.Fatal("expected duplicate review to be rejected")
	}

	// Quality unchanged from first review
	q := getQuality(t, db, pageID)
	if math.Abs(q-0.8) > 0.001 {
		t.Fatalf("quality after rejected dup: got %f, want 0.8", q)
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	// First migrate ran in NewDB. Run a second time and confirm no error.
	if err := db.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	// And a third for good measure.
	if err := db.migrate(); err != nil {
		t.Fatalf("third migrate: %v", err)
	}
}

func TestInsertPage_StoresETagAndLastModified(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	now := time.Now().Truncate(time.Second)
	p := &Page{
		URL:           "https://example.com/test",
		Title:         "Test",
		TextContent:   "hello",
		LicenseURL:    "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:   "CC BY-SA",
		ContentHash:   "abc123",
		CrawledAt:     now,
		ETag:          `"v1-deadbeef"`,
		LastModified:  "Wed, 21 Oct 2026 07:28:00 GMT",
	}
	if _, err := db.InsertPage(p); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := db.GetPageByURL("https://example.com/test")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ETag != `"v1-deadbeef"` {
		t.Errorf("ETag = %q, want %q", got.ETag, `"v1-deadbeef"`)
	}
	if got.LastModified != "Wed, 21 Oct 2026 07:28:00 GMT" {
		t.Errorf("LastModified = %q, want header value", got.LastModified)
	}
	if got.LastValidated.IsZero() {
		t.Error("LastValidated should default to CrawledAt for fresh insert, got zero time")
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", got.ConsecutiveFailures)
	}
}

func TestInsertPage_LastValidatedDefaultsToCrawledAt(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	crawled := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	p := &Page{
		URL:         "https://example.com/no-validated",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		CrawledAt:   crawled,
		// LastValidated intentionally zero
	}
	if _, err := db.InsertPage(p); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := db.GetPageByURL("https://example.com/no-validated")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.LastValidated.Equal(crawled) {
		t.Errorf("LastValidated = %v, want %v (defaulted to CrawledAt)", got.LastValidated, crawled)
	}
}

func TestNormalizeURL_StripsTrackingParams(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"share=linkedin stripped",
			"https://bartoszmilewski.com/about?share=linkedin",
			"https://bartoszmilewski.com/about",
		},
		{
			"share=twitter stripped",
			"https://bartoszmilewski.com/2025/10/18/post?share=twitter",
			"https://bartoszmilewski.com/2025/10/18/post",
		},
		{
			"utm params stripped",
			"https://example.com/post?utm_source=newsletter&utm_medium=email&utm_campaign=launch",
			"https://example.com/post",
		},
		{
			"fbclid stripped",
			"https://example.com/post?fbclid=IwAR0xyz",
			"https://example.com/post",
		},
		{
			"content params preserved",
			"https://example.com/page?id=42&page=2",
			"https://example.com/page?id=42&page=2",
		},
		{
			"mixed: tracking stripped, content kept",
			"https://example.com/page?id=42&utm_source=x&fbclid=y",
			"https://example.com/page?id=42",
		},
		{
			"share variant dedupes with canonical",
			"https://bartoszmilewski.com/about?share=linkedin",
			NormalizeURL("https://bartoszmilewski.com/about"),
		},
		{
			"no query params unchanged",
			"https://example.com/page",
			"https://example.com/page",
		},
	}

	for _, tt := range tests {
		got := NormalizeURL(tt.in)
		if got != tt.want {
			t.Errorf("%s: NormalizeURL(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}
