package main

import (
	"context"
	"fmt"
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
	reg, err := provider.QuickRegistryStrict(map[provider.ProviderName]string{
		provider.ProviderOpenAI:      os.Getenv("OPENAI_API_KEY"),
		provider.ProviderQwen:        os.Getenv("QWEN_API_KEY"),
		provider.ProviderZhipu:       os.Getenv("ZHIPU_API_KEY"),
		provider.ProviderSiliconFlow: os.Getenv("SILICONFLOW_API_KEY"),
		provider.ProviderMoonshot:    os.Getenv("MOONSHOT_API_KEY"),
	})
	if err != nil {
		return fmt.Errorf("build registry failed: %w", err)
	}

	p, err := reg.Default()
	if err != nil {
		return fmt.Errorf("set at least one vision-capable provider API key: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := p.Chat(ctx, &provider.ChatRequest{
		Messages: []provider.Message{
			provider.UserMessage(
				provider.TextPart("请用一句话描述这张图的主体内容"),
				provider.ImageURLPart("https://upload.wikimedia.org/wikipedia/commons/3/3a/Cat03.jpg"),
			),
		},
	})
	if err != nil {
		return fmt.Errorf("vision chat failed: %w", err)
	}

	fmt.Printf("Provider: %s\n", p.Name())
	fmt.Printf("URL image reply: %s\n", resp.Content)

	if imagePath := os.Getenv("VISION_IMAGE_PATH"); imagePath != "" {
		imageBytes, err := os.ReadFile(imagePath)
		if err != nil {
			return fmt.Errorf("read local image: %w", err)
		}

		localResp, err := p.Chat(ctx, &provider.ChatRequest{
			Messages: []provider.Message{
				provider.UserMessage(
					provider.TextPart("请识别这张本地图片中的主要内容"),
					provider.ImageDataPart(imageBytes, "image/png"),
				),
			},
		})
		if err != nil {
			return fmt.Errorf("local image chat failed: %w", err)
		}

		fmt.Printf("Local image reply: %s\n", localResp.Content)
	}

	return nil
}
