package provider

import (
	"errors"
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
	// 断言字节精确相等而非 JSONEq：注入必须是原字节末尾追加，不得重排或重编码，
	// JSONEq 会把字段重排、空白压缩这类回归一并放过。
	//nolint:testifylint // encoded-compare: 此处要的正是字节级相等，不能退化成 JSONEq
	assert.Equal(t, `{"model":"m","thinking":{"type":"enabled"}}`, string(body))
	assert.Equal(t, int64(len(body)), sent.ContentLength)

	// GetBody 必须可重放同一注入后的请求体（重试场景）
	require.NotNil(t, sent.GetBody)
	replay, err := sent.GetBody()
	require.NoError(t, err)
	replayBody, err := io.ReadAll(replay)
	require.NoError(t, err)
	assert.Equal(t, body, replayBody)
	assert.NoError(t, replay.Close())
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

func TestArkThinkingDoerWrapsTransportError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("dial tcp: connection refused")

	tests := []struct {
		name     string
		thinking *Thinking
	}{
		{name: "inject path", thinking: &Thinking{Enabled: boolPtr(true)}},
		{name: "passthrough path", thinking: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doer := &arkThinkingDoer{next: &errHTTPClient{err: sentinel}}
			ctx := arkThinkingContext(t.Context(), ProviderArk, tc.thinking)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				"https://ark.test/api/v3/chat/completions", strings.NewReader(`{"model":"m"}`))
			require.NoError(t, err)

			resp, doErr := doer.Do(req) //nolint:bodyclose // 传输失败路径不返回响应体
			require.ErrorIs(t, doErr, sentinel)
			require.Nil(t, resp)
			assert.Contains(t, doErr.Error(), "ark http request")
		})
	}
}

func TestArkThinkingDoerBodyFailuresReturnError(t *testing.T) {
	t.Parallel()

	readErr := errors.New("boom read")
	closeErr := errors.New("boom close")

	tests := []struct {
		name string
		body io.ReadCloser
		want error
	}{
		{name: "read fails", body: &errReadCloser{readErr: readErr}, want: readErr},
		{name: "close fails", body: &errReadCloser{closeErr: closeErr}, want: closeErr},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recordingHTTPClient{}
			doer := &arkThinkingDoer{next: rec}

			ctx := arkThinkingContext(t.Context(), ProviderArk, &Thinking{Enabled: boolPtr(true)})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				"https://ark.test/api/v3/chat/completions", http.NoBody)
			require.NoError(t, err)
			req.Body = tc.body

			resp, doErr := doer.Do(req) //nolint:bodyclose // 请求体失败路径不返回响应体
			require.ErrorIs(t, doErr, tc.want)
			require.Nil(t, resp)
			assert.Contains(t, doErr.Error(), "read request body")
			assert.Empty(t, rec.requests, "请求体不可用时不得发出请求")
		})
	}
}

func TestArkThinkingDoerRejectsNonObjectBody(t *testing.T) {
	t.Parallel()

	rec := &recordingHTTPClient{}
	doer := &arkThinkingDoer{next: rec}

	ctx := arkThinkingContext(t.Context(), ProviderArk, &Thinking{Enabled: boolPtr(true)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://ark.test/api/v3/chat/completions", strings.NewReader(`[{"model":"m"}]`))
	require.NoError(t, err)

	resp, doErr := doer.Do(req) //nolint:bodyclose // 注入失败路径不返回响应体
	require.Error(t, doErr)
	require.Nil(t, resp)
	assert.Contains(t, doErr.Error(), "decode request body")
	assert.Empty(t, rec.requests, "注入失败时不得发出请求")
}

// ============================================================
// injectArkThinking
// ============================================================

func TestInjectArkThinkingPreservesOriginalBytes(t *testing.T) {
	t.Parallel()

	// 字段顺序、大整数精度、Unicode 转义都必须原样保留：
	// 走 map 重编码会重排字段并把 1e30 之类的数值改写成其他形式。
	body := `{"model":"ep-x","messages":[{"role":"user","content":"a&b \" \\ 中文"}],` +
		`"max_tokens":1000000000000000000000000000000,"temperature":0.10}`

	out, err := injectArkThinking([]byte(body), arkThinkingTypeEnabled)
	require.NoError(t, err)

	want := body[:len(body)-1] + `,"thinking":{"type":"enabled"}}`
	assert.Equal(t, want, string(out))
	assert.True(t, json.Valid(out))
}

func TestInjectArkThinkingBodyVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "object", body: `{"model":"m"}`, want: `{"model":"m","thinking":{"type":"disabled"}}`},
		{name: "empty object", body: `{}`, want: `{"thinking":{"type":"disabled"}}`},
		{name: "object with padding", body: "  {\"model\":\"m\"}\n", want: `{"model":"m","thinking":{"type":"disabled"}}`},
		// 对象内部空白属于原字节，只裁剪外层
		{name: "whitespace only object", body: `{   }`, want: `{   "thinking":{"type":"disabled"}}`},
		// 已有 thinking 字段时追加项在后，按 JSON 重复键取后者的惯例覆盖
		{
			name: "existing thinking field",
			body: `{"thinking":{"type":"enabled"},"model":"m"}`,
			want: `{"thinking":{"type":"enabled"},"model":"m","thinking":{"type":"disabled"}}`,
		},
		{name: "json null", body: `null`, wantErr: true},
		{name: "json array", body: `[{"model":"m"}]`, wantErr: true},
		{name: "json string", body: `"body"`, wantErr: true},
		{name: "json number", body: `42`, wantErr: true},
		{name: "empty body", body: ``, wantErr: true},
		{name: "whitespace body", body: "  \n\t ", wantErr: true},
		{name: "not json", body: `not-json`, wantErr: true},
		{name: "unclosed object", body: `{"model":"m"`, wantErr: true},
		{name: "single brace", body: `{`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := injectArkThinking([]byte(tc.body), arkThinkingTypeDisabled)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "decode request body")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))
		})
	}
}

func TestInjectArkThinkingRejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	// 拼接进 JSON 字面量前必须先校验，杜绝任意字符串破坏请求体结构
	_, err := injectArkThinking([]byte(`{"model":"m"}`), `auto","x":"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported thinking type")
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

// ============================================================
// 测试替身
// ============================================================

// errHTTPClient 固定返回传输错误，用于验证 arkThinkingDoer 的错误包装。
type errHTTPClient struct {
	err error
}

func (c *errHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, c.err
}

// errReadCloser 按需在读取或关闭阶段报错，用于验证请求体失败路径。
type errReadCloser struct {
	readErr  error
	closeErr error
}

func (r *errReadCloser) Read([]byte) (int, error) {
	if r.readErr != nil {
		return 0, r.readErr
	}

	return 0, io.EOF
}

func (r *errReadCloser) Close() error {
	return r.closeErr
}
