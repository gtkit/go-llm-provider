package provider

import (
	"context"
	"errors"
	"time"
)

// ObserveOperation identifies the observed provider operation.
type ObserveOperation string

const (
	// ObserveOperationChat identifies a non-streaming chat call.
	ObserveOperationChat ObserveOperation = "chat"
	// ObserveOperationStream identifies a streaming chat creation call.
	ObserveOperationStream ObserveOperation = "stream"
	// ObserveOperationEmbed identifies an embedding call.
	ObserveOperationEmbed ObserveOperation = "embed"
)

// ObserveEvent describes a completed provider operation for logs, metrics, or traces.
// It is safe to read after the hook returns.
type ObserveEvent struct {
	Operation  ObserveOperation
	Provider   ProviderName
	Model      string
	RequestID  string
	Usage      Usage
	Metadata   ResponseMetadata
	Duration   time.Duration
	Err        error
	ErrorCode  ErrorCode
	StatusCode int
	Retryable  bool
}

// ObserveHook receives an ObserveEvent after an operation completes.
// It must be safe for concurrent use when the wrapped provider is used concurrently.
type ObserveHook func(ctx context.Context, event ObserveEvent)

// ObserveOptions configures observability middleware.
// A zero-value ObserveOptions makes observability middleware a no-op.
type ObserveOptions struct {
	OnEvent ObserveHook
}

// WithObservability returns a Provider decorated with observability hooks.
// The returned Provider is safe for concurrent use when p and opts.OnEvent are safe for concurrent use.
// If p is nil, the returned Provider reports ErrNilProvider on Chat and ChatStream calls.
func WithObservability(p Provider, opts ObserveOptions) Provider {
	wrapped, err := TryWithObservability(p, opts)
	if err != nil {
		return &wrappedProvider{}
	}
	return wrapped
}

// TryWithObservability returns a Provider decorated with observability hooks without panicking.
// The returned Provider is safe for concurrent use when p and opts.OnEvent are safe for concurrent use.
func TryWithObservability(p Provider, opts ObserveOptions) (Provider, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	return TryWithMiddlewares(p, MiddlewareOptions{
		Chat:   []Middleware{ObserveMiddleware(p.Name(), opts)},
		Stream: []StreamMiddleware{ObserveStreamMiddleware(p.Name(), opts)},
	})
}

// WithEmbedderObservability returns an Embedder decorated with observability hooks.
// The returned Embedder is safe for concurrent use when e and opts.OnEvent are safe for concurrent use.
// If e is nil, the returned Embedder reports ErrNilEmbedder on Embed calls.
func WithEmbedderObservability(e Embedder, opts ObserveOptions) Embedder {
	wrapped, err := TryWithEmbedderObservability(e, opts)
	if err != nil {
		return &wrappedEmbedder{}
	}
	return wrapped
}

// TryWithEmbedderObservability returns an Embedder decorated with observability hooks without panicking.
// The returned Embedder is safe for concurrent use when e and opts.OnEvent are safe for concurrent use.
func TryWithEmbedderObservability(e Embedder, opts ObserveOptions) (Embedder, error) {
	if embedderIsNil(e) {
		return nil, ErrNilEmbedder
	}
	return TryWithEmbedderMiddlewares(e, ObserveEmbedMiddleware(e.Name(), opts))
}

// ObserveMiddleware reports Chat outcomes to opts.OnEvent.
func ObserveMiddleware(provider ProviderName, opts ObserveOptions) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
			start := time.Now()
			if next == nil {
				err := ErrNilProvider
				emitObserveEvent(ctx, opts.OnEvent, chatObserveEvent(provider, req, nil, err, time.Since(start)))
				return nil, err
			}
			resp, err := next(ctx, req)
			emitObserveEvent(ctx, opts.OnEvent, chatObserveEvent(provider, req, resp, err, time.Since(start)))
			return resp, err
		}
	}
}

// ObserveStreamMiddleware reports ChatStream creation outcomes to opts.OnEvent.
func ObserveStreamMiddleware(provider ProviderName, opts ObserveOptions) StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
			start := time.Now()
			if next == nil {
				err := ErrNilProvider
				emitObserveEvent(ctx, opts.OnEvent, streamObserveEvent(provider, req, err, time.Since(start)))
				return nil, err
			}
			stream, err := next(ctx, req)
			emitObserveEvent(ctx, opts.OnEvent, streamObserveEvent(provider, req, err, time.Since(start)))
			return stream, err
		}
	}
}

// ObserveEmbedMiddleware reports Embed outcomes to opts.OnEvent.
func ObserveEmbedMiddleware(provider ProviderName, opts ObserveOptions) EmbedMiddleware {
	return func(next EmbedHandler) EmbedHandler {
		return func(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
			start := time.Now()
			if next == nil {
				err := ErrNilEmbedder
				emitObserveEvent(ctx, opts.OnEvent, embedObserveEvent(provider, req, nil, err, time.Since(start)))
				return nil, err
			}
			resp, err := next(ctx, req)
			emitObserveEvent(ctx, opts.OnEvent, embedObserveEvent(provider, req, resp, err, time.Since(start)))
			return resp, err
		}
	}
}

func emitObserveEvent(ctx context.Context, hook ObserveHook, event ObserveEvent) {
	if hook == nil {
		return
	}
	hook(ctx, event)
}

func chatObserveEvent(provider ProviderName, req *ChatRequest, resp *ChatResponse, err error, duration time.Duration) ObserveEvent {
	event := ObserveEvent{
		Operation: ObserveOperationChat,
		Provider:  provider,
		Model:     requestChatModel(req),
		Duration:  duration,
		Err:       err,
	}
	if resp != nil {
		metadata := cloneResponseMetadata(resp.Metadata)
		event.Provider = firstProviderName(metadata.Provider, event.Provider)
		event.Model = firstString(metadata.Model, event.Model)
		event.RequestID = metadata.RequestID
		event.Usage = resp.Usage
		event.Metadata = metadata
	}
	applyErrorToObserveEvent(&event, err)
	return event
}

func streamObserveEvent(provider ProviderName, req *ChatRequest, err error, duration time.Duration) ObserveEvent {
	event := ObserveEvent{
		Operation: ObserveOperationStream,
		Provider:  provider,
		Model:     requestChatModel(req),
		Duration:  duration,
		Err:       err,
	}
	applyErrorToObserveEvent(&event, err)
	return event
}

func embedObserveEvent(provider ProviderName, req *EmbeddingRequest, resp *EmbeddingResponse, err error, duration time.Duration) ObserveEvent {
	event := ObserveEvent{
		Operation: ObserveOperationEmbed,
		Provider:  provider,
		Model:     requestEmbeddingModel(req),
		Duration:  duration,
		Err:       err,
	}
	if resp != nil {
		metadata := cloneResponseMetadata(resp.Metadata)
		event.Provider = firstProviderName(metadata.Provider, event.Provider)
		event.Model = firstString(metadata.Model, firstString(resp.Model, event.Model))
		event.RequestID = metadata.RequestID
		event.Usage = resp.Usage
		event.Metadata = metadata
	}
	applyErrorToObserveEvent(&event, err)
	return event
}

func cloneResponseMetadata(metadata ResponseMetadata) ResponseMetadata {
	if len(metadata.Headers) > 0 {
		metadata.Headers = metadata.Headers.Clone()
	}
	return metadata
}

func applyErrorToObserveEvent(event *ObserveEvent, err error) {
	if event == nil || err == nil {
		return
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		event.Provider = firstProviderName(providerErr.Provider, event.Provider)
		event.ErrorCode = providerErr.Code
		event.StatusCode = providerErr.StatusCode
		event.Retryable = providerErr.Retryable
	}
}

func requestChatModel(req *ChatRequest) string {
	if req == nil {
		return ""
	}
	return req.Model
}

func requestEmbeddingModel(req *EmbeddingRequest) string {
	if req == nil {
		return ""
	}
	return req.Model
}

func firstProviderName(value, fallback ProviderName) ProviderName {
	if value != "" {
		return value
	}
	return fallback
}

func firstString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
