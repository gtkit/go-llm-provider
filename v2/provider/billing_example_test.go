package provider_test

import (
	"context"
	"fmt"
	"io"

	"github.com/gtkit/go-llm-provider/v2/provider"
)

type compactExampleProvider struct{ exampleProvider }

func (compactExampleProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "用户希望生成正式周报，结构包含本周完成、数据亮点和下周计划。"}, nil
}

type toolLoopExampleProvider struct {
	round int
}

func (*toolLoopExampleProvider) Name() provider.ProviderName { return provider.ProviderOpenAI }

func (*toolLoopExampleProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}

func (p *toolLoopExampleProvider) ChatStream(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
	p.round++
	chunks := []*provider.StreamChunk{{
		FinishReason: "tool_calls",
		ToolCalls: []provider.ToolCallDelta{{
			Index: 0,
			ID:    "call-weather",
			Function: provider.FunctionCallDelta{
				Name:      "get_weather",
				Arguments: `{"city":"北京"}`,
			},
		}},
	}}
	if p.round > 1 {
		chunks = []*provider.StreamChunk{{Delta: "适合户外办公", FinishReason: "stop"}}
	}
	index := 0
	return provider.NewStreamReader(func() (*provider.StreamChunk, error) {
		if index >= len(chunks) {
			return nil, io.EOF
		}
		chunk := chunks[index]
		index++
		return chunk, nil
	}, nil), nil
}

func ExampleNewBillingHook() {
	store := provider.NewMemoryUsageStore()
	// Provider 的 Chat / ChatStream 一处挂载即可归账；Embedder 使用同一个 hook
	// 配合 WithEmbedderObservability 独立挂载。
	billed := provider.WithObservability(exampleProvider{}, provider.ObserveOptions{
		OnEvent: provider.NewBillingHook(store),
	})

	// 请求入口注入身份（如 Gin 鉴权中间件里）：
	ctx := provider.WithUserID(context.Background(), "user-1001")
	ctx = provider.WithConversationID(ctx, "conv-01")
	_, err := billed.Chat(ctx, &provider.ChatRequest{})
	totals, ok := store.UserTotals("user-1001")
	fmt.Println(err == nil, ok, totals.Calls)
	// Output: true true 1
}

func ExampleCostBudgetMiddleware() {
	table := provider.PricingTable{
		"deepseek-chat": {InputPer1M: 2_000_000, OutputPer1M: 8_000_000, Currency: "CNY"},
	}
	guarded, err := provider.TryWithMiddlewares(exampleProvider{}, provider.MiddlewareOptions{
		Chat:   []provider.Middleware{provider.CostBudgetMiddleware(table)},
		Stream: []provider.StreamMiddleware{provider.CostBudgetStreamMiddleware(table)},
	})

	// 业务从账务系统查出余额后注入（1 元 = 1_000_000 微元）；
	// 余额不足返回 ErrQuotaExceeded，足够则自动收缩 MaxTokens。
	ctx := provider.WithCostBudget(context.Background(), 1_000_000)
	resp, callErr := guarded.Chat(ctx, &provider.ChatRequest{
		Model: "deepseek-chat", Messages: []provider.Message{provider.UserText("你好")},
	})
	fmt.Println(err == nil, callErr == nil, resp != nil)
	// Output: true true true
}

func ExampleCompactMessages() {
	history := []provider.Message{
		provider.SystemText("你是办公助手。"),
		provider.UserText("帮我规划周报结构"),
		provider.AssistantText("建议分三块：本周完成、数据亮点、下周计划。"),
		provider.UserText("本周完成了计费模块。"),
		provider.AssistantText("已记录。"),
		provider.UserText("按这些内容生成正式周报"),
	}

	result, err := provider.CompactMessages(context.Background(), compactExampleProvider{}, history, provider.CompactOptions{
		Model: "summary-model", KeepRecentGroups: 1,
	})
	fmt.Println(err == nil, result.Compacted(), result.CompactedCount, result.Summary != "")
	// Output: true true 4 true
}

func ExampleRunToolLoopStream() {
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

	toolCalled := false
	handler := func(_ context.Context, _, _ string) (string, error) {
		toolCalled = true
		return `{"weather":"晴","temperature":"22°C"}`, nil // 业务侧真实实现
	}
	var deltas string
	resp, err := provider.RunToolLoopStream(context.Background(), &toolLoopExampleProvider{}, req, 5, handler, func(chunk provider.StreamChunk) {
		deltas += chunk.Delta
	})
	fmt.Println(err == nil, toolCalled, resp.Content, deltas)
	// Output: true true 适合户外办公 适合户外办公
}
