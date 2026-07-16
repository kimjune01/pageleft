package search

import (
	"math"
	"sort"

	"github.com/kimjune01/pageleft/platform"
)

// SearchCachedChunks scans the compact in-memory chunk cache and keeps the
// same ranking pipeline as SearchChunks while avoiding per-chunk page metadata
// duplication in memory. Results come back with Page/Snippet unset and
// ChunkID set instead -- the caller hydrates display fields for just the
// final result set via platform.DB.HydrateChunks (see handler/search.go).
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
		sim     float64
		chunkID int64
		chunk   platform.CachedChunk
	}
	best := make(map[int64]scored)

	for _, c := range chunks {
		if len(c.Embedding) == 0 {
			continue
		}
		sim := cosineSimFloat32(query32, c.Embedding)
		if sim <= 0 {
			continue
		}
		if prev, ok := best[c.PageID]; !ok || sim > prev.sim {
			best[c.PageID] = scored{sim: sim, chunkID: c.ChunkID, chunk: c}
		}
	}

	type candidate struct {
		sim        float64
		final      float64
		chunkID    int64
		domain     string
		pageRank   float64
		compilable bool
		embedding  []float32
	}

	results := make([]candidate, 0, len(best))
	for _, s := range best {
		boost := 1.0 + math.Log(1.0+s.chunk.PageRank*n)
		quality := s.chunk.Quality
		if quality <= 0 {
			quality = 1.0
		}
		compilableBoost := 1.0
		if s.chunk.Compilable {
			compilableBoost = 2.0
		}
		results = append(results, candidate{
			sim:        s.sim,
			final:      s.sim * boost * quality * compilableBoost,
			chunkID:    s.chunkID,
			domain:     s.chunk.Domain,
			pageRank:   s.chunk.PageRank,
			compilable: s.chunk.Compilable,
			embedding:  s.chunk.Embedding,
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
			Similarity: r.sim,
			FinalScore: r.final,
			ChunkID:    r.chunkID,
			Domain:     r.domain,
			PageRank:   r.pageRank,
			Compilable: r.compilable,
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
