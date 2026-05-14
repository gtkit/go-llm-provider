package provider_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gtkit/go-llm-provider/v2/provider"
)

type exampleProvider struct{}

func (exampleProvider) Name() provider.ProviderName {
	return provider.ProviderOpenAI
}

func (exampleProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Content: `{"city":"杭州","temperature":27}`,
		Metadata: provider.ResponseMetadata{
			Provider:  provider.ProviderOpenAI,
			Model:     "example-model",
			RequestID: "req_example",
			Headers: http.Header{
				"X-Request-Id": []string{"req_example"},
			},
		},
	}, nil
}

func (exampleProvider) ChatStream(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
	return provider.NewStreamReader(func() (*provider.StreamChunk, error) {
		return nil, io.EOF
	}, nil), nil
}

func ExampleModelCapabilitiesFromPreset() {
	caps, ok := provider.ModelCapabilitiesFromPreset(provider.ProviderOpenAI)
	fmt.Println(ok)
	fmt.Println(caps.Supports(provider.CapabilityEmbedding))
	fmt.Println(caps.EmbeddingModel)
	// Output:
	// true
	// true
	// text-embedding-3-small
}

func ExampleGenerateJSON() {
	type weather struct {
		City        string `json:"city"`
		Temperature int    `json:"temperature"`
	}

	result, _, err := provider.GenerateJSON[weather](context.Background(), exampleProvider{}, &provider.ChatRequest{
		Messages: []provider.Message{provider.UserText("杭州天气")},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(result.City, result.Temperature)
	// Output: 杭州 27
}

func ExampleGenerateJSONWithValidator() {
	type weather struct {
		City        string `json:"city"`
		Temperature int    `json:"temperature"`
	}

	result, _, err := provider.GenerateJSONWithValidator[weather](
		context.Background(),
		exampleProvider{},
		&provider.ChatRequest{Messages: []provider.Message{provider.UserText("杭州天气")}},
		func(v weather) error {
			if v.City == "" {
				return fmt.Errorf("city is required")
			}
			return nil
		},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(result.City, result.Temperature)
	// Output: 杭州 27
}

func ExampleCosineSimilarity() {
	score, err := provider.CosineSimilarity([]float32{1, 0}, []float32{0.5, 0.5})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("%.3f\n", score)
	// Output: 0.707
}

func ExampleRankBySimilarity() {
	results, err := provider.RankBySimilarity([]float32{1, 0}, [][]float32{
		{0, 1},
		{1, 0},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(results[0].Index)
	// Output: 1
}

func ExampleResponseMetadata() {
	resp, err := exampleProvider{}.Chat(context.Background(), &provider.ChatRequest{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(resp.Metadata.Provider)
	fmt.Println(resp.Metadata.RequestID)
	// Output:
	// openai
	// req_example
}

func ExampleWithRetry() {
	wrapped := provider.WithRetry(exampleProvider{}, provider.RetryOptions{
		MaxAttempts: 2,
		Backoff:     provider.ConstantBackoff(10 * time.Millisecond),
	})

	resp, err := wrapped.Chat(context.Background(), &provider.ChatRequest{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(resp.Metadata.Provider)
	// Output: openai
}

func ExampleNewFallbackProvider() {
	primary := exampleProvider{}
	backup := exampleProvider{}

	p, err := provider.NewFallbackProvider(primary, backup)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	resp, err := p.Chat(context.Background(), &provider.ChatRequest{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(resp.Metadata.RequestID)
	// Output: req_example
}

func ExampleWithObservability() {
	wrapped := provider.WithObservability(exampleProvider{}, provider.ObserveOptions{
		OnEvent: func(_ context.Context, event provider.ObserveEvent) {
			fmt.Println(event.Operation, event.Provider, event.RequestID)
		},
	})

	_, err := wrapped.Chat(context.Background(), &provider.ChatRequest{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// Output: chat openai req_example
}
