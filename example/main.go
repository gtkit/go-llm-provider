package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	reg, err := provider.QuickRegistryStrict(map[provider.ProviderName]string{
		provider.ProviderOpenAI:      os.Getenv("OPENAI_API_KEY"),
		provider.ProviderDeepSeek:    os.Getenv("DEEPSEEK_API_KEY"),
		provider.ProviderQwen:        os.Getenv("QWEN_API_KEY"),
		provider.ProviderZhipu:       os.Getenv("ZHIPU_API_KEY"),
		provider.ProviderSiliconFlow: os.Getenv("SILICONFLOW_API_KEY"),
		provider.ProviderMoonshot:    os.Getenv("MOONSHOT_API_KEY"),
		provider.ProviderQianfan:     os.Getenv("QIANFAN_API_KEY"),
	})
	if err != nil {
		return fmt.Errorf("build registry failed: %w", err)
	}

	p, err := reg.Default()
	if err != nil {
		return fmt.Errorf("set at least one provider API key before running the example: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reply, err := provider.SimpleChatWithSystem(
		ctx,
		p,
		"You are a concise Go assistant.",
		"Explain goroutines in one sentence.",
	)
	if err != nil {
		return fmt.Errorf("chat failed: %w", err)
	}

	fmt.Printf("Provider: %s\n", p.Name())
	fmt.Printf("Reply: %s\n\n", reply)

	stream, err := p.ChatStream(ctx, &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "You are a concise Go assistant."},
			{Role: provider.RoleUser, Content: "Give me two tips for writing stable Go services."},
		},
		MaxTokens: 256,
	})
	if err != nil {
		return fmt.Errorf("streaming chat failed: %w", err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "close stream: %v\n", closeErr)
		}
	}()

	fmt.Println("Streaming reply:")
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Println()
				return nil
			}

			return fmt.Errorf("stream recv failed: %w", err)
		}

		fmt.Print(chunk.Delta)
	}
}
