package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekSmoke(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run DeepSeek smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewProviderFromPreset(ProviderDeepSeek, apiKey, "deepseek-chat")
	require.NoError(t, err)

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

// TestDeepSeekCacheUsageSmoke 是 S-03 依赖预检：确认 DeepSeek 是否通过标准
// prompt_tokens_details.cached_tokens 回传缓存命中量（go-openai 只解析标准字段，
// DeepSeek 专有的 prompt_cache_hit_tokens 拿不到）。
// 同一长 prompt 连续调用两次，第二次预期命中 DeepSeek 自动缓存（命中粒度 64 token）。
func TestDeepSeekCacheUsageSmoke(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run DeepSeek cache usage smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	p, err := NewProviderFromPreset(ProviderDeepSeek, apiKey, "deepseek-chat")
	require.NoError(t, err)

	// prompt 需超过缓存最小粒度（64 token），用重复长文本保证。
	longSystem := strings.Repeat("你是一个严谨的办公助手，回答保持简短、准确、专业。", 30)
	req := &ChatRequest{
		Messages: []Message{
			SystemText(longSystem),
			UserText("回复 ok"),
		},
		MaxTokens: 16,
	}

	first, err := p.Chat(ctx, req)
	require.NoError(t, err)
	require.NotZero(t, first.Usage.TotalTokens)

	second, err := p.Chat(ctx, req)
	require.NoError(t, err)
	require.NotZero(t, second.Usage.TotalTokens)

	// 预检结论：CacheReadTokens > 0 说明 DeepSeek 回传了标准 cached_tokens 字段，
	// 现有映射即可覆盖，无需自建响应体二次解析。
	t.Logf("S-03 预检结论：第二次调用 PromptTokens=%d CacheReadTokens=%d（>0 表示标准字段可用）",
		second.Usage.PromptTokens, second.Usage.CacheReadTokens)
	if second.Usage.CacheReadTokens == 0 {
		t.Log("警告：未观测到标准 cached_tokens——可能 DeepSeek 仅回专有字段（需决策二次解析），也可能本次未命中缓存，请重跑确认")
	}
}

func TestDeepSeekStructuredSmoke(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run DeepSeek structured smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewProviderFromPreset(ProviderDeepSeek, apiKey, "deepseek-chat")
	require.NoError(t, err)

	type smokeResult struct {
		Status string `json:"status"`
	}

	out, resp, err := GenerateJSONWithValidator[smokeResult](ctx, p, &ChatRequest{
		Messages: []Message{
			SystemText("只输出 JSON 对象，不要输出 Markdown。"),
			UserText(`返回 {"status":"ok"}`),
		},
		MaxTokens: 32,
	}, func(v smokeResult) error {
		if v.Status != "ok" {
			return ErrStructuredValidation
		}
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ok", out.Status)
	assert.NotZero(t, resp.Usage.TotalTokens)
}
