package provider_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gtkit/go-llm-provider/provider"
)

func ExampleWithRetry() {
	base, _ := provider.NewProvider(provider.ProviderConfig{
		Name:   provider.ProviderDeepSeek,
		APIKey: "sk-your-key",
		Model:  "deepseek-chat",
	})

	// 限流/超时/5xx/网络错误自动重试，指数退避带全抖动。
	p := provider.WithRetry(base, provider.RetryOptions{
		MaxAttempts: 3,
		Backoff:     provider.ExponentialBackoffWithJitter(200*time.Millisecond, 2*time.Second),
	})
	fmt.Println(p != nil)
	// Output: true
}

func ExampleNewFallbackProviderWithOptions() {
	primary, _ := provider.NewProvider(provider.ProviderConfig{
		Name: provider.ProviderDeepSeek, APIKey: "sk-key-a", Model: "deepseek-chat",
	})
	backup, _ := provider.NewProvider(provider.ProviderConfig{
		Name: provider.ProviderQwen, APIKey: "sk-key-b", Model: "qwen3.6-plus",
	})

	// 降级链：各成员在构造时配置模型，请求里 Model 留空；
	// 自定义判定放宽切换条件（key 失效也切换到备用厂商）。
	fb, err := provider.NewFallbackProviderWithOptions(
		[]provider.Provider{primary, backup},
		provider.FallbackOptions{ShouldFallback: func(err error) bool {
			return provider.IsRetryableError(err) || errors.Is(err, provider.ErrAuth)
		}},
	)
	fmt.Println(fb != nil, err == nil)
	// Output: true true
}

// breakerExampleProvider 总是返回可重试的服务端错误，用于演示熔断与均衡。
type breakerExampleProvider struct{}

func (breakerExampleProvider) Name() provider.ProviderName {
	return provider.ProviderDeepSeek
}

func (breakerExampleProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, &provider.ProviderError{
		Provider:  provider.ProviderDeepSeek,
		Code:      provider.ErrorCodeServerError,
		Retryable: true,
		Message:   "upstream unavailable",
	}
}

func (breakerExampleProvider) ChatStream(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
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
	return &provider.ChatRequest{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "你好"}},
	}
}

func ExampleNewBreaker() {
	breaker := provider.NewBreaker(provider.BreakerOptions{
		Name:             "deepseek",
		FailureThreshold: 1,
		OpenDuration:     time.Minute,
	})
	p := provider.WithBreaker(breakerExampleProvider{}, breaker)

	_, err := p.Chat(context.Background(), exampleChatRequest())
	fmt.Println("首次调用失败:", err != nil)

	// 失败次数达到阈值后，后续请求被本地拦下，不再打到平台。
	_, err = p.Chat(context.Background(), exampleChatRequest())
	fmt.Println("被熔断拦下:", errors.Is(err, provider.ErrBreakerOpen))
	fmt.Println("熔断状态:", breaker.State())

	// Output:
	// 首次调用失败: true
	// 被熔断拦下: true
	// 熔断状态: open
}

// ExampleFailureRateTrip 演示按失败率而非绝对失败次数熔断：
// 高 QPS 服务下绝对次数阈值容易被偶发错误触发，失败率判定与流量规模解耦。
func ExampleFailureRateTrip() {
	breaker := provider.NewBreaker(provider.BreakerOptions{
		Name: "deepseek",
		// 窗口内至少 10 次调用、且失败率超过 50% 才跳闸。
		ReadyToTrip: provider.FailureRateTrip(10, 0.5),
	})

	// 9 次成功 + 1 次失败：失败率 10%，远低于阈值，保持闭合。
	for range 9 {
		_ = breaker.Allow()
		breaker.Report(nil)
	}
	_ = breaker.Allow()
	breaker.Report(&provider.ProviderError{Code: provider.ErrorCodeServerError, Retryable: true})

	stats := breaker.Stats()
	fmt.Println("窗口内成功:", stats.Successes)
	fmt.Println("窗口内失败:", stats.Failures)
	fmt.Println("熔断状态:", breaker.State())

	// Output:
	// 窗口内成功: 9
	// 窗口内失败: 1
	// 熔断状态: closed
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
