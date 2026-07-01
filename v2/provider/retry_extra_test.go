package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	assert.Equal(t, time.Duration(0), parseRetryAfter(""))
	assert.Equal(t, 5*time.Second, parseRetryAfter("5"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("0"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("-3"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("not-a-number"))

	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(future)
	assert.Positive(t, d)
	assert.LessOrEqual(t, d, 30*time.Second)

	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	assert.Equal(t, time.Duration(0), parseRetryAfter(past))
}

func TestRetryDelayPrefersRetryAfter(t *testing.T) {
	t.Parallel()

	opts := normalizeRetryOptions(RetryOptions{Backoff: ConstantBackoff(time.Hour)})

	withRA := &ProviderError{Retryable: true, RetryAfter: 2 * time.Second}
	assert.Equal(t, 2*time.Second, retryDelay(opts, 1, withRA))

	noRA := &ProviderError{Retryable: true}
	assert.Equal(t, time.Hour, retryDelay(opts, 1, noRA))
}

func TestExponentialBackoffWithJitter(t *testing.T) {
	t.Parallel()

	backoff := ExponentialBackoffWithJitter(100*time.Millisecond, time.Second)
	for attempt := 1; attempt <= 6; attempt++ {
		d := backoff(attempt)
		assert.GreaterOrEqual(t, d, time.Duration(0))
		assert.LessOrEqual(t, d, time.Second) // 不超过上界
	}
}

func TestNativeProviderCapturesRetryAfter(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewOllamaProvider(OllamaProviderConfig{BaseURL: srv.URL, Model: "llama3.2"})
	require.NoError(t, err)

	_, err = p.Chat(context.Background(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.Error(t, err)

	pe, ok := errors.AsType[*ProviderError](err)
	require.True(t, ok)
	assert.Equal(t, 7*time.Second, pe.RetryAfter)
}
