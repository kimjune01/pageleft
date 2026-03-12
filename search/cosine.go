package search

import "math"

func CosineSim(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	dot := 0.0
	for i := range a {
		dot += a[i] * b[i]
	}
	na := vecNorm(a)
	nb := vecNorm(b)
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (na * nb)
}

func vecNorm(v []float64) float64 {
	sum := 0.0
	for _, x := range v {
		sum += x * x
	}
	return math.Sqrt(sum)
}
