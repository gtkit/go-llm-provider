package provider

import (
	"context"
	"errors"
	"fmt"
)

// FallbackProvider tries providers in order when the previous provider returns
// a retryable error. It is safe for concurrent use when the wrapped providers
// are safe for concurrent use.
type FallbackProvider struct {
	providers      []Provider
	shouldFallback func(error) bool
}

// FallbackOptions 配置降级链的切换行为。
type FallbackOptions struct {
	// ShouldFallback 判定某个错误是否应尝试下一个 provider。
	// nil 时默认切换平台侧可重试错误（限流/超时/5xx/网络，口径同
	// IsRetryableError）与本地熔断打开（ErrBreakerOpen）。
	// 多供应商冗余场景可放宽——如 key 失效（401）、模型下线（404）
	// 也触发切换：
	//
	//	provider.FallbackOptions{ShouldFallback: func(err error) bool {
	//	    return provider.IsRetryableError(err) ||
	//	        errors.Is(err, provider.ErrBreakerOpen) ||
	//	        errors.Is(err, provider.ErrAuth)
	//	}}
	//
	// 无论如何判定，ctx 已取消/超时时都不会继续尝试（调用方已放弃）。
	ShouldFallback func(error) bool
}

// NewFallbackProvider returns a Provider that falls back across providers in order.
func NewFallbackProvider(providers ...Provider) (*FallbackProvider, error) {
	return NewFallbackProviderWithOptions(providers, FallbackOptions{})
}

// NewFallbackProviderWithOptions 以自定义切换判定构造降级链。
func NewFallbackProviderWithOptions(providers []Provider, opts FallbackOptions) (*FallbackProvider, error) {
	if len(providers) == 0 {
		return nil, ErrNilProvider
	}
	out := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if providerIsNil(p) {
			return nil, ErrNilProvider
		}
		out = append(out, p)
	}
	shouldFallback := opts.ShouldFallback
	if shouldFallback == nil {
		shouldFallback = defaultShouldFallback
	}
	return &FallbackProvider{providers: out, shouldFallback: shouldFallback}, nil
}

// defaultShouldFallback 是降级链与均衡器的默认切换判定：平台侧可重试错误
// 与本地熔断打开都切到下一个成员。熔断打开意味着该成员正在冷却，
// 留在原地重试必然继续失败，切换是唯一有意义的动作。
func defaultShouldFallback(err error) bool {
	return IsRetryableError(err) || errors.Is(err, ErrBreakerOpen)
}

// Name returns the first provider name.
func (p *FallbackProvider) Name() ProviderName {
	if p == nil || len(p.providers) == 0 || providerIsNil(p.providers[0]) {
		return ""
	}
	return p.providers[0].Name()
}

// Chat tries providers in order until one succeeds or a non-retryable error occurs.
func (p *FallbackProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if p == nil || len(p.providers) == 0 {
		return nil, ErrNilProvider
	}

	var errs []error
	for i, provider := range p.providers {
		resp, err := provider.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", provider.Name(), err))
		// 调用方已取消/超时：继续尝试没有意义，立即返回。
		if ctx.Err() != nil {
			return nil, errors.Join(errs...)
		}
		if i == len(p.providers)-1 || !p.shouldFallback(err) {
			return nil, errors.Join(errs...)
		}
	}

	return nil, errors.Join(errs...)
}

// ChatStream tries providers in order until stream creation succeeds or a non-retryable error occurs.
func (p *FallbackProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p == nil || len(p.providers) == 0 {
		return nil, ErrNilProvider
	}

	var errs []error
	for i, provider := range p.providers {
		stream, err := provider.ChatStream(ctx, req)
		if err == nil {
			return stream, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", provider.Name(), err))
		// 调用方已取消/超时：继续尝试没有意义，立即返回。
		if ctx.Err() != nil {
			return nil, errors.Join(errs...)
		}
		if i == len(p.providers)-1 || !p.shouldFallback(err) {
			return nil, errors.Join(errs...)
		}
	}

	return nil, errors.Join(errs...)
}
