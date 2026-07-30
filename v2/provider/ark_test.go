package provider

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestArkThinkingContextOnlyAppliesToArkWithEnabled(t *testing.T) {
	t.Parallel()

	enabled := true
	ctx := t.Context()

	assert.Equal(t, ctx, arkThinkingContext(ctx, ProviderOpenAI, &Thinking{Enabled: &enabled}))
	assert.Equal(t, ctx, arkThinkingContext(ctx, ProviderArk, nil))
	assert.Equal(t, ctx, arkThinkingContext(ctx, ProviderArk, &Thinking{Effort: ThinkingEffortLow}))
	assert.NotEqual(t, ctx, arkThinkingContext(ctx, ProviderArk, &Thinking{Enabled: &enabled}))
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
// arkThinkingDoer
// ============================================================

func TestArkThinkingDoerRewritesBodyAndGetBody(t *testing.T) {
	t.Parallel()

	rec := &recordingHTTPClient{}
	doer := &arkThinkingDoer{next: rec}

	enabled := true
	ctx := arkThinkingContext(t.Context(), ProviderArk, &Thinking{Enabled: &enabled})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://ark.test/api/v3/chat/completions", strings.NewReader(`{"model":"m"}`))
	require.NoError(t, err)

	resp, err := doer.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Len(t, rec.requests, 1)
	sent := rec.requests[0]

	body, err := io.ReadAll(sent.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"m","thinking":{"type":"enabled"}}`, string(body))
	assert.Equal(t, int64(len(body)), sent.ContentLength)

	// GetBody 必须可重放同一注入后的请求体（重试场景）
	require.NotNil(t, sent.GetBody)
	replay, err := sent.GetBody()
	require.NoError(t, err)
	replayBody, err := io.ReadAll(replay)
	require.NoError(t, err)
	assert.Equal(t, body, replayBody)
}

func TestArkThinkingDoerPassthroughWithoutContextValue(t *testing.T) {
	t.Parallel()

	rec := &recordingHTTPClient{}
	doer := &arkThinkingDoer{next: rec}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"https://ark.test/api/v3/chat/completions", strings.NewReader(`{"model":"m"}`))
	require.NoError(t, err)

	resp, err := doer.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Len(t, rec.requests, 1)
	body, err := io.ReadAll(rec.requests[0].Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"m"}`, string(body))
}

func TestInjectArkThinkingInvalidBodyReturnsError(t *testing.T) {
	t.Parallel()

	_, err := injectArkThinking([]byte("not-json"), arkThinkingTypeEnabled)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode request body")
}
