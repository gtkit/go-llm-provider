package provider

import (
	"context"
	"io"
	"testing"
)

func BenchmarkCosineSimilarity(b *testing.B) {
	a := make([]float32, 1536)
	c := make([]float32, 1536)
	for i := range a {
		a[i] = float32(i%7) + 0.1
		c[i] = float32(i%5) + 0.2
	}

	b.ReportAllocs()
	for b.Loop() {
		_, err := CosineSimilarity(a, c)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRankBySimilarity(b *testing.B) {
	query := make([]float32, 768)
	candidates := make([][]float32, 128)
	for i := range query {
		query[i] = float32(i%11) + 0.1
	}
	for i := range candidates {
		candidates[i] = make([]float32, len(query))
		for j := range candidates[i] {
			candidates[i][j] = float32((i+j)%13) + 0.2
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		_, err := RankBySimilarity(query, candidates)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateJSON(b *testing.B) {
	type payload struct {
		Value string `json:"value"`
	}

	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: `{"value":"ok"}`}, nil
		},
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) { return nil, io.EOF }, nil), nil
		},
	}
	req := &ChatRequest{Messages: []Message{UserText("value")}}

	b.ReportAllocs()
	for b.Loop() {
		_, _, err := GenerateJSON[payload](context.Background(), p, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}
