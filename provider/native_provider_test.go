package provider

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAnthropicProviderV1ChatMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": [{"type":"text","text":"hello from claude"}],
			"usage": {"input_tokens": 11, "output_tokens": 7}
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
		Messages:  []Message{{Role: RoleSystem, Content: "be concise"}, {Role: RoleUser, Content: "hi"}},
		MaxTokens: 128,
	})
	require.NoError(t, err)
	assert.Equal(t, "hello from claude", resp.Content)
	assert.Equal(t, "end_turn", resp.FinishReason)
	assert.Equal(t, Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}, resp.Usage)
	assert.Equal(t, "claude-sonnet-4-5", captured["model"])
	assert.Equal(t, "be concise", captured["system"])
	assert.InDelta(t, 128, captured["max_tokens"], 0.0001)
}

func TestNewGeminiProviderV1ChatMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash:generateContent", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[{"text":"hello from gemini"}]},
				"finishReason": "STOP"
			}],
			"usageMetadata": {
				"promptTokenCount": 5,
				"candidatesTokenCount": 6,
				"totalTokenCount": 11
			},
			"modelVersion": "gemini-2.5-flash"
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{{Role: RoleSystem, Content: "be brief"}, {Role: RoleUser, Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hello from gemini", resp.Content)
	assert.Equal(t, "STOP", resp.FinishReason)
	assert.Equal(t, Usage{PromptTokens: 5, CompletionTokens: 6, TotalTokens: 11}, resp.Usage)

	systemInstruction, ok := captured["systemInstruction"].(map[string]any)
	require.True(t, ok)
	parts, ok := systemInstruction["parts"].([]any)
	require.True(t, ok)
	assert.Equal(t, "be brief", parts[0].(map[string]any)["text"])
}

func TestNativeProviderV1ChatStreamMapsEvents(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\" world\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	first, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "hello", first.Delta)

	second, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, " world", second.Delta)
	assert.Equal(t, "STOP", second.FinishReason)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestNewProviderFromPresetV1CreatesNativeProviders(t *testing.T) {
	t.Parallel()

	anthropic, err := NewProviderFromPreset(ProviderAnthropic, "test-key", "")
	require.NoError(t, err)
	assert.Equal(t, ProviderAnthropic, anthropic.Name())
	assert.IsType(t, &anthropicProvider{}, anthropic)

	gemini, err := NewProviderFromPreset(ProviderGemini, "test-key", "")
	require.NoError(t, err)
	assert.Equal(t, ProviderGemini, gemini.Name())
	assert.IsType(t, &geminiProvider{}, gemini)
}

func TestNativeProviderV1MapsToolCallsAndToolResults(t *testing.T) {
	t.Parallel()

	t.Run("anthropic tools", func(t *testing.T) {
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
				"stop_reason": "tool_use",
				"content": [{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}],
				"usage": {"input_tokens": 3, "output_tokens": 4}
			}`))
		}))
		t.Cleanup(srv.Close)

		p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL, Model: "claude-sonnet-4-5"})
		require.NoError(t, err)

		resp, err := p.Chat(t.Context(), &ChatRequest{
			Messages: []Message{{Role: RoleUser, Content: "weather"}},
			Tools: []Tool{{Function: FunctionDef{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  ParamSchema{Type: "object", Properties: map[string]ParamSchema{"city": {Type: "string"}}},
			}}},
			ToolChoice: ToolChoiceFunction{Name: "get_weather"},
		})
		require.NoError(t, err)
		assert.Equal(t, "tool_calls", resp.FinishReason)
		require.Len(t, resp.ToolCalls, 1)
		assert.JSONEq(t, `{"city":"Paris"}`, resp.ToolCalls[0].Function.Arguments)

		tools := captured["tools"].([]any)
		assert.Equal(t, "get_weather", tools[0].(map[string]any)["name"])
		choice := captured["tool_choice"].(map[string]any)
		assert.Equal(t, "tool", choice["type"])
	})

	t.Run("gemini tools and tool result", func(t *testing.T) {
		t.Parallel()

		var captured map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"candidates": [{
					"content": {"role":"model","parts":[{"functionCall":{"id":"call_1","name":"get_weather","args":{"city":"Paris"}}}]},
					"finishReason": "STOP"
				}]
			}`))
		}))
		t.Cleanup(srv.Close)

		p, err := NewGeminiProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL + "/v1beta", Model: "gemini-2.5-flash"})
		require.NoError(t, err)

		resp, err := p.Chat(t.Context(), &ChatRequest{
			Messages: []Message{
				{Role: RoleUser, Content: "weather"},
				{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_prev", Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`}}}},
				ToolResultMessage("call_prev", `{"temperature":20}`),
			},
			Tools:      []Tool{{Function: FunctionDef{Name: "get_weather", Parameters: ParamSchema{Type: "object"}}}},
			ToolChoice: ToolChoiceRequired,
		})
		require.NoError(t, err)
		assert.Equal(t, "tool_calls", resp.FinishReason)
		require.Len(t, resp.ToolCalls, 1)
		assert.JSONEq(t, `{"city":"Paris"}`, resp.ToolCalls[0].Function.Arguments)

		contents := captured["contents"].([]any)
		require.Len(t, contents, 3)
		assert.Equal(t, "function", contents[2].(map[string]any)["role"])
		assert.Contains(t, captured, "tools")
		assert.Contains(t, captured, "toolConfig")
	})
}

func TestNativeProviderV1AnthropicStreamAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("stream maps events", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: message_start\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4-5\"}}\n\n")
			_, _ = fmt.Fprint(w, "event: content_block_delta\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
			_, _ = fmt.Fprint(w, "event: message_delta\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n")
			_, _ = fmt.Fprint(w, "event: message_stop\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
		}))
		t.Cleanup(srv.Close)

		p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL, Model: "claude-sonnet-4-5"})
		require.NoError(t, err)

		stream, err := p.ChatStream(t.Context(), &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		require.NoError(t, err)
		defer func() { assert.NoError(t, stream.Close()) }()

		first, err := stream.Recv()
		require.NoError(t, err)
		assert.Empty(t, first.Delta)
		second, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(t, "hello", second.Delta)
		third, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(t, "end_turn", third.FinishReason)
		_, err = stream.Recv()
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("error classification", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"too many requests"}}`))
		}))
		t.Cleanup(srv.Close)

		p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL, Model: "claude-sonnet-4-5"})
		require.NoError(t, err)

		resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		require.Error(t, err)
		assert.Nil(t, resp)
		require.ErrorIs(t, err, ErrRateLimit)
		var providerErr *ProviderError
		require.ErrorAs(t, err, &providerErr)
		assert.Equal(t, ProviderAnthropic, providerErr.Provider)
		assert.Equal(t, ErrorCodeRateLimit, providerErr.Code)
	})
}

func TestNativeProviderV1AdditionalBranches(t *testing.T) {
	t.Parallel()

	t.Run("invalid config and nil names", func(t *testing.T) {
		t.Parallel()

		anthropic, err := NewAnthropicProvider(NativeProviderConfig{})
		require.ErrorIs(t, err, ErrInvalidProviderConfig)
		assert.Nil(t, anthropic)

		gemini, err := NewGeminiProvider(NativeProviderConfig{})
		require.ErrorIs(t, err, ErrInvalidProviderConfig)
		assert.Nil(t, gemini)

		var ap *anthropicProvider
		assert.Empty(t, ap.Name())
		var gp *geminiProvider
		assert.Empty(t, gp.Name())
	})

	t.Run("tool choices and invalid roles", func(t *testing.T) {
		t.Parallel()

		p, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "test-key", Model: "claude-sonnet-4-5"})
		require.NoError(t, err)
		anthropic := p.(*anthropicProvider)
		_, err = anthropic.buildRequest(&ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}, ToolChoice: ToolChoiceMode("bad")}, false)
		require.ErrorIs(t, err, ErrInvalidToolChoice)
		_, err = anthropic.buildRequest(&ChatRequest{Messages: []Message{{Role: Role("developer"), Content: "hi"}}}, false)
		require.ErrorIs(t, err, ErrInvalidRequest)

		p, err = NewGeminiProvider(NativeProviderConfig{APIKey: "test-key", Model: "gemini-2.5-flash"})
		require.NoError(t, err)
		gemini := p.(*geminiProvider)
		_, _, err = gemini.buildRequest(&ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}, ToolChoice: ToolChoiceFunction{}}, false)
		require.ErrorIs(t, err, ErrInvalidToolChoice)
		_, _, err = gemini.buildRequest(&ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}, Tools: []Tool{{Function: FunctionDef{Name: "f"}}}}, true)
		require.ErrorIs(t, err, ErrInvalidRequest)
	})

	t.Run("gemini error classification", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"permission denied","status":"PERMISSION_DENIED"}}`))
		}))
		t.Cleanup(srv.Close)

		p, err := NewGeminiProvider(NativeProviderConfig{APIKey: "test-key", BaseURL: srv.URL + "/v1beta", Model: "gemini-2.5-flash"})
		require.NoError(t, err)

		resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		require.Error(t, err)
		assert.Nil(t, resp)
		require.ErrorIs(t, err, ErrAuth)
		var providerErr *ProviderError
		require.ErrorAs(t, err, &providerErr)
		assert.Equal(t, ProviderGemini, providerErr.Provider)
		assert.Equal(t, ErrorCodeAuth, providerErr.Code)
	})

	t.Run("transport error is wrapped", func(t *testing.T) {
		t.Parallel()

		p, err := NewGeminiProvider(NativeProviderConfig{
			APIKey:     "test-key",
			Model:      "gemini-2.5-flash",
			HTTPClient: failingHTTPDoer{err: assert.AnError},
		})
		require.NoError(t, err)

		resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		require.Error(t, err)
		assert.Nil(t, resp)
		require.ErrorIs(t, err, assert.AnError)
		var providerErr *ProviderError
		require.ErrorAs(t, err, &providerErr)
		assert.Equal(t, ErrorCodeNetwork, providerErr.Code)
	})

	t.Run("helpers and stream parser", func(t *testing.T) {
		t.Parallel()

		_, err := rawJSONArgument("{bad")
		require.Error(t, err)

		assert.Equal(t, ErrorCodeServerError, anthropicErrorCode(http.StatusInternalServerError, "overloaded_error"))
		assert.Equal(t, ErrorCodeInvalidRequest, geminiErrorCode(http.StatusBadRequest, "INVALID_ARGUMENT"))

		chunk, ok, err := anthropicStreamChunk([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`))
		require.Error(t, err)
		assert.Nil(t, chunk)
		assert.False(t, ok)

		chunk, ok, err = geminiStreamChunk([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{}}}]}}]}`))
		require.ErrorIs(t, err, ErrInvalidRequest)
		assert.Nil(t, chunk)
		assert.False(t, ok)
	})
}

type failingHTTPDoer struct {
	err error
}

func (f failingHTTPDoer) Do(*http.Request) (*http.Response, error) {
	return nil, f.err
}
