package platform

import (
	"math"
	"os"
	"testing"
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
