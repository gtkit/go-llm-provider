package einoadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
)

// StreamControl owns one stream's cancellation and underlying Close result.
// Close is concurrency safe and waits for the bridge to exit. The provider must
// support concurrent Recv/Close, and Close must unblock Recv.
type StreamControl struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	started bool
	closed  bool
	done    chan struct{}
	stop    func() error
}

// NewStreamControl creates a single-use control without starting goroutines.
// cancel must cancel the context passed to Stream, including opening the request.
func NewStreamControl(cancel context.CancelFunc) (*StreamControl, error) {
	if cancel == nil {
		return nil, invalid("nil stream cancel")
	}
	return &StreamControl{cancel: cancel}, nil
}

func (c *StreamControl) claim() error {
	if c == nil {
		return invalid("Stream requires WithStreamControl")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel == nil || c.started || c.closed {
		return invalid("StreamControl is uninitialized or already used")
	}
	c.started = true
	c.done = make(chan struct{})
	return nil
}

// Close cancels the call, closes its upstream at most once and waits for the
// bridge. Repeated calls return the same underlying Close error, including after
// natural EOF. Also close the Eino reader exactly once, as required by Eino.
func (c *StreamControl) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	cancel, stop, done := c.cancel, c.stop, c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stop != nil {
		_ = stop()
	} // The same result is returned after pump completion.
	if done != nil {
		<-done
	}
	c.mu.Lock()
	stop = c.stop
	c.mu.Unlock()
	if stop != nil {
		return stop()
	}
	return nil
}

// Stream invokes the provider once and bridges incrementally through a pipe of
// capacity one. The first frame contains only opening metadata. Always pass a
// fresh StreamControl, close the returned reader, and check control.Close().
func (m *ChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	req, control, err := m.request(messages, opts)
	if err != nil {
		return nil, err
	}
	if err := control.claim(); err != nil {
		return nil, err
	}
	upstream, err := m.provider.ChatStream(ctx, req)
	if err != nil || upstream == nil {
		close(control.done)
		if err == nil {
			err = invalid("nil upstream stream")
		}
		return nil, err
	}
	reader, writer := schema.Pipe[*schema.Message](1)
	// In the pinned Eino version this enables atomic, idempotent pipe Close.
	// Set it before publishing: cancellation and Eino's own Close may race.
	reader.SetAutomaticClose()
	stop := sync.OnceValue(func() error { reader.Close(); return upstream.Close() })
	control.mu.Lock()
	control.stop = stop
	closed := control.closed
	control.mu.Unlock()
	if closed {
		_ = stop()
	} // Retained by control.Close.
	stopCancellation := context.AfterFunc(ctx, func() { _ = stop() })
	go func() {
		defer close(control.done)
		defer writer.Close()
		defer stopCancellation()
		defer func() { _ = stop() }() // Retained by control.Close.
		pump(ctx, upstream, writer)
	}()
	return schema.StreamReaderWithConvert(reader, func(msg *schema.Message) (*schema.Message, error) { return msg, nil },
		schema.WithOnEOF(func() (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("einoadapter: stream: %w", err)
			}
			return nil, io.EOF
		})), nil
}

func pump(ctx context.Context, upstream *provider.StreamReader, writer *schema.StreamWriter[*schema.Message]) {
	metadata := upstream.Metadata()
	extra, err := eventExtra(event{Kind: "opening", Metadata: &metadata})
	if err != nil {
		writer.Send(nil, err)
		return
	}
	if writer.Send(&schema.Message{Role: schema.Assistant, Extra: extra}, nil) {
		return
	}
	reasoningEnd := 0
	for ctx.Err() == nil {
		chunk, err := upstream.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			writer.Send(nil, err)
			return
		}
		if chunk == nil {
			writer.Send(nil, invalid("nil upstream chunk"))
			return
		}
		reasoningEnd += len(chunk.ReasoningDelta)
		message, err := chunkMessage(chunk, reasoningEnd)
		if writer.Send(message, err) || err != nil {
			return
		}
	}
}

func chunkMessage(chunk *provider.StreamChunk, reasoningEnd int) (*schema.Message, error) {
	out := &schema.Message{Role: schema.Assistant, Content: chunk.Delta, ReasoningContent: chunk.ReasoningDelta}
	if chunk.FinishReason != "" || chunk.Usage != (provider.Usage{}) {
		out.ResponseMeta = &schema.ResponseMeta{FinishReason: chunk.FinishReason, Usage: nativeUsage(chunk.Usage)}
	}
	e := event{Kind: "chunk", Model: chunk.Model, Parts: chunk.Parts, Search: chunk.Search, Signature: chunk.ThinkingSignature, Redacted: chunk.RedactedThinking}
	if e.Signature != "" {
		e.ReasoningEnd = reasoningEnd
	}
	if chunk.Usage != (provider.Usage{}) {
		e.Usage = &chunk.Usage
	}
	for _, call := range chunk.ToolCalls {
		if call.Index < 0 {
			return nil, invalid("negative tool index")
		}
		out.ToolCalls = append(out.ToolCalls, schema.ToolCall{Index: new(call.Index), ID: call.ID, Type: "function", Function: schema.FunctionCall{Name: call.Function.Name, Arguments: call.Function.Arguments}})
		if call.ThoughtSignature != "" {
			e.Tools = append(e.Tools, toolSignature{Index: call.Index, Value: call.ThoughtSignature})
		}
	}
	// Ordinary text deltas stay entirely native; only non-native data is framed.
	if e.Model != "" || e.Usage != nil || len(e.Parts) > 0 || e.Search != nil || e.Signature != "" || e.Redacted != "" || len(e.Tools) > 0 {
		var err error
		out.Extra, err = eventExtra(e)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
