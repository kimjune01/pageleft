package search

import (
	"testing"

	"github.com/kimjune01/pageleft/platform"
)

func TestXQuADRerank_DiversifiesByDomain(t *testing.T) {
	// 6 candidates from 3 domains. Limit 3 — should pick one from each domain.
	candidates := []Result{
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc9110"}, FinalScore: 0.9},
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc793"}, FinalScore: 0.85},
		{Page: &platform.Page{URL: "https://bartoszmilewski.com/ch1"}, FinalScore: 0.8},
		{Page: &platform.Page{URL: "https://bartoszmilewski.com/ch2"}, FinalScore: 0.75},
		{Page: &platform.Page{URL: "https://gutenberg.org/ebooks/5740"}, FinalScore: 0.7},
		{Page: &platform.Page{URL: "https://gutenberg.org/ebooks/3300"}, FinalScore: 0.65},
	}

	results := xquadRerank(candidates, 3)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// Should have one from each domain
	domains := map[string]bool{}
	for _, r := range results {
		d := extractDomain(r.Page.URL)
		domains[d] = true
	}
	if len(domains) != 3 {
		t.Errorf("got %d unique domains, want 3: %v", len(domains), domains)
	}
}

func TestXQuADRerank_BestPerDomain(t *testing.T) {
	// Within each domain, the highest-scored result should be picked first.
	candidates := []Result{
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc9110"}, FinalScore: 0.9},
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc793"}, FinalScore: 0.5},
		{Page: &platform.Page{URL: "https://gutenberg.org/ebooks/5740"}, FinalScore: 0.8},
		{Page: &platform.Page{URL: "https://gutenberg.org/ebooks/3300"}, FinalScore: 0.4},
	}

	results := xquadRerank(candidates, 2)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	// First result should be the best overall (rfc9110, 0.9)
	if results[0].FinalScore != 0.9 {
		t.Errorf("first result score = %f, want 0.9", results[0].FinalScore)
	}
	// Second should be gutenberg (0.8) — best from uncovered domain
	if results[1].FinalScore != 0.8 {
		t.Errorf("second result score = %f, want 0.8 (best from uncovered domain)", results[1].FinalScore)
	}
}

func TestXQuADRerank_FallsBackWhenFewDomains(t *testing.T) {
	// All from one domain, limit 3 — should return top 3 by score.
	candidates := []Result{
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc9110"}, FinalScore: 0.9},
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc793"}, FinalScore: 0.8},
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc791"}, FinalScore: 0.7},
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc768"}, FinalScore: 0.6},
	}

	results := xquadRerank(candidates, 3)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// Should be top 3 by score since only one domain
	for i, want := range []float64{0.9, 0.8, 0.7} {
		if results[i].FinalScore != want {
			t.Errorf("result[%d] score = %f, want %f", i, results[i].FinalScore, want)
		}
	}
}

func TestXQuADRerank_FewerCandidatesThanK(t *testing.T) {
	candidates := []Result{
		{Page: &platform.Page{URL: "https://rfc-editor.org/rfc/rfc9110"}, FinalScore: 0.9},
	}

	results := xquadRerank(candidates, 5)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestXQuADRerank_Empty(t *testing.T) {
	results := xquadRerank(nil, 5)
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.rfc-editor.org/rfc/rfc9110", "rfc-editor.org"},
		{"https://bartoszmilewski.com/2014/ch1", "bartoszmilewski.com"},
		{"https://math.libretexts.org/some/chapter", "libretexts.org"},
		{"https://eng.libretexts.org/some/chapter", "libretexts.org"},
		{"https://louis.pressbooks.pub/stats/ch1", "pressbooks.pub"},
		{"https://www.gutenberg.org/ebooks/5740", "gutenberg.org"},
		{"https://www.june.kim/some-post", "june.kim"},
		{"https://uscode.house.gov/view.xhtml", "uscode.house.gov"},
	}
	for _, tt := range tests {
		got := extractDomain(tt.url)
		if got != tt.want {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
