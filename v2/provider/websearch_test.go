package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicProviderChatMapsWebSearchTool(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": [
				{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"go 1.26 release"}},
				{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[
					{"type":"web_search_result","url":"https://go.dev/blog/go1.26","title":"Go 1.26 Release Notes"}
				]},
				{"type":"text","text":"Go 1.26 已发布。"}
			],
			"usage": {"input_tokens": 20, "output_tokens": 9, "server_tool_use": {"web_search_requests": 2}}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("go 1.26 发布了吗")},
		Tools: []Tool{WebSearchToolWithOptions(WebSearchOptions{
			MaxUses:        3,
			AllowedDomains: []string{"go.dev"},
		})},
	})
	require.NoError(t, err)

	// 请求侧：web search 映射为版本化 server tool。
	tools, ok := captured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	search, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, anthropicWebSearchToolType, search["type"])
	assert.Equal(t, "web_search", search["name"])
	assert.InDelta(t, 3, search["max_uses"], 0.0001)
	assert.Equal(t, []any{"go.dev"}, search["allowed_domains"])

	// 响应侧：服务端工具块不外露为 ToolCall，计次与元数据归一。
	assert.Equal(t, "Go 1.26 已发布。", resp.Content)
	assert.Equal(t, "end_turn", resp.FinishReason)
	assert.Empty(t, resp.ToolCalls)
	assert.Equal(t, 2, resp.Usage.WebSearchRequests)
	assert.Equal(t, 0, resp.Usage.WebSearchGroundedPrompts)
	require.NotNil(t, resp.Search)
	assert.Equal(t, []string{"go 1.26 release"}, resp.Search.Queries)
	require.Len(t, resp.Search.Sources, 1)
	assert.Equal(t, "https://go.dev/blog/go1.26", resp.Search.Sources[0].URL)
	assert.Equal(t, "Go 1.26 Release Notes", resp.Search.Sources[0].Title)
}

func TestAnthropicWebSearchDomainsMutuallyExclusive(t *testing.T) {
	t.Parallel()

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)

	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools: []Tool{WebSearchToolWithOptions(WebSearchOptions{
			AllowedDomains: []string{"go.dev"},
			BlockedDomains: []string{"example.com"},
		})},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestAnthropicWebSearchRejectsFunctionToolMix(t *testing.T) {
	t.Parallel()

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)

	// 混用时服务端工具块无法跨轮往返，必须显式拒绝而非丢上下文。
	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools: []Tool{
			WebSearchTool(),
			{Function: FunctionDef{Name: "get_time"}},
		},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)

	_, err = p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools: []Tool{
			WebSearchTool(),
			{Function: FunctionDef{Name: "get_time"}},
		},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestAnthropicWebSearchRejectsStructuredOutput(t *testing.T) {
	t.Parallel()

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)

	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages:       []Message{UserText("hi")},
		Tools:          []Tool{WebSearchTool()},
		ResponseFormat: JSONObjectFormat(),
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestAnthropicProviderChatPauseTurnReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"stop_reason": "pause_turn",
			"content": [{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"x"}}],
			"usage": {"input_tokens": 5, "output_tokens": 1, "server_tool_use": {"web_search_requests": 1}}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)

	// 续跑暂停回合需要回传服务端工具块，当前不支持——必须明确报错，
	// 而不是把被截断的响应当正常结果返回。
	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.ErrorIs(t, err, ErrUnsupportedCapability)

	// 暂停前服务端已执行搜索、已产生真实消耗：错误必须携带用量与搜索元数据，
	// 计费层据此归账，不得按零消耗处理。
	var pauseErr *PauseTurnError
	require.ErrorAs(t, err, &pauseErr)
	assert.Equal(t, 6, pauseErr.Usage.PromptTokens+pauseErr.Usage.CompletionTokens)
	assert.Equal(t, 1, pauseErr.Usage.WebSearchRequests)
	require.NotNil(t, pauseErr.Search)
	assert.Equal(t, []string{"x"}, pauseErr.Search.Queries)
	assert.NotContains(t, pauseErr.Error(), "retry")
}

func TestAnthropicProviderChatStreamPauseTurnReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":5}}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"pause_turn\"},\"usage\":{\"output_tokens\":1,\"server_tool_use\":{\"web_search_requests\":1}}}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	for {
		_, err := stream.Recv()
		if err != nil {
			require.ErrorIs(t, err, ErrUnsupportedCapability)
			// 流上已累积的输入/输出与搜索计次随错误携带，供计费层提取。
			var pauseErr *PauseTurnError
			require.ErrorAs(t, err, &pauseErr)
			assert.Equal(t, 5, pauseErr.Usage.PromptTokens)
			assert.Equal(t, 1, pauseErr.Usage.CompletionTokens)
			assert.Equal(t, 1, pauseErr.Usage.WebSearchRequests)
			break
		}
	}
}

func TestStreamPauseTurnBillsThroughRecorder(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":18}}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"pause_turn\"},\"usage\":{\"output_tokens\":2,\"server_tool_use\":{\"web_search_requests\":3}}}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)

	// 完整计费链路：ChatStream → observedStream → stream_complete 事件 →
	// NewBillingHook → UsageRecorder。pause_turn 已产生真实消耗，即使流以错误
	// 终止，用量也必须记入存储，不漏账。
	store := NewMemoryUsageStore()
	billed := WithObservability(p, ObserveOptions{OnEvent: NewBillingHook(store)})

	ctx := WithUserID(t.Context(), "u1")
	stream, err := billed.ChatStream(ctx, &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	for {
		_, recvErr := stream.Recv()
		if recvErr != nil {
			require.ErrorIs(t, recvErr, ErrUnsupportedCapability)
			break
		}
	}
	require.NoError(t, stream.Close())

	totals, ok := store.UserTotals("u1")
	require.True(t, ok, "pause_turn 的消耗必须记入计费存储")
	assert.Equal(t, 18, totals.Usage.PromptTokens)
	assert.Equal(t, 2, totals.Usage.CompletionTokens)
	assert.Equal(t, 3, totals.Usage.WebSearchRequests)
}

func TestObservabilityExtractsUsageFromPauseTurnError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"stop_reason": "pause_turn",
			"content": [{"type":"text","text":"searching"}],
			"usage": {"input_tokens": 20, "output_tokens": 3, "server_tool_use": {"web_search_requests": 2}}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)

	var events []ObserveEvent
	observed := WithObservability(p, ObserveOptions{
		OnEvent: func(_ context.Context, event ObserveEvent) { events = append(events, event) },
	})

	_, err = observed.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.ErrorIs(t, err, ErrUnsupportedCapability)

	// 失败调用的观测事件必须携带错误内的真实用量，计费 hook 才不会漏账。
	require.Len(t, events, 1)
	assert.Equal(t, Usage{
		PromptTokens:      20,
		CompletionTokens:  3,
		TotalTokens:       23,
		WebSearchRequests: 2,
	}, events[0].Usage)
	assert.Error(t, events[0].Err)
}

func TestAnthropicProviderChatStreamSuppressesServerToolBlocks(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":15}}}\n\n")
		// 服务端搜索块：start 与 input_json_delta 均不得外露为客户端工具调用。
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"server_tool_use\",\"id\":\"srvtoolu_1\",\"name\":\"web_search\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"go 1.26\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"web_search_tool_result\",\"tool_use_id\":\"srvtoolu_1\",\"content\":[{\"type\":\"web_search_result\",\"url\":\"https://go.dev\",\"title\":\"go.dev\"}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"text_delta\",\"text\":\"结果如下\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":6,\"server_tool_use\":{\"web_search_requests\":1}}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	var text string
	var final *StreamChunk
	for {
		chunk, err := stream.Recv()
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		assert.Empty(t, chunk.ToolCalls, "服务端搜索块不得外露为 ToolCallDelta")
		text += chunk.Delta
		if chunk.FinishReason != "" {
			final = chunk
		}
	}
	assert.Equal(t, "结果如下", text)
	require.NotNil(t, final)
	assert.Equal(t, "end_turn", final.FinishReason)
	assert.Equal(t, 1, final.Usage.WebSearchRequests)
	assert.Equal(t, 15, final.Usage.PromptTokens)
	assert.Equal(t, 6, final.Usage.CompletionTokens)
	// 被抑制的 server_tool_use 输入在流尾解析出查询，来源随结果块收集。
	require.NotNil(t, final.Search)
	assert.Equal(t, []string{"go 1.26"}, final.Search.Queries)
	require.Len(t, final.Search.Sources, 1)
	assert.Equal(t, "https://go.dev", final.Search.Sources[0].URL)
}

func TestAnthropicProviderChatSurfacesSearchErrors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 搜索失败经 HTTP 200 的错误内容块返回，模型仍生成了解释文本。
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": [
				{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"go 1.26"}},
				{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":{"type":"web_search_tool_result_error","error_code":"max_uses_exceeded"}},
				{"type":"text","text":"搜索次数已达上限，基于已有知识回答。"}
			],
			"usage": {"input_tokens": 10, "output_tokens": 8}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)

	// 搜索失败必须透出，调用方据此区分"未搜索"与"搜索失败"。
	require.NotNil(t, resp.Search)
	require.Len(t, resp.Search.Errors, 1)
	assert.Equal(t, "max_uses_exceeded", resp.Search.Errors[0].Code)
	assert.Empty(t, resp.Search.Sources)
	assert.Equal(t, []string{"go 1.26"}, resp.Search.Queries)
	assert.Equal(t, "搜索次数已达上限，基于已有知识回答。", resp.Content)
}

func TestAnthropicProviderChatStreamSurfacesSearchErrors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":10}}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"web_search_tool_result\",\"tool_use_id\":\"srvtoolu_1\",\"content\":{\"type\":\"web_search_tool_result_error\",\"error_code\":\"too_many_requests\"}}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"稍后再试\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	var final *StreamChunk
	for {
		chunk, err := stream.Recv()
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		if chunk.FinishReason != "" {
			final = chunk
		}
	}
	require.NotNil(t, final)
	require.NotNil(t, final.Search)
	require.Len(t, final.Search.Errors, 1)
	assert.Equal(t, "too_many_requests", final.Search.Errors[0].Code)
}

func TestWebSearchRequestValidation(t *testing.T) {
	t.Parallel()

	anthropic, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)
	gemini, err := NewGeminiProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)

	tests := []struct {
		name string
		req  ChatRequest
	}{
		{
			name: "重复声明搜索工具",
			req: ChatRequest{
				Messages: []Message{UserText("hi")},
				Tools:    []Tool{WebSearchTool(), WebSearchTool()},
			},
		},
		{
			name: "同一 Tool 同时声明 Function 与 WebSearch",
			req: ChatRequest{
				Messages: []Message{UserText("hi")},
				Tools: []Tool{{
					Function:  FunctionDef{Name: "web_search"},
					WebSearch: &WebSearchOptions{},
				}},
			},
		},
		{
			name: "ToolChoiceRequired 指向服务端搜索工具",
			req: ChatRequest{
				Messages:   []Message{UserText("hi")},
				Tools:      []Tool{WebSearchTool()},
				ToolChoice: ToolChoiceRequired,
			},
		},
		{
			name: "ToolChoiceNone 与已声明搜索工具矛盾",
			req: ChatRequest{
				Messages:   []Message{UserText("hi")},
				Tools:      []Tool{WebSearchTool()},
				ToolChoice: ToolChoiceNone,
			},
		},
		{
			name: "ToolChoiceFunction 指向服务端搜索工具",
			req: ChatRequest{
				Messages:   []Message{UserText("hi")},
				Tools:      []Tool{WebSearchTool()},
				ToolChoice: ToolChoiceFunction{Name: "web_search"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := tt.req
			_, err := anthropic.Chat(t.Context(), &req)
			require.ErrorIs(t, err, ErrInvalidRequest, "anthropic")
			_, err = gemini.Chat(t.Context(), &req)
			require.ErrorIs(t, err, ErrInvalidRequest, "gemini")
		})
	}
}

func TestGeminiWebSearchRejectsMultipleCandidates(t *testing.T) {
	t.Parallel()

	p, err := NewGeminiProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)

	// 多候选下各 candidate 的搜索归属不明确，拒绝而非按首个近似。
	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages:       []Message{UserText("hi")},
		Tools:          []Tool{WebSearchTool()},
		CandidateCount: 2,
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestGeminiProviderChatMapsWebSearchTool(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[{"text":"已根据搜索结果回答。"}]},
				"finishReason": "STOP",
				"groundingMetadata": {
					"webSearchQueries": ["go 1.26 release", "go 1.26 features"],
					"searchEntryPoint": {"renderedContent": "<div>entry</div>"},
					"groundingChunks": [{"web": {"uri": "https://go.dev/blog", "title": "The Go Blog"}}]
				}
			}],
			"usageMetadata": {"promptTokenCount": 8, "candidatesTokenCount": 5, "totalTokenCount": 13},
			"modelVersion": "gemini-2.5-flash"
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("go 1.26 发布了吗")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)

	tools, ok := captured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	search, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, search, "google_search")
	assert.NotContains(t, search, "functionDeclarations")

	// 双口径用量：query 数 + grounded prompt，计价时按费率配置二选一。
	assert.Equal(t, "已根据搜索结果回答。", resp.Content)
	assert.Equal(t, 2, resp.Usage.WebSearchRequests)
	assert.Equal(t, 1, resp.Usage.WebSearchGroundedPrompts)
	require.NotNil(t, resp.Search)
	assert.Equal(t, []string{"go 1.26 release", "go 1.26 features"}, resp.Search.Queries)
	assert.Equal(t, "<div>entry</div>", resp.Search.SearchEntryPoint)
	require.Len(t, resp.Search.Sources, 1)
	assert.Equal(t, "https://go.dev/blog", resp.Search.Sources[0].URL)
	assert.Equal(t, "The Go Blog", resp.Search.Sources[0].Title)
}

func TestGeminiWebSearchCountsUniqueNonEmptyQueries(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[{"text":"ok"}]},
				"finishReason": "STOP",
				"groundingMetadata": {"webSearchQueries": ["", "go 1.26", "go 1.26", "  "]}
			}],
			"usageMetadata": {"promptTokenCount": 4, "candidatesTokenCount": 2, "totalTokenCount": 6}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)

	// Google 计费口径是"非空的唯一搜索查询"：空白与重复不计，防多收费；
	// 元数据 Queries 保留平台原始返回值供展示。
	assert.Equal(t, 1, resp.Usage.WebSearchRequests)
	assert.Equal(t, 1, resp.Usage.WebSearchGroundedPrompts)
	require.NotNil(t, resp.Search)
	assert.Equal(t, []string{"", "go 1.26", "go 1.26", "  "}, resp.Search.Queries)
}

func TestGeminiProviderChatWithoutGroundingReportsZeroSearches(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{"content":{"role":"model","parts":[{"text":"直接回答。"}]},"finishReason":"STOP"}],
			"usageMetadata": {"promptTokenCount": 4, "candidatesTokenCount": 3, "totalTokenCount": 7}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("1+1")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, resp.Usage.WebSearchRequests)
	assert.Equal(t, 0, resp.Usage.WebSearchGroundedPrompts)
	assert.Nil(t, resp.Search)
}

func TestGeminiWebSearchRejectsFunctionToolMix(t *testing.T) {
	t.Parallel()

	p, err := NewGeminiProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)

	// Gemini 2.5 系不支持 google_search 与函数声明同用，客户端先行拒绝。
	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools: []Tool{
			WebSearchTool(),
			{Function: FunctionDef{Name: "get_time"}},
		},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestGeminiWebSearchRejectsUnsupportedOptions(t *testing.T) {
	t.Parallel()

	p, err := NewGeminiProvider(NativeProviderConfig{APIKey: "test-key"})
	require.NoError(t, err)

	// MaxUses 与域名过滤具有安全/费用约束语义，Gemini 不支持时必须报错，
	// 静默忽略会绕过调用方策略。
	for _, opts := range []WebSearchOptions{
		{MaxUses: 3},
		{AllowedDomains: []string{"go.dev"}},
		{BlockedDomains: []string{"example.com"}},
	} {
		_, err = p.Chat(t.Context(), &ChatRequest{
			Messages: []Message{UserText("hi")},
			Tools:    []Tool{WebSearchToolWithOptions(opts)},
		})
		require.ErrorIs(t, err, ErrInvalidRequest, "opts=%+v", opts)
	}
}

func TestGeminiProviderChatStreamReportsWebSearchUsage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 两个 grounding 快照：查询有重叠、来源不同——聚合必须去重合并而非覆盖。
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"搜索中\"}]},\"groundingMetadata\":{\"webSearchQueries\":[\"go 1.26\"],\"searchEntryPoint\":{\"renderedContent\":\"<div>e</div>\"},\"groundingChunks\":[{\"web\":{\"uri\":\"https://go.dev\",\"title\":\"go.dev\"}}]}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"，已完成。\"}]},\"finishReason\":\"STOP\",\"groundingMetadata\":{\"webSearchQueries\":[\"go 1.26\",\"go release notes\"],\"groundingChunks\":[{\"web\":{\"uri\":\"https://go.dev\",\"title\":\"go.dev\"}},{\"web\":{\"uri\":\"https://go.dev/blog\",\"title\":\"The Go Blog\"}}]}}],\"usageMetadata\":{\"promptTokenCount\":6,\"candidatesTokenCount\":4,\"totalTokenCount\":10}}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("搜一下")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	var final *StreamChunk
	for {
		chunk, err := stream.Recv()
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		if chunk.FinishReason != "" {
			final = chunk
		}
	}
	require.NotNil(t, final)
	// 两个快照聚合去重：查询 go 1.26（重叠）+ go release notes，来源 2 条去重。
	assert.Equal(t, 2, final.Usage.WebSearchRequests)
	assert.Equal(t, 1, final.Usage.WebSearchGroundedPrompts)
	require.NotNil(t, final.Search)
	assert.Equal(t, []string{"go 1.26", "go release notes"}, final.Search.Queries)
	assert.Equal(t, "<div>e</div>", final.Search.SearchEntryPoint)
	require.Len(t, final.Search.Sources, 2)
}

func TestOpenAICompatibleProviderRejectsWebSearchTool(t *testing.T) {
	t.Parallel()

	p, err := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "test-key", Model: "deepseek-chat"})
	require.NoError(t, err)

	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)

	_, err = p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools:    []Tool{WebSearchTool()},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestWebSearchAndFileCapabilityPresets(t *testing.T) {
	t.Parallel()

	anthropic, ok := ModelCapabilitiesFromPreset(ProviderAnthropic)
	require.True(t, ok)
	assert.True(t, anthropic.Supports(CapabilityWebSearch))
	assert.True(t, anthropic.Supports(CapabilityFile))

	gemini, ok := ModelCapabilitiesFromPreset(ProviderGemini)
	require.True(t, ok)
	assert.True(t, gemini.Supports(CapabilityWebSearch))
	assert.True(t, gemini.Supports(CapabilityFile))

	openai, ok := ModelCapabilitiesFromPreset(ProviderOpenAI)
	require.True(t, ok)
	assert.False(t, openai.Supports(CapabilityWebSearch))
	// OpenAI Chat Completions 路径不映射文件输入（buildOpenAIMessage 显式拒绝），
	// 能力声明必须与实现一致。
	assert.False(t, openai.Supports(CapabilityFile))

	deepseek, ok := ModelCapabilitiesFromPreset(ProviderDeepSeek)
	require.True(t, ok)
	assert.False(t, deepseek.Supports(CapabilityWebSearch))
	assert.False(t, deepseek.Supports(CapabilityFile))
}
