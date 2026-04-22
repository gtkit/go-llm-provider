package provider

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// stubEmbedder（仅 middleware_test.go 使用）
// ============================================================

type stubEmbedder struct {
	name  ProviderName
	embed func(context.Context, *EmbeddingRequest) (*EmbeddingResponse, error)
}

func (e *stubEmbedder) Name() ProviderName {
	if e == nil {
		return ""
	}
	return e.name
}

func (e *stubEmbedder) Embed(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	if e.embed == nil {
		return nil, errors.New("embed not implemented")
	}
	return e.embed(ctx, req)
}

// ============================================================
// WithMiddlewares：基础行为
// ============================================================

func TestWithMiddlewares_EmptyOptions_PassThrough(t *testing.T) {
	t.Parallel()

	base := &stubProvider{
		name: "base",
		chat: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: "ok"}, nil
		},
	}

	wrapped := WithMiddlewares(base, MiddlewareOptions{})

	assert.Equal(t, ProviderName("base"), wrapped.Name())

	resp, err := wrapped.Chat(t.Context(), &ChatRequest{})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

func TestWithMiddlewares_PanicsOnNilProvider(t *testing.T) {
	t.Parallel()

	assert.PanicsWithError(t, ErrNilProvider.Error(), func() {
		WithMiddlewares(nil, MiddlewareOptions{})
	})
}

func TestTryWithMiddlewares_ReturnsErrorOnNilProvider(t *testing.T) {
	t.Parallel()

	wrapped, err := TryWithMiddlewares(nil, MiddlewareOptions{})
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, wrapped)
}

func TestWithMiddlewares_NameProxy(t *testing.T) {
	t.Parallel()

	base := &stubProvider{name: "openai"}
	wrapped := WithMiddlewares(base, MiddlewareOptions{})
	assert.Equal(t, ProviderName("openai"), wrapped.Name())
}

// ============================================================
// WithMiddlewares：洋葱顺序
// ============================================================

func TestWithMiddlewares_OnionOrder_Chat(t *testing.T) {
	t.Parallel()

	var events []string
	record := func(tag string) Middleware {
		return func(next Handler) Handler {
			return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
				events = append(events, tag+"-enter")
				resp, err := next(ctx, req)
				events = append(events, tag+"-exit")
				return resp, err
			}
		}
	}

	base := &stubProvider{
		name: "base",
		chat: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			events = append(events, "core")
			return &ChatResponse{Content: "ok"}, nil
		},
	}

	wrapped := WithMiddlewares(base, MiddlewareOptions{
		Chat: []Middleware{record("A"), record("B"), record("C")},
	})

	_, err := wrapped.Chat(t.Context(), &ChatRequest{})
	require.NoError(t, err)

	// opts.Chat[0]=A 最外层；opts.Chat[2]=C 最内层（贴近 core）
	assert.Equal(t, []string{
		"A-enter", "B-enter", "C-enter",
		"core",
		"C-exit", "B-exit", "A-exit",
	}, events)
}

func TestWithMiddlewares_OnionOrder_Stream(t *testing.T) {
	t.Parallel()

	var events []string
	record := func(tag string) StreamMiddleware {
		return func(next StreamHandler) StreamHandler {
			return func(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
				events = append(events, tag+"-enter")
				sr, err := next(ctx, req)
				events = append(events, tag+"-exit")
				return sr, err
			}
		}
	}

	base := &stubProvider{
		name: "base",
		chatStream: func(_ context.Context, _ *ChatRequest) (*StreamReader, error) {
			events = append(events, "core")
			return NewStreamReader(
				func() (*StreamChunk, error) { return nil, io.EOF },
				func() error { return nil },
			), nil
		},
	}

	wrapped := WithMiddlewares(base, MiddlewareOptions{
		Stream: []StreamMiddleware{record("A"), record("B")},
	})

	sr, err := wrapped.ChatStream(t.Context(), &ChatRequest{})
	require.NoError(t, err)
	require.NotNil(t, sr)

	assert.Equal(t, []string{
		"A-enter", "B-enter",
		"core",
		"B-exit", "A-exit",
	}, events)
}

// ============================================================
// WithMiddlewares：nil 条目跳过 / 可多次叠加
// ============================================================

func TestWithMiddlewares_NilMiddleware_Skipped(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inc := func(next Handler) Handler {
		return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
			calls.Add(1)
			return next(ctx, req)
		}
	}

	base := &stubProvider{
		chat: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: "ok"}, nil
		},
	}

	wrapped := WithMiddlewares(base, MiddlewareOptions{
		Chat: []Middleware{nil, inc, nil, inc, nil},
	})

	_, err := wrapped.Chat(t.Context(), &ChatRequest{})
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "nil middlewares should be skipped, only 2 real middlewares run")
}

func TestWithMiddlewares_Stackable(t *testing.T) {
	t.Parallel()

	var events []string
	record := func(tag string) Middleware {
		return func(next Handler) Handler {
			return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
				events = append(events, tag)
				return next(ctx, req)
			}
		}
	}

	base := &stubProvider{
		chat: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			events = append(events, "core")
			return &ChatResponse{Content: "ok"}, nil
		},
	}

	// 第一次装饰
	first := WithMiddlewares(base, MiddlewareOptions{
		Chat: []Middleware{record("inner")},
	})
	// 第二次装饰（在外层再包一层）
	second := WithMiddlewares(first, MiddlewareOptions{
		Chat: []Middleware{record("outer")},
	})

	_, err := second.Chat(t.Context(), &ChatRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"outer", "inner", "core"}, events)
}

// ============================================================
// WithMiddlewares：ctx 传递
// ============================================================

func TestWithMiddlewares_ContextPropagation(t *testing.T) {
	t.Parallel()

	type ctxKey string
	const key ctxKey = "trace"

	var seen string
	base := &stubProvider{
		chat: func(ctx context.Context, _ *ChatRequest) (*ChatResponse, error) {
			if v, ok := ctx.Value(key).(string); ok {
				seen = v
			}
			return &ChatResponse{}, nil
		},
	}

	wrapped := WithMiddlewares(base, MiddlewareOptions{
		Chat: []Middleware{
			func(next Handler) Handler {
				return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
					return next(ctx, req)
				}
			},
		},
	})

	ctx := context.WithValue(t.Context(), key, "abc")
	_, err := wrapped.Chat(ctx, &ChatRequest{})
	require.NoError(t, err)
	assert.Equal(t, "abc", seen)
}

// ============================================================
// WithEmbedderMiddlewares
// ============================================================

func TestWithEmbedderMiddlewares_EmptyMws_PassThrough(t *testing.T) {
	t.Parallel()

	base := &stubEmbedder{
		name: "base",
		embed: func(_ context.Context, _ *EmbeddingRequest) (*EmbeddingResponse, error) {
			return &EmbeddingResponse{Model: "m1"}, nil
		},
	}

	wrapped := WithEmbedderMiddlewares(base)

	assert.Equal(t, ProviderName("base"), wrapped.Name())

	resp, err := wrapped.Embed(t.Context(), &EmbeddingRequest{Input: []string{"x"}})
	require.NoError(t, err)
	assert.Equal(t, "m1", resp.Model)
}

func TestWithEmbedderMiddlewares_OnionOrder(t *testing.T) {
	t.Parallel()

	var events []string
	record := func(tag string) EmbedMiddleware {
		return func(next EmbedHandler) EmbedHandler {
			return func(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
				events = append(events, tag+"-enter")
				resp, err := next(ctx, req)
				events = append(events, tag+"-exit")
				return resp, err
			}
		}
	}

	base := &stubEmbedder{
		embed: func(_ context.Context, _ *EmbeddingRequest) (*EmbeddingResponse, error) {
			events = append(events, "core")
			return &EmbeddingResponse{}, nil
		},
	}

	wrapped := WithEmbedderMiddlewares(base, record("A"), nil, record("B"))

	_, err := wrapped.Embed(t.Context(), &EmbeddingRequest{Input: []string{"x"}})
	require.NoError(t, err)

	assert.Equal(t, []string{"A-enter", "B-enter", "core", "B-exit", "A-exit"}, events)
}

func TestWithEmbedderMiddlewares_PanicsOnNilEmbedder(t *testing.T) {
	t.Parallel()

	assert.PanicsWithError(t, ErrNilEmbedder.Error(), func() {
		WithEmbedderMiddlewares(nil)
	})
}

func TestTryWithEmbedderMiddlewares_ReturnsErrorOnNilEmbedder(t *testing.T) {
	t.Parallel()

	wrapped, err := TryWithEmbedderMiddlewares(nil)
	require.ErrorIs(t, err, ErrNilEmbedder)
	assert.Nil(t, wrapped)
}

func TestWithEmbedderMiddlewares_NameProxy(t *testing.T) {
	t.Parallel()

	base := &stubEmbedder{name: "qwen"}
	wrapped := WithEmbedderMiddlewares(base)
	assert.Equal(t, ProviderName("qwen"), wrapped.Name())
}
