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

type exampleTokenCounter struct {
	exampleProvider
}

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

func (exampleTokenCounter) CountTokens(context.Context, *provider.ChatRequest) (*provider.TokenCountResponse, error) {
	return &provider.TokenCountResponse{
		Model:       "example-model",
		TotalTokens: 12,
	}, nil
}

func ExampleFileDataPart() {
	part := provider.FileDataPart([]byte("%PDF-1.7"), "application/pdf", "brief.pdf")
	fmt.Println(part.Type)
	fmt.Println(part.MIMEType)
	fmt.Println(part.Filename)
	// Output:
	// file
	// application/pdf
	// brief.pdf
}

func ExampleWithCacheControl() {
	part := provider.WithCacheControl(
		provider.TextPart("高成本上下文"),
		provider.CacheControlEphemeral(),
	)
	fmt.Println(part.Text)
	fmt.Println(part.CacheControl.Type)
	// Output:
	// 高成本上下文
	// ephemeral
}

func ExampleCountTokens() {
	resp, err := provider.CountTokens(context.Background(), exampleTokenCounter{}, &provider.ChatRequest{
		Messages: []provider.Message{provider.UserText("hello")},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(resp.Model, resp.TotalTokens)
	// Output: example-model 12
}

func ExampleWebSearchTool() {
	tool := provider.WebSearchTool()
	fmt.Println(tool.WebSearch != nil)

	caps, _ := provider.ModelCapabilitiesFromPreset(provider.ProviderAnthropic)
	fmt.Println(caps.Supports(provider.CapabilityWebSearch))
	// Output:
	// true
	// true
}

func ExampleWebSearchToolWithOptions() {
	tool := provider.WebSearchToolWithOptions(provider.WebSearchOptions{
		MaxUses:        3,
		AllowedDomains: []string{"go.dev"},
	})
	fmt.Println(tool.WebSearch.MaxUses)
	fmt.Println(tool.WebSearch.AllowedDomains[0])
	// Output:
	// 3
	// go.dev
}

func ExamplePricingTable_Validate() {
	table := provider.PricingTable{
		"good":            {InputPer1M: 2_000_000, OutputPer1M: 8_000_000, Currency: "CNY"},
		"conflict-search": {WebSearchPer1K: 1, GroundedPromptPer1K: 1, Currency: "CNY"},
	}
	// 启动期整表校验：把配置错误挡在计价之前。
	if err := table.Validate(); err != nil {
		fmt.Println("invalid pricing table")
	}
	// Output: invalid pricing table
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

func ExampleSchemaFromType() {
	type weather struct {
		City        string `json:"city"`
		Temperature int    `json:"temperature"`
		Note        string `json:"note,omitempty"` // omitempty 字段不进 required
	}

	schema, err := provider.SchemaFromType[weather]()
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(schema.Type)
	fmt.Println(schema.Properties["city"].Type)
	fmt.Println(schema.Properties["temperature"].Type)
	fmt.Println(schema.Required)
	// Output:
	// object
	// string
	// integer
	// [city temperature]
}

func ExampleJSONSchemaFormatFor() {
	type weather struct {
		City string `json:"city"`
	}

	format, err := provider.JSONSchemaFormatFor[weather]("")
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(format.Type)
	fmt.Println(format.Name)
	// Output:
	// json_schema
	// weather
}

func ExampleGenerateJSONWithSchema() {
	type weather struct {
		City        string `json:"city"`
		Temperature int    `json:"temperature"`
	}

	result, _, err := provider.GenerateJSONWithSchema[weather](context.Background(), exampleProvider{}, &provider.ChatRequest{
		Messages: []provider.Message{provider.UserText("杭州天气")},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(result.City, result.Temperature)
	// Output: 杭州 27
}

func ExampleMaskSecret() {
	fmt.Println(provider.MaskSecret("sk-1234567890wxyz"))
	fmt.Println(provider.MaskSecret("short"))
	// Output:
	// sk-1****wxyz
	// ****
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

func ExampleNewAnthropicProvider() {
	p, err := provider.NewAnthropicProvider(provider.NativeProviderConfig{
		APIKey: "sk-ant-xxxxxxxx",
		Model:  "claude-sonnet-4-5",
	})
	fmt.Println(p != nil, err == nil)
	// Output: true true
}

func ExampleNewGeminiProvider() {
	p, err := provider.NewGeminiProvider(provider.NativeProviderConfig{
		APIKey: "AIza-xxxxxxxx",
		Model:  "gemini-2.5-flash",
	})
	fmt.Println(p != nil, err == nil)
	// Output: true true
}

func ExampleNewGeminiEmbedder() {
	e, err := provider.NewGeminiEmbedder(provider.NativeProviderConfig{
		APIKey: "AIza-xxxxxxxx",
		Model:  "gemini-embedding-001",
	})
	fmt.Println(e != nil, err == nil)
	// Output: true true
}

func ExampleNewAzureOpenAIProvider() {
	p, err := provider.NewAzureOpenAIProvider(provider.AzureOpenAIConfig{
		APIKey:     "azure-key",
		Endpoint:   "https://example.openai.azure.com",
		Deployment: "gpt-4o-mini",
	})
	fmt.Println(p != nil, err == nil)
	// Output: true true
}

func ExampleNewBedrockOpenAIProvider() {
	p, err := provider.NewBedrockOpenAIProvider(provider.BedrockOpenAIConfig{
		APIKey: "bedrock-key",
		Region: "us-east-1",
		Model:  "anthropic.claude-sonnet-4-5-20250929-v1:0",
	})
	fmt.Println(p != nil, err == nil)
	// Output: true true
}
