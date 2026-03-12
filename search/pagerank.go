package search

import "github.com/kimjune01/pageleft/platform"

const (
	damping    = 0.85
	iterations = 50
)

// ComputePageRank runs iterative power method on the link graph and writes results to the DB.
func ComputePageRank(db *platform.DB) error {
	pages, err := db.AllPages()
	if err != nil {
		return err
	}
	links, err := db.AllLinks()
	if err != nil {
		return err
	}

	n := len(pages)
	if n == 0 {
		return nil
	}

	// Map page IDs to indices
	idToIdx := make(map[int64]int, n)
	for i, p := range pages {
		idToIdx[p.ID] = i
	}

	// Build adjacency: outgoing links per page
	outLinks := make([][]int, n)
	for _, l := range links {
		fromIdx, ok1 := idToIdx[l.FromPageID]
		toIdx, ok2 := idToIdx[l.ToPageID]
		if ok1 && ok2 {
			outLinks[fromIdx] = append(outLinks[fromIdx], toIdx)
		}
	}

	// Initialize ranks
	rank := make([]float64, n)
	newRank := make([]float64, n)
	initial := 1.0 / float64(n)
	for i := range rank {
		rank[i] = initial
	}

	for iter := 0; iter < iterations; iter++ {
		// Collect dangling node rank
		danglingSum := 0.0
		for i := range rank {
			if len(outLinks[i]) == 0 {
				danglingSum += rank[i]
			}
		}

		base := (1-damping)/float64(n) + damping*danglingSum/float64(n)
		for i := range newRank {
			newRank[i] = base
		}

		// Distribute rank through links
		for i := range rank {
			if len(outLinks[i]) > 0 {
				share := damping * rank[i] / float64(len(outLinks[i]))
				for _, j := range outLinks[i] {
					newRank[j] += share
				}
			}
		}

		rank, newRank = newRank, rank
	}

	// Write back
	for i, p := range pages {
		if err := db.UpdatePageRank(p.ID, rank[i]); err != nil {
			return err
		}
	}

	return nil
}
