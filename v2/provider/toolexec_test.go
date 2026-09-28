package provider

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runToolRound 让模型首轮返回 calls，返回循环错误与第二轮请求中的工具结果消息。
func runToolRound(t *testing.T, calls []ToolCall, handler ToolHandler, opts RunToolLoopOptions) ([]Message, error) {
	t.Helper()
	var toolMsgs []Message
	var round int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			round++
			if round == 1 {
				return &ChatResponse{ToolCalls: calls}, nil
			}
			for _, m := range req.Messages {
				if m.Role == RoleTool {
					toolMsgs = append(toolMsgs, m)
				}
			}
			return &ChatResponse{Content: "done"}, nil
		},
	}
	opts.MaxRounds = 3
	_, err := RunToolLoopWithOptions(t.Context(), p, &ChatRequest{
		Tools:    offeredTools("f"),
		Messages: []Message{UserText("hi")},
	}, handler, opts)
	return toolMsgs, err
}

func toolCalls(n int, args string) []ToolCall {
	calls := make([]ToolCall, n)
	for i := range calls {
		calls[i] = ToolCall{ID: string(rune('a' + i)), Function: FunctionCall{Name: "f", Arguments: args}}
	}
	return calls
}

func TestRunToolLoopRecoversCallbackPanic(t *testing.T) {
	t.Parallel()

	failing := func(context.Context, string, string) (string, error) { return "", errors.New("boom") }
	cases := map[string]struct {
		handler ToolHandler
		opts    RunToolLoopOptions
	}{
		"handler": {
			handler: func(context.Context, string, string) (string, error) { panic("handler") },
		},
		"encoder": {
			handler: failing,
			opts: RunToolLoopOptions{ToolErrorEncoder: func(context.Context, ToolCall, error) (Message, error) {
				panic("encoder")
			}},
		},
		"should retry": {
			handler: failing,
			opts: RunToolLoopOptions{ToolRetry: ToolRetryOptions{MaxAttempts: 2, ShouldRetry: func(error) bool {
				panic("should retry")
			}}},
		},
	}
	for name, tc := range cases {
		for _, parallel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/parallel=%t", name, parallel), func(t *testing.T) {
				t.Parallel()
				opts := tc.opts
				opts.ParallelToolCalls = parallel
				_, err := runToolRound(t, toolCalls(2, "{}"), tc.handler, opts)
				require.ErrorIs(t, err, ErrToolHandlerPanic)
				assert.Contains(t, err.Error(), `tool "f"`)
			})
		}
	}
}

func TestRunToolLoopInvalidArgumentsSkipHandler(t *testing.T) {
	t.Parallel()

	for _, retry := range []ToolRetryOptions{{}, {MaxAttempts: 3}} {
		var handled atomic.Int32
		var encoded error
		msgs, err := runToolRound(t, toolCalls(1, `{"city":`), func(context.Context, string, string) (string, error) {
			handled.Add(1)
			return "ok", nil
		}, RunToolLoopOptions{
			ToolRetry: retry,
			ToolErrorEncoder: func(ctx context.Context, call ToolCall, err error) (Message, error) {
				encoded = err
				return DefaultToolErrorEncoder(ctx, call, err)
			},
		})
		require.NoError(t, err)
		assert.Zero(t, handled.Load())
		require.ErrorIs(t, encoded, ErrToolArgumentsInvalid)
		require.Len(t, msgs, 1)
		assert.Contains(t, msgs[0].Content[0].Text, "tool arguments are not valid JSON")
	}
}

func TestRunToolLoopEmptyArgumentsCallHandler(t *testing.T) {
	t.Parallel()

	var got []string
	msgs, err := runToolRound(t, toolCalls(1, ""), func(_ context.Context, _ string, args string) (string, error) {
		got = append(got, args)
		return "ok", nil
	}, RunToolLoopOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{""}, got)
	require.Len(t, msgs, 1)
	assert.Equal(t, "ok", msgs[0].Content[0].Text)
}

func TestDefaultToolErrorEncoderMessages(t *testing.T) {
	t.Parallel()

	cases := map[string]error{
		`{"error":"tool not available in this round; use only the tools provided"}`:                                       ErrToolNotOffered,
		`{"error":"tool arguments are not valid JSON (possibly truncated); regenerate the call with complete arguments"}`: ErrToolArgumentsInvalid,
		`{"error":"tool execution failed"}`: errors.New("db password leaked"),
	}
	for want, err := range cases {
		msg, encErr := DefaultToolErrorEncoder(t.Context(), ToolCall{ID: "c1"}, err)
		require.NoError(t, encErr)
		assert.Equal(t, "c1", msg.ToolCallID)
		assert.JSONEq(t, want, msg.Content[0].Text)
	}
}
