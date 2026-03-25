// Search ranking pipeline: score → pool → DPP rerank.
// See docs/search-ranking.md for the full explanation.
// If you change parameters or behavior here, update that doc too.
package search

import (
	"math"
	"net/url"
	"sort"

	"github.com/kimjune01/pageleft/platform"
)

// sourcePenalty is added to embedding similarity when two candidates share a domain.
// This makes same-source results look more redundant to DPP, spreading selections
// across sources even when their embeddings differ.
const sourcePenalty = 0.3

const overfetchMultiplier = 5

type Result struct {
	Page       *platform.Page
	Similarity float64
	FinalScore float64
	Snippet    string
	embedding  []float64 // used internally for DPP reranking
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
		boost := 1.0 + math.Log(1.0+p.PageRank*n)
		quality := p.Quality
		if quality <= 0 {
			quality = 1.0
		}
		compilableBoost := 1.0
		if p.Compilable {
			compilableBoost = 2.0
		}
		results = append(results, Result{
			Page:       p,
			Similarity: sim,
			FinalScore: sim * boost * quality * compilableBoost,
			embedding:  p.Embedding,
		})
	}

	// Sort by semantic similarity for pool admission.
	// PageRank/quality live in FinalScore for DPP tie-breaking,
	// but don't gate pool entry — high-relevance, low-PageRank
	// pages must be DPP candidates.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	pool := limit * overfetchMultiplier
	if pool > 0 && len(results) > pool {
		results = results[:pool]
	}
	results = dppRerank(results, limit)

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
		sim   float64
		chunk platform.ChunkWithPage
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
		quality := s.chunk.Quality
		if quality <= 0 {
			quality = 1.0
		}
		compilableBoost := 1.0
		if s.chunk.Compilable {
			compilableBoost = 2.0
		}
		results = append(results, Result{
			Page: &platform.Page{
				URL:         s.chunk.PageURL,
				Title:       s.chunk.PageTitle,
				PageRank:    s.chunk.PageRank,
				Quality:     s.chunk.Quality,
				Compilable:  s.chunk.Compilable,
				LicenseType: s.chunk.LicenseType,
			},
			Similarity: s.sim,
			FinalScore: s.sim * boost * quality * compilableBoost,
			Snippet:    s.chunk.Text,
			embedding:  s.chunk.Embedding,
		})
	}

	// Sort by semantic similarity for pool admission.
	// PageRank/quality live in FinalScore for DPP tie-breaking,
	// but don't gate pool entry — high-relevance, low-PageRank
	// pages must be DPP candidates.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	pool := limit * overfetchMultiplier
	if pool > 0 && len(results) > pool {
		results = results[:pool]
	}
	results = dppRerank(results, limit)

	return results
}

// dppRerank selects a diverse subset using greedy DPP in embedding space.
// At each step, picks the candidate that maximizes:
//   relevance * (relevanceFloor + (1 - relevanceFloor) * (1 - maxSim))
// The floor prevents diversity from zeroing out a highly relevant result.
// With floor=0.5, even a near-duplicate of an existing selection keeps
// half its relevance score — so it ranks above irrelevant but diverse noise.
const relevanceFloor = 0.7

func dppRerank(candidates []Result, k int) []Result {
	if len(candidates) <= k || k <= 0 {
		return candidates
	}

	// Filter to candidates with embeddings
	var pool []int
	for i := range candidates {
		if len(candidates[i].embedding) > 0 {
			pool = append(pool, i)
		}
	}
	if len(pool) <= k {
		if len(candidates) > k {
			return candidates[:k]
		}
		return candidates
	}

	selected := make([]int, 0, k)
	used := make(map[int]bool)

	for len(selected) < k {
		bestIdx := -1
		bestGain := -1.0

		for _, i := range pool {
			if used[i] {
				continue
			}

			// Marginal gain: relevance * diversity across both embedding and source.
			maxSim := 0.0
			for _, j := range selected {
				sim := CosineSim(candidates[i].embedding, candidates[j].embedding)
				if sameSource(candidates[i].Page.URL, candidates[j].Page.URL) {
					sim = math.Min(1.0, sim+sourcePenalty)
				}
				if sim > maxSim {
					maxSim = sim
				}
			}
			diversity := relevanceFloor + (1.0-relevanceFloor)*(1.0-maxSim)
			// Use semantic similarity for DPP gain, not FinalScore.
			// FinalScore (which includes PageRank) determines the overfetch pool;
			// DPP selects within it based on what the query actually asked for.
			gain := candidates[i].Similarity * diversity

			if gain > bestGain {
				bestGain = gain
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}
		selected = append(selected, bestIdx)
		used[bestIdx] = true
	}

	results := make([]Result, len(selected))
	for i, idx := range selected {
		results[i] = candidates[idx]
	}
	return results
}

func sameSource(a, b string) bool {
	da := extractDomain(a)
	db := extractDomain(b)
	return da != "" && da == db
}

func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	// Normalize: strip www. prefix so www.june.kim == june.kim
	if len(h) > 4 && h[:4] == "www." {
		h = h[4:]
	}
	return h
}
