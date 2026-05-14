package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		a       []float32
		b       []float32
		want    float64
		wantErr error
	}{
		{name: "same direction", a: []float32{1, 0}, b: []float32{1, 0}, want: 1},
		{name: "orthogonal", a: []float32{1, 0}, b: []float32{0, 1}, want: 0},
		{name: "opposite", a: []float32{1, 0}, b: []float32{-1, 0}, want: -1},
		{name: "dimension mismatch", a: []float32{1}, b: []float32{1, 2}, wantErr: ErrEmbeddingDimensionMismatch},
		{name: "empty", a: nil, b: nil, wantErr: ErrEmptyEmbeddingVector},
		{name: "zero vector", a: []float32{0, 0}, b: []float32{1, 0}, wantErr: ErrZeroEmbeddingVector},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := CosineSimilarity(tt.a, tt.b)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.InDelta(t, tt.want, got, 1e-9)
		})
	}
}

func TestRankBySimilarity(t *testing.T) {
	t.Parallel()

	results, err := RankBySimilarity([]float32{1, 0}, [][]float32{
		{0, 1},
		{1, 0},
		{0.5, 0.5},
	})
	require.NoError(t, err)
	require.Len(t, results, 3)

	assert.Equal(t, 1, results[0].Index)
	assert.InDelta(t, 1, results[0].Score, 1e-9)
	assert.Equal(t, 2, results[1].Index)
	assert.Greater(t, results[1].Score, results[2].Score)
	assert.Equal(t, 0, results[2].Index)
}

func TestRankBySimilarityRejectsBadCandidate(t *testing.T) {
	t.Parallel()

	_, err := RankBySimilarity([]float32{1, 0}, [][]float32{
		{1, 0},
		{1},
	})
	require.ErrorIs(t, err, ErrEmbeddingDimensionMismatch)
	assert.ErrorContains(t, err, "candidate 1")
}

func TestMostSimilar(t *testing.T) {
	t.Parallel()

	best, err := MostSimilar([]float32{0, 1}, [][]float32{
		{1, 0},
		{0, 0.5},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, best.Index)
	assert.InDelta(t, 1, best.Score, 1e-9)

	_, err = MostSimilar([]float32{0, 1}, nil)
	require.ErrorIs(t, err, ErrEmptyEmbeddingVector)
}
