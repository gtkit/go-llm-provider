package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/gtkit/go-llm-provider/v2/provider"
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
		"deepseek-reasoner",
	)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := p.Chat(ctx, &provider.ChatRequest{
		Messages: []provider.Message{
			provider.UserText("用两句话解释 Go 的 goroutine 和 channel 的关系"),
		},
		Thinking: &provider.Thinking{
			Enabled: boolPtr(true),
		},
	})
	if err != nil {
		return fmt.Errorf("chat failed: %w", err)
	}

	fmt.Printf("Reply: %s\n", resp.Content)
	fmt.Printf("Reasoning: %s\n", resp.Reasoning)
	fmt.Printf("Reasoning tokens: %d\n\n", resp.Usage.ReasoningTokens)

	stream, err := p.ChatStream(ctx, &provider.ChatRequest{
		Messages: []provider.Message{
			provider.UserText("请边思考边给出一个 Go 并发示例"),
		},
		Thinking: &provider.Thinking{
			Enabled: boolPtr(true),
		},
	})
	if err != nil {
		return fmt.Errorf("chat stream failed: %w", err)
	}
	defer func() { _ = stream.Close() }()

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("stream recv failed: %w", err)
		}

		if chunk.ReasoningDelta != "" {
			fmt.Printf("[thinking] %s", chunk.ReasoningDelta)
		}
		if chunk.Delta != "" {
			fmt.Printf("[answer] %s", chunk.Delta)
		}
	}

	fmt.Println()
	return nil
}

func boolPtr(v bool) *bool {
	return &v
}
