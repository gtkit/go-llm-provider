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

func TestAnthropicSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("set ANTHROPIC_API_KEY to run Anthropic smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey: apiKey,
		Model:  firstString(os.Getenv("ANTHROPIC_MODEL"), defaultAnthropicModel),
	})
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
	assert.Equal(t, ProviderAnthropic, resp.Metadata.Provider)
}

func TestGeminiSmoke(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("set GEMINI_API_KEY to run Gemini smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey: apiKey,
		Model:  firstString(os.Getenv("GEMINI_MODEL"), defaultGeminiModel),
	})
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
	assert.Equal(t, ProviderGemini, resp.Metadata.Provider)
}

func TestGeminiEmbeddingSmoke(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("set GEMINI_API_KEY to run Gemini embedding smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	e, err := NewGeminiEmbedder(NativeProviderConfig{
		APIKey: apiKey,
		Model:  firstString(os.Getenv("GEMINI_EMBEDDING_MODEL"), defaultGeminiEmbeddingModel),
	})
	require.NoError(t, err)

	resp, err := e.Embed(ctx, &EmbeddingRequest{Input: []string{"hello"}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Data, 1)
	assert.NotEmpty(t, resp.Data[0].Vector)
	assert.Equal(t, ProviderGemini, resp.Metadata.Provider)
}

func TestOllamaSmoke(t *testing.T) {
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		t.Skip("set OLLAMA_MODEL to run Ollama smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewOllamaProvider(OllamaProviderConfig{
		BaseURL: os.Getenv("OLLAMA_BASE_URL"),
		Model:   model,
	})
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
	assert.Equal(t, ProviderOllama, resp.Metadata.Provider)
}

// TestAnthropicThinkingSmoke 用真实平台校验推理控制的两端：
// thinking 参数的格式被平台接受（参数名或结构错误会直接 400），
// 且响应中的思考内容确实归位到 Reasoning、没有混进正文。
// 离线测试只能验证到本库自己的编解码，平台侧的字段语义必须靠这条用例确认。
func TestAnthropicThinkingSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("set ANTHROPIC_API_KEY to run Anthropic thinking smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey: apiKey,
		Model:  firstString(os.Getenv("ANTHROPIC_MODEL"), defaultAnthropicModel),
	})
	require.NoError(t, err)

	budget := 2048
	resp, err := p.Chat(ctx, &ChatRequest{
		Messages:  []Message{UserText("13 和 17 的乘积是多少？只回答数字。")},
		MaxTokens: 4096,
		Thinking:  &Thinking{BudgetTokens: &budget},
	})
	require.NoError(t, err, "thinking 参数被平台拒绝说明请求侧映射有误")
	require.NotNil(t, resp)

	assert.NotEmpty(t, resp.Content, "正文不应为空")
	assert.NotEmpty(t, resp.Reasoning, "开启思考后应能取到思考内容；为空说明响应侧未正确归位")
	assert.NotContains(t, resp.Content, resp.Reasoning, "思考内容不得混进正文")
	// 思考 token 计入输出，按输出价计费。
	assert.Positive(t, resp.Usage.CompletionTokens)

	t.Logf("content=%q reasoning 长度=%d usage=%+v", resp.Content, len(resp.Reasoning), resp.Usage)
}

// TestAnthropicCacheWriteTierSmoke 用真实平台校验缓存写入的 TTL 分档明细：
// 字段名或层级写错时 CacheWrite5m/1hTokens 会静默为 0，而这直接影响计费金额。
// 缓存的最小可缓存长度随模型变化，因此只在平台确实上报了写入量时才断言分档关系。
func TestAnthropicCacheWriteTierSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("set ANTHROPIC_API_KEY to run Anthropic cache tier smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey: apiKey,
		Model:  firstString(os.Getenv("ANTHROPIC_MODEL"), defaultAnthropicModel),
	})
	require.NoError(t, err)

	// 提示词缓存有最小长度门槛，这里堆出足够长的可缓存前缀。
	filler := strings.Repeat("这是一段用于触发提示词缓存的填充文本。", 400)
	resp, err := p.Chat(ctx, &ChatRequest{
		Messages: []Message{
			SystemMessage(WithCacheControl(TextPart(filler), CacheControlEphemeral())),
			UserText("回复 ok"),
		},
		MaxTokens: 16,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	usage := resp.Usage
	t.Logf("usage=%+v", usage)

	// 无论是否命中缓存，归一化后的包含关系必须成立，否则计价会直接失败。
	require.NoError(t, validateUsage(usage), "归一化后的用量必须自洽")

	if usage.CacheWriteTokens == 0 {
		t.Skip("本次调用未产生缓存写入（可能已命中缓存或未达最小可缓存长度），分档断言跳过")
	}
	assert.LessOrEqual(t, usage.CacheWrite5mTokens+usage.CacheWrite1hTokens, usage.CacheWriteTokens,
		"分档之和必须是写入总量的子集")
	if usage.CacheWrite5mTokens+usage.CacheWrite1hTokens == 0 {
		t.Log("平台未上报 TTL 分档明细，写入总量将整体按 CacheWritePer1M 计价")
	}
}
