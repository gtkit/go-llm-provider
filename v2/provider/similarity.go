package provider

import (
	"fmt"
	"math"
	"slices"
)

// SimilarityResult identifies a candidate vector and its score.
type SimilarityResult struct {
	Index int
	Score float64
}

// CosineSimilarity returns the cosine similarity between two embedding vectors.
func CosineSimilarity(a, b []float32) (float64, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, ErrEmptyEmbeddingVector
	}
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: %d != %d", ErrEmbeddingDimensionMismatch, len(a), len(b))
	}

	var dot, normA, normB float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0, ErrZeroEmbeddingVector
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}

// RankBySimilarity ranks candidate vectors by descending cosine similarity.
func RankBySimilarity(query []float32, candidates [][]float32) ([]SimilarityResult, error) {
	if len(candidates) == 0 {
		return nil, ErrEmptyEmbeddingVector
	}

	results := make([]SimilarityResult, 0, len(candidates))
	for i, candidate := range candidates {
		score, err := CosineSimilarity(query, candidate)
		if err != nil {
			return nil, fmt.Errorf("candidate %d: %w", i, err)
		}
		results = append(results, SimilarityResult{Index: i, Score: score})
	}

	slices.SortFunc(results, func(a, b SimilarityResult) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		case a.Index < b.Index:
			return -1
		case a.Index > b.Index:
			return 1
		default:
			return 0
		}
	})

	return results, nil
}

// MostSimilar returns the highest-scoring candidate by cosine similarity.
func MostSimilar(query []float32, candidates [][]float32) (SimilarityResult, error) {
	results, err := RankBySimilarity(query, candidates)
	if err != nil {
		return SimilarityResult{}, err
	}
	return results[0], nil
}
