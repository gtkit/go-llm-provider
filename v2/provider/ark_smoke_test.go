package provider

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 方舟真实链路烟测：验证注入后的请求体确实被平台接受，而不只是"看起来正确"。
// 需要 ARK_API_KEY；可选 ARK_MODEL（模型 ID 或 ep- 开头的推理接入点 ID）、
// ARK_EMBEDDING_MODEL 覆盖默认模型。未设置 ARK_API_KEY 时整组跳过。

// newArkSmokeProvider 构造烟测用 Provider，缺 API Key 时跳过当前测试。
func newArkSmokeProvider(t *testing.T) Provider {
	t.Helper()

	apiKey := os.Getenv("ARK_API_KEY")
	if apiKey == "" {
		t.Skip("set ARK_API_KEY to run Ark smoke test")
	}

	p, err := NewProviderFromPreset(ProviderArk, apiKey, os.Getenv("ARK_MODEL"))
	require.NoError(t, err)

	return p
}

func TestArkChatSmoke(t *testing.T) {
	p := newArkSmokeProvider(t)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	resp, err := p.Chat(ctx, &ChatRequest{
		Messages: []Message{
			SystemText("只输出简短答案。"),
			UserText("回复 ok"),
		},
		MaxTokens: 16,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Content)
	assert.NotZero(t, resp.Usage.TotalTokens)
}

// TestArkThinkingEnabledSmoke 验证注入的顶层 thinking 字段被方舟接受：
// enabled 与 disabled 都必须不报错，enabled 额外记录是否回传思考过程。
func TestArkThinkingEnabledSmoke(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "enabled", enabled: true},
		{name: "disabled", enabled: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newArkSmokeProvider(t)

			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()

			enabled := tc.enabled
			resp, err := p.Chat(ctx, &ChatRequest{
				Messages:  []Message{UserText("1+1 等于几？只回答数字。")},
				Thinking:  &Thinking{Enabled: &enabled},
				MaxTokens: 512,
			})
			require.NoError(t, err, "方舟拒绝了注入 thinking 字段的请求体")
			require.NotNil(t, resp)
			assert.NotEmpty(t, resp.Content)

			t.Logf("thinking=%v content=%q reasoning_len=%d reasoning_tokens=%d",
				tc.enabled, resp.Content, len(resp.Reasoning), resp.Usage.ReasoningTokens)
			if !tc.enabled && resp.Usage.ReasoningTokens > 0 {
				t.Log("警告：disabled 仍产生 reasoning tokens——该模型可能不支持关闭深度思考")
			}
		})
	}
}

// TestArkThinkingEffortSmoke 验证 reasoning_effort 被方舟 Chat Completions 接受。
// 该字段与 thinking 相互独立，本库原样下发。
func TestArkThinkingEffortSmoke(t *testing.T) {
	p := newArkSmokeProvider(t)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	resp, err := p.Chat(ctx, &ChatRequest{
		Messages:  []Message{UserText("1+1 等于几？只回答数字。")},
		Thinking:  &Thinking{Effort: ThinkingEffortLow},
		MaxTokens: 512,
	})
	require.NoError(t, err, "方舟拒绝了带 reasoning_effort 的请求")
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Content)
	t.Logf("effort=low content=%q reasoning_tokens=%d", resp.Content, resp.Usage.ReasoningTokens)
}

// TestArkStreamReasoningSmoke 验证流式链路：注入后的请求体被接受，
// 且 reasoning_content 增量与收尾 usage 能正常回传。
func TestArkStreamReasoningSmoke(t *testing.T) {
	p := newArkSmokeProvider(t)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	enabled := true
	stream, err := p.ChatStream(ctx, &ChatRequest{
		Messages:  []Message{UserText("1+1 等于几？只回答数字。")},
		Thinking:  &Thinking{Enabled: &enabled},
		MaxTokens: 512,
	})
	require.NoError(t, err, "方舟拒绝了注入 thinking 字段的流式请求体")

	var content, reasoning strings.Builder
	var usage Usage
	for {
		chunk, recvErr := stream.Recv()
		if recvErr != nil {
			require.ErrorIs(t, recvErr, io.EOF)

			break
		}
		content.WriteString(chunk.Delta)
		reasoning.WriteString(chunk.ReasoningDelta)
		if chunk.Usage != (Usage{}) {
			usage = chunk.Usage
		}
	}
	require.NoError(t, stream.Close())

	assert.NotEmpty(t, content.String())
	require.NotZero(t, usage.TotalTokens, "未收到流式 usage——检查平台是否支持 stream_options.include_usage")
	t.Logf("流式实测：content=%q reasoning_len=%d usage=%+v",
		content.String(), reasoning.Len(), usage)
}

func TestArkEmbeddingSmoke(t *testing.T) {
	apiKey := os.Getenv("ARK_API_KEY")
	if apiKey == "" {
		t.Skip("set ARK_API_KEY to run Ark embedding smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	e, err := NewEmbedderFromPreset(ProviderArk, apiKey, os.Getenv("ARK_EMBEDDING_MODEL"))
	require.NoError(t, err)

	resp, err := e.Embed(ctx, &EmbeddingRequest{Input: []string{"方舟 embedding 烟测"}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Data, 1)
	assert.NotEmpty(t, resp.Data[0].Vector)
	t.Logf("embedding 维度=%d usage=%+v", len(resp.Data[0].Vector), resp.Usage)
}
