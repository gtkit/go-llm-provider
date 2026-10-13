package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiProviderCountTokensMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash:countTokens", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalTokens": 17}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	counter, ok := p.(TokenCounter)
	require.True(t, ok)

	resp, err := counter.CountTokens(t.Context(), &ChatRequest{
		Messages: []Message{
			SystemText("be concise"),
			UserText("hello"),
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "gemini-2.5-flash", resp.Model)
	assert.Equal(t, 17, resp.TotalTokens)
	contents, ok := captured["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contents, 1)
	systemInstruction, ok := captured["systemInstruction"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, systemInstruction)
}

func TestAnthropicProviderCountTokensMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages/count_tokens", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens": 23}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	counter, ok := p.(TokenCounter)
	require.True(t, ok)

	resp, err := counter.CountTokens(t.Context(), &ChatRequest{
		Messages: []Message{
			SystemText("be concise"),
			UserText("hello"),
		},
		MaxTokens: 128,
		Tools:     []Tool{{Function: FunctionDef{Name: "get_time"}}},
	})
	require.NoError(t, err)

	assert.Equal(t, "claude-sonnet-4-5", resp.Model)
	assert.Equal(t, 23, resp.TotalTokens)

	assert.Equal(t, "claude-sonnet-4-5", captured["model"])
	system, ok := captured["system"].([]any)
	require.True(t, ok, "system 以内容块数组下发")
	require.Len(t, system, 1)
	assert.Equal(t, "be concise", system[0].(map[string]any)["text"])
	require.Len(t, captured["messages"], 1)
	require.Len(t, captured["tools"], 1)
	// count_tokens 端点不接受生成参数，请求体不得携带。
	assert.NotContains(t, captured, "max_tokens")
	assert.NotContains(t, captured, "stream")
}

func TestAnthropicProviderCountTokensRejectsEmptyMessages(t *testing.T) {
	t.Parallel()

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)

	counter, ok := p.(TokenCounter)
	require.True(t, ok)

	_, err = counter.CountTokens(t.Context(), &ChatRequest{})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestCountTokensReturnsUnsupportedForProviderWithoutCounter(t *testing.T) {
	t.Parallel()

	resp, err := CountTokens(t.Context(), tokenTestProvider{name: ProviderOpenAI}, &ChatRequest{
		Messages: []Message{UserText("hello")},
	})

	require.ErrorIs(t, err, ErrUnsupportedCapability)
	assert.Nil(t, resp)
}

type tokenTestProvider struct {
	name ProviderName
}

func (p tokenTestProvider) Name() ProviderName {
	return p.name
}

func (p tokenTestProvider) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return nil, nil
}

func (p tokenTestProvider) ChatStream(context.Context, *ChatRequest) (*StreamReader, error) {
	return nil, nil
}

func TestRateLimitEstimateTokens(t *testing.T) {
	t.Parallel()

	assert.Zero(t, estimateTokens(nil))

	// 纯英文：4 字符 ≈ 1 token + 每条消息 4 开销。
	en := estimateTokens([]Message{UserText("hello world!")})
	assert.Equal(t, 4+3, en)

	// 中文按字计。
	zh := estimateTokens([]Message{UserText("你好世界")})
	assert.Equal(t, 4+4, zh)

	// 附件按固定预算计。
	img := estimateTokens([]Message{UserMessage(ImageURLPart("https://example.com/a.png"))})
	assert.Equal(t, 4+estimateTokensPerAttachment, img)

	// tool call 计入函数名与参数。
	withTool := estimateTokens([]Message{{
		Role:      RoleAssistant,
		Content:   []ContentPart{TextPart("")},
		ToolCalls: []ToolCall{{ID: "c", Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`}}},
	}})
	assert.Greater(t, withTool, 12)
}
