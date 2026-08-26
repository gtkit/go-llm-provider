package provider

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const arkChatResponseFixture = `{"id":"chatcmpl-ark","object":"chat.completion","created":1,` +
	`"model":"doubao-seed-2-0-pro-260215","choices":[{"index":0,"message":{"role":"assistant",` +
	`"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

func newArkTestProvider(t *testing.T, handler http.HandlerFunc) Provider {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	p, err := NewProvider(ProviderConfig{
		Name:    ProviderArk,
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "doubao-seed-2-0-pro-260215",
	})
	require.NoError(t, err)

	return p
}

// ============================================================
// 预设
// ============================================================

func TestArkPresetRegistered(t *testing.T) {
	t.Parallel()

	preset, ok := AllPresets()[ProviderArk]
	require.True(t, ok)
	assert.Equal(t, "https://ark.cn-beijing.volces.com/api/v3", preset.BaseURL)
	assert.Equal(t, "doubao-seed-2-0-pro-260215", preset.DefaultModel)
	assert.Equal(t, "doubao-embedding-text-240515", preset.EmbeddingModel)

	caps := preset.Capabilities
	assert.Equal(t, ProviderArk, caps.Provider)
	for _, capability := range []Capability{
		CapabilityChat,
		CapabilityStreaming,
		CapabilityTools,
		CapabilityStructuredOutput,
		CapabilityReasoning,
		CapabilityVision,
		CapabilityEmbedding,
	} {
		assert.Truef(t, caps.Supports(capability), "capability=%s", capability)
	}
}

// ============================================================
// Thinking 映射
// ============================================================

func TestBuildRequestThinkingArkEffort(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{name: ProviderArk, model: "doubao-seed-2-0-pro-260215"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Effort: ThinkingEffortHigh},
	})
	require.NoError(t, err)
	assert.Equal(t, "high", req.ReasoningEffort)
}

func TestArkExtraFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *ChatRequest
		want map[string]any
	}{
		{name: "nil request", req: nil},
		{name: "no thinking", req: &ChatRequest{}},
		// Effort 走 go-openai 的类型化 reasoning_effort 字段，不需要顶层注入
		{name: "effort only", req: &ChatRequest{Thinking: &Thinking{Effort: ThinkingEffortLow}}},
		{
			name: "enabled",
			req:  &ChatRequest{Thinking: &Thinking{Enabled: boolPtr(true)}},
			want: map[string]any{"thinking": map[string]any{"type": "enabled"}},
		},
		{
			name: "disabled",
			req:  &ChatRequest{Thinking: &Thinking{Enabled: boolPtr(false)}},
			want: map[string]any{"thinking": map[string]any{"type": "disabled"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, arkExtraFields(tc.req))
		})
	}
}

func TestArkChatInjectsThinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		enabled  bool
		wantType string
	}{
		{name: "enabled", enabled: true, wantType: "enabled"},
		{name: "disabled", enabled: false, wantType: "disabled"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var captured map[string]any
			p := newArkTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(arkChatResponseFixture))
			})

			enabled := tc.enabled
			resp, err := p.Chat(t.Context(), &ChatRequest{
				Messages: []Message{UserText("hello")},
				Thinking: &Thinking{Enabled: &enabled},
			})
			require.NoError(t, err)
			assert.Equal(t, "ok", resp.Content)

			thinking, ok := captured["thinking"].(map[string]any)
			require.Truef(t, ok, "thinking field missing in request body: %v", captured)
			assert.Equal(t, tc.wantType, thinking["type"])

			// 注入不得影响其余字段
			assert.Equal(t, "doubao-seed-2-0-pro-260215", captured["model"])
			messages, ok := captured["messages"].([]any)
			require.True(t, ok)
			require.Len(t, messages, 1)
		})
	}
}

func TestArkChatWithoutEnabledOmitsThinkingField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		thinking *Thinking
	}{
		{name: "no thinking", thinking: nil},
		{name: "effort only", thinking: &Thinking{Effort: ThinkingEffortLow}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var captured map[string]any
			p := newArkTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(arkChatResponseFixture))
			})

			_, err := p.Chat(t.Context(), &ChatRequest{
				Messages: []Message{UserText("hello")},
				Thinking: tc.thinking,
			})
			require.NoError(t, err)

			_, ok := captured["thinking"]
			assert.False(t, ok, "thinking field should be omitted: %v", captured)
		})
	}
}

// TestArkChatEnabledAndEffortAreIndependent 锁定 Enabled 与 Effort 的组合语义：
// 两者分别映射到 thinking 与 reasoning_effort，本库原样下发、不做取舍，
// 由方舟侧裁决（README「Thinking / Reasoning」一节已声明该契约）。
func TestArkChatEnabledAndEffortAreIndependent(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	p := newArkTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(arkChatResponseFixture))
	})

	enabled := false
	_, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: &enabled, Effort: ThinkingEffortHigh},
	})
	require.NoError(t, err)

	thinking, ok := captured["thinking"].(map[string]any)
	require.Truef(t, ok, "thinking field missing in request body: %v", captured)
	assert.Equal(t, "disabled", thinking["type"])
	assert.Equal(t, "high", captured["reasoning_effort"])
}

func TestArkChatStreamInjectsThinking(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	p := newArkTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,"+
			"\"model\":\"doubao-seed-2-0-pro-260215\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},"+
			"\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})

	enabled := true
	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: &enabled},
	})
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()

	first, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "hi", first.Delta)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)

	thinking, ok := captured["thinking"].(map[string]any)
	require.Truef(t, ok, "thinking field missing in stream request body: %v", captured)
	assert.Equal(t, "enabled", thinking["type"])
}

// ============================================================
// Embedding / Registry 接入
// ============================================================

func TestArkEmbedderFromPreset(t *testing.T) {
	t.Parallel()

	t.Run("default embedding model", func(t *testing.T) {
		t.Parallel()

		e, err := NewEmbedderFromPreset(ProviderArk, "test-key", "")
		require.NoError(t, err)
		require.NotNil(t, e)
		assert.Equal(t, ProviderArk, e.Name())
	})

	t.Run("custom endpoint id", func(t *testing.T) {
		t.Parallel()

		e, err := NewEmbedderFromPreset(ProviderArk, "test-key", "ep-20260101-abcde")
		require.NoError(t, err)
		require.NotNil(t, e)
		assert.Equal(t, ProviderArk, e.Name())
	})
}

func TestArkQuickRegistry(t *testing.T) {
	t.Parallel()

	reg := QuickRegistry(map[ProviderName]string{ProviderArk: "ark-test"})

	assert.Equal(t, []ProviderName{ProviderArk}, reg.Names())
	assert.Equal(t, []ProviderName{ProviderArk}, reg.EmbedderNames())
}

// ============================================================
// Reasoning 回传解析
// ============================================================

func TestArkChatParsesReasoningContent(t *testing.T) {
	t.Parallel()

	p := newArkTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ark","object":"chat.completion","created":1,` +
			`"model":"doubao-seed-2-0-pro-260215","choices":[{"index":0,"message":{"role":"assistant",` +
			`"content":"ok","reasoning_content":"先拆解问题"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":1,"completion_tokens":9,"total_tokens":10,` +
			`"completion_tokens_details":{"reasoning_tokens":7}}}`))
	})

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: boolPtr(true)},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.Equal(t, "先拆解问题", resp.Reasoning)
	assert.Equal(t, 7, resp.Usage.ReasoningTokens)
}

func TestArkChatStreamParsesReasoningContent(t *testing.T) {
	t.Parallel()

	p := newArkTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,"+
			"\"model\":\"doubao-seed-2-0-pro-260215\",\"choices\":[{\"index\":0,"+
			"\"delta\":{\"reasoning_content\":\"先拆解\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,"+
			"\"model\":\"doubao-seed-2-0-pro-260215\",\"choices\":[{\"index\":0,"+
			"\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: boolPtr(true)},
	})
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()

	first, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "先拆解", first.ReasoningDelta)
	assert.Empty(t, first.Delta)

	second, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "ok", second.Delta)
	assert.Empty(t, second.ReasoningDelta)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}
