package provider

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 这些用例逐条对应 README 与 GoDoc 里写下的契约：每条契约若为假，对应用例必然失败。

const compatSearchChatBody = `{"id":"1","object":"chat.completion","model":"m",` +
	`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

// newCompatSearchProvider 起一个记录请求体的打桩服务。
// requestBody 指向服务端收到的最后一个请求体，未收到请求时为空串。
func newCompatSearchProvider(
	t *testing.T, name ProviderName, respBody string, sse bool,
) (p Provider, requestBody *string) {
	t.Helper()

	captured := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// handler 在另一个 goroutine 上跑，断言失败只能记录不能中止测试；
		// 读不到请求体时留空串，由调用方的断言暴露。
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求体失败: %v", err)

			return
		}
		*captured = string(body)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)

	provider, err := NewProvider(ProviderConfig{Name: name, BaseURL: srv.URL, APIKey: "test-key", Model: "m"})
	require.NoError(t, err)

	return provider, captured
}

func decodeRequestBody(t *testing.T, raw string) map[string]any {
	t.Helper()

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &body))

	return body
}

// ============================================================
// 能力声明
// ============================================================

func TestCompatWebSearchCapabilityDeclaration(t *testing.T) {
	t.Parallel()

	for name, want := range map[ProviderName]bool{
		ProviderZhipu:    true,
		ProviderQwen:     true,
		ProviderMoonshot: false,
		ProviderArk:      false,
		ProviderDeepSeek: false,
		ProviderQianfan:  false,
	} {
		caps, ok := ModelCapabilitiesFromPreset(name)
		require.True(t, ok, name)
		assert.Equal(t, want, caps.Supports(CapabilityWebSearch), name)
	}
}

// ============================================================
// 请求映射
// ============================================================

// 契约：智谱的搜索声明是 tools 里的 web_search 项，且可与函数工具共存。
func TestZhipuWebSearchToolRequestShape(t *testing.T) {
	t.Parallel()

	p, captured := newCompatSearchProvider(t, ProviderZhipu, compatSearchChatBody, false)
	_, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("q")},
		Tools: []Tool{
			{Function: FunctionDef{Name: "get_weather", Description: "d", Parameters: map[string]any{"type": "object"}}},
			WebSearchToolWithOptions(WebSearchOptions{AllowedDomains: []string{"go.dev"}}),
		},
	})
	require.NoError(t, err)

	tools, ok := decodeRequestBody(t, *captured)["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)

	fn, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "function", fn["type"])
	assert.Equal(t, "get_weather", fn["function"].(map[string]any)["name"])

	search, ok := tools[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "web_search", search["type"])
	assert.Equal(t, map[string]any{
		"enable":               "True",
		"search_result":        true,
		"search_domain_filter": "go.dev",
	}, search["web_search"])
}

// 契约：百炼的搜索声明是顶层参数，不进 tools。
func TestQwenWebSearchRequestShape(t *testing.T) {
	t.Parallel()

	p, captured := newCompatSearchProvider(t, ProviderQwen, compatSearchChatBody, false)
	_, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("q")},
		Tools:    []Tool{WebSearchToolWithOptions(WebSearchOptions{AllowedDomains: []string{"a.com", "b.com"}})},
	})
	require.NoError(t, err)

	body := decodeRequestBody(t, *captured)
	assert.Equal(t, true, body["enable_search"])
	assert.Equal(t, map[string]any{"assigned_site_list": []any{"a.com", "b.com"}}, body["search_options"])
	assert.NotContains(t, body, "tools")
}

// 契约：未声明搜索工具的请求，请求体与响应都不受本特性影响。
func TestCompatWebSearchAbsentLeavesRequestUntouched(t *testing.T) {
	t.Parallel()

	for _, name := range []ProviderName{ProviderZhipu, ProviderQwen} {
		p, captured := newCompatSearchProvider(t, name, compatSearchChatBody, false)
		resp, err := p.Chat(t.Context(), &ChatRequest{
			Messages: []Message{UserText("q")},
			Tools:    []Tool{{Function: FunctionDef{Name: "f", Parameters: map[string]any{"type": "object"}}}},
		})
		require.NoError(t, err, name)
		assert.Nil(t, resp.Search, name)
		assert.NotContains(t, *captured, "enable_search", name)
		assert.NotContains(t, *captured, "web_search", name)
	}
}

// ============================================================
// fail-closed：不支持的选项一律拒绝，不静默降级
// ============================================================

func TestCompatWebSearchRejectsUnsupportedOptions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		provider ProviderName
		opts     WebSearchOptions
		wantMsg  string
	}{
		{"智谱拒绝 MaxUses", ProviderZhipu, WebSearchOptions{MaxUses: 3}, "MaxUses"},
		{"百炼拒绝 MaxUses", ProviderQwen, WebSearchOptions{MaxUses: 1}, "MaxUses"},
		{"智谱拒绝 BlockedDomains", ProviderZhipu, WebSearchOptions{BlockedDomains: []string{"x.com"}}, "BlockedDomains"},
		{"百炼拒绝 BlockedDomains", ProviderQwen, WebSearchOptions{BlockedDomains: []string{"x.com"}}, "BlockedDomains"},
		{"智谱拒绝多域名", ProviderZhipu, WebSearchOptions{AllowedDomains: []string{"a.com", "b.com"}}, "AllowedDomains"},
		{"智谱拒绝空域名", ProviderZhipu, WebSearchOptions{AllowedDomains: []string{""}}, "empty domain"},
		{"百炼拒绝空域名", ProviderQwen, WebSearchOptions{AllowedDomains: []string{"a.com", ""}}, "empty domain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, captured := newCompatSearchProvider(t, tc.provider, compatSearchChatBody, false)
			_, err := p.Chat(t.Context(), &ChatRequest{
				Messages: []Message{UserText("q")},
				Tools:    []Tool{WebSearchToolWithOptions(tc.opts)},
			})
			require.ErrorIs(t, err, ErrInvalidRequest)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Contains(t, err.Error(), string(tc.provider))
			// 请求必须被挡在发出之前，否则"拒绝"只是事后报错。
			assert.Empty(t, *captured)
		})
	}
}

// 契约：未接入内建搜索的平台收到该工具仍然拒绝，错误指明 provider 不支持。
func TestCompatWebSearchRejectedOnUnmappedProviders(t *testing.T) {
	t.Parallel()

	for _, name := range []ProviderName{ProviderDeepSeek, ProviderMoonshot, ProviderArk, ProviderQianfan} {
		p, _ := newCompatSearchProvider(t, name, compatSearchChatBody, false)
		_, err := p.Chat(t.Context(), &ChatRequest{
			Messages: []Message{UserText("q")},
			Tools:    []Tool{WebSearchTool()},
		})
		require.ErrorIs(t, err, ErrInvalidRequest, name)
		assert.Contains(t, err.Error(), "does not support native web search", name)
	}
}

// ============================================================
// 来源归一与 SourcesUnavailable 语义
// ============================================================

// 契约：智谱回传的来源按平台顺序归一为 Sources，且不置 SourcesUnavailable。
func TestZhipuWebSearchSourcesNormalized(t *testing.T) {
	t.Parallel()

	body := `{"id":"1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
		`"search_result":[{"title":"T1","link":"https://a.com"},{"title":"T2","link":"https://b.com"}]}`
	p, _ := newCompatSearchProvider(t, ProviderZhipu, body, false)
	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("q")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Search)
	assert.False(t, resp.Search.SourcesUnavailable)
	assert.Equal(t, []SearchSource{
		{URL: "https://a.com", Title: "T1"},
		{URL: "https://b.com", Title: "T2"},
	}, resp.Search.Sources)
}

// 契约：平台没回传来源时 Sources 为空且 SourcesUnavailable 为 false——
// 这两个取值合起来才表示"模型未搜索"，是该字段三态语义的关键一态。
func TestZhipuWebSearchWithoutSources(t *testing.T) {
	t.Parallel()

	p, _ := newCompatSearchProvider(t, ProviderZhipu, compatSearchChatBody, false)
	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("q")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Search)
	assert.False(t, resp.Search.SourcesUnavailable)
	assert.Empty(t, resp.Search.Sources)
}

// 契约：来源字段畸形不能让本来成功的调用失败。
func TestZhipuWebSearchMalformedSourcesDegrade(t *testing.T) {
	t.Parallel()

	body := `{"id":"1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
		`"search_result":"not-an-array"}`
	p, _ := newCompatSearchProvider(t, ProviderZhipu, body, false)
	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("q")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	require.NotNil(t, resp.Search)
	assert.Empty(t, resp.Search.Sources)
}

// 契约：百炼恒置 SourcesUnavailable 且 Sources 恒空，非流式与流式一致。
func TestQwenWebSearchSourcesUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("非流式", func(t *testing.T) {
		t.Parallel()

		p, _ := newCompatSearchProvider(t, ProviderQwen, compatSearchChatBody, false)
		resp, err := p.Chat(t.Context(), &ChatRequest{
			Messages: []Message{UserText("q")},
			Tools:    []Tool{WebSearchTool()},
		})
		require.NoError(t, err)
		require.NotNil(t, resp.Search)
		assert.True(t, resp.Search.SourcesUnavailable)
		assert.Empty(t, resp.Search.Sources)
	})

	t.Run("流式", func(t *testing.T) {
		t.Parallel()

		sse := strings.Join([]string{
			`data: {"id":"1","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			"data: [DONE]",
			"",
		}, "\n\n")
		p, _ := newCompatSearchProvider(t, ProviderQwen, sse, true)
		final := collectFinalSearchChunk(t, p)
		require.NotNil(t, final.Search)
		assert.True(t, final.Search.SourcesUnavailable)
	})
}

// 契约：流式下来源随 FinishReason 非空的 chunk 透出，与非流式同一形状。
func TestZhipuWebSearchStreamSources(t *testing.T) {
	t.Parallel()

	sse := strings.Join([]string{
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`data: {"id":"1","search_result":[{"title":"T1","link":"https://a.com"}]}`,
		`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]",
		"",
	}, "\n\n")
	p, _ := newCompatSearchProvider(t, ProviderZhipu, sse, true)
	final := collectFinalSearchChunk(t, p)
	require.NotNil(t, final.Search)
	assert.False(t, final.Search.SourcesUnavailable)
	assert.Equal(t, []SearchSource{{URL: "https://a.com", Title: "T1"}}, final.Search.Sources)
}

func collectFinalSearchChunk(t *testing.T, p Provider) *StreamChunk {
	t.Helper()

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("q")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = stream.Close() })

	var final *StreamChunk
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if chunk.FinishReason != "" {
			final = chunk
		}
	}
	require.NotNil(t, final, "未收到 FinishReason 非空的 chunk")

	return final
}
