package provider

import "context"

// Handler 对应 Provider.Chat 调用的函数签名。
type Handler func(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

// StreamHandler 对应 Provider.ChatStream 调用的函数签名。
type StreamHandler func(ctx context.Context, req *ChatRequest) (*StreamReader, error)

// EmbedHandler 对应 Embedder.Embed 调用的函数签名。
type EmbedHandler func(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)

// Middleware 装饰 Chat 处理链。
type Middleware func(next Handler) Handler

// StreamMiddleware 装饰 ChatStream 处理链。
type StreamMiddleware func(next StreamHandler) StreamHandler

// EmbedMiddleware 装饰 Embed 处理链。
type EmbedMiddleware func(next EmbedHandler) EmbedHandler

// MiddlewareOptions 按分类组织装饰 Provider 时的中间件集合。
// 未提供某类中间件时，该路径直通原 Provider。
type MiddlewareOptions struct {
	Chat   []Middleware
	Stream []StreamMiddleware
}

// WithMiddlewares 返回一个被中间件装饰过的 Provider。
//
// 洋葱模型：opts.Chat[0] 最外层执行，opts.Chat[len-1] 最贴近真实 Provider.Chat；
// opts.Stream 同理。切片中的 nil 条目会被跳过。
//
// 传入 nil Provider 会触发 panic(ErrNilProvider)——装饰器构造期的错误不适合由错误值承载。
// 返回的 Provider 的 Name() 代理到原 p。
func WithMiddlewares(p Provider, opts MiddlewareOptions) Provider {
	wrapped, err := TryWithMiddlewares(p, opts)
	if err != nil {
		panic(err)
	}
	return wrapped
}

// TryWithMiddlewares returns a decorated Provider without panicking on invalid input.
func TryWithMiddlewares(p Provider, opts MiddlewareOptions) (Provider, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}

	chat := Handler(p.Chat)
	for i := len(opts.Chat) - 1; i >= 0; i-- {
		if opts.Chat[i] == nil {
			continue
		}
		chat = opts.Chat[i](chat)
	}

	stream := StreamHandler(p.ChatStream)
	for i := len(opts.Stream) - 1; i >= 0; i-- {
		if opts.Stream[i] == nil {
			continue
		}
		stream = opts.Stream[i](stream)
	}

	return &wrappedProvider{
		base:   p,
		chat:   chat,
		stream: stream,
	}, nil
}

// WithEmbedderMiddlewares 返回一个被中间件装饰过的 Embedder。
//
// 洋葱模型：mws[0] 最外层，mws[len-1] 最贴近真实 Embedder.Embed。
// 切片中的 nil 条目会被跳过。
//
// 传入 nil Embedder 会触发 panic(ErrNilEmbedder)。
// 返回的 Embedder 的 Name() 代理到原 e。
func WithEmbedderMiddlewares(e Embedder, mws ...EmbedMiddleware) Embedder {
	wrapped, err := TryWithEmbedderMiddlewares(e, mws...)
	if err != nil {
		panic(err)
	}
	return wrapped
}

// TryWithEmbedderMiddlewares returns a decorated Embedder without panicking on invalid input.
func TryWithEmbedderMiddlewares(e Embedder, mws ...EmbedMiddleware) (Embedder, error) {
	if embedderIsNil(e) {
		return nil, ErrNilEmbedder
	}

	handler := EmbedHandler(e.Embed)
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] == nil {
			continue
		}
		handler = mws[i](handler)
	}

	return &wrappedEmbedder{
		base:  e,
		embed: handler,
	}, nil
}

// wrappedProvider 是 WithMiddlewares 返回的装饰器，包含组合好的 Chat / ChatStream 处理链。
type wrappedProvider struct {
	base   Provider
	chat   Handler
	stream StreamHandler
}

func (w *wrappedProvider) Name() ProviderName {
	if w == nil || w.base == nil {
		return ""
	}
	return w.base.Name()
}

func (w *wrappedProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if w == nil || w.chat == nil {
		return nil, ErrNilProvider
	}
	return w.chat(ctx, req)
}

func (w *wrappedProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if w == nil || w.stream == nil {
		return nil, ErrNilProvider
	}
	return w.stream(ctx, req)
}

// wrappedEmbedder 是 WithEmbedderMiddlewares 返回的装饰器。
type wrappedEmbedder struct {
	base  Embedder
	embed EmbedHandler
}

func (w *wrappedEmbedder) Name() ProviderName {
	if w == nil || w.base == nil {
		return ""
	}
	return w.base.Name()
}

func (w *wrappedEmbedder) Embed(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	if w == nil || w.embed == nil {
		return nil, ErrNilEmbedder
	}
	return w.embed(ctx, req)
}
