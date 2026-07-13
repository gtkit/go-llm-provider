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

func TestObserveStreamMiddlewareReportsStreamCompletion(t *testing.T) {
	t.Parallel()

	chunks := []*StreamChunk{
		{Delta: "hel"},
		{Delta: "lo", FinishReason: "stop", Usage: Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}},
	}
	var next int
	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) {
				if next >= len(chunks) {
					return nil, io.EOF
				}
				chunk := chunks[next]
				next++
				return chunk, nil
			}, nil), nil
		},
	}

	var events []ObserveEvent
	wrapped := WithObservability(p, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			events = append(events, got)
		},
	})

	stream, err := wrapped.ChatStream(context.Background(), &ChatRequest{Model: "stream-model"})
	require.NoError(t, err)
	for {
		if _, err := stream.Recv(); err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
	require.NoError(t, stream.Close())

	// 创建事件 + 终止事件各一次；Close 不应重复上报。
	require.Len(t, events, 2)
	assert.Equal(t, ObserveOperationStream, events[0].Operation)
	complete := events[1]
	assert.Equal(t, ObserveOperationStreamComplete, complete.Operation)
	assert.Equal(t, ProviderOpenAI, complete.Provider)
	assert.Equal(t, "stream-model", complete.Model)
	assert.Equal(t, Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}, complete.Usage)
	require.NoError(t, complete.Err)
	assert.Equal(t, StreamFinishEOF, complete.StreamFinish)
}

func TestObserveStreamMiddlewareReportsStreamRecvError(t *testing.T) {
	t.Parallel()

	recvErr := &ProviderError{Provider: ProviderOpenAI, Code: ErrorCodeServerError, Retryable: true}
	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) {
				return nil, recvErr
			}, nil), nil
		},
	}

	var events []ObserveEvent
	wrapped := WithObservability(p, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			events = append(events, got)
		},
	})

	stream, err := wrapped.ChatStream(context.Background(), &ChatRequest{Model: "stream-model"})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)

	require.Len(t, events, 2)
	complete := events[1]
	assert.Equal(t, ObserveOperationStreamComplete, complete.Operation)
	assert.Equal(t, ErrorCodeServerError, complete.ErrorCode)
	assert.True(t, complete.Retryable)
	require.ErrorIs(t, complete.Err, recvErr)
	assert.Equal(t, StreamFinishError, complete.StreamFinish)
}

func TestObserveStreamMiddlewareReportsCompletionOnEarlyClose(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) {
				return &StreamChunk{Delta: "partial"}, nil
			}, nil), nil
		},
	}

	var events []ObserveEvent
	wrapped := WithObservability(p, ObserveOptions{
		OnEvent: func(_ context.Context, got ObserveEvent) {
			events = append(events, got)
		},
	})

	stream, err := wrapped.ChatStream(context.Background(), &ChatRequest{Model: "stream-model"})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	// 未读到流尾即 Close：终止事件仍上报，Usage 为零值，供计费方识别漏单。
	require.Len(t, events, 2)
	complete := events[1]
	assert.Equal(t, ObserveOperationStreamComplete, complete.Operation)
	assert.Equal(t, Usage{}, complete.Usage)
	require.NoError(t, complete.Err)
	assert.Equal(t, StreamFinishClosed, complete.StreamFinish)
}

func TestObserveStreamMiddlewareReportsCloseError(t *testing.T) {
	t.Parallel()

	closeErr := &ProviderError{Provider: ProviderOpenAI, Code: ErrorCodeNetwork, Retryable: true}
	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) {
				return &StreamChunk{Delta: "partial"}, nil
			}, func() error { return closeErr }), nil
		},
	}

	var events []ObserveEvent
	wrapped := WithObservability(p, ObserveOptions{OnEvent: func(_ context.Context, event ObserveEvent) {
		events = append(events, event)
	}})
	stream, err := wrapped.ChatStream(t.Context(), &ChatRequest{Model: "stream-model"})
	require.NoError(t, err)
	require.ErrorIs(t, stream.Close(), closeErr)

	require.Len(t, events, 2)
	complete := events[1]
	require.ErrorIs(t, complete.Err, closeErr)
	assert.Equal(t, ErrorCodeNetwork, complete.ErrorCode)
	assert.Equal(t, StreamFinishClosed, complete.StreamFinish)
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
