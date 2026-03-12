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
