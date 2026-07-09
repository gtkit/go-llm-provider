package provider

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// ObserveOperation identifies the observed provider operation.
type ObserveOperation string

const (
	// ObserveOperationChat identifies a non-streaming chat call.
	ObserveOperationChat ObserveOperation = "chat"
	// ObserveOperationStream identifies a streaming chat creation call.
	ObserveOperationStream ObserveOperation = "stream"
	// ObserveOperationStreamComplete identifies the termination of a streaming chat call.
	// The event carries the final Usage observed on the stream; if the stream is
	// closed before reaching io.EOF, Usage may be zero.
	ObserveOperationStreamComplete ObserveOperation = "stream_complete"
	// ObserveOperationEmbed identifies an embedding call.
	ObserveOperationEmbed ObserveOperation = "embed"
)

// StreamFinishReason describes how a streaming call terminated.
type StreamFinishReason string

const (
	// StreamFinishEOF indicates the stream was fully consumed to io.EOF.
	StreamFinishEOF StreamFinishReason = "eof"
	// StreamFinishError indicates the stream terminated with a receive error.
	StreamFinishError StreamFinishReason = "error"
	// StreamFinishClosed indicates the stream was closed before reaching io.EOF;
	// Usage may be zero even though the provider incurred consumption.
	StreamFinishClosed StreamFinishReason = "closed"
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

	// StreamFinish 仅在 Operation == ObserveOperationStreamComplete 时非空，
	// 标识流的终止方式；计费方可据此区分"正常结束但无 usage"与"提前关闭漏单"。
	StreamFinish StreamFinishReason
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

// ObserveStreamMiddleware reports ChatStream outcomes to opts.OnEvent.
// It emits an ObserveOperationStream event when the stream is created, and an
// ObserveOperationStreamComplete event carrying the final Usage when the stream
// terminates (io.EOF, a receive error, or an early Close).
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
			if err != nil || stream == nil || opts.OnEvent == nil {
				return stream, err
			}
			obs := &observedStream{
				inner:    stream,
				hook:     opts.OnEvent,
				provider: provider,
				model:    requestChatModel(req),
				start:    start,
			}
			return NewStreamReader(
				func() (*StreamChunk, error) { return obs.recv(ctx) },
				func() error { return obs.close(ctx) },
			), nil
		}
	}
}

// observedStream 包装流读取过程，在流终止时上报一次 stream_complete 事件。
type observedStream struct {
	inner    *StreamReader
	hook     ObserveHook
	provider ProviderName
	model    string
	start    time.Time

	mu      sync.Mutex
	usage   Usage
	emitted bool
}

func (s *observedStream) recv(ctx context.Context) (*StreamChunk, error) {
	chunk, err := s.inner.Recv()
	if chunk != nil && chunk.Usage != (Usage{}) {
		s.mu.Lock()
		s.usage = chunk.Usage
		s.mu.Unlock()
	}
	switch {
	case errors.Is(err, io.EOF):
		s.emit(ctx, nil, StreamFinishEOF)
	case err != nil:
		s.emit(ctx, err, StreamFinishError)
	}
	return chunk, err
}

func (s *observedStream) close(ctx context.Context) error {
	err := s.inner.Close()
	// 未读到流尾就 Close 时也上报，Usage 以已读到的为准（可能为零值）。
	s.emit(ctx, nil, StreamFinishClosed)
	return err
}

// emit 上报 stream_complete 事件，整个流生命周期内至多一次。
func (s *observedStream) emit(ctx context.Context, streamErr error, finish StreamFinishReason) {
	s.mu.Lock()
	if s.emitted {
		s.mu.Unlock()
		return
	}
	s.emitted = true
	usage := s.usage
	s.mu.Unlock()

	event := ObserveEvent{
		Operation:    ObserveOperationStreamComplete,
		Provider:     s.provider,
		Model:        s.model,
		Usage:        usage,
		Duration:     time.Since(s.start),
		Err:          streamErr,
		StreamFinish: finish,
	}
	applyErrorToObserveEvent(&event, streamErr)
	s.hook(ctx, event)
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
