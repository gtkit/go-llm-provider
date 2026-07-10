package provider_test

import (
	"context"
	"fmt"

	"github.com/gtkit/go-llm-provider/v2/provider"
)

func ExampleNewBillingHook() {
	store := provider.NewMemoryUsageStore()
	base, _ := provider.NewProvider(provider.ProviderConfig{
		Name: provider.ProviderDeepSeek, APIKey: "sk-your-key", Model: "deepseek-chat",
	})

	// 一处挂载：所有 Chat / ChatStream / Embed 按 ctx 中的用户与会话自动归账。
	billed := provider.WithObservability(base, provider.ObserveOptions{
		OnEvent: provider.NewBillingHook(store),
	})

	// 请求入口注入身份（如 Gin 鉴权中间件里）：
	ctx := provider.WithUserID(context.Background(), "user-1001")
	ctx = provider.WithConversationID(ctx, "conv-01")
	_ = ctx

	fmt.Println(billed != nil)
	// Output: true
}

func ExampleCostBudgetMiddleware() {
	table := provider.PricingTable{
		"deepseek-chat": {InputPer1M: 2_000_000, OutputPer1M: 8_000_000, Currency: "CNY"},
	}
	base, _ := provider.NewProvider(provider.ProviderConfig{
		Name: provider.ProviderDeepSeek, APIKey: "sk-your-key", Model: "deepseek-chat",
	})

	guarded, err := provider.TryWithMiddlewares(base, provider.MiddlewareOptions{
		Chat:   []provider.Middleware{provider.CostBudgetMiddleware(table)},
		Stream: []provider.StreamMiddleware{provider.CostBudgetStreamMiddleware(table)},
	})

	// 业务从账务系统查出余额后注入（1 元 = 1_000_000 微元）；
	// 余额不足返回 ErrQuotaExceeded，足够则自动收缩 MaxTokens。
	ctx := provider.WithCostBudget(context.Background(), 1_000_000)
	_ = ctx

	fmt.Println(guarded != nil, err == nil)
	// Output: true true
}

func ExampleCompactMessages() {
	history := []provider.Message{
		provider.SystemText("你是办公助手。"),
		provider.UserText("帮我规划周报结构"),
		provider.AssistantText("建议分三块：本周完成、数据亮点、下周计划。"),
		provider.UserText("按这些内容生成正式周报"),
	}

	// 组数不足 KeepRecentGroups 时不压缩、不发起摘要调用（本示例即此分支）。
	// 真实压缩发生时：result.Summary / result.CompactedCount 供业务按会话缓存。
	opts := provider.CompactOptions{
		Model:            "deepseek-chat", // 摘要用便宜模型
		KeepRecentGroups: 4,
		TriggerTokens:    20_000,
	}
	fmt.Println(len(history), opts.KeepRecentGroups)
	// Output: 4 4
}

func ExampleRunToolLoopStream() {
	base, _ := provider.NewProvider(provider.ProviderConfig{
		Name: provider.ProviderDeepSeek, APIKey: "sk-your-key", Model: "deepseek-chat",
	})

	req := &provider.ChatRequest{
		Messages: []provider.Message{provider.UserText("北京今天适合户外办公吗？先查天气再回答。")},
		Tools: []provider.Tool{{Function: provider.FunctionDef{
			Name:        "get_weather",
			Description: "查询城市当前天气",
			Parameters: provider.ParamSchema{
				Type:       "object",
				Properties: map[string]provider.ParamSchema{"city": {Type: "string"}},
				Required:   []string{"city"},
			},
		}}},
	}

	handler := func(_ context.Context, name, args string) (string, error) {
		_ = name
		_ = args
		return `{"weather":"晴","temperature":"22°C"}`, nil // 业务侧真实实现
	}
	onChunk := func(chunk provider.StreamChunk) {
		fmt.Print(chunk.Delta) // 实时透传给前端（SSE 打字机）
	}

	// 组装完成后以 RunToolLoopStream(ctx, base, req, 5, handler, onChunk) 发起：
	// 每轮流式输出实时经 onChunk 透传，工具调用由库内拼装并自动执行衔接。
	_, _ = handler, onChunk
	fmt.Println(base != nil, len(req.Tools))
	// Output: true 1
}
