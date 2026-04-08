package platform

import (
	"database/sql"
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

// TestMigrate_UpgradesOldSchema simulates a production upgrade: it manually
// creates an old pages table without the Layer 0 validator columns, then runs
// migrate() and verifies the new columns exist and inserts/reads work.
// This is the actual scenario migrate() needs to handle safely.
func TestMigrate_UpgradesOldSchema(t *testing.T) {
	f, err := os.CreateTemp("", "pageleft-upgrade-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	// Open a raw connection and create the pre-Layer-0 schema by hand.
	raw, err := sql.Open("sqlite", f.Name())
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		CREATE TABLE pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT UNIQUE NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			text_content TEXT NOT NULL DEFAULT '',
			license_url TEXT NOT NULL DEFAULT '',
			license_type TEXT NOT NULL DEFAULT '',
			embedding JSON,
			pagerank REAL NOT NULL DEFAULT 0,
			crawled_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			content_hash TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO pages (url, title, text_content, license_url, license_type, content_hash)
		VALUES ('https://example.com/legacy', 'Legacy', 'old content',
		        'https://creativecommons.org/licenses/by-sa/4.0/', 'CC BY-SA', 'oldhash');
	`)
	if err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	raw.Close()

	// Now open via NewDB — migrate() runs and should add the validator columns
	// without losing the legacy row.
	db, err := NewDB(f.Name())
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer db.Close()

	// The legacy row should still be readable, with empty validator fields.
	got, err := db.GetPageByURL("https://example.com/legacy")
	if err != nil {
		t.Fatalf("read legacy row after upgrade: %v", err)
	}
	if got.Title != "Legacy" {
		t.Errorf("legacy title lost: got %q", got.Title)
	}
	if got.ETag != "" {
		t.Errorf("legacy ETag should be empty default, got %q", got.ETag)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("legacy consecutive_failures should default to 0, got %d", got.ConsecutiveFailures)
	}

	// And new inserts with validators should work post-upgrade.
	now := time.Now().Truncate(time.Second)
	p := &Page{
		URL:          "https://example.com/post-upgrade",
		Title:        "New",
		LicenseURL:   "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:  "CC BY-SA",
		CrawledAt:    now,
		ETag:         `"new-etag"`,
		LastModified: "Mon, 01 Jan 2026 00:00:00 GMT",
	}
	if _, err := db.InsertPage(p); err != nil {
		t.Fatalf("insert post-upgrade: %v", err)
	}
	got, err = db.GetPageByURL("https://example.com/post-upgrade")
	if err != nil {
		t.Fatalf("read post-upgrade row: %v", err)
	}
	if got.ETag != `"new-etag"` {
		t.Errorf("post-upgrade ETag = %q, want %q", got.ETag, `"new-etag"`)
	}
}

// TestInsertPage_UpsertUpdatesValidators verifies the ON CONFLICT path
// updates etag, last_modified, last_validated, and consecutive_failures
// when re-inserting an existing URL.
func TestInsertPage_UpsertUpdatesValidators(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.InsertPage(&Page{
		URL:           "https://example.com/upsert",
		Title:         "v1",
		LicenseURL:    "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:   "CC BY-SA",
		CrawledAt:     first,
		ETag:          `"v1"`,
		LastModified:  "Wed, 01 Jan 2026 00:00:00 GMT",
		LastValidated: first,
	}); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Re-insert with new validators.
	second := first.Add(24 * time.Hour)
	if _, err := db.InsertPage(&Page{
		URL:           "https://example.com/upsert",
		Title:         "v2",
		LicenseURL:    "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:   "CC BY-SA",
		CrawledAt:     second,
		ETag:          `"v2"`,
		LastModified:  "Thu, 02 Jan 2026 00:00:00 GMT",
		LastValidated: second,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := db.GetPageByURL("https://example.com/upsert")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ETag != `"v2"` {
		t.Errorf("ETag after upsert = %q, want %q", got.ETag, `"v2"`)
	}
	if got.LastModified != "Thu, 02 Jan 2026 00:00:00 GMT" {
		t.Errorf("LastModified after upsert = %q, want updated value", got.LastModified)
	}
	if !got.LastValidated.Equal(second) {
		t.Errorf("LastValidated after upsert = %v, want %v", got.LastValidated, second)
	}
}

func TestInsertPage_StoresETagAndLastModified(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	now := time.Now().Truncate(time.Second)
	p := &Page{
		URL:          "https://example.com/test",
		Title:        "Test",
		TextContent:  "hello",
		LicenseURL:   "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:  "CC BY-SA",
		ContentHash:  "abc123",
		CrawledAt:    now,
		ETag:         `"v1-deadbeef"`,
		LastModified: "Wed, 21 Oct 2026 07:28:00 GMT",
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

// --- Layer 1: revalidation ---

func TestPagesForRevalidation_OldestFirst(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	// Insert pages with different last_validated values, plus one never-validated.
	mkPage := func(url string, lv time.Time) {
		p := &Page{
			URL:           url,
			LicenseURL:    "https://creativecommons.org/licenses/by-sa/4.0/",
			LicenseType:   "CC BY-SA",
			CrawledAt:     time.Now(),
			LastValidated: lv,
		}
		if _, err := db.InsertPage(p); err != nil {
			t.Fatalf("insert %s: %v", url, err)
		}
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)

	mkPage("https://example.com/recent", recent)
	mkPage("https://example.com/old", old)
	mkPage("https://example.com/mid", mid)

	// And a never-validated page (last_validated NULL). Insert via raw SQL
	// because InsertPage defaults LastValidated to CrawledAt.
	if _, err := db.conn.Exec(`
		INSERT INTO pages (url, license_url, license_type, last_validated)
		VALUES ('https://example.com/never', '', '', NULL)
	`); err != nil {
		t.Fatalf("insert never-validated: %v", err)
	}

	got, err := db.PagesForRevalidation(0)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d pages, want 4", len(got))
	}

	// Never-validated should come first, then oldest, mid, recent.
	wantOrder := []string{
		"https://example.com/never",
		"https://example.com/old",
		"https://example.com/mid",
		"https://example.com/recent",
	}
	for i, want := range wantOrder {
		if got[i].URL != want {
			t.Errorf("position %d: got %s, want %s", i, got[i].URL, want)
		}
	}
}

func TestUpdatePageValidators_BumpsLastValidated(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	id, err := db.InsertPage(&Page{
		URL:           "https://example.com/test",
		LicenseURL:    "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:   "CC BY-SA",
		CrawledAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ETag:          `"v1"`,
		LastModified:  "Wed, 01 Jan 2026 00:00:00 GMT",
		LastValidated: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	newTime := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	if err := db.UpdatePageValidators(id, `"v2"`, "Mon, 08 Apr 2026 12:00:00 GMT", newTime, 0); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := db.GetPageByURL("https://example.com/test")
	if err != nil {
		t.Fatal(err)
	}
	if got.ETag != `"v2"` {
		t.Errorf("ETag = %q, want %q", got.ETag, `"v2"`)
	}
	if !got.LastValidated.Equal(newTime) {
		t.Errorf("LastValidated = %v, want %v", got.LastValidated, newTime)
	}
}

func TestUpdatePageContent_RefreshesAllFields(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	id, err := db.InsertPage(&Page{
		URL:                 "https://example.com/test",
		TextContent:         "old content",
		ContentHash:         "oldhash",
		LicenseURL:          "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType:         "CC BY-SA",
		CrawledAt:           time.Now(),
		ConsecutiveFailures: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	newTime := time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)
	newChunks := []Chunk{{Idx: 0, Text: "new chunk"}}
	if err := db.UpdatePageContent(id, "new content", "newhash", `"v2"`, "Mon, 08 Apr 2026 00:00:00 GMT", newTime, newChunks); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := db.GetPageByURL("https://example.com/test")
	if err != nil {
		t.Fatal(err)
	}
	if got.TextContent != "new content" {
		t.Errorf("TextContent = %q, want %q", got.TextContent, "new content")
	}
	if got.ContentHash != "newhash" {
		t.Errorf("ContentHash = %q, want %q", got.ContentHash, "newhash")
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 (should reset)", got.ConsecutiveFailures)
	}
}

func TestIncrementPageFailures(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	id, err := db.InsertPage(&Page{
		URL:         "https://example.com/test",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		CrawledAt:   time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for want := 1; want <= 3; want++ {
		got, err := db.IncrementPageFailures(id, time.Now())
		if err != nil {
			t.Fatalf("increment: %v", err)
		}
		if got != want {
			t.Errorf("after %d increments: got %d, want %d", want, got, want)
		}
	}
}

func TestDeletePage_CascadesChunksLinksReviews(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	pageA, err := db.InsertPage(&Page{
		URL:         "https://example.com/a",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		CrawledAt:   time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	pageB, err := db.InsertPage(&Page{
		URL:         "https://example.com/b",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		LicenseType: "CC BY-SA",
		CrawledAt:   time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add chunks, an outbound link from A→B, and a quality review on A.
	if err := db.InsertChunks(pageA, []Chunk{{PageID: pageA, Idx: 0, Text: "x"}}); err != nil {
		t.Fatalf("insert chunks: %v", err)
	}
	if err := db.InsertLink(pageA, pageB, "ref"); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	if err := db.SubmitQualityScore(pageA, 0.8, "test", "test-contributor"); err != nil {
		t.Fatalf("insert review: %v", err)
	}

	if err := db.DeletePage(pageA); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Page should be gone.
	if _, err := db.GetPageByURL("https://example.com/a"); err != sql.ErrNoRows {
		t.Errorf("page should be deleted, got err = %v", err)
	}
	// Chunks gone (FK cascade).
	var chunkCount int
	db.conn.QueryRow("SELECT COUNT(*) FROM chunks WHERE page_id = ?", pageA).Scan(&chunkCount)
	if chunkCount != 0 {
		t.Errorf("chunks remaining: %d, want 0", chunkCount)
	}
	// Reviews gone (FK cascade).
	var reviewCount int
	db.conn.QueryRow("SELECT COUNT(*) FROM quality_reviews WHERE page_id = ?", pageA).Scan(&reviewCount)
	if reviewCount != 0 {
		t.Errorf("reviews remaining: %d, want 0", reviewCount)
	}
	// Links gone (explicit delete in DeletePage — no FK cascade on links table).
	var linkCount int
	db.conn.QueryRow("SELECT COUNT(*) FROM links WHERE from_page_id = ? OR to_page_id = ?", pageA, pageA).Scan(&linkCount)
	if linkCount != 0 {
		t.Errorf("links remaining: %d, want 0", linkCount)
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
