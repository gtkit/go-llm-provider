package provider

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// BackoffFunc returns the delay before the next retry attempt.
type BackoffFunc func(attempt int) time.Duration

// ShouldRetryFunc reports whether err should be retried.
type ShouldRetryFunc func(err error) bool

// RetryOptions configures WithRetry.
type RetryOptions struct {
	MaxAttempts int
	Backoff     BackoffFunc
	ShouldRetry ShouldRetryFunc
}

// ConstantBackoff returns a BackoffFunc that always returns d.
func ConstantBackoff(d time.Duration) BackoffFunc {
	return func(int) time.Duration {
		return d
	}
}

// ExponentialBackoffWithJitter 在 ExponentialBackoff 的上界内做全抖动（结果均匀分布于
// [0, 上界]），避免大量并发请求同步重试造成的惊群效应。
func ExponentialBackoffWithJitter(base, maximum time.Duration) BackoffFunc {
	inner := ExponentialBackoff(base, maximum)
	return func(attempt int) time.Duration {
		d := inner(attempt)
		if d <= 0 {
			return 0
		}
		return time.Duration(rand.Int64N(int64(d) + 1))
	}
}

// ExponentialBackoff returns a bounded exponential BackoffFunc.
func ExponentialBackoff(base, maximum time.Duration) BackoffFunc {
	if base < 0 {
		base = 0
	}
	if maximum <= 0 {
		maximum = base
	}
	return func(attempt int) time.Duration {
		if attempt <= 1 || base == 0 {
			return min(base, maximum)
		}
		delay := base
		for range attempt - 1 {
			if delay >= maximum/2 {
				return maximum
			}
			delay *= 2
		}
		if delay > maximum {
			return maximum
		}
		return delay
	}
}

// WithRetry returns a Provider that retries retryable Chat and ChatStream errors.
func WithRetry(p Provider, opts RetryOptions) Provider {
	wrapped, err := TryWithRetry(p, opts)
	if err != nil {
		return errorProvider{err: err}
	}
	return wrapped
}

// TryWithRetry returns a Provider with retry behavior without panicking on invalid input.
func TryWithRetry(p Provider, opts RetryOptions) (Provider, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	normalized := normalizeRetryOptions(opts)
	return WithMiddlewares(p, MiddlewareOptions{
		Chat: []Middleware{RetryMiddleware(normalized)},
		Stream: []StreamMiddleware{
			RetryStreamMiddleware(normalized),
		},
	}), nil
}

// RetryMiddleware retries Chat calls according to opts.
func RetryMiddleware(opts RetryOptions) Middleware {
	normalized := normalizeRetryOptions(opts)
	return func(next Handler) Handler {
		return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
			var lastErr error
			for attempt := 1; attempt <= normalized.MaxAttempts; attempt++ {
				resp, err := next(ctx, req)
				if err == nil {
					return resp, nil
				}
				lastErr = err
				if attempt == normalized.MaxAttempts || !normalized.ShouldRetry(err) {
					return nil, err
				}
				if err := waitBackoff(ctx, retryDelay(normalized, attempt, err)); err != nil {
					return nil, err
				}
			}
			return nil, lastErr
		}
	}
}

// retryDelay 返回本次重试前的等待时长：供应商通过 Retry-After 给出建议时优先采用，
// 否则回退到配置的退避函数。
func retryDelay(opts RetryOptions, attempt int, err error) time.Duration {
	if ra := retryAfterFromError(err); ra > 0 {
		return ra
	}
	return opts.Backoff(attempt)
}

func retryAfterFromError(err error) time.Duration {
	if pe, ok := errors.AsType[*ProviderError](err); ok {
		return pe.RetryAfter
	}
	return 0
}

// RetryStreamMiddleware retries ChatStream creation according to opts.
func RetryStreamMiddleware(opts RetryOptions) StreamMiddleware {
	normalized := normalizeRetryOptions(opts)
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
			var lastErr error
			for attempt := 1; attempt <= normalized.MaxAttempts; attempt++ {
				stream, err := next(ctx, req)
				if err == nil {
					return stream, nil
				}
				lastErr = err
				if attempt == normalized.MaxAttempts || !normalized.ShouldRetry(err) {
					return nil, err
				}
				if err := waitBackoff(ctx, retryDelay(normalized, attempt, err)); err != nil {
					return nil, err
				}
			}
			return nil, lastErr
		}
	}
}

func normalizeRetryOptions(opts RetryOptions) RetryOptions {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.Backoff == nil {
		opts.Backoff = ConstantBackoff(0)
	}
	if opts.ShouldRetry == nil {
		opts.ShouldRetry = IsRetryableError
	}
	return opts
}

// IsRetryableError reports whether err is classified as retryable.
func IsRetryableError(err error) bool {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable
	}
	return false
}

func waitBackoff(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("retry backoff canceled: %w", err)
		}
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("retry backoff canceled: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
