package einoadapter

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
	"github.com/gtkit/json/v2"
)

func mustControl(t *testing.T, cancel context.CancelFunc) *StreamControl {
	t.Helper()
	c, err := NewStreamControl(cancel)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	return c
}
func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop")
	}
}

func TestStreamRoundTripConcatAndTailUsage(t *testing.T) {
	t.Parallel()
	usage := fullUsage()
	prior := usage
	prior.PromptTokens = 1000
	chunks := []*provider.StreamChunk{
		{ReasoningDelta: "a", Model: "actual"},
		{ReasoningDelta: "b", ThinkingSignature: "signature"},
		{RedactedThinking: "hidden"},
		{Delta: "ans", ToolCalls: []provider.ToolCallDelta{{Index: 1, ID: "b", Function: provider.FunctionCallDelta{Name: "two", Arguments: `{"q":`}, ThoughtSignature: "sig-b"}}},
		{Delta: "wer", ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "a", Function: provider.FunctionCallDelta{Name: "one", Arguments: `{"q":`}}}},
		{ToolCalls: []provider.ToolCallDelta{{Index: 1, Function: provider.FunctionCallDelta{Arguments: `2}`}}, {Index: 0, Function: provider.FunctionCallDelta{Arguments: `1}`}}}, Usage: prior},
		{FinishReason: "tool_calls", Parts: responseFixture().Parts, Search: responseFixture().Search},
		{Usage: usage}, // Deliberately smaller than the previous complete snapshot.
	}
	metadata := responseFixture().Metadata
	i, calls := 0, 0
	var closes atomic.Int32
	closeErr := errors.New("close result")
	m := mustModel(t, &fakeProvider{stream: func(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
		calls++
		return provider.NewStreamReaderWithMetadata(func() (*provider.StreamChunk, error) {
			if i == len(chunks) {
				return nil, io.EOF
			}
			c := chunks[i]
			i++
			return c, nil
		}, func() error { closes.Add(1); return closeErr }, metadata), nil
	}})
	ctx, cancel := context.WithCancel(t.Context())
	control := mustControl(t, cancel)
	reader, err := m.Stream(ctx, nil, WithStreamControl(control))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	opening, err := reader.Recv()
	if err != nil {
		t.Fatal(err)
	}
	gotMetadata, err := OpeningMetadata(opening)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, metadata, gotMetadata)
	if _, err = ToChunk(opening); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatal(err)
	}
	messages := make([]*schema.Message, 1, 1+len(chunks))
	messages[0] = opening
	for _, want := range chunks {
		msg, err := reader.Recv()
		if err != nil {
			t.Fatal(err)
		}
		got, err := ToChunk(msg)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, want, got)
		messages = append(messages, msg)
	}
	if _, err = reader.Recv(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if err = control.Close(); !errors.Is(err, closeErr) {
		t.Fatal(err)
	}
	if err = control.Close(); !errors.Is(err, closeErr) {
		t.Fatal(err)
	}
	equal(t, int32(1), closes.Load())
	equal(t, 1, calls)
	for range 2 {
		joined, err := schema.ConcatMessages(messages)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ToResponse(joined)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, "answer", got.Content)
		equal(t, "ab", got.Reasoning)
		equal(t, "tool_calls", got.FinishReason)
		equal(t, usage, got.Usage)
		equal(t, metadata, got.Metadata)
		equal(t, responseFixture().Parts, got.Parts)
		equal(t, responseFixture().Search, got.Search)
		equal(t, []provider.ThinkingBlock{{Thinking: "ab", Signature: "signature"}, {Redacted: "hidden"}}, got.ThinkingBlocks)
		equal(t, []provider.ToolCall{{ID: "a", Function: provider.FunctionCall{Name: "one", Arguments: `{"q":1}`}}, {ID: "b", Function: provider.FunctionCall{Name: "two", Arguments: `{"q":2}`}, ThoughtSignature: "sig-b"}}, got.ToolCalls)
		raw, err := json.Marshal(messages)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &messages); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBlockedReceiveAndSendCancelOrClose(t *testing.T) {
	t.Parallel()
	for _, blocked := range []string{"receive", "send"} {
		for _, action := range []string{"cancel", "close"} {
			t.Run(blocked+"/"+action, func(t *testing.T) {
				t.Parallel()
				entered, closed := make(chan struct{}), make(chan struct{})
				var enterOnce, closeOnce sync.Once
				var closes atomic.Int32
				m := mustModel(t, &fakeProvider{stream: func(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
					return provider.NewStreamReader(func() (*provider.StreamChunk, error) {
						enterOnce.Do(func() { close(entered) })
						if blocked == "receive" {
							<-closed
							return nil, context.Canceled
						}
						return &provider.StreamChunk{Delta: "chunk"}, nil
					}, func() error { closes.Add(1); closeOnce.Do(func() { close(closed) }); return nil }), nil
				}})
				ctx, cancel := context.WithCancel(t.Context())
				control := mustControl(t, cancel)
				reader, err := m.Stream(ctx, nil, WithStreamControl(control))
				if err != nil {
					t.Fatal(err)
				}
				waitSignal(t, entered) // Opening fills the pipe; Send or Recv now blocks.
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					if action == "cancel" {
						cancel()
						<-control.done
					} else {
						reader.Close()
					}
					if err := control.Close(); err != nil {
						t.Error(err)
					}
				}()
				waitSignal(t, finished)
				if action == "cancel" {
					reader.Close()
				}
				equal(t, int32(1), closes.Load())
				if _, err = m.Stream(ctx, nil, WithStreamControl(control)); !errors.Is(err, provider.ErrInvalidRequest) {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestConcurrentCloseWhileOpening(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	m := mustModel(t, &fakeProvider{stream: func(ctx context.Context, _ *provider.ChatRequest) (*provider.StreamReader, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	ctx, cancel := context.WithCancel(t.Context())
	control := mustControl(t, cancel)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		if _, err := m.Stream(ctx, nil, WithStreamControl(control)); !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	waitSignal(t, entered)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := control.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	waitSignal(t, finished)
}

func TestStreamValidationErrorsAndEOF(t *testing.T) {
	t.Parallel()
	if _, err := NewStreamControl(nil); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatal(err)
	}
	m := mustModel(t, &fakeProvider{})
	if _, err := m.Stream(t.Context(), nil); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatal(err)
	}
	for _, scenario := range []string{"empty", "nil stream", "nil chunk", "upstream error", "bad index", "plain text"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			upstreamErr := errors.New("upstream")
			recv := 0
			p := &fakeProvider{stream: func(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
				if scenario == "nil stream" {
					return nil, nil
				}
				return provider.NewStreamReader(func() (*provider.StreamChunk, error) {
					recv++
					if recv > 1 {
						return nil, io.EOF
					}
					switch scenario {
					case "nil chunk":
						return nil, nil
					case "upstream error":
						return nil, upstreamErr
					case "bad index":
						return &provider.StreamChunk{ToolCalls: []provider.ToolCallDelta{{Index: -1}}}, nil
					case "plain text":
						return &provider.StreamChunk{Delta: "text"}, nil
					default:
						return nil, io.EOF
					}
				}, nil), nil
			}}
			ctx, cancel := context.WithCancel(t.Context())
			control := mustControl(t, cancel)
			reader, err := mustModel(t, p).Stream(ctx, nil, WithStreamControl(control))
			if scenario == "nil stream" {
				if !errors.Is(err, provider.ErrInvalidRequest) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			opening, err := reader.Recv()
			if err != nil {
				t.Fatal(err)
			}
			msg, err := reader.Recv()
			switch scenario {
			case "empty":
				if !errors.Is(err, io.EOF) {
					t.Fatal(err)
				}
			case "upstream error":
				if !errors.Is(err, upstreamErr) {
					t.Fatal(err)
				}
			case "plain text":
				if err != nil {
					t.Fatal(err)
				}
				joined, err := schema.ConcatMessages([]*schema.Message{opening, msg})
				if err != nil {
					t.Fatal(err)
				}
				resp, err := ToResponse(joined)
				if err != nil {
					t.Fatal(err)
				}
				equal(t, "text", resp.Content)
			default:
				if !errors.Is(err, provider.ErrInvalidRequest) {
					t.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMessageBridge(b *testing.B) {
	chunk := &provider.StreamChunk{Delta: "text", Usage: fullUsage()}
	b.ReportAllocs()
	for b.Loop() {
		message, err := chunkMessage(chunk, 0)
		if err != nil {
			b.Fatal(err)
		}
		if _, err = ToChunk(message); err != nil {
			b.Fatal(err)
		}
	}
}
