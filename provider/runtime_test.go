package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gtkit/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubProvider struct {
	name       ProviderName
	chat       func(context.Context, *ChatRequest) (*ChatResponse, error)
	chatStream func(context.Context, *ChatRequest) (*StreamReader, error)
}

func (p *stubProvider) Name() ProviderName {
	if p == nil {
		return ""
	}

	return p.name
}

func (p *stubProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if p.chat == nil {
		return nil, errors.New("chat not implemented")
	}

	return p.chat(ctx, req)
}

func (p *stubProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p.chatStream == nil {
		return nil, errors.New("chat stream not implemented")
	}

	return p.chatStream(ctx, req)
}

func TestSimpleChatRejectsNilProvider(t *testing.T) {
	t.Parallel()

	_, err := SimpleChat(t.Context(), nil, "hello")
	require.Error(t, err)
	assert.ErrorContains(t, err, "provider is nil")
}

func TestSimpleChatWithSystemRejectsTypedNilProvider(t *testing.T) {
	t.Parallel()

	var p *openaiProvider

	_, err := SimpleChatWithSystem(t.Context(), p, "system", "hello")
	require.Error(t, err)
	assert.ErrorContains(t, err, "provider is nil")
}

func TestCollectStreamRejectsNilProvider(t *testing.T) {
	t.Parallel()

	_, err := CollectStream(t.Context(), nil, &ChatRequest{
		Messages: []Message{UserText("hello")},
	}, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "provider is nil")
}

func TestRunToolLoopRejectsNilRequest(t *testing.T) {
	t.Parallel()

	resp, err := RunToolLoop(t.Context(), &stubProvider{name: ProviderOpenAI}, nil, 1, func(context.Context, string, string) (string, error) {
		return "", nil
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorContains(t, err, "chat request is nil")
}

func TestRunToolLoopRejectsTypedNilProvider(t *testing.T) {
	t.Parallel()

	var p *openaiProvider

	resp, err := RunToolLoop(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hello")},
	}, 1, func(context.Context, string, string) (string, error) {
		return "", nil
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorContains(t, err, "provider is nil")
}

func TestRunToolLoopPreservesEnableThinking(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			require.NotNil(t, req)
			assert.True(t, req.EnableThinking)
			return &ChatResponse{Content: "done"}, nil
		},
	}

	resp, err := RunToolLoop(t.Context(), p, &ChatRequest{
		Messages:       []Message{UserText("hello")},
		EnableThinking: true,
	}, 1, func(context.Context, string, string) (string, error) {
		return "", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
}

func TestRunToolLoopSanitizesHandlerErrorsByDefault(t *testing.T) {
	t.Parallel()

	requests := make([]*ChatRequest, 0, 2)
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{
						{
							ID: "call_1",
							Function: FunctionCall{
								Name:      "explode",
								Arguments: `{"value":1}`,
							},
						},
					},
				}, nil
			}

			return &ChatResponse{Content: "done"}, nil
		},
	}

	handlerErr := errors.New("bad \"quote\"\nline")

	resp, err := RunToolLoop(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hello")},
	}, 2, func(context.Context, string, string) (string, error) {
		return "", handlerErr
	})
	require.NoError(t, err)
	require.Equal(t, "done", resp.Content)
	require.Len(t, requests, 2)
	require.NotEmpty(t, requests[1].Messages)

	lastMessage := requests[1].Messages[len(requests[1].Messages)-1]
	assert.Equal(t, RoleTool, lastMessage.Role)
	require.Len(t, lastMessage.Content, 1)

	var payload struct {
		Error string `json:"error"`
	}

	require.NoError(t, json.Unmarshal([]byte(lastMessage.Content[0].Text), &payload))
	assert.Equal(t, "tool execution failed", payload.Error)
	assert.NotContains(t, lastMessage.Content[0].Text, handlerErr.Error())
}

func TestRunToolLoopUsesCustomToolErrorEncoder(t *testing.T) {
	t.Parallel()

	requests := make([]*ChatRequest, 0, 2)
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{
						{
							ID: "call_custom",
							Function: FunctionCall{
								Name:      "explode",
								Arguments: `{"value":1}`,
							},
						},
					},
				}, nil
			}

			return &ChatResponse{Content: "done"}, nil
		},
	}

	handlerErr := errors.New("custom handler detail")

	resp, err := RunToolLoopWithOptions(
		t.Context(),
		p,
		&ChatRequest{Messages: []Message{UserText("hello")}},
		func(context.Context, string, string) (string, error) {
			return "", handlerErr
		},
		RunToolLoopOptions{
			MaxRounds: 2,
			ToolErrorEncoder: func(_ context.Context, tc ToolCall, err error) (Message, error) {
				return ToolResultMessageJSON(tc.ID, map[string]string{
					"error": err.Error(),
					"mode":  "custom",
				})
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "done", resp.Content)
	require.Len(t, requests, 2)

	lastMessage := requests[1].Messages[len(requests[1].Messages)-1]
	require.Len(t, lastMessage.Content, 1)
	var payload struct {
		Error string `json:"error"`
		Mode  string `json:"mode"`
	}
	require.NoError(t, json.Unmarshal([]byte(lastMessage.Content[0].Text), &payload))
	assert.Equal(t, handlerErr.Error(), payload.Error)
	assert.Equal(t, "custom", payload.Mode)
}

func TestRunToolLoopDefaultExecutionRemainsSerial(t *testing.T) {
	t.Parallel()

	requests := make([]*ChatRequest, 0, 2)
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{
						{ID: "call_1", Function: FunctionCall{Name: "one", Arguments: `{}`}},
						{ID: "call_2", Function: FunctionCall{Name: "two", Arguments: `{}`}},
					},
				}, nil
			}

			return &ChatResponse{Content: "done"}, nil
		},
	}

	var active atomic.Int32
	var maxActive atomic.Int32

	resp, err := RunToolLoop(
		t.Context(),
		p,
		&ChatRequest{Messages: []Message{UserText("hello")}},
		2,
		func(_ context.Context, _ string, _ string) (string, error) {
			current := active.Add(1)
			for {
				seen := maxActive.Load()
				if current <= seen || maxActive.CompareAndSwap(seen, current) {
					break
				}
			}
			defer active.Add(-1)
			time.Sleep(10 * time.Millisecond)
			return "ok", nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, "done", resp.Content)
	assert.Equal(t, int32(1), maxActive.Load())
}

func TestRunToolLoopParallelToolCallsPreserveMessageOrder(t *testing.T) {
	t.Parallel()

	requests := make([]*ChatRequest, 0, 2)
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{
						{ID: "call_1", Function: FunctionCall{Name: "slow", Arguments: `{"idx":1}`}},
						{ID: "call_2", Function: FunctionCall{Name: "fast", Arguments: `{"idx":2}`}},
					},
				}, nil
			}

			return &ChatResponse{Content: "done"}, nil
		},
	}

	var active atomic.Int32
	var maxActive atomic.Int32
	slowStarted := make(chan struct{})
	fastStarted := make(chan struct{})

	resp, err := RunToolLoopWithOptions(
		t.Context(),
		p,
		&ChatRequest{Messages: []Message{UserText("hello")}},
		func(_ context.Context, name, _ string) (string, error) {
			current := active.Add(1)
			for {
				seen := maxActive.Load()
				if current <= seen || maxActive.CompareAndSwap(seen, current) {
					break
				}
			}
			defer active.Add(-1)

			if name == "slow" {
				close(slowStarted)
				select {
				case <-fastStarted:
				case <-time.After(time.Second):
					return "", errors.New("fast tool did not start in parallel")
				}
				time.Sleep(40 * time.Millisecond)
				return "slow-result", nil
			}

			close(fastStarted)
			select {
			case <-slowStarted:
			case <-time.After(time.Second):
				return "", errors.New("slow tool did not start in parallel")
			}
			time.Sleep(5 * time.Millisecond)
			return "fast-result", nil
		},
		RunToolLoopOptions{
			MaxRounds:         2,
			ParallelToolCalls: true,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "done", resp.Content)
	require.Len(t, requests, 2)
	require.Greater(t, maxActive.Load(), int32(1))

	lastTwo := requests[1].Messages[len(requests[1].Messages)-2:]
	require.Len(t, lastTwo[0].Content, 1)
	require.Len(t, lastTwo[1].Content, 1)
	assert.Equal(t, []string{"call_1", "call_2"}, []string{lastTwo[0].ToolCallID, lastTwo[1].ToolCallID})
	assert.Equal(t, []string{"slow-result", "fast-result"}, []string{lastTwo[0].Content[0].Text, lastTwo[1].Content[0].Text})
}

func TestRunToolLoopStopsOnContextErrorFromHandler(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{
				ToolCalls: []ToolCall{
					{ID: "call_ctx", Function: FunctionCall{Name: "cancel", Arguments: `{}`}},
				},
			}, nil
		},
	}

	resp, err := RunToolLoop(
		t.Context(),
		p,
		&ChatRequest{Messages: []Message{UserText("hello")}},
		2,
		func(context.Context, string, string) (string, error) {
			return "", fmt.Errorf("wrapped: %w", context.Canceled)
		},
	)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, resp)
}

func TestQuickRegistrySelectsDeterministicDefault(t *testing.T) {
	t.Parallel()

	defaults := make(map[ProviderName]int)
	keys := map[ProviderName]string{
		ProviderDeepSeek: "deepseek-key",
		ProviderQwen:     "qwen-key",
		ProviderZhipu:    "zhipu-key",
	}

	for range 200 {
		reg := QuickRegistry(keys)
		p, err := reg.Default()
		require.NoError(t, err)
		defaults[p.Name()]++
	}

	assert.Equal(t, map[ProviderName]int{
		ProviderDeepSeek: 200,
	}, defaults)
}

func TestRegistryNamesAreSorted(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Register(&stubProvider{name: ProviderZhipu})
	reg.Register(&stubProvider{name: ProviderDeepSeek})
	reg.Register(&stubProvider{name: ProviderQwen})

	expected := []ProviderName{
		ProviderDeepSeek,
		ProviderQwen,
		ProviderZhipu,
	}

	for range 50 {
		assert.Equal(t, expected, reg.Names())
	}
}

func TestRegistryRegisterIgnoresNilProvider(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()

	require.NotPanics(t, func() {
		reg.Register(nil)
	})

	_, err := reg.Default()
	require.Error(t, err)
	assert.ErrorContains(t, err, "no provider registered")
}

func TestRegistryRegisterIgnoresTypedNilProvider(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	var p *openaiProvider

	require.NotPanics(t, func() {
		reg.Register(p)
	})

	_, err := reg.Default()
	require.Error(t, err)
	assert.ErrorContains(t, err, "no provider registered")
}

func TestStreamReaderZeroValueIsSafe(t *testing.T) {
	t.Parallel()

	var reader StreamReader

	require.NotPanics(t, func() {
		reader.Close()
	})

	_, err := reader.Recv()
	require.Error(t, err)
	assert.ErrorContains(t, err, "stream is not initialized")
}

// TestNewStreamReaderUsesInjectedCallbacks 验证自定义回调能够驱动统一的流读取器。
func TestNewStreamReaderUsesInjectedCallbacks(t *testing.T) {
	t.Parallel()

	chunks := []*StreamChunk{
		{Delta: "he"},
		{Delta: "llo"},
	}
	index := 0
	closed := false

	reader := NewStreamReader(func() (*StreamChunk, error) {
		if index >= len(chunks) {
			return nil, io.EOF
		}

		chunk := chunks[index]
		index++
		return chunk, nil
	}, func() error {
		closed = true
		return nil
	})

	first, err := reader.Recv()
	require.NoError(t, err)
	assert.Equal(t, "he", first.Delta)

	second, err := reader.Recv()
	require.NoError(t, err)
	assert.Equal(t, "llo", second.Delta)

	_, err = reader.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, reader.Close())
	assert.True(t, closed)
}

// TestNewStreamReaderAllowsNilClose 验证未提供关闭回调时 Close 仍然安全可用。
func TestNewStreamReaderAllowsNilClose(t *testing.T) {
	t.Parallel()

	reader := NewStreamReader(func() (*StreamChunk, error) {
		return nil, io.EOF
	}, nil)

	_, err := reader.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, reader.Close())
}
