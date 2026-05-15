// Package main 演示 v1 中 Claude / Gemini 原生 HTTP provider 的使用方式。
//
// 运行前至少设置一个环境变量：
//
//	ANTHROPIC_API_KEY=... go run ./example/native
//	GEMINI_API_KEY=... go run ./example/native
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		claude, err := provider.NewProviderFromPreset(provider.ProviderAnthropic, key, "")
		if err != nil {
			return fmt.Errorf("create claude provider: %w", err)
		}
		if err := demoProvider(ctx, claude); err != nil {
			return err
		}
	}

	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		gemini, err := provider.NewGeminiProvider(provider.NativeProviderConfig{
			APIKey: key,
			Model:  "gemini-2.5-flash",
		})
		if err != nil {
			return fmt.Errorf("create gemini provider: %w", err)
		}
		if err := demoProvider(ctx, gemini); err != nil {
			return err
		}
	}

	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
		return errors.New("set ANTHROPIC_API_KEY or GEMINI_API_KEY before running this example")
	}
	return nil
}

func demoProvider(ctx context.Context, p provider.Provider) error {
	reply, err := provider.SimpleChatWithSystem(
		ctx,
		p,
		"You are a concise assistant.",
		"Reply with one short sentence about Go.",
	)
	if err != nil {
		return fmt.Errorf("%s chat: %w", p.Name(), err)
	}
	fmt.Printf("[%s] reply: %s\n", p.Name(), reply)

	stream, err := p.ChatStream(ctx, &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "You are a concise assistant."},
			{Role: provider.RoleUser, Content: "Give two short tips for writing stable Go services."},
		},
		MaxTokens: 128,
	})
	if err != nil {
		return fmt.Errorf("%s stream: %w", p.Name(), err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "close %s stream: %v\n", p.Name(), closeErr)
		}
	}()

	fmt.Printf("[%s] stream: ", p.Name())
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Println()
				return nil
			}
			return fmt.Errorf("%s stream recv: %w", p.Name(), err)
		}
		fmt.Print(chunk.Delta)
	}
}
