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
	assert.Equal(t, "be concise", captured["system"])
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

func TestEstimateTokens(t *testing.T) {
	t.Parallel()

	assert.Zero(t, EstimateTokens(nil))

	// 纯英文：4 字符 ≈ 1 token + 每条消息 4 开销。
	en := EstimateTokens([]Message{UserText("hello world!")})
	assert.Equal(t, 4+3, en)

	// 中文按字计。
	zh := EstimateTokens([]Message{UserText("你好世界")})
	assert.Equal(t, 4+4, zh)

	// 附件按固定预算计。
	img := EstimateTokens([]Message{UserMessage(ImageURLPart("https://example.com/a.png"))})
	assert.Equal(t, 4+estimateTokensPerAttachment, img)

	// tool call 计入函数名与参数。
	withTool := EstimateTokens([]Message{{
		Role:      RoleAssistant,
		Content:   []ContentPart{TextPart("")},
		ToolCalls: []ToolCall{{ID: "c", Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`}}},
	}})
	assert.Greater(t, withTool, 12)
}

func TestTrimMessagesToTokenBudget(t *testing.T) {
	t.Parallel()

	system := SystemText("你是助手")
	old1 := UserText("第一轮很老的问题")
	old2 := Message{Role: RoleAssistant, Content: []ContentPart{TextPart("第一轮很老的回答")}}
	// tool 组：assistant(tool_calls) + tool 结果，不可拆分。
	toolCall := Message{Role: RoleAssistant, Content: []ContentPart{TextPart("")},
		ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}}}
	toolResult := ToolResultMessage("c1", "result")
	latest := UserText("最新问题")
	msgs := []Message{system, old1, old2, toolCall, toolResult, latest}

	t.Run("预算充足全保留", func(t *testing.T) {
		t.Parallel()
		out := TrimMessagesToTokenBudget(msgs, 100_000)
		assert.Equal(t, msgs, out)
	})

	t.Run("预算紧张保 system 与最新组", func(t *testing.T) {
		t.Parallel()
		out := TrimMessagesToTokenBudget(msgs, 1)
		require.Len(t, out, 2)
		assert.Equal(t, RoleSystem, out[0].Role)
		assert.Equal(t, "最新问题", contentText(out[1].Content))
	})

	t.Run("tool 组整体保留或整体丢弃", func(t *testing.T) {
		t.Parallel()
		// 预算容得下 latest + tool 组，容不下更早的。
		budget := EstimateTokens([]Message{system, toolCall, toolResult, latest}) + 1
		out := TrimMessagesToTokenBudget(msgs, budget)
		roles := make([]Role, 0, len(out))
		for _, m := range out {
			roles = append(roles, m.Role)
		}
		assert.Equal(t, []Role{RoleSystem, RoleAssistant, RoleTool, RoleUser}, roles)
	})

	t.Run("空输入返回 nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, TrimMessagesToTokenBudget(nil, 100))
	})
}
