package provider_test

import (
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
