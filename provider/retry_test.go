package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryMiddlewareRetriesRetryableErrors(t *testing.T) {
	t.Parallel()

	var attempts int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			attempts++
			if attempts < 3 {
				return nil, &ProviderError{
					Provider:  ProviderOpenAI,
					Code:      ErrorCodeRateLimit,
					Retryable: true,
					Message:   "slow down",
				}
			}
			return &ChatResponse{Content: "ok"}, nil
		},
	}

	wrapped := WithRetry(p, RetryOptions{
		MaxAttempts: 3,
		Backoff:     ConstantBackoff(0),
	})

	resp, err := wrapped.Chat(context.Background(), &ChatRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ok", resp.Content)
	assert.Equal(t, 3, attempts)
}

func TestRetryMiddlewareDefaultsToThreeAttempts(t *testing.T) {
	t.Parallel()

	var attempts int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			attempts++
			if attempts < 3 {
				return nil, &ProviderError{
					Provider:  ProviderOpenAI,
					Code:      ErrorCodeServerError,
					Retryable: true,
				}
			}
			return &ChatResponse{Content: "ok"}, nil
		},
	}

	wrapped := WithRetry(p, RetryOptions{})
	resp, err := wrapped.Chat(context.Background(), &ChatRequest{})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.Equal(t, 3, attempts)
}

func TestRetryMiddlewareDoesNotRetryNonRetryableErrors(t *testing.T) {
	t.Parallel()

	var attempts int
	cause := &ProviderError{
		Provider:  ProviderOpenAI,
		Code:      ErrorCodeInvalidRequest,
		Retryable: false,
		Message:   "bad request",
	}
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			attempts++
			return nil, cause
		},
	}

	wrapped := WithRetry(p, RetryOptions{
		MaxAttempts: 3,
		Backoff:     ConstantBackoff(0),
	})

	resp, err := wrapped.Chat(context.Background(), &ChatRequest{})
	require.ErrorIs(t, err, cause)
	assert.Nil(t, resp)
	assert.Equal(t, 1, attempts)
}

func TestRetryMiddlewareHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, &ProviderError{
				Provider:  ProviderOpenAI,
				Code:      ErrorCodeServerError,
				Retryable: true,
			}
		},
	}

	wrapped := WithRetry(p, RetryOptions{
		MaxAttempts: 2,
		Backoff:     ConstantBackoff(time.Second),
	})

	resp, err := wrapped.Chat(ctx, &ChatRequest{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, resp)
}

func TestRetryMiddlewareRejectsInvalidProvider(t *testing.T) {
	t.Parallel()

	wrapped, err := TryWithRetry(nil, RetryOptions{})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, wrapped)
}

func TestWithRetryNilProviderReturnsErroringProvider(t *testing.T) {
	t.Parallel()

	wrapped := WithRetry(nil, RetryOptions{})
	require.NotNil(t, wrapped)
	assert.Empty(t, wrapped.Name())

	resp, err := wrapped.Chat(t.Context(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, resp)

	stream, err := wrapped.ChatStream(t.Context(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, stream)
}

func TestRetryMiddlewareUsesCustomShouldRetry(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	var attempts int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			attempts++
			if attempts == 1 {
				return nil, boom
			}
			return &ChatResponse{Content: "ok"}, nil
		},
	}

	wrapped := WithRetry(p, RetryOptions{
		MaxAttempts: 2,
		Backoff:     ConstantBackoff(0),
		ShouldRetry: func(err error) bool {
			return errors.Is(err, boom)
		},
	})

	resp, err := wrapped.Chat(context.Background(), &ChatRequest{})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.Equal(t, 2, attempts)
}

func TestExponentialBackoffIsBounded(t *testing.T) {
	t.Parallel()

	backoff := ExponentialBackoff(200*time.Millisecond, 50*time.Millisecond)
	assert.Equal(t, 50*time.Millisecond, backoff(1))
	assert.Equal(t, 50*time.Millisecond, backoff(2))
}
