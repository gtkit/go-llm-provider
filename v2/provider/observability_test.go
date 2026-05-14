package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObserveMiddlewareReportsChatSuccess(t *testing.T) {
	t.Parallel()

	var events []ObserveEvent
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{
				Content: "ok",
				Usage: Usage{
					PromptTokens:     1,
					CompletionTokens: 2,
					TotalTokens:      3,
				},
				Metadata: ResponseMetadata{
					Provider:  ProviderOpenAI,
					Model:     "gpt-test",
					RequestID: "req-test",
					Headers:   http.Header{"X-Request-Id": []string{"req-test"}},
				},
			}, nil
		},
	}

	wrapped := WithObservability(p, ObserveOptions{
		OnEvent: func(_ context.Context, event ObserveEvent) {
			events = append(events, event)
		},
	})

	resp, err := wrapped.Chat(context.Background(), &ChatRequest{Model: "fallback-model"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, ObserveOperationChat, event.Operation)
	assert.Equal(t, ProviderOpenAI, event.Provider)
	assert.Equal(t, "gpt-test", event.Model)
	assert.Equal(t, "req-test", event.RequestID)
	assert.Equal(t, resp.Usage, event.Usage)
	assert.Equal(t, "req-test", event.Metadata.Header("x-request-id"))
	require.NoError(t, event.Err)
	assert.Empty(t, event.ErrorCode)
	assert.GreaterOrEqual(t, event.Duration, time.Duration(0))

	event.Metadata.Headers.Set("X-Request-Id", "mutated")
	assert.Equal(t, "req-test", resp.Metadata.Header("x-request-id"))
}

func TestObserveMiddlewareReportsChatError(t *testing.T) {
	t.Parallel()

	cause := &ProviderError{
		Provider:   ProviderDeepSeek,
		Code:       ErrorCodeRateLimit,
		StatusCode: 429,
		Retryable:  true,
	}
	var event ObserveEvent
	p := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, cause
		},
	}

	wrapped := WithObservability(p, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			event = got
		},
	})

	resp, err := wrapped.Chat(context.Background(), &ChatRequest{Model: "deepseek-chat"})
	require.ErrorIs(t, err, cause)
	assert.Nil(t, resp)
	assert.Equal(t, ObserveOperationChat, event.Operation)
	assert.Equal(t, ProviderDeepSeek, event.Provider)
	assert.Equal(t, "deepseek-chat", event.Model)
	assert.Equal(t, ErrorCodeRateLimit, event.ErrorCode)
	assert.Equal(t, 429, event.StatusCode)
	assert.True(t, event.Retryable)
}

func TestObserveStreamMiddlewareReportsStreamCreation(t *testing.T) {
	t.Parallel()

	var event ObserveEvent
	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) {
				return nil, io.EOF
			}, nil), nil
		},
	}

	wrapped := WithObservability(p, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			event = got
		},
	})

	stream, err := wrapped.ChatStream(context.Background(), &ChatRequest{Model: "stream-model"})
	require.NoError(t, err)
	require.NotNil(t, stream)
	assert.Equal(t, ObserveOperationStream, event.Operation)
	assert.Equal(t, ProviderOpenAI, event.Provider)
	assert.Equal(t, "stream-model", event.Model)
}

func TestObserveEmbedMiddlewareReportsEmbedSuccess(t *testing.T) {
	t.Parallel()

	var event ObserveEvent
	e := &stubEmbedder{
		name: ProviderOpenAI,
		embed: func(context.Context, *EmbeddingRequest) (*EmbeddingResponse, error) {
			return &EmbeddingResponse{
				Model: "text-embedding-test",
				Usage: Usage{
					PromptTokens: 4,
					TotalTokens:  4,
				},
				Metadata: ResponseMetadata{
					Provider:  ProviderOpenAI,
					Model:     "text-embedding-test",
					RequestID: "req-embed",
				},
			}, nil
		},
	}

	wrapped := WithEmbedderObservability(e, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			event = got
		},
	})

	resp, err := wrapped.Embed(context.Background(), &EmbeddingRequest{Model: "fallback-embedding", Input: []string{"hello"}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, ObserveOperationEmbed, event.Operation)
	assert.Equal(t, ProviderOpenAI, event.Provider)
	assert.Equal(t, "text-embedding-test", event.Model)
	assert.Equal(t, "req-embed", event.RequestID)
	assert.Equal(t, resp.Usage, event.Usage)
}

func TestObserveMiddlewareNoopsWhenHookNil(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, boom
		},
	}

	wrapped := WithObservability(p, ObserveOptions{})
	resp, err := wrapped.Chat(context.Background(), &ChatRequest{})
	require.ErrorIs(t, err, boom)
	assert.Nil(t, resp)
}

func TestObserveMiddlewareReportsNilHandlerError(t *testing.T) {
	t.Parallel()

	var event ObserveEvent
	handler := ObserveMiddleware(ProviderOpenAI, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			event = got
		},
	})(nil)

	resp, err := handler(context.Background(), &ChatRequest{Model: "gpt-test"})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, resp)
	assert.Equal(t, ObserveOperationChat, event.Operation)
	assert.Equal(t, ProviderOpenAI, event.Provider)
	assert.Equal(t, "gpt-test", event.Model)
	require.ErrorIs(t, event.Err, ErrNilProvider)
}

func TestObserveStreamMiddlewareReportsNilHandlerError(t *testing.T) {
	t.Parallel()

	var event ObserveEvent
	handler := ObserveStreamMiddleware(ProviderOpenAI, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			event = got
		},
	})(nil)

	stream, err := handler(context.Background(), &ChatRequest{Model: "gpt-stream"})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, stream)
	assert.Equal(t, ObserveOperationStream, event.Operation)
	assert.Equal(t, ProviderOpenAI, event.Provider)
	assert.Equal(t, "gpt-stream", event.Model)
	require.ErrorIs(t, event.Err, ErrNilProvider)
}

func TestObserveEmbedMiddlewareReportsNilHandlerError(t *testing.T) {
	t.Parallel()

	var event ObserveEvent
	handler := ObserveEmbedMiddleware(ProviderOpenAI, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			event = got
		},
	})(nil)

	resp, err := handler(context.Background(), &EmbeddingRequest{Model: "text-embedding-test"})
	require.ErrorIs(t, err, ErrNilEmbedder)
	assert.Nil(t, resp)
	assert.Equal(t, ObserveOperationEmbed, event.Operation)
	assert.Equal(t, ProviderOpenAI, event.Provider)
	assert.Equal(t, "text-embedding-test", event.Model)
	require.ErrorIs(t, event.Err, ErrNilEmbedder)
}

func TestTryWithObservabilityReturnsErrorOnNilProvider(t *testing.T) {
	t.Parallel()

	wrapped, err := TryWithObservability(nil, ObserveOptions{})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, wrapped)
}

func TestWithObservabilityReturnsErroringProviderOnNilProvider(t *testing.T) {
	t.Parallel()

	wrapped := WithObservability(nil, ObserveOptions{})

	resp, err := wrapped.Chat(context.Background(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, resp)

	stream, err := wrapped.ChatStream(context.Background(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, stream)
}

func TestTryWithEmbedderObservabilityReturnsErrorOnNilEmbedder(t *testing.T) {
	t.Parallel()

	wrapped, err := TryWithEmbedderObservability(nil, ObserveOptions{})
	require.ErrorIs(t, err, ErrNilEmbedder)
	assert.Nil(t, wrapped)
}

func TestWithEmbedderObservabilityReturnsErroringEmbedderOnNilEmbedder(t *testing.T) {
	t.Parallel()

	wrapped := WithEmbedderObservability(nil, ObserveOptions{})

	resp, err := wrapped.Embed(context.Background(), &EmbeddingRequest{Input: []string{"hello"}})
	require.ErrorIs(t, err, ErrNilEmbedder)
	assert.Nil(t, resp)
}
