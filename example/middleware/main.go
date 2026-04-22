// 演示如何用 provider.WithMiddlewares 挂载自定义中间件。
//
// 主包故意不内置 Logging / TokenStats / Retry / RateLimit 等具体策略——
// 本文件展示调用方用 ~30 行就可以自行实现任一能力。
//
// 运行前：至少设置一个 provider 的 API key 环境变量（如 OPENAI_API_KEY / QWEN_API_KEY）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync/atomic"
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
	reg := provider.QuickRegistry(map[provider.ProviderName]string{
		provider.ProviderOpenAI:      os.Getenv("OPENAI_API_KEY"),
		provider.ProviderDeepSeek:    os.Getenv("DEEPSEEK_API_KEY"),
		provider.ProviderQwen:        os.Getenv("QWEN_API_KEY"),
		provider.ProviderZhipu:       os.Getenv("ZHIPU_API_KEY"),
		provider.ProviderSiliconFlow: os.Getenv("SILICONFLOW_API_KEY"),
	})

	base, err := reg.Default()
	if err != nil {
		return fmt.Errorf("set at least one provider API key before running: %w", err)
	}

	// 累计 token 计数器（由 TokenStats Middleware 填充，主程序消费）
	var totalTokens int64

	// 组装洋葱：logging（最外层） → tokenStats → retry（最内层，贴近真实 Chat）
	p := provider.WithMiddlewares(base, provider.MiddlewareOptions{
		Chat: []provider.Middleware{
			loggingMiddleware(base.Name()),
			tokenStatsMiddleware(&totalTokens),
			retryMiddleware(3),
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reply, err := provider.SimpleChat(ctx, p, "用一句话介绍 Go 语言")
	if err != nil {
		return fmt.Errorf("chat failed: %w", err)
	}

	fmt.Printf("\nReply: %s\n", reply)
	fmt.Printf("Total tokens so far: %d\n", atomic.LoadInt64(&totalTokens))
	return nil
}

// ============================================================
// Middleware 1：日志
// ============================================================

// loggingMiddleware 打印 provider、耗时与 token 消耗。
// 真实生产环境建议写结构化日志（zap / slog）并脱敏敏感字段。
func loggingMiddleware(providerName provider.ProviderName) provider.Middleware {
	return func(next provider.Handler) provider.Handler {
		return func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			elapsed := time.Since(start)

			if err != nil {
				log.Printf("[chat] provider=%s model=%s elapsed=%s err=%v", providerName, req.Model, elapsed, err)
				return resp, err
			}
			log.Printf(
				"[chat] provider=%s model=%s elapsed=%s tokens=%d",
				providerName,
				req.Model,
				elapsed,
				resp.Usage.TotalTokens,
			)
			return resp, nil
		}
	}
}

// ============================================================
// Middleware 2：Token 统计
// ============================================================

// tokenStatsMiddleware 原子累加 token 消耗，供外部读取。
// counter 由调用方持有，便于跨多个 provider 合计。
func tokenStatsMiddleware(counter *int64) provider.Middleware {
	return func(next provider.Handler) provider.Handler {
		return func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			resp, err := next(ctx, req)
			if err == nil && resp != nil {
				atomic.AddInt64(counter, int64(resp.Usage.TotalTokens))
			}
			return resp, err
		}
	}
}

// ============================================================
// Middleware 3：重试（基于 ProviderError.Retryable 判断可重试）
// ============================================================

// retryMiddleware 在遇到可恢复错误时自动重试。
// 这里为演示保持最简；生产环境建议加退避、jitter、上限。
func retryMiddleware(maxAttempts int) provider.Middleware {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return func(next provider.Handler) provider.Handler {
		return func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			var lastErr error
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				resp, err := next(ctx, req)
				if err == nil {
					return resp, nil
				}
				lastErr = err

				// 不可重试的错误直接返回
				if !isRetryable(err) {
					return nil, err
				}

				if attempt == maxAttempts {
					break
				}

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
			}
			return nil, lastErr
		}
	}
}

// isRetryable 判断一个错误是否值得重试。
// 规则：
//   - context.Canceled / context.DeadlineExceeded → 不重试
//   - *provider.ProviderError 且 Retryable=true → 重试
//   - 其他错误 → 不重试
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var providerErr *provider.ProviderError
	return errors.As(err, &providerErr) && providerErr.Retryable
}
