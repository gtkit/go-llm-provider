package provider

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json/v2"
	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newResponsesTestProvider(t *testing.T, handler http.HandlerFunc) Provider {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	p, err := NewOpenAIResponsesProvider(OpenAIResponsesConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "gpt-5.6-terra",
	})
	require.NoError(t, err)
	return p
}

const responsesCompletedFixture = `{
	"id": "resp_1", "object": "response", "status": "completed", "model": "gpt-5.6-terra-2026-07-09",
	"output": [
		{"type":"reasoning","id":"rs_1","encrypted_content":"ENC","summary":[{"type":"summary_text","text":"先查天气"}]},
		{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"巴黎天气"}},
		{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[
			{"type":"output_text","text":"巴黎晴天","annotations":[{"type":"url_citation","url":"https://weather.example/paris","title":"Paris"}]}
		]}
	],
	"usage": {"input_tokens": 120, "input_tokens_details": {"cached_tokens": 100, "cache_write_tokens": 20},
	          "output_tokens": 30, "output_tokens_details": {"reasoning_tokens": 12}, "total_tokens": 150}
}`

func TestNewOpenAIResponsesProviderValidatesConfig(t *testing.T) {
	t.Parallel()

	_, err := NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: "m"})
	require.ErrorIs(t, err, ErrInvalidProviderConfig)
	_, err = NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k"})
	require.ErrorIs(t, err, ErrInvalidProviderConfig)

	p, err := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: "m"})
	require.NoError(t, err)
	assert.Equal(t, ProviderOpenAI, p.Name(), "Name 留空时默认 OpenAI")

	named, err := NewOpenAIResponsesProvider(OpenAIResponsesConfig{Name: ProviderDeepSeek, APIKey: "k", Model: "m"})
	require.NoError(t, err)
	assert.Equal(t, ProviderDeepSeek, named.Name())
}

// TestResponsesProviderChatMapsRequest 锁定请求形态：system 进 instructions，历史拆成
// reasoning / message / function_call / function_call_output 输入项，工具与 tool_choice 用
// Responses API 的扁平形态，结构化输出走 text.format，不落库时请求 encrypted_content。
func TestResponsesProviderChatMapsRequest(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	p := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsesCompletedFixture))
	})

	call := ToolCall{ID: "call_1", Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`}}
	_, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{
			SystemText("你是天气助手"),
			UserMessage(TextPart("巴黎天气"), ImageDataPart([]byte{1, 2}, "image/png")),
			{
				Role:           RoleAssistant,
				Content:        []ContentPart{TextPart("")},
				Reasoning:      "先查",
				ThinkingBlocks: []ThinkingBlock{{Thinking: "先查", Signature: "rs_0", Redacted: "ENC0"}, {Thinking: "无加密内容不回放", Signature: "rs_x"}},
				ToolCalls:      []ToolCall{call},
			},
			ToolResultMessageFor(call, `{"weather":"sunny"}`),
		},
		MaxTokens: 256,
		Tools: []Tool{
			{Function: FunctionDef{Name: "get_weather", Description: "查天气", Parameters: ParamSchema{Type: "object"}, Strict: new(true)}},
			WebSearchToolWithOptions(WebSearchOptions{AllowedDomains: []string{"weather.example"}}),
		},
		ToolChoice: ToolChoiceFunction{Name: "get_weather"},
		Thinking:   &Thinking{Effort: ThinkingEffortLow},
		ResponseFormat: JSONSchemaFormatStrict("weather", ParamSchema{
			Type: "object", Properties: map[string]ParamSchema{"city": {Type: "string"}}, Required: []string{"city"},
		}),
	})
	require.NoError(t, err)

	assert.Equal(t, "gpt-5.6-terra", captured["model"])
	assert.Equal(t, "你是天气助手", captured["instructions"])
	assert.InDelta(t, 256, captured["max_output_tokens"], 0.0001)
	assert.Equal(t, false, captured["store"])
	assert.Equal(t, []any{"reasoning.encrypted_content"}, captured["include"])
	reasoning := captured["reasoning"].(map[string]any)
	assert.Equal(t, "low", reasoning["effort"])
	assert.Equal(t, "auto", reasoning["summary"])

	input := captured["input"].([]any)
	// user + reasoning + function_call + function_call_output；assistant 正文为空不产生 message 项。
	require.Len(t, input, 4)
	user := input[0].(map[string]any)
	assert.Equal(t, "user", user["role"])
	userContent := user["content"].([]any)
	require.Len(t, userContent, 2)
	assert.Equal(t, "input_text", userContent[0].(map[string]any)["type"])
	assert.Equal(t, "input_image", userContent[1].(map[string]any)["type"])
	assert.Contains(t, userContent[1].(map[string]any)["image_url"], "data:image/png;base64,")

	reasoningItem := input[1].(map[string]any)
	assert.Equal(t, "reasoning", reasoningItem["type"])
	assert.Equal(t, "rs_0", reasoningItem["id"])
	assert.Equal(t, "ENC0", reasoningItem["encrypted_content"])

	functionCall := input[2].(map[string]any)
	assert.Equal(t, "function_call", functionCall["type"])
	assert.Equal(t, "call_1", functionCall["call_id"])
	assert.Equal(t, "get_weather", functionCall["name"])
	assert.JSONEq(t, `{"city":"Paris"}`, functionCall["arguments"].(string))

	output := input[3].(map[string]any)
	assert.Equal(t, "function_call_output", output["type"])
	assert.Equal(t, "call_1", output["call_id"])
	assert.JSONEq(t, `{"weather":"sunny"}`, output["output"].(string))

	tools := captured["tools"].([]any)
	require.Len(t, tools, 2)
	fn := tools[0].(map[string]any)
	assert.Equal(t, "function", fn["type"])
	assert.Equal(t, "get_weather", fn["name"], "Responses API 的函数工具是扁平形态")
	assert.Equal(t, true, fn["strict"])
	search := tools[1].(map[string]any)
	assert.Equal(t, "web_search", search["type"])
	assert.Equal(t, []any{"weather.example"}, search["filters"].(map[string]any)["allowed_domains"])

	toolChoice := captured["tool_choice"].(map[string]any)
	assert.Equal(t, "function", toolChoice["type"])
	assert.Equal(t, "get_weather", toolChoice["name"])
	assert.NotContains(t, toolChoice, "function", "不得使用 Chat Completions 的嵌套形态")

	format := captured["text"].(map[string]any)["format"].(map[string]any)
	assert.Equal(t, "json_schema", format["type"])
	assert.Equal(t, "weather", format["name"])
	assert.Equal(t, true, format["strict"])
}

// TestResponsesProviderChatParsesOutput 锁定响应归一：推理项进 Reasoning 与 ThinkingBlocks
// （带 encrypted_content 供往返），搜索调用与引用进 Search，用量按统一包含关系映射。
func TestResponsesProviderChatParsesOutput(t *testing.T) {
	t.Parallel()

	p := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "req-resp")
		_, _ = w.Write([]byte(responsesCompletedFixture))
	})

	resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("巴黎天气")}, Tools: []Tool{WebSearchTool()}})
	require.NoError(t, err)

	assert.Equal(t, "巴黎晴天", resp.Content)
	assert.Equal(t, "先查天气", resp.Reasoning)
	assert.Equal(t, []ThinkingBlock{{Thinking: "先查天气", Signature: "rs_1", Redacted: "ENC"}}, resp.ThinkingBlocks)
	assert.Equal(t, "stop", resp.FinishReason)
	assert.Empty(t, resp.ToolCalls)
	assert.Equal(t, Usage{
		PromptTokens: 120, CompletionTokens: 30, ReasoningTokens: 12,
		CacheReadTokens: 100, CacheWriteTokens: 20, TotalTokens: 150, WebSearchRequests: 1,
	}, resp.Usage)
	require.NotNil(t, resp.Search)
	assert.Equal(t, []string{"巴黎天气"}, resp.Search.Queries)
	assert.Equal(t, []SearchSource{{URL: "https://weather.example/paris", Title: "Paris"}}, resp.Search.Sources)
	assert.Equal(t, "gpt-5.6-terra-2026-07-09", resp.Metadata.Model)
	assert.Equal(t, "req-resp", resp.Metadata.RequestID)
}

func TestResponsesProviderChatMapsFunctionCallsAndIncomplete(t *testing.T) {
	t.Parallel()

	p := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_2","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"model":"m",
			"output":[{"type":"function_call","id":"fc_1","call_id":"call_9","name":"get_weather","arguments":"{\"city\":\"Rome\"}"}],
			"usage":{"input_tokens":5,"output_tokens":6,"total_tokens":11}}`))
	})

	resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_9", resp.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Function.Name)
	assert.Equal(t, "tool_calls", resp.FinishReason, "有函数调用时优先报 tool_calls")

	assert.Equal(t, "length", responsesFinishReason(openai.ResponseStatusIncomplete, &openai.ResponseIncompleteDetails{Reason: "max_output_tokens"}, false))
	assert.Equal(t, "content_filter", responsesFinishReason(openai.ResponseStatusIncomplete, &openai.ResponseIncompleteDetails{Reason: "content_filter"}, false))
	assert.Equal(t, "stop", responsesFinishReason(openai.ResponseStatusCompleted, nil, false))
}

func TestResponsesProviderChatFailedStatusIsProviderError(t *testing.T) {
	t.Parallel()

	p := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_3","status":"failed","model":"m","output":[],"error":{"code":"rate_limit_exceeded","message":"slow down"}}`))
	})

	_, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.ErrorIs(t, err, ErrRateLimit)
	assert.True(t, IsRetryableError(err))
}

func TestResponsesProviderRejectsUnmappableControls(t *testing.T) {
	t.Parallel()

	p := &responsesProvider{name: ProviderOpenAI, model: "m"}
	seed := 1
	enabled := true
	for name, req := range map[string]*ChatRequest{
		"stop sequences":      {Messages: []Message{UserText("hi")}, Stop: []string{"\n"}},
		"seed":                {Messages: []Message{UserText("hi")}, Seed: &seed},
		"candidate count":     {Messages: []Message{UserText("hi")}, CandidateCount: 2},
		"web search max_uses": {Messages: []Message{UserText("hi")}, Tools: []Tool{WebSearchToolWithOptions(WebSearchOptions{MaxUses: 3})}},
		"thinking enabled":    {Messages: []Message{UserText("hi")}, Thinking: &Thinking{Enabled: &enabled}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := p.buildRequest(req)
			require.ErrorIs(t, err, ErrInvalidRequest)
		})
	}
}

// TestResponsesProviderChatStreamMapsEvents 锁定流式事件归一：文本与推理摘要增量、
// 函数调用项与入参增量按项编号、推理项完成时以签名 + 加密内容闭合思考块、
// completed 事件给出结束原因与用量。
func TestResponsesProviderChatStreamMapsEvents(t *testing.T) {
	t.Parallel()

	p := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var captured map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		assert.Equal(t, true, captured["stream"])
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(eventType, data string) {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
		}
		write("response.created", `{"type":"response.created","response":{"id":"resp_s","status":"in_progress","output":[]}}`)
		write("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_s","summary":[]}}`)
		write("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_s","delta":"先查"}`)
		write("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_s","delta":"天气"}`)
		write("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_s","encrypted_content":"ENC_S","summary":[{"type":"summary_text","text":"先查天气"}]}}`)
		write("response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_s","call_id":"call_s","name":"get_weather","arguments":""}}`)
		write("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_s","delta":"{\"city\":"}`)
		write("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_s","delta":"\"Paris\"}"}`)
		write("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_s","delta":"稍等"}`)
		write("response.completed", `{"type":"response.completed","response":{"id":"resp_s","status":"completed","model":"gpt-5.6-terra","output":[],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}`)
	})

	stream, err := p.ChatStream(t.Context(), &ChatRequest{Messages: []Message{UserText("巴黎天气")}})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	var chunks []StreamChunk
	for {
		chunk, err := stream.Recv()
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		chunks = append(chunks, *chunk)
	}
	require.Len(t, chunks, 8)

	assert.Equal(t, "先查", chunks[0].ReasoningDelta)
	assert.Equal(t, "天气", chunks[1].ReasoningDelta)
	assert.Equal(t, "rs_s", chunks[2].ThinkingSignature)
	assert.Equal(t, "ENC_S", chunks[2].RedactedThinking)

	require.Len(t, chunks[3].ToolCalls, 1)
	assert.Equal(t, 0, chunks[3].ToolCalls[0].Index)
	assert.Equal(t, "call_s", chunks[3].ToolCalls[0].ID)
	assert.Equal(t, "get_weather", chunks[3].ToolCalls[0].Function.Name)
	assert.Equal(t, `{"city":`, chunks[4].ToolCalls[0].Function.Arguments)
	assert.Equal(t, `"Paris"}`, chunks[5].ToolCalls[0].Function.Arguments)
	assert.Equal(t, 0, chunks[5].ToolCalls[0].Index)

	assert.Equal(t, "稍等", chunks[6].Delta)

	assert.Equal(t, "tool_calls", chunks[7].FinishReason)
	assert.Equal(t, "gpt-5.6-terra", chunks[7].Model)
	assert.Equal(t, Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}, chunks[7].Usage)
}

func TestResponsesProviderChatStreamErrorEvent(t *testing.T) {
	t.Parallel()

	p := newResponsesTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"boom\"}\n\n")
	})

	stream, err := p.ChatStream(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	_, err = stream.Recv()
	require.ErrorIs(t, err, ErrServerError)
}
