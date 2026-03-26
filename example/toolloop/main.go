package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
				Name:        "calculate",
				Description: "Evaluate a basic math expression.",
				Parameters: provider.ParamSchema{
					Type: "object",
					Properties: map[string]provider.ParamSchema{
						"expression": {
							Type:        "string",
							Description: "Math expression, for example '(15 + 27) * 3'.",
						},
					},
					Required: []string{"expression"},
				},
			},
		},
		{
			Function: provider.FunctionDef{
				Name:        "get_current_time",
				Description: "Get the current local time.",
				Parameters: provider.ParamSchema{
					Type:       "object",
					Properties: map[string]provider.ParamSchema{},
				},
			},
		},
	}

	resp, err := provider.RunToolLoop(ctx, p, &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "You are a helpful assistant that can call tools."},
			{Role: provider.RoleUser, Content: "What time is it, and what is (15 + 27) * 3?"},
		},
		Tools: tools,
	}, 5, func(_ context.Context, name, arguments string) (string, error) {
		switch name {
		case "calculate":
			return runCalculator(arguments)
		case "get_current_time":
			return runClock()
		default:
			return "", fmt.Errorf("unknown tool: %s", name)
		}
	})
	if err != nil {
		return fmt.Errorf("run tool loop failed: %w", err)
	}

	fmt.Printf("Final reply:\n%s\n", resp.Content)
	return nil
}

func runCalculator(arguments string) (string, error) {
	var args struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("decode calculate arguments: %w", err)
	}

	expression := strings.TrimSpace(args.Expression)
	result := map[string]any{
		"expression": expression,
	}

	switch expression {
	case "(15 + 27) * 3":
		result["result"] = 126
	default:
		return "", fmt.Errorf("unsupported expression: %s", expression)
	}

	return marshalResult(result)
}

func runClock() (string, error) {
	return marshalResult(map[string]string{
		"time":     time.Now().Format("2006-01-02 15:04:05"),
		"timezone": time.Local.String(),
	})
}

func marshalResult(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal tool result: %w", err)
	}

	return string(data), nil
}
