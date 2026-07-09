package provider

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectStreamResult(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderDeepSeek,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			chunks := []StreamChunk{
				{ReasoningDelta: "先想想"},
				{Delta: "答案"},
				{Delta: "是 42", FinishReason: "stop"},
				{Usage: Usage{PromptTokens: 8, CompletionTokens: 4, TotalTokens: 12}}, // OpenAI 兼容式收尾 usage chunk
			}
			next := 0
			return NewStreamReader(func() (*StreamChunk, error) {
				if next >= len(chunks) {
					return nil, io.EOF
				}
				chunk := chunks[next]
				next++
				return &chunk, nil
			}, nil), nil
		},
	}

	var deltas []string
	result, err := CollectStreamResult(t.Context(), p, &ChatRequest{Messages: []Message{UserText("hi")}},
		func(delta string) { deltas = append(deltas, delta) })
	require.NoError(t, err)
	assert.Equal(t, "答案是 42", result.Content)
	assert.Equal(t, "先想想", result.Reasoning)
	assert.Equal(t, "stop", result.FinishReason)
	assert.Equal(t, 12, result.Usage.TotalTokens)
	assert.Equal(t, []string{"答案", "是 42"}, deltas, "空 delta 的 usage chunk 不回调")
}

func TestCollectStreamResultWithoutUsage(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderOllama,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			sent := false
			return NewStreamReader(func() (*StreamChunk, error) {
				if sent {
					return nil, io.EOF
				}
				sent = true
				return &StreamChunk{Delta: "ok", FinishReason: "stop"}, nil
			}, nil), nil
		},
	}

	result, err := CollectStreamResult(t.Context(), p, &ChatRequest{Messages: []Message{UserText("hi")}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Content)
	assert.Equal(t, Usage{}, result.Usage, "无 usage 帧返回零值不报错")
}

func TestCollectStreamResultValidatesInputs(t *testing.T) {
	t.Parallel()

	_, err := CollectStreamResult(t.Context(), nil, &ChatRequest{}, nil)
	require.ErrorIs(t, err, ErrNilProvider)

	p := &stubProvider{name: ProviderOpenAI}
	_, err = CollectStreamResult(t.Context(), p, nil, nil)
	require.ErrorIs(t, err, ErrNilChatRequest)
}
