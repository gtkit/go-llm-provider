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

func newOllamaTestProvider(t *testing.T, handler http.HandlerFunc) Provider {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	p, err := NewOllamaProvider(OllamaProviderConfig{BaseURL: srv.URL, Model: "qwen3"})
	require.NoError(t, err)
	return p
}

// TestOllamaProviderChatMapsToolsAndToolCalls 锁定 Ollama 原生工具映射：
// 工具声明下发为 {type:function,function:{...}}；响应的 arguments 是 JSON 对象，
// 归一为字符串；Ollama 不签发调用 ID，库合成唯一 ID 并把 done_reason 对齐为 tool_calls。
func TestOllamaProviderChatMapsToolsAndToolCalls(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	p := newOllamaTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "qwen3",
			"message": {"role":"assistant","content":"","tool_calls":[
				{"function":{"name":"get_weather","arguments":{"city":"Paris"}}},
				{"function":{"name":"get_weather","arguments":{"city":"Rome"}}}
			]},
			"done": true, "done_reason": "stop", "prompt_eval_count": 9, "eval_count": 4
		}`))
	})

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("巴黎和罗马的天气")},
		Tools: []Tool{{Function: FunctionDef{
			Name:        "get_weather",
			Description: "查天气",
			Parameters:  ParamSchema{Type: "object", Properties: map[string]ParamSchema{"city": {Type: "string"}}},
		}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	require.Len(t, resp.ToolCalls, 2)
	assert.Equal(t, "ollama_get_weather_0", resp.ToolCalls[0].ID)
	assert.Equal(t, "ollama_get_weather_1", resp.ToolCalls[1].ID)
	assert.JSONEq(t, `{"city":"Paris"}`, resp.ToolCalls[0].Function.Arguments)
	assert.JSONEq(t, `{"city":"Rome"}`, resp.ToolCalls[1].Function.Arguments)

	tools, ok := captured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	assert.Equal(t, "function", tool["type"])
	fn := tool["function"].(map[string]any)
	assert.Equal(t, "get_weather", fn["name"])
	assert.Contains(t, fn, "parameters")
}

// TestOllamaProviderMapsToolLoopHistory 锁定工具循环第二轮的消息形态：
// assistant 的 tool_calls 以对象入参回放并带 thinking，工具结果带 tool_name（按 ID 反查）。
func TestOllamaProviderMapsToolLoopHistory(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	p := newOllamaTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen3","message":{"role":"assistant","content":"巴黎晴天"},"done":true,"done_reason":"stop"}`))
	})

	call := ToolCall{ID: "ollama_get_weather_0", Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`}}
	_, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{
			UserText("巴黎天气"),
			{Role: RoleAssistant, Reasoning: "需要查询", ToolCalls: []ToolCall{call}},
			ToolResultMessage(call.ID, `{"weather":"sunny"}`),
		},
	})
	require.NoError(t, err)

	messages := captured["messages"].([]any)
	require.Len(t, messages, 3)
	assistant := messages[1].(map[string]any)
	assert.Equal(t, "需要查询", assistant["thinking"])
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	fn := toolCalls[0].(map[string]any)["function"].(map[string]any)
	assert.Equal(t, "get_weather", fn["name"])
	assert.Equal(t, map[string]any{"city": "Paris"}, fn["arguments"], "入参必须是 JSON 对象而非字符串")

	tool := messages[2].(map[string]any)
	assert.Equal(t, "tool", tool["role"])
	assert.Equal(t, "get_weather", tool["tool_name"])
	content, ok := tool["content"].(string)
	require.True(t, ok)
	assert.JSONEq(t, `{"weather":"sunny"}`, content)
}

func TestOllamaProviderMapsFormatImagesAndSeed(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	p := newOllamaTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen3","message":{"role":"assistant","content":"{\"city\":\"Paris\"}"},"done":true,"done_reason":"stop"}`))
	})

	seed := 42
	_, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserMessage(TextPart("描述图片"), ImageDataPart([]byte{1, 2, 3}, "image/png"))},
		Seed:     &seed,
		ResponseFormat: JSONSchemaFormat("city", ParamSchema{
			Type: "object", Properties: map[string]ParamSchema{"city": {Type: "string"}},
		}),
	})
	require.NoError(t, err)

	format := captured["format"].(map[string]any)
	assert.Equal(t, "object", format["type"])
	assert.InDelta(t, 42, captured["options"].(map[string]any)["seed"], 0.0001)
	user := captured["messages"].([]any)[0].(map[string]any)
	assert.Equal(t, "描述图片", user["content"])
	assert.Equal(t, []any{"AQID"}, user["images"])

	// json_object 映射为 "json"
	_, err = p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}, ResponseFormat: JSONObjectFormat()})
	require.NoError(t, err)
	assert.Equal(t, "json", captured["format"])
}

// TestOllamaProviderRejectsUnmappableControls 锁定 fail-closed：Ollama 没有 tool_choice、
// parallel_tool_calls、远程图片 URL 与文件片段，一律拒绝而非静默丢弃。
func TestOllamaProviderRejectsUnmappableControls(t *testing.T) {
	t.Parallel()

	p := &ollamaProvider{model: "qwen3"}
	tools := []Tool{{Function: FunctionDef{Name: "f"}}}
	for name, req := range map[string]*ChatRequest{
		"tool choice required": {Messages: []Message{UserText("hi")}, Tools: tools, ToolChoice: ToolChoiceRequired},
		"parallel tool calls":  {Messages: []Message{UserText("hi")}, Tools: tools, ParallelToolCalls: new(false)},
		"remote image url":     {Messages: []Message{UserMessage(ImageURLPart("https://example.com/a.png"))}},
		"file part":            {Messages: []Message{UserMessage(FileIDPart("file-1"))}},
		"web search tool":      {Messages: []Message{UserText("hi")}, Tools: []Tool{WebSearchTool()}},
		"candidate count":      {Messages: []Message{UserText("hi")}, CandidateCount: 2},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := p.buildRequest(req, false)
			require.ErrorIs(t, err, ErrInvalidRequest)
		})
	}

	// ToolChoiceAuto 是 Ollama 的默认行为，放行。
	_, _, err := p.buildRequest(&ChatRequest{Messages: []Message{UserText("hi")}, Tools: tools, ToolChoice: ToolChoiceAuto}, false)
	require.NoError(t, err)
}

// TestOllamaProviderChatStreamMapsToolCallsAndThinking 锁定流式：thinking 增量归位到
// ReasoningDelta，整块到达的工具调用按到达顺序编号，done 事件折算 tool_calls 结束原因。
func TestOllamaProviderChatStreamMapsToolCallsAndThinking(t *testing.T) {
	t.Parallel()

	p := newOllamaTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"","thinking":"先查"},"done":false}`)
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"a","arguments":{"x":1}}}]},"done":false}`)
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"b","arguments":{}}}]},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":4}`)
	})

	stream, err := p.ChatStream(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	first, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "先查", first.ReasoningDelta)
	assert.Empty(t, first.ToolCalls)

	second, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, second.ToolCalls, 1)
	assert.Equal(t, 0, second.ToolCalls[0].Index)
	assert.Equal(t, "ollama_a_0", second.ToolCalls[0].ID)
	assert.JSONEq(t, `{"x":1}`, second.ToolCalls[0].Function.Arguments)
	assert.Empty(t, second.FinishReason)

	third, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, third.ToolCalls, 1)
	assert.Equal(t, 1, third.ToolCalls[0].Index)
	assert.Equal(t, "ollama_b_1", third.ToolCalls[0].ID)
	assert.Equal(t, "{}", third.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "tool_calls", third.FinishReason)
	assert.Equal(t, Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}, third.Usage)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

// TestOllamaProviderChatMapsThinkingEndToEnd 锁定 think 参数真的进入请求体，
// 且响应的 thinking 归位到 Reasoning 而不混入正文。
func TestOllamaProviderChatMapsThinkingEndToEnd(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	p := newOllamaTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen3","message":{"role":"assistant","content":"42","thinking":"先算一下"},"done":true,"done_reason":"stop"}`))
	})

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("6*7")},
		Thinking: &Thinking{Enabled: new(true)},
	})
	require.NoError(t, err)
	assert.Equal(t, true, captured["think"])
	assert.Equal(t, "42", resp.Content)
	assert.Equal(t, "先算一下", resp.Reasoning)

	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("6*7")},
		Thinking: &Thinking{Effort: ThinkingEffortHigh},
	})
	require.NoError(t, err)
	assert.Equal(t, "high", captured["think"])

	// BudgetTokens 在 Ollama 没有映射，fail-closed。
	budget := 1024
	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("6*7")},
		Thinking: &Thinking{BudgetTokens: &budget},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestOllamaThink(t *testing.T) {
	t.Parallel()

	enabled, disabled := true, false
	tests := []struct {
		name     string
		thinking *Thinking
		want     any
	}{
		{name: "nil", thinking: nil, want: nil},
		{name: "empty", thinking: &Thinking{}, want: nil},
		{name: "enabled", thinking: &Thinking{Enabled: &enabled}, want: true},
		{name: "disabled", thinking: &Thinking{Enabled: &disabled}, want: false},
		{name: "effort", thinking: &Thinking{Effort: ThinkingEffortHigh}, want: "high"},
		{name: "enabled with effort", thinking: &Thinking{Enabled: &enabled, Effort: ThinkingEffortLow}, want: "low"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ollamaThink(tc.thinking)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	_, err := ollamaThink(&Thinking{Enabled: &disabled, Effort: ThinkingEffortLow})
	require.ErrorIs(t, err, ErrInvalidRequest)
}
