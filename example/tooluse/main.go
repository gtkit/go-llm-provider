package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gtkit/go-llm-provider/provider"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	p, err := provider.NewProviderFromPreset(
		provider.ProviderDeepSeek,
		os.Getenv("DEEPSEEK_API_KEY"),
		"",
	)
	if err != nil {
		return fmt.Errorf("create provider failed: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tools := []provider.Tool{
		{
			Function: provider.FunctionDef{
				Name:        "get_weather",
				Description: "Get the current weather for a city.",
				Parameters: provider.ParamSchema{
					Type: "object",
					Properties: map[string]provider.ParamSchema{
						"city": {
							Type:        "string",
							Description: "City name, for example 'Beijing'.",
						},
						"unit": {
							Type:        "string",
							Description: "Temperature unit.",
							Enum:        []string{"celsius", "fahrenheit"},
						},
					},
					Required: []string{"city"},
				},
			},
		},
		{
			Function: provider.FunctionDef{
				Name:        "get_exchange_rate",
				Description: "Get the current exchange rate between two currencies.",
				Parameters: provider.ParamSchema{
					Type: "object",
					Properties: map[string]provider.ParamSchema{
						"from_currency": {
							Type:        "string",
							Description: "Source currency code, for example 'USD'.",
						},
						"to_currency": {
							Type:        "string",
							Description: "Target currency code, for example 'CNY'.",
						},
					},
					Required: []string{"from_currency", "to_currency"},
				},
			},
		},
	}

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "You are a helpful assistant that can call tools."},
		{Role: provider.RoleUser, Content: "What is the weather in Beijing, and what is the USD to CNY exchange rate?"},
	}

	resp, err := p.Chat(ctx, &provider.ChatRequest{
		Messages:   messages,
		Tools:      tools,
		ToolChoice: provider.ToolChoiceAuto,
	})
	if err != nil {
		return fmt.Errorf("first chat request failed: %w", err)
	}

	if !resp.HasToolCalls() {
		fmt.Printf("Model replied directly:\n%s\n", resp.Content)
		return nil
	}

	messages = append(messages, resp.AssistantMessage())

	for _, tc := range resp.ToolCalls {
		result, err := dispatchTool(tc.Function.Name, tc.Function.Arguments)
		if err != nil {
			msg, msgErr := provider.ToolResultMessageJSON(tc.ID, map[string]string{"error": err.Error()})
			if msgErr != nil {
				return fmt.Errorf("encode tool error failed: %w", msgErr)
			}
			messages = append(messages, msg)
			continue
		}

		messages = append(messages, provider.ToolResultMessage(tc.ID, result))
	}

	finalResp, err := p.Chat(ctx, &provider.ChatRequest{
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return fmt.Errorf("final chat request failed: %w", err)
	}

	fmt.Printf("Final reply:\n%s\n", finalResp.Content)
	fmt.Printf(
		"Usage: prompt=%d completion=%d total=%d\n",
		finalResp.Usage.PromptTokens,
		finalResp.Usage.CompletionTokens,
		finalResp.Usage.TotalTokens,
	)
	return nil
}

func dispatchTool(name, arguments string) (string, error) {
	switch name {
	case "get_weather":
		return handleGetWeather(arguments)
	case "get_exchange_rate":
		return handleGetExchangeRate(arguments)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func handleGetWeather(arguments string) (string, error) {
	var args struct {
		City string `json:"city"`
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("decode weather arguments: %w", err)
	}

	if args.Unit == "" {
		args.Unit = "celsius"
	}

	return marshalExampleResult(map[string]any{
		"city":        args.City,
		"temperature": 28,
		"unit":        args.Unit,
		"condition":   "sunny",
		"humidity":    45,
		"wind":        "southeast 3-4",
	})
}

func handleGetExchangeRate(arguments string) (string, error) {
	var args struct {
		FromCurrency string `json:"from_currency"`
		ToCurrency   string `json:"to_currency"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("decode exchange-rate arguments: %w", err)
	}

	return marshalExampleResult(map[string]any{
		"from":       args.FromCurrency,
		"to":         args.ToCurrency,
		"rate":       7.2450,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func marshalExampleResult(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}

	return string(data), nil
}
