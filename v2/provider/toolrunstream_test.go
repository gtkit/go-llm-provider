package provider

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chunkStream 把预置 chunk 序列包装成 StreamReader。
func chunkStream(chunks []StreamChunk) *StreamReader {
	next := 0
	return NewStreamReader(func() (*StreamChunk, error) {
		if next >= len(chunks) {
			return nil, io.EOF
		}
		chunk := chunks[next]
		next++
		return &chunk, nil
	}, nil)
}

func TestRunToolLoopStreamPlainReply(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return chunkStream([]StreamChunk{
				{Delta: "你好", ReasoningDelta: "想一下"},
				{Delta: "，世界", FinishReason: "stop", Usage: Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}},
			}), nil
		},
	}

	var deltas []string
	resp, err := RunToolLoopStream(t.Context(), p, &ChatRequest{Messages: []Message{UserText("hi")}}, 5,
		func(context.Context, string, string) (string, error) { return "", nil },
		func(chunk StreamChunk) {
			if chunk.Delta != "" {
				deltas = append(deltas, chunk.Delta)
			}
		})
	require.NoError(t, err)
	assert.Equal(t, "你好，世界", resp.Content)
	assert.Equal(t, "想一下", resp.Reasoning)
	assert.Equal(t, "stop", resp.FinishReason)
	assert.Equal(t, 5, resp.Usage.TotalTokens)
	assert.Equal(t, []string{"你好", "，世界"}, deltas)
}

func TestRunToolLoopStreamExecutesToolsAcrossRounds(t *testing.T) {
	t.Parallel()

	var round int
	var secondRoundReq *ChatRequest
	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(_ context.Context, req *ChatRequest) (*StreamReader, error) {
			round++
			if round == 1 {
				// tool call 的 arguments 分两个 chunk 到达，验证库内拼装。
				return chunkStream([]StreamChunk{
					{ToolCalls: []ToolCallDelta{{Index: 0, ID: "call_1", Function: FunctionCallDelta{Name: "get_weather", Arguments: `{"city":`}}}},
					{ToolCalls: []ToolCallDelta{{Index: 0, Function: FunctionCallDelta{Arguments: `"Paris"}`}}}},
					{FinishReason: "tool_calls", Usage: Usage{TotalTokens: 10}},
				}), nil
			}
			secondRoundReq = req
			return chunkStream([]StreamChunk{
				{Delta: "巴黎晴天", FinishReason: "stop", Usage: Usage{TotalTokens: 7}},
			}), nil
		},
	}

	var toolName, toolArgs string
	resp, err := RunToolLoopStreamWithOptions(t.Context(), p,
		&ChatRequest{Messages: []Message{UserText("巴黎天气如何")}},
		func(_ context.Context, name, args string) (string, error) {
			toolName, toolArgs = name, args
			return `{"weather":"sunny"}`, nil
		},
		nil,
		RunToolLoopOptions{MaxRounds: 5, AccumulateUsage: true})
	require.NoError(t, err)

	assert.Equal(t, "get_weather", toolName)
	assert.JSONEq(t, `{"city":"Paris"}`, toolArgs)
	assert.Equal(t, "巴黎晴天", resp.Content)
	assert.Equal(t, 17, resp.Usage.TotalTokens, "AccumulateUsage 跨轮累加")

	// 第二轮请求应包含 assistant 的 tool_calls 与 tool 结果消息。
	require.NotNil(t, secondRoundReq)
	require.Len(t, secondRoundReq.Messages, 3)
	assert.Equal(t, RoleAssistant, secondRoundReq.Messages[1].Role)
	require.Len(t, secondRoundReq.Messages[1].ToolCalls, 1)
	assert.Equal(t, "call_1", secondRoundReq.Messages[1].ToolCalls[0].ID)
	assert.Equal(t, RoleTool, secondRoundReq.Messages[2].Role)
	assert.Equal(t, "call_1", secondRoundReq.Messages[2].ToolCallID)
	assert.Contains(t, contentText(secondRoundReq.Messages[2].Content), "sunny")
}

func TestRunToolLoopStreamExceedsMaxRounds(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return chunkStream([]StreamChunk{
				{ToolCalls: []ToolCallDelta{{Index: 0, ID: "c", Function: FunctionCallDelta{Name: "f", Arguments: "{}"}}}},
				{FinishReason: "tool_calls"},
			}), nil
		},
	}

	_, err := RunToolLoopStream(t.Context(), p, &ChatRequest{Messages: []Message{UserText("hi")}}, 2,
		func(context.Context, string, string) (string, error) { return "ok", nil }, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "max rounds (2)")
}

func TestRunToolLoopStreamValidatesInputs(t *testing.T) {
	t.Parallel()

	p := &stubProvider{name: ProviderOpenAI}
	handler := func(context.Context, string, string) (string, error) { return "", nil }

	_, err := RunToolLoopStream(t.Context(), nil, &ChatRequest{}, 1, handler, nil)
	require.ErrorIs(t, err, ErrNilProvider)
	_, err = RunToolLoopStream(t.Context(), p, nil, 1, handler, nil)
	require.ErrorIs(t, err, ErrNilChatRequest)
	_, err = RunToolLoopStream(t.Context(), p, &ChatRequest{}, 1, nil, nil)
	require.ErrorIs(t, err, ErrToolHandlerRequired)
}

func TestToolCallAssemblerMultipleCalls(t *testing.T) {
	t.Parallel()

	assembler := newToolCallAssembler()
	assembler.add(ToolCallDelta{Index: 0, ID: "a", Function: FunctionCallDelta{Name: "f1", Arguments: "{"}})
	assembler.add(ToolCallDelta{Index: 1, ID: "b", Function: FunctionCallDelta{Name: "f2", Arguments: `{"x":1}`}})
	assembler.add(ToolCallDelta{Index: 0, Function: FunctionCallDelta{Arguments: "}"}})

	calls := assembler.result()
	require.Len(t, calls, 2)
	assert.Equal(t, "a", calls[0].ID)
	assert.Equal(t, "f1", calls[0].Function.Name)
	assert.Equal(t, "{}", calls[0].Function.Arguments)
	assert.Equal(t, "b", calls[1].ID)

	empty := newToolCallAssembler()
	assert.Nil(t, empty.result())
}

func TestRunToolLoopStreamAppliesToolResultTransformer(t *testing.T) {
	t.Parallel()

	var round int
	var secondRoundReq *ChatRequest
	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(_ context.Context, req *ChatRequest) (*StreamReader, error) {
			round++
			if round == 1 {
				return chunkStream([]StreamChunk{
					{ToolCalls: []ToolCallDelta{{Index: 0, ID: "call_1", Function: FunctionCallDelta{Name: "f", Arguments: "{}"}}}},
					{FinishReason: "tool_calls"},
				}), nil
			}
			secondRoundReq = req
			return chunkStream([]StreamChunk{{Delta: "done", FinishReason: "stop"}}), nil
		},
	}

	resp, err := RunToolLoopStreamWithOptions(t.Context(), p,
		&ChatRequest{Messages: []Message{UserText("hi")}},
		func(context.Context, string, string) (string, error) { return "raw-result", nil },
		nil,
		RunToolLoopOptions{
			MaxRounds:             5,
			ToolResultTransformer: WrapToolResultInTag("tool_result"),
		})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)

	require.NotNil(t, secondRoundReq)
	lastMessage := secondRoundReq.Messages[len(secondRoundReq.Messages)-1]
	assert.Equal(t, "<tool_result>\nraw-result\n</tool_result>", contentText(lastMessage.Content))
}

func TestRunToolLoopStreamResponseValidatorRejectsFinalResponse(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return chunkStream([]StreamChunk{{Delta: "done", FinishReason: "stop"}}), nil
		},
	}

	rejectErr := errors.New("suspicious output")
	resp, err := RunToolLoopStreamWithOptions(t.Context(), p,
		&ChatRequest{Messages: []Message{UserText("hi")}},
		func(context.Context, string, string) (string, error) { return "", nil },
		nil,
		RunToolLoopOptions{
			MaxRounds: 5,
			ResponseValidator: func(_ context.Context, resp *ChatResponse) error {
				if resp.Content == "done" {
					return rejectErr
				}
				return nil
			},
		})
	require.Error(t, err)
	assert.Nil(t, resp)
	require.ErrorIs(t, err, rejectErr)
}

func TestRunToolLoopStreamPropagatesRecvError(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) {
				return nil, &ProviderError{Provider: ProviderOpenAI, Code: ErrorCodeServerError, Message: "boom"}
			}, nil), nil
		},
	}

	_, err := RunToolLoopStream(t.Context(), p, &ChatRequest{Messages: []Message{UserText("hi")}}, 3,
		func(context.Context, string, string) (string, error) { return "", nil }, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "round 1")
	assert.ErrorIs(t, err, ErrServerError)
}
