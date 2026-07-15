package search

import (
	"math"
	"sort"

	"github.com/kimjune01/pageleft/platform"
)

// SearchCachedChunks scans the compact in-memory chunk cache and keeps the
// same ranking pipeline as SearchChunks while avoiding per-chunk page metadata
// duplication in memory.
func SearchCachedChunks(chunks []platform.CachedChunk, queryEmb []float64, totalPages int, limit int) []Result {
	if len(chunks) == 0 || len(queryEmb) == 0 {
		return nil
	}

	query32 := make([]float32, len(queryEmb))
	for i, v := range queryEmb {
		query32[i] = float32(v)
	}

	n := float64(totalPages)

	type scored struct {
		sim   float64
		text  string
		page  *platform.CachedPage
		embed []float32
	}
	best := make(map[int64]scored)

	for _, c := range chunks {
		if len(c.Embedding) == 0 || c.Page == nil {
			continue
		}
		sim := cosineSimFloat32(query32, c.Embedding)
		if sim <= 0 {
			continue
		}
		if prev, ok := best[c.PageID]; !ok || sim > prev.sim {
			best[c.PageID] = scored{sim: sim, text: c.Text, page: c.Page, embed: c.Embedding}
		}
	}

	type candidate struct {
		sim       float64
		final     float64
		text      string
		page      *platform.CachedPage
		embedding []float32
	}

	results := make([]candidate, 0, len(best))
	for _, s := range best {
		boost := 1.0 + math.Log(1.0+s.page.PageRank*n)
		quality := s.page.Quality
		if quality <= 0 {
			quality = 1.0
		}
		compilableBoost := 1.0
		if s.page.Compilable {
			compilableBoost = 2.0
		}
		results = append(results, candidate{
			sim:       s.sim,
			final:     s.sim * boost * quality * compilableBoost,
			text:      s.text,
			page:      s.page,
			embedding: s.embed,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].sim > results[j].sim
	})

	pool := limit * overfetchMultiplier
	if pool > 0 && len(results) > pool {
		results = results[:pool]
	}

	rerank := make([]Result, 0, len(results))
	for _, r := range results {
		rerank = append(rerank, Result{
			Page: &platform.Page{
				URL:         r.page.URL,
				Title:       r.page.Title,
				PageRank:    r.page.PageRank,
				Quality:     r.page.Quality,
				Compilable:  r.page.Compilable,
				LicenseType: r.page.LicenseType,
			},
			Similarity: r.sim,
			FinalScore: r.final,
			Snippet:    r.text,
			embedding:  float32To64(r.embedding),
		})
	}

	return dppRerank(rerank, limit)
}

func cosineSimFloat32(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func float32To64(v []float32) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = float64(x)
	}
	return out
}
