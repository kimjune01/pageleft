package search

import (
	"math"
	"sort"

	"github.com/kimjune01/pageleft/platform"
)

type Result struct {
	Page       *platform.Page
	Similarity float64
	FinalScore float64
	Snippet    string
}

// Search finds pages most similar to the query embedding, boosted by PageRank.
func Search(pages []*platform.Page, queryEmb []float64, limit int) []Result {
	if len(pages) == 0 || len(queryEmb) == 0 {
		return nil
	}

	n := float64(len(pages))
	var results []Result

	for _, p := range pages {
		if len(p.Embedding) == 0 {
			continue
		}
		sim := CosineSim(queryEmb, p.Embedding)
		if sim <= 0 {
			continue
		}
		// FinalScore = similarity * (1 + log(1 + pagerank * N))
		boost := 1.0 + math.Log(1.0+p.PageRank*n)
		results = append(results, Result{
			Page:       p,
			Similarity: sim,
			FinalScore: sim * boost,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].FinalScore > results[j].FinalScore
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}

// SearchChunks finds the best-matching chunk per page, deduplicates by page,
// and ranks by similarity * pagerank boost.
func SearchChunks(chunks []platform.ChunkWithPage, queryEmb []float64, totalPages int, limit int) []Result {
	if len(chunks) == 0 || len(queryEmb) == 0 {
		return nil
	}

	n := float64(totalPages)

	// Best chunk per page
	type scored struct {
		sim     float64
		chunk   platform.ChunkWithPage
	}
	best := make(map[int64]scored) // keyed by page_id

	for _, c := range chunks {
		if len(c.Embedding) == 0 {
			continue
		}
		sim := CosineSim(queryEmb, c.Embedding)
		if sim <= 0 {
			continue
		}
		if prev, ok := best[c.PageID]; !ok || sim > prev.sim {
			best[c.PageID] = scored{sim: sim, chunk: c}
		}
	}

	var results []Result
	for _, s := range best {
		boost := 1.0 + math.Log(1.0+s.chunk.PageRank*n)
		results = append(results, Result{
			Page: &platform.Page{
				URL:         s.chunk.PageURL,
				Title:       s.chunk.PageTitle,
				PageRank:    s.chunk.PageRank,
				LicenseType: s.chunk.LicenseType,
			},
			Similarity: s.sim,
			FinalScore: s.sim * boost,
			Snippet:    s.chunk.Text,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].FinalScore > results[j].FinalScore
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}
