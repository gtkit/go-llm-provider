package provider_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gtkit/go-llm-provider/v2/provider"
)

// failingExampleProvider 总是返回可重试的服务端错误，用于演示可靠性组件。
type failingExampleProvider struct{}

func (failingExampleProvider) Name() provider.ProviderName {
	return provider.ProviderDeepSeek
}

func (failingExampleProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, &provider.ProviderError{
		Provider:  provider.ProviderDeepSeek,
		Code:      provider.ErrorCodeServerError,
		Retryable: true,
		Message:   "upstream unavailable",
	}
}

func (failingExampleProvider) ChatStream(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
	return nil, &provider.ProviderError{
		Provider:  provider.ProviderDeepSeek,
		Code:      provider.ErrorCodeServerError,
		Retryable: true,
	}
}

// namedExampleProvider 是恒成功的成员，回复内容即自己的名字。
type namedExampleProvider struct {
	name provider.ProviderName
}

func (p namedExampleProvider) Name() provider.ProviderName {
	return p.name
}

func (p namedExampleProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: string(p.name)}, nil
}

func (p namedExampleProvider) ChatStream(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
	return provider.NewStreamReader(func() (*provider.StreamChunk, error) {
		return &provider.StreamChunk{Delta: string(p.name), FinishReason: "stop"}, nil
	}, nil), nil
}

func exampleChatRequest() *provider.ChatRequest {
	return &provider.ChatRequest{Messages: []provider.Message{provider.UserText("你好")}}
}

func ExampleNewBreaker() {
	breaker := provider.NewBreaker(provider.BreakerOptions{
		Name:             "deepseek",
		FailureThreshold: 1,
		OpenDuration:     time.Minute,
	})
	p := provider.WithBreaker(failingExampleProvider{}, breaker)

	_, err := p.Chat(context.Background(), exampleChatRequest())
	fmt.Println("首次调用失败:", err != nil)

	// 失败次数达到阈值后，后续请求被本地拦下，不再打到平台。
	_, err = p.Chat(context.Background(), exampleChatRequest())
	fmt.Println("被熔断拦下:", errors.Is(err, provider.ErrBreakerOpen))
	fmt.Println("熔断状态:", breaker.State())
	fmt.Println("连续跳闸:", breaker.Stats().Trips)

	// Output:
	// 首次调用失败: true
	// 被熔断拦下: true
	// 熔断状态: open
	// 连续跳闸: 1
}

func ExampleNewRateLimiter() {
	limiter := provider.NewRateLimiter(provider.RateLimitOptions{
		Name:              "qwen",
		RequestsPerMinute: 60,
		RequestBurst:      1,
		TokensPerMinute:   100_000,
	})
	p := provider.WithRateLimit(namedExampleProvider{name: provider.ProviderQwen}, limiter)

	_, err := p.Chat(context.Background(), exampleChatRequest())
	fmt.Println("首次放行:", err == nil)

	// 桶内额度已用尽，第二次请求在本地被挡住，不消耗平台配额。
	_, err = p.Chat(context.Background(), exampleChatRequest())
	fmt.Println("本地限流:", errors.Is(err, provider.ErrLocalRateLimited))

	// Output:
	// 首次放行: true
	// 本地限流: true
}

func ExampleNewBalancedProvider() {
	lb, err := provider.NewBalancedProvider(
		provider.BalanceMember{Provider: namedExampleProvider{name: provider.ProviderDeepSeek}, Weight: 3},
		provider.BalanceMember{Provider: namedExampleProvider{name: provider.ProviderZhipu}, Weight: 1},
	)
	if err != nil {
		fmt.Println("构造失败:", err)
		return
	}

	// 平滑加权轮询按 3:1 把流量铺开，低权重成员插在中间而非攒到末尾。
	for range 4 {
		resp, chatErr := lb.Chat(context.Background(), exampleChatRequest())
		if chatErr != nil {
			fmt.Println("调用失败:", chatErr)
			return
		}
		fmt.Println(resp.Content)
	}

	// Output:
	// deepseek
	// deepseek
	// zhipu
	// deepseek
}

func ExampleBalancedProvider_Stats() {
	lb, err := provider.NewBalancedProviderWithOptions([]provider.BalanceMember{
		{
			Provider: failingExampleProvider{},
			Weight:   9,
			Breaker:  provider.NewBreaker(provider.BreakerOptions{Name: "primary", FailureThreshold: 1}),
		},
		{Provider: namedExampleProvider{name: provider.ProviderZhipu}, Weight: 1},
	}, provider.BalanceOptions{Strategy: provider.BalanceWeightedRoundRobin})
	if err != nil {
		fmt.Println("构造失败:", err)
		return
	}

	// 主成员失败后自动转移到备用成员，并把主成员熔断。
	resp, err := lb.Chat(context.Background(), exampleChatRequest())
	if err != nil {
		fmt.Println("调用失败:", err)
		return
	}
	fmt.Println("实际服务:", resp.Content)

	for _, stats := range lb.Stats() {
		fmt.Printf("%s weight=%d breaker=%s\n", stats.Provider, stats.Weight, stats.Breaker.State)
	}

	// Output:
	// 实际服务: zhipu
	// deepseek weight=9 breaker=open
	// zhipu weight=1 breaker=closed
}

func ExampleNewPricingRegistry() {
	registry, err := provider.NewPricingRegistry(provider.PricingTable{
		"glm-5.1": {InputPer1M: 2_000_000, OutputPer1M: 8_000_000, Currency: "CNY"},
	}, "2026-08-21")
	if err != nil {
		fmt.Println("价格表非法:", err)
		return
	}

	usage := provider.Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000, TotalTokens: 1_500_000}
	micros, currency, err := registry.Cost("glm-5.1", usage)
	if err != nil {
		fmt.Println("计价失败:", err)
		return
	}
	fmt.Println("费用（微元）:", micros, currency)
	fmt.Println("价格版本:", registry.Version())

	// 调价时整表原子替换，计价路径无需加锁。
	if err := registry.Replace(provider.PricingTable{
		"glm-5.1": {InputPer1M: 1_000_000, OutputPer1M: 4_000_000, Currency: "CNY"},
	}, "2026-09-01"); err != nil {
		fmt.Println("替换失败:", err)
		return
	}
	micros, _, _ = registry.Cost("glm-5.1", usage)
	fmt.Println("调价后（微元）:", micros)
	fmt.Println("价格版本:", registry.Version())

	// Output:
	// 费用（微元）: 6000000 CNY
	// 价格版本: 2026-08-21
	// 调价后（微元）: 3000000
	// 价格版本: 2026-09-01
}

func ExampleNewReranker() {
	// 真实使用时 BaseURL 换成平台地址，例如 https://api.siliconflow.cn/v1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"results": [
				{"index": 1, "relevance_score": 0.98},
				{"index": 2, "relevance_score": 0.41}
			],
			"tokens": {"input_tokens": 120, "output_tokens": 0}
		}`))
	}))
	defer srv.Close()

	reranker, err := provider.NewReranker(provider.RerankerConfig{
		Name:    provider.ProviderSiliconFlow,
		BaseURL: srv.URL,
		APIKey:  "sk-example",
		Model:   "BAAI/bge-reranker-v2-m3",
	})
	if err != nil {
		fmt.Println("构造失败:", err)
		return
	}

	documents := []string{
		"卡森城是内华达州的首府。",
		"华盛顿特区是美国的首都。",
		"塞班岛是北马里亚纳群岛的首府。",
	}
	resp, err := reranker.Rerank(context.Background(), &provider.RerankRequest{
		Query:     "美国的首都是哪里？",
		Documents: documents,
	})
	if err != nil {
		fmt.Println("重排失败:", err)
		return
	}

	for _, result := range resp.Results {
		fmt.Printf("index=%d score=%.2f\n", result.Index, result.RelevanceScore)
	}
	fmt.Println("精排后的文档:", resp.SortedDocuments(documents))
	fmt.Println("输入 token:", resp.Usage.PromptTokens)

	// Output:
	// index=1 score=0.98
	// index=2 score=0.41
	// 精排后的文档: [华盛顿特区是美国的首都。 塞班岛是北马里亚纳群岛的首府。]
	// 输入 token: 120
}
