package search

import (
	"testing"

	"github.com/kimjune01/pageleft/platform"
)

// makeEmb returns a simple embedding where the i-th dimension is 1.0.
// Two embeddings with different hot dimensions have cosine similarity 0.
func makeEmb(dim, hot int) []float64 {
	e := make([]float64, dim)
	e[hot] = 1.0
	return e
}

// makeNearEmb returns an embedding near the given hot dimension but slightly off.
func makeNearEmb(dim, hot int, offset float64) []float64 {
	e := make([]float64, dim)
	e[hot] = 1.0
	e[(hot+1)%dim] = offset
	return e
}

func TestDPPRerank_DiversifiesInEmbeddingSpace(t *testing.T) {
	// 6 candidates: 3 pairs of near-duplicates in different embedding regions.
	// DPP should pick one from each region.
	candidates := []Result{
		{Page: &platform.Page{URL: "a1"}, Similarity: 0.9, FinalScore: 0.9, embedding: makeEmb(3, 0)},
		{Page: &platform.Page{URL: "a2"}, Similarity: 0.85, FinalScore: 0.85, embedding: makeNearEmb(3, 0, 0.01)},
		{Page: &platform.Page{URL: "b1"}, Similarity: 0.8, FinalScore: 0.8, embedding: makeEmb(3, 1)},
		{Page: &platform.Page{URL: "b2"}, Similarity: 0.75, FinalScore: 0.75, embedding: makeNearEmb(3, 1, 0.01)},
		{Page: &platform.Page{URL: "c1"}, Similarity: 0.7, FinalScore: 0.7, embedding: makeEmb(3, 2)},
		{Page: &platform.Page{URL: "c2"}, Similarity: 0.65, FinalScore: 0.65, embedding: makeNearEmb(3, 2, 0.01)},
	}

	results := dppRerank(candidates, 3)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// Should pick one from each region (a, b, c) — not both from region a
	urls := map[string]bool{}
	for _, r := range results {
		urls[r.Page.URL] = true
	}
	for _, prefix := range []string{"a", "b", "c"} {
		if !urls[prefix+"1"] && !urls[prefix+"2"] {
			t.Errorf("no result from region %s", prefix)
		}
	}
}

func TestDPPRerank_BestFirstThenDiverse(t *testing.T) {
	// With relevanceFloor=0.7, a near-duplicate with high relevance beats
	// a distant result with low relevance. Diversity only breaks ties
	// between similarly relevant candidates.
	candidates := []Result{
		{Page: &platform.Page{URL: "best"}, Similarity: 0.9, FinalScore: 0.9, embedding: makeEmb(2, 0)},
		{Page: &platform.Page{URL: "similar"}, Similarity: 0.85, FinalScore: 0.85, embedding: makeNearEmb(2, 0, 0.01)},
		{Page: &platform.Page{URL: "different"}, Similarity: 0.5, FinalScore: 0.5, embedding: makeEmb(2, 1)},
	}

	results := dppRerank(candidates, 2)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	// First should be "best" (highest score)
	if results[0].Page.URL != "best" {
		t.Errorf("first result = %s, want best", results[0].Page.URL)
	}
	// Second should be "similar" — relevance dominates over diversity
	// when the gap between candidates is large (0.85 vs 0.5)
	if results[1].Page.URL != "similar" {
		t.Errorf("second result = %s, want similar (relevance over diversity when gap is large)", results[1].Page.URL)
	}
}

func TestDPPRerank_DiversityBreaksTies(t *testing.T) {
	// When candidates have similar relevance, diversity should break the tie.
	candidates := []Result{
		{Page: &platform.Page{URL: "best"}, Similarity: 0.9, FinalScore: 0.9, embedding: makeEmb(2, 0)},
		{Page: &platform.Page{URL: "similar"}, Similarity: 0.88, FinalScore: 0.88, embedding: makeNearEmb(2, 0, 0.01)},
		{Page: &platform.Page{URL: "different"}, Similarity: 0.87, FinalScore: 0.87, embedding: makeEmb(2, 1)},
	}

	results := dppRerank(candidates, 2)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	if results[0].Page.URL != "best" {
		t.Errorf("first result = %s, want best", results[0].Page.URL)
	}
	// "different" (0.87, fully diverse) should beat "similar" (0.88, near-duplicate)
	if results[1].Page.URL != "different" {
		t.Errorf("second result = %s, want different (diversity breaks tie at similar relevance)", results[1].Page.URL)
	}
}

func TestDPPRerank_FewerCandidatesThanK(t *testing.T) {
	candidates := []Result{
		{Page: &platform.Page{URL: "only"}, Similarity: 0.9, FinalScore: 0.9, embedding: makeEmb(2, 0)},
	}

	results := dppRerank(candidates, 5)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestDPPRerank_Empty(t *testing.T) {
	results := dppRerank(nil, 5)
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
}
