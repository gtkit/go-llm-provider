package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func compactFixtureMessages() []Message {
	return []Message{
		SystemText("你是办公助手"),
		UserText("帮我写周报"),
		{Role: RoleAssistant, Content: []ContentPart{TextPart("好的，请给我本周工作内容")}},
		UserText("本周完成了计费模块"),
		{Role: RoleAssistant, Content: []ContentPart{TextPart("已记录：完成计费模块")}},
		UserText("再加上多模态调研"),
		{Role: RoleAssistant, Content: []ContentPart{TextPart("已记录：多模态调研")}},
		UserText("生成最终版本"),
	}
}

func TestCompactMessagesSummarizesOldHistory(t *testing.T) {
	t.Parallel()

	var summaryReq *ChatRequest
	p := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			summaryReq = req
			return &ChatResponse{
				Content: "用户在写周报，已确认内容：计费模块、多模态调研。",
				Usage:   Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
			}, nil
		},
	}

	msgs := compactFixtureMessages()
	out, resp, err := CompactMessages(t.Context(), p, msgs, CompactOptions{
		Model:            "deepseek-chat",
		KeepRecentGroups: 3,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 120, resp.Usage.TotalTokens, "摘要调用自身的 usage 可供计费")

	// 结构：system + 摘要 + 最近 3 组。
	require.Len(t, out, 1+1+3)
	assert.Equal(t, RoleSystem, out[0].Role)
	assert.Equal(t, RoleUser, out[1].Role)
	assert.True(t, strings.HasPrefix(contentText(out[1].Content), "【此前对话摘要】"))
	assert.Contains(t, contentText(out[1].Content), "计费模块")
	assert.Equal(t, "生成最终版本", contentText(out[len(out)-1].Content))

	// 摘要请求：指定模型、带默认指令、transcript 含旧消息且不含最近组。
	require.NotNil(t, summaryReq)
	assert.Equal(t, "deepseek-chat", summaryReq.Model)
	assert.Equal(t, defaultCompactMaxSummaryTokens, summaryReq.MaxTokens)
	transcript := contentText(summaryReq.Messages[1].Content)
	assert.Contains(t, transcript, "帮我写周报")
	assert.NotContains(t, transcript, "生成最终版本")

	// 入参不被修改。
	assert.Equal(t, compactFixtureMessages(), msgs)
}

func TestCompactMessagesSkipsWhenNotNeeded(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			t.Fatal("未触发压缩时不应发起摘要调用")
			return nil, nil
		},
	}
	msgs := compactFixtureMessages()

	t.Run("低于触发阈值", func(t *testing.T) {
		t.Parallel()
		out, resp, err := CompactMessages(t.Context(), p, msgs, CompactOptions{TriggerTokens: 1_000_000})
		require.NoError(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, msgs, out)
	})

	t.Run("组数不足无旧历史", func(t *testing.T) {
		t.Parallel()
		out, resp, err := CompactMessages(t.Context(), p, msgs, CompactOptions{KeepRecentGroups: 100})
		require.NoError(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, msgs, out)
	})

	t.Run("空输入", func(t *testing.T) {
		t.Parallel()
		out, resp, err := CompactMessages(t.Context(), p, nil, CompactOptions{})
		require.NoError(t, err)
		assert.Nil(t, resp)
		assert.Empty(t, out)
	})
}

func TestCompactMessagesPropagatesSummaryError(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, ErrRateLimit
		},
	}
	_, _, err := CompactMessages(t.Context(), p, compactFixtureMessages(), CompactOptions{KeepRecentGroups: 1})
	require.ErrorIs(t, err, ErrRateLimit)
}
