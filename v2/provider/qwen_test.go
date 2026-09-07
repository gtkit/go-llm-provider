package provider

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const qwenChatResponseFixture = `{"id":"chatcmpl-qwen","object":"chat.completion","created":1,` +
	`"model":"qwen3.6-plus","choices":[{"index":0,"message":{"role":"assistant",` +
	`"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

func intPtr(value int) *int {
	return &value
}

func newQwenTestProvider(t *testing.T, handler http.HandlerFunc) Provider {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	p, err := NewProvider(ProviderConfig{
		Name:    ProviderQwen,
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "qwen3.6-plus",
	})
	require.NoError(t, err)

	return p
}

// ============================================================
// 预设
// ============================================================

func TestQwenPresetDeclaresReasoning(t *testing.T) {
	t.Parallel()

	preset, ok := AllPresets()[ProviderQwen]
	require.True(t, ok)

	caps := preset.Capabilities
	assert.Equal(t, ProviderQwen, caps.Provider)
	assert.True(t, caps.Supports(CapabilityReasoning))
}

// ============================================================
// Thinking 映射
// ============================================================

func TestQwenExtraFields(t *testing.T) {
	t.Parallel()

	enabled, disabled := true, false
	budget, zero, negative, huge := 1024, 0, -1, 1<<40

	tests := []struct {
		name string
		req  *ChatRequest
		want map[string]any
	}{
		{name: "nil request", req: nil},
		{name: "no thinking", req: &ChatRequest{}},
		{name: "empty thinking", req: &ChatRequest{Thinking: &Thinking{}}},
		// Effort 在百炼没有映射，请求构建阶段就会被拒绝，注入通道不必处理
		{name: "effort only", req: &ChatRequest{Thinking: &Thinking{Effort: ThinkingEffortLow}}},
		{
			name: "enabled",
			req:  &ChatRequest{Thinking: &Thinking{Enabled: &enabled}},
			want: map[string]any{"enable_thinking": true},
		},
		{
			name: "disabled",
			req:  &ChatRequest{Thinking: &Thinking{Enabled: &disabled}},
			want: map[string]any{"enable_thinking": false},
		},
		{
			name: "budget only",
			req:  &ChatRequest{Thinking: &Thinking{BudgetTokens: &budget}},
			want: map[string]any{"thinking_budget": 1024},
		},
		{
			name: "enabled and budget",
			req:  &ChatRequest{Thinking: &Thinking{Enabled: &enabled, BudgetTokens: &budget}},
			want: map[string]any{"enable_thinking": true, "thinking_budget": 1024},
		},
		// 取值语义由平台裁决，本库原样透传，不代为设边界
		{
			name: "zero budget",
			req:  &ChatRequest{Thinking: &Thinking{BudgetTokens: &zero}},
			want: map[string]any{"thinking_budget": 0},
		},
		{
			name: "negative budget",
			req:  &ChatRequest{Thinking: &Thinking{BudgetTokens: &negative}},
			want: map[string]any{"thinking_budget": -1},
		},
		{
			name: "huge budget",
			req:  &ChatRequest{Thinking: &Thinking{BudgetTokens: &huge}},
			want: map[string]any{"thinking_budget": 1 << 40},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, qwenExtraFields(tc.req))
		})
	}
}

func TestQwenChatInjectsThinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		thinking *Thinking
		want     map[string]any
	}{
		{
			name:     "enabled",
			thinking: &Thinking{Enabled: boolPtr(true)},
			want:     map[string]any{"enable_thinking": true},
		},
		{
			name:     "disabled",
			thinking: &Thinking{Enabled: boolPtr(false)},
			want:     map[string]any{"enable_thinking": false},
		},
		{
			name:     "budget only",
			thinking: &Thinking{BudgetTokens: intPtr(1024)},
			want:     map[string]any{"thinking_budget": float64(1024)},
		},
		{
			name:     "enabled and budget",
			thinking: &Thinking{Enabled: boolPtr(true), BudgetTokens: intPtr(512)},
			want:     map[string]any{"enable_thinking": true, "thinking_budget": float64(512)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var captured map[string]any
			p := newQwenTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(qwenChatResponseFixture))
			})

			resp, err := p.Chat(t.Context(), &ChatRequest{
				Messages: []Message{UserText("hello")},
				Thinking: tc.thinking,
			})
			require.NoError(t, err)
			assert.Equal(t, "ok", resp.Content)

			for field, want := range tc.want {
				assert.Equalf(t, want, captured[field], "field=%s body=%v", field, captured)
			}
			// 未设置的那个字段不得出现
			for _, field := range []string{"enable_thinking", "thinking_budget"} {
				if _, ok := tc.want[field]; ok {
					continue
				}
				_, ok := captured[field]
				assert.Falsef(t, ok, "field %s should be omitted: %v", field, captured)
			}

			// 注入不得影响其余字段
			assert.Equal(t, "qwen3.6-plus", captured["model"])
			messages, ok := captured["messages"].([]any)
			require.True(t, ok)
			require.Len(t, messages, 1)
		})
	}
}

func TestQwenChatWithoutThinkingOmitsFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		thinking *Thinking
	}{
		{name: "no thinking", thinking: nil},
		{name: "empty thinking", thinking: &Thinking{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var captured map[string]any
			p := newQwenTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(qwenChatResponseFixture))
			})

			_, err := p.Chat(t.Context(), &ChatRequest{
				Messages: []Message{UserText("hello")},
				Thinking: tc.thinking,
			})
			require.NoError(t, err)

			for _, field := range []string{"enable_thinking", "thinking_budget"} {
				_, ok := captured[field]
				assert.Falsef(t, ok, "field %s should be omitted: %v", field, captured)
			}
		})
	}
}

// TestQwenChatPreservesRequestBodyBytes 锁定注入的字节保真契约：
// 注入只在顶层对象末尾追加字段，其余字节（字段顺序、转义形式）与未注入时完全一致。
// 若改成"解析后重新编码"，同一请求的其余字节会重排，本用例即失败。
func TestQwenChatPreservesRequestBodyBytes(t *testing.T) {
	t.Parallel()

	capture := func(thinking *Thinking) string {
		var body string
		p := newQwenTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			raw, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			body = string(raw)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(qwenChatResponseFixture))
		})

		_, err := p.Chat(t.Context(), &ChatRequest{
			Messages: []Message{UserText(`带"引号"与\反斜杠`)},
			Thinking: thinking,
		})
		require.NoError(t, err)

		return body
	}

	plain := capture(nil)
	injected := capture(&Thinking{Enabled: boolPtr(true), BudgetTokens: intPtr(1024)})

	require.True(t, strings.HasSuffix(plain, "}"))
	want := strings.TrimSuffix(plain, "}") + `,"enable_thinking":true,"thinking_budget":1024}`
	assert.Equal(t, want, injected)
}

// TestQwenChatRejectsEffort 锁定 fail-closed 契约：百炼没有 Effort 的映射，
// 请求在构建阶段就被拒绝，错误信息指向该平台已映射的两个字段，且不发出任何 HTTP 请求。
func TestQwenChatRejectsEffort(t *testing.T) {
	t.Parallel()

	var called atomic.Bool
	p := newQwenTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(qwenChatResponseFixture))
	})

	_, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Effort: ThinkingEffortHigh},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
	assert.Contains(t, err.Error(), thinkingFieldEnabled)
	assert.Contains(t, err.Error(), thinkingFieldBudget)
	assert.False(t, called.Load(), "请求不应发出")
}

// TestQwenIgnoresSupportsReasoningEffort 锁定"内置预设优先"契约：
// 百炼的推理字段支持范围由库判定，调用方的 SupportsReasoningEffort 声明对它不生效。
// 若该声明被误当作解锁开关，本用例会拿到 nil 错误而失败。
func TestQwenIgnoresSupportsReasoningEffort(t *testing.T) {
	t.Parallel()

	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(qwenChatResponseFixture))
	}))
	t.Cleanup(srv.Close)

	p, err := NewProvider(ProviderConfig{
		Name:                    ProviderQwen,
		BaseURL:                 srv.URL,
		APIKey:                  "test-key",
		Model:                   "qwen3.6-plus",
		SupportsReasoningEffort: true,
	})
	require.NoError(t, err)

	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Effort: ThinkingEffortHigh},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
	assert.False(t, called.Load(), "请求不应发出")
}

func TestQwenChatStreamInjectsThinking(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	p := newQwenTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,"+
			"\"model\":\"qwen3.6-plus\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},"+
			"\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: boolPtr(true), BudgetTokens: intPtr(1024)},
	})
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()

	first, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "hi", first.Delta)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)

	assert.Equal(t, true, captured["enable_thinking"])
	assert.Equal(t, float64(1024), captured["thinking_budget"])
}

// ============================================================
// Reasoning 回传解析
// ============================================================

func TestQwenChatParsesReasoningContent(t *testing.T) {
	t.Parallel()

	p := newQwenTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-qwen","object":"chat.completion","created":1,` +
			`"model":"qwen3.6-plus","choices":[{"index":0,"message":{"role":"assistant",` +
			`"content":"ok","reasoning_content":"先拆解问题"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":1,"completion_tokens":9,"total_tokens":10}}`))
	})

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: boolPtr(true)},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.Equal(t, "先拆解问题", resp.Reasoning)
}
