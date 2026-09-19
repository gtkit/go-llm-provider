// Command demo 演示"函数工具 + 联网搜索 + 提示注入防护"的完整接法。
//
// 运行：
//
//	export BOCHA_API_KEY="sk-..."      # https://open.bochaai.com/api-keys
//	export DEEPSEEK_API_KEY="sk-..."   # 任意 OpenAI 兼容平台均可，见下方 newProvider
//	go run ./cmd/demo -q "2026 年 Go 语言有哪些新特性"
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gtkit/go-llm-provider/v2/example/websearch"
	"github.com/gtkit/go-llm-provider/v2/provider"
)

func main() {
	query := flag.String("q", "今天有哪些重要的科技新闻", "要回答的问题")
	timeout := flag.Duration("timeout", 3*time.Minute, "整体超时；思考模型走非流式可能较慢")
	flag.Parse()

	if err := run(*query, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(query string, timeout time.Duration) error {
	search, err := websearch.NewBocha(websearch.BochaConfig{
		APIKey: os.Getenv("BOCHA_API_KEY"),
		// 官方建议保持 noLimit：搜索侧会自行改写时间范围，强行限定反而可能搜不到。
		Freshness: websearch.FreshnessNoLimit,
	})
	if err != nil {
		return fmt.Errorf("初始化博查客户端失败（设置 BOCHA_API_KEY）: %w", err)
	}

	// 命中可疑内容时记审计日志。生产环境应接入你的日志/告警系统，
	// 并保留原始内容以便复盘（这里只打印必要信息，不回显可疑正文）。
	toolset, err := websearch.NewToolset(search, websearch.ToolsetOptions{
		MaxResults: 5,
		MaxTextLen: 600,
		OnSuspicious: func(hit websearch.SearchHit) {
			fmt.Fprintf(os.Stderr, "[security] 来源 %s 命中可疑指令特征，正文已替换\n", hit.URL)
		},
	})
	if err != nil {
		return err
	}

	model, err := newProvider()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := provider.RunToolLoopWithOptions(ctx, model, &provider.ChatRequest{
		Messages: []provider.Message{
			provider.SystemText(websearch.SystemPrompt),
			provider.UserText(query),
		},
		Tools:     []provider.Tool{toolset.Tool()},
		MaxTokens: 1024,
	}, toolset.Handler(), toolset.LoopOptions())
	if err != nil {
		return fmt.Errorf("工具循环失败: %w", err)
	}

	sources := toolset.Sources()
	fmt.Println(resp.Content)

	// 第三道防线：校验模型引用的来源编号确实存在，防止编造出处。
	if websearch.HasFabricatedRef(resp.Content, len(sources)) {
		fmt.Fprintln(os.Stderr, "\n[warn] 回复中引用了不存在的来源编号，建议拒绝或要求重答")
	}

	if len(sources) > 0 {
		fmt.Println("\n来源：")
		for i, hit := range sources {
			fmt.Printf("  [ref:%d] %s\n          %s\n", i+1, strings.TrimSpace(hit.Title), hit.URL)
		}
	}
	fmt.Printf("\nToken: prompt=%d completion=%d total=%d\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	return nil
}

// newProvider 按环境变量挑一个可用的平台。
// 换平台只改这里，搜索工具、防护策略、工具循环都不用动。
func newProvider() (provider.Provider, error) {
	for _, candidate := range []struct {
		env  string
		name provider.ProviderName
	}{
		{"DEEPSEEK_API_KEY", provider.ProviderDeepSeek},
		{"QWEN_API_KEY", provider.ProviderQwen},
		{"ZHIPU_API_KEY", provider.ProviderZhipu},
		{"MOONSHOT_API_KEY", provider.ProviderMoonshot},
	} {
		if key := os.Getenv(candidate.env); key != "" {
			return provider.NewProviderFromPreset(candidate.name, key, "")
		}
	}
	return nil, errors.New("未找到可用的模型 API Key，请设置 DEEPSEEK_API_KEY / QWEN_API_KEY / ZHIPU_API_KEY / MOONSHOT_API_KEY 之一")
}
