package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gtkit/go-llm-provider/v2/provider"
	"github.com/gtkit/json"
)

type citySummary struct {
	City       string `json:"city"`
	Population int    `json:"population"`
	Summary    string `json:"summary"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	p, err := provider.NewProviderFromPreset(
		provider.ProviderOpenAI,
		os.Getenv("OPENAI_API_KEY"),
		"gpt-4.1-mini",
	)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := p.Chat(ctx, &provider.ChatRequest{
		Messages: []provider.Message{
			provider.UserText("返回上海的结构化摘要，字段包括 city、population、summary"),
		},
		ResponseFormat: provider.JSONSchemaFormatStrict("city_summary", provider.ParamSchema{
			Type: "object",
			Properties: map[string]provider.ParamSchema{
				"city":       {Type: "string"},
				"population": {Type: "integer"},
				"summary":    {Type: "string"},
			},
			Required: []string{"city", "population", "summary"},
		}),
	})
	if err != nil {
		return fmt.Errorf("chat failed: %w", err)
	}

	var out citySummary
	if err := json.Unmarshal([]byte(resp.Content), &out); err != nil {
		return fmt.Errorf("decode structured output: %w", err)
	}

	fmt.Printf("%+v\n", out)
	return nil
}
