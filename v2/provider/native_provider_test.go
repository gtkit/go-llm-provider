package provider

import (
	"bytes"
	"context"
	"encoding/base64"
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

func TestNewAnthropicProviderRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	p, err := NewAnthropicProvider(NativeProviderConfig{})
	require.ErrorIs(t, err, ErrInvalidProviderConfig)
	assert.Nil(t, p)
}

func TestAnthropicProviderChatMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "req-anthropic")
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

	temp := float32(0.2)
	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{
			SystemText("be concise"),
			UserMessage(TextPart("describe this"), ImageURLPart("https://example.com/cat.png")),
		},
		MaxTokens:   128,
		Temperature: &temp,
	})
	require.NoError(t, err)

	assert.Equal(t, "hello from claude", resp.Content)
	assert.Equal(t, "end_turn", resp.FinishReason)
	assert.Equal(t, Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}, resp.Usage)
	assert.Equal(t, ProviderAnthropic, resp.Metadata.Provider)
	assert.Equal(t, "claude-sonnet-4-5", resp.Metadata.Model)
	assert.Equal(t, "req-anthropic", resp.Metadata.RequestID)

	assert.Equal(t, "claude-sonnet-4-5", captured["model"])
	assert.Equal(t, "be concise", captured["system"])
	assert.InDelta(t, 128, captured["max_tokens"], 0.0001)
	assert.InDelta(t, 0.2, captured["temperature"], 0.0001)

	messages, ok := captured["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 1)
	first, ok := messages[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "user", first["role"])

	content, ok := first["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)
	assert.Equal(t, "text", content[0].(map[string]any)["type"])
	assert.Equal(t, "describe this", content[0].(map[string]any)["text"])
	imagePart, ok := content[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "image", imagePart["type"])
	source, ok := imagePart["source"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "url", source["type"])
	assert.Equal(t, "https://example.com/cat.png", source["url"])
	assert.NotContains(t, source, "media_type")
	assert.NotContains(t, source, "data")
}

func TestAnthropicProviderChatMapsFileAndCacheControl(t *testing.T) {
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
			"content": [{"type":"text","text":"read"}],
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

	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{
			UserMessage(
				WithCacheControl(TextPart("read this document"), CacheControlEphemeral()),
				FileDataPart([]byte("%PDF-1.7"), "application/pdf", "brief.pdf"),
			),
		},
	})
	require.NoError(t, err)

	messages, ok := captured["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 1)
	first, ok := messages[0].(map[string]any)
	require.True(t, ok)
	content, ok := first["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)

	textPart, ok := content[0].(map[string]any)
	require.True(t, ok)
	cacheControl, ok := textPart["cache_control"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ephemeral", cacheControl["type"])

	docPart, ok := content[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "document", docPart["type"])
	assert.Equal(t, "brief.pdf", docPart["title"])
	source, ok := docPart["source"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "base64", source["type"])
	assert.Equal(t, "application/pdf", source["media_type"])
	assert.Equal(t, "JVBERi0xLjc=", source["data"])
}

func TestAnthropicProviderChatMapsToolsAndToolCalls(t *testing.T) {
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
			"content": [{
				"type": "tool_use",
				"id": "toolu_1",
				"name": "get_weather",
				"input": {"city":"Paris"}
			}],
			"usage": {"input_tokens": 12, "output_tokens": 8}
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
		Messages: []Message{UserText("hi")},
		Tools: []Tool{{
			Function: FunctionDef{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters: ParamSchema{
					Type:       "object",
					Properties: map[string]ParamSchema{"city": {Type: "string"}},
					Required:   []string{"city"},
				},
			},
		}},
		ToolChoice: ToolChoiceFunction{Name: "get_weather"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "toolu_1", resp.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"city":"Paris"}`, resp.ToolCalls[0].Function.Arguments)

	tools, ok := captured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "get_weather", tool["name"])
	assert.Equal(t, "Get weather", tool["description"])
	assert.Contains(t, tool, "input_schema")
	toolChoice, ok := captured["tool_choice"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "tool", toolChoice["type"])
	assert.Equal(t, "get_weather", toolChoice["name"])
}

func TestAnthropicProviderChatMapsJSONSchemaResponseFormatToToolChoice(t *testing.T) {
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
			"content": [{
				"type": "tool_use",
				"id": "toolu_json",
				"name": "city_response",
				"input": {"city":"Paris"}
			}],
			"usage": {"input_tokens": 12, "output_tokens": 8}
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
		Messages: []Message{UserText("return city JSON")},
		ResponseFormat: JSONSchemaFormatStrict("city_response", ParamSchema{
			Type:       "object",
			Properties: map[string]ParamSchema{"city": {Type: "string"}},
			Required:   []string{"city"},
		}),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.JSONEq(t, `{"city":"Paris"}`, resp.Content)
	assert.Equal(t, "stop", resp.FinishReason)
	assert.Empty(t, resp.ToolCalls)

	tools, ok := captured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "city_response", tool["name"])
	assert.Contains(t, tool, "input_schema")
	toolChoice, ok := captured["tool_choice"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "tool", toolChoice["type"])
	assert.Equal(t, "city_response", toolChoice["name"])
}

func TestAnthropicProviderChatStreamMapsEvents(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
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
	// input_tokens 来自 message_start，output_tokens 来自 message_delta，随最终 chunk 给出。
	assert.Equal(t, Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}, third.Usage)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestAnthropicProviderChatStreamReportsCacheUsage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0,\"cache_creation_input_tokens\":20,\"cache_read_input_tokens\":30}}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
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
	// PromptTokens 归一化为 input + cache_read + cache_write。
	assert.Equal(t, Usage{
		PromptTokens:     60,
		CompletionTokens: 5,
		CacheReadTokens:  30,
		CacheWriteTokens: 20,
		TotalTokens:      65,
	}, final.Usage)
}

func TestAnthropicProviderChatMapsCacheUsage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": [{"type":"text","text":"cached"}],
			"usage": {"input_tokens": 10, "output_tokens": 7, "cache_creation_input_tokens": 20, "cache_read_input_tokens": 30}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)

	// PromptTokens 归一化为 input + cache_read + cache_write。
	assert.Equal(t, Usage{
		PromptTokens:     60,
		CompletionTokens: 7,
		CacheReadTokens:  30,
		CacheWriteTokens: 20,
		TotalTokens:      67,
	}, resp.Usage)
}

func TestAnthropicProviderChatStreamMapsToolUseDeltas(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_weather\",\"input\":{}}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"Paris\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
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
		Messages: []Message{UserText("hi")},
		Tools: []Tool{{
			Function: FunctionDef{Name: "get_weather"},
		}},
	})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	first, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, first.ToolCalls, 1)
	assert.Equal(t, 0, first.ToolCalls[0].Index)
	assert.Equal(t, "toolu_1", first.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", first.ToolCalls[0].Function.Name)

	second, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, second.ToolCalls, 1)
	assert.Equal(t, "{\"city\":", second.ToolCalls[0].Function.Arguments)

	third, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, third.ToolCalls, 1)
	assert.Equal(t, "\"Paris\"}", third.ToolCalls[0].Function.Arguments)

	fourth, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "tool_calls", fourth.FinishReason)

	assert.Contains(t, captured, "tools")
}

func TestAnthropicProviderErrorClassification(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"too many requests"}}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.Error(t, err)
	assert.Nil(t, resp)

	var providerErr *ProviderError
	require.ErrorAs(t, err, &providerErr)
	assert.Equal(t, ProviderAnthropic, providerErr.Provider)
	assert.Equal(t, ErrorCodeRateLimit, providerErr.Code)
	assert.Equal(t, "rate_limit_error", providerErr.RawType)
	assert.True(t, providerErr.Retryable)
	assert.ErrorIs(t, err, ErrRateLimit)
}

func TestGeminiProviderChatMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash:generateContent", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "req-gemini")
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

	topP := float32(0.9)
	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{
			SystemText("be brief"),
			UserMessage(TextPart("describe this"), ImageDataPart([]byte("raw-image"), "image/png")),
		},
		MaxTokens: 64,
		TopP:      &topP,
	})
	require.NoError(t, err)

	assert.Equal(t, "hello from gemini", resp.Content)
	assert.Equal(t, "STOP", resp.FinishReason)
	assert.Equal(t, Usage{PromptTokens: 5, CompletionTokens: 6, TotalTokens: 11}, resp.Usage)
	assert.Equal(t, ProviderGemini, resp.Metadata.Provider)
	assert.Equal(t, "gemini-2.5-flash", resp.Metadata.Model)
	assert.Equal(t, "req-gemini", resp.Metadata.RequestID)

	systemInstruction, ok := captured["systemInstruction"].(map[string]any)
	require.True(t, ok)
	parts, ok := systemInstruction["parts"].([]any)
	require.True(t, ok)
	assert.Equal(t, "be brief", parts[0].(map[string]any)["text"])

	contents, ok := captured["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contents, 1)
	first, ok := contents[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "user", first["role"])
	contentParts, ok := first["parts"].([]any)
	require.True(t, ok)
	require.Len(t, contentParts, 2)
	assert.Equal(t, "describe this", contentParts[0].(map[string]any)["text"])
	inlineData, ok := contentParts[1].(map[string]any)["inline_data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "image/png", inlineData["mime_type"])
	assert.NotEmpty(t, inlineData["data"])
}

func TestGeminiProviderChatMapsFileAndCandidateCount(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[{"text":"read"}]},
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

	seed := 7
	_, err = p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{
			UserMessage(
				TextPart("read this"),
				FileDataPart([]byte("%PDF-1.7"), "application/pdf", "brief.pdf"),
			),
		},
		Seed:           &seed,
		CandidateCount: 2,
	})
	require.NoError(t, err)

	generation, ok := captured["generationConfig"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 2, generation["candidateCount"], 0.0001)
	assert.InDelta(t, 7, generation["seed"], 0.0001)

	contents, ok := captured["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contents, 1)
	first, ok := contents[0].(map[string]any)
	require.True(t, ok)
	parts, ok := first["parts"].([]any)
	require.True(t, ok)
	require.Len(t, parts, 2)
	inlineData, ok := parts[1].(map[string]any)["inline_data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "application/pdf", inlineData["mime_type"])
	assert.Equal(t, "JVBERi0xLjc=", inlineData["data"])
}

func TestGeminiProviderChatMapsToolsAndToolCalls(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[{
					"functionCall": {"id":"call_gemini_1","name":"get_weather","args":{"city":"Paris"}}
				}]},
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
		Messages: []Message{UserText("hi")},
		Tools: []Tool{{
			Function: FunctionDef{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters: ParamSchema{
					Type:       "object",
					Properties: map[string]ParamSchema{"city": {Type: "string"}},
					Required:   []string{"city"},
				},
			},
		}},
		ToolChoice: ToolChoiceFunction{Name: "get_weather"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_gemini_1", resp.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"city":"Paris"}`, resp.ToolCalls[0].Function.Arguments)

	tools, ok := captured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	declarations, ok := tool["functionDeclarations"].([]any)
	require.True(t, ok)
	require.Len(t, declarations, 1)
	declaration, ok := declarations[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "get_weather", declaration["name"])
	assert.Equal(t, "Get weather", declaration["description"])
	assert.Contains(t, declaration, "parameters")

	toolConfig, ok := captured["toolConfig"].(map[string]any)
	require.True(t, ok)
	functionConfig, ok := toolConfig["functionCallingConfig"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ANY", functionConfig["mode"])
	assert.Equal(t, []any{"get_weather"}, functionConfig["allowedFunctionNames"])
}

func TestGeminiProviderChatMapsJSONSchemaResponseFormat(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[{"text":"{\"city\":\"Paris\"}"}]},
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
		Messages: []Message{UserText("return city JSON")},
		ResponseFormat: JSONSchemaFormatStrict("city_response", ParamSchema{
			Type:       "object",
			Properties: map[string]ParamSchema{"city": {Type: "string"}},
			Required:   []string{"city"},
		}),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"city":"Paris"}`, resp.Content)

	generation, ok := captured["generationConfig"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "application/json", generation["responseMimeType"])
	schema, ok := generation["responseSchema"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", schema["type"])
	assert.NotContains(t, generation, "name")
	assert.NotContains(t, generation, "strict")
}

func TestGeminiProviderChatStreamMapsSSE(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", r.URL.Path)
		assert.Equal(t, "sse", r.URL.Query().Get("alt"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":1,\"totalTokenCount\":6}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\" world\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":4,\"thoughtsTokenCount\":2,\"cachedContentTokenCount\":3,\"totalTokenCount\":11}}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	first, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "hello", first.Delta)

	second, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, " world", second.Delta)
	assert.Equal(t, "STOP", second.FinishReason)
	// 中间 chunk 不携带 usage，最终 chunk 给出归一化后的完整统计：
	// CompletionTokens 含 thoughtsTokenCount，CacheReadTokens 取 cachedContentTokenCount。
	assert.Equal(t, Usage{}, first.Usage)
	assert.Equal(t, Usage{
		PromptTokens:     5,
		CompletionTokens: 6,
		ReasoningTokens:  2,
		CacheReadTokens:  3,
		TotalTokens:      11,
	}, second.Usage)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestGeminiProviderChatMapsThoughtsAndCacheUsage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[{"text":"thought out"}]},
				"finishReason": "STOP"
			}],
			"usageMetadata": {
				"promptTokenCount": 10,
				"candidatesTokenCount": 4,
				"thoughtsTokenCount": 6,
				"cachedContentTokenCount": 3,
				"totalTokenCount": 20
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)

	// CompletionTokens 归一化为 candidates + thoughts，与其他 provider 语义一致。
	assert.Equal(t, Usage{
		PromptTokens:     10,
		CompletionTokens: 10,
		ReasoningTokens:  6,
		CacheReadTokens:  3,
		TotalTokens:      20,
	}, resp.Usage)
}

func TestGeminiProviderImageOutput(t *testing.T) {
	t.Parallel()

	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47}
	pngBase64 := base64.StdEncoding.EncodeToString(pngBytes)

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[
					{"text":"这是生成的图片"},
					{"inline_data":{"mime_type":"image/png","data":"` + pngBase64 + `"}}
				]},
				"finishReason": "STOP"
			}]
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.0-flash-preview-image-generation",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages:         []Message{UserText("画一只猫")},
		OutputModalities: []Modality{ModalityText, ModalityImage},
	})
	require.NoError(t, err)

	// 请求侧带 responseModalities。
	generation, ok := captured["generationConfig"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"TEXT", "IMAGE"}, generation["responseModalities"])

	// 响应侧：文本进 Content，图像进 Parts（base64 已解码）。
	assert.Equal(t, "这是生成的图片", resp.Content)
	require.Len(t, resp.Parts, 1)
	assert.Equal(t, pngBytes, resp.Parts[0].ImageData)
	assert.Equal(t, "image/png", resp.Parts[0].MIMEType)

	// AssistantMessage 并入图像，可直接回传对话历史。
	msg := resp.AssistantMessage()
	require.Len(t, msg.Content, 2)
	assert.Equal(t, pngBytes, msg.Content[1].ImageData)
}

func TestGeminiProviderChatStreamMapsImageOutput(t *testing.T) {
	t.Parallel()

	imgBase64 := base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"inline_data\":{\"mime_type\":\"image/jpeg\",\"data\":\""+imgBase64+"\"}}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.0-flash-preview-image-generation",
	})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages:         []Message{UserText("画一只猫")},
		OutputModalities: []Modality{ModalityImage},
	})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, chunk.Parts, 1)
	assert.Equal(t, []byte{0xFF, 0xD8}, chunk.Parts[0].ImageData)
	assert.Equal(t, "image/jpeg", chunk.Parts[0].MIMEType)
	assert.Equal(t, "STOP", chunk.FinishReason)
}

func TestOutputModalitiesRejectedByTextOnlyProviders(t *testing.T) {
	t.Parallel()

	req := &ChatRequest{
		Messages:         []Message{UserText("画一只猫")},
		OutputModalities: []Modality{ModalityImage},
	}

	anthropic, err := NewAnthropicProvider(NativeProviderConfig{APIKey: "k", Model: "claude-sonnet-4-5"})
	require.NoError(t, err)
	_, err = anthropic.Chat(t.Context(), req)
	require.ErrorIs(t, err, ErrInvalidRequest)

	openaiCompat, err := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "k", Model: "deepseek-chat"})
	require.NoError(t, err)
	_, err = openaiCompat.Chat(t.Context(), req)
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestGeminiProviderChatStreamMapsToolCalls(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"call_1\",\"name\":\"get_weather\",\"args\":{\"city\":\"Paris\"}}}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	stream, err := p.ChatStream(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools: []Tool{{
			Function: FunctionDef{Name: "get_weather"},
		}},
	})
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()

	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, chunk.ToolCalls, 1)
	assert.Equal(t, "call_1", chunk.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", chunk.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"city":"Paris"}`, chunk.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "tool_calls", chunk.FinishReason)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestGeminiProviderErrorClassification(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"permission denied","status":"PERMISSION_DENIED"}}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.Error(t, err)
	assert.Nil(t, resp)

	var providerErr *ProviderError
	require.ErrorAs(t, err, &providerErr)
	assert.Equal(t, ProviderGemini, providerErr.Provider)
	assert.Equal(t, ErrorCodeAuth, providerErr.Code)
	assert.Equal(t, "PERMISSION_DENIED", providerErr.RawType)
	assert.False(t, providerErr.Retryable)
	assert.ErrorIs(t, err, ErrAuth)
}

func TestGeminiEmbedderEmbedMapsSingleRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-embedding-001:embedContent", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "req-gemini-embedding")
		_, _ = w.Write([]byte(`{"embedding":{"values":[0.1,0.2,0.3]}}`))
	}))
	t.Cleanup(srv.Close)

	e, err := NewGeminiEmbedder(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
	})
	require.NoError(t, err)

	dimensions := 256
	resp, err := e.Embed(t.Context(), &EmbeddingRequest{
		Input:      []string{"hello"},
		Dimensions: &dimensions,
	})
	require.NoError(t, err)

	require.Len(t, resp.Data, 1)
	assert.Equal(t, 0, resp.Data[0].Index)
	require.Len(t, resp.Data[0].Vector, 3)
	assert.InDelta(t, 0.1, resp.Data[0].Vector[0], 0.0001)
	assert.InDelta(t, 0.2, resp.Data[0].Vector[1], 0.0001)
	assert.InDelta(t, 0.3, resp.Data[0].Vector[2], 0.0001)
	assert.Equal(t, defaultGeminiEmbeddingModel, resp.Model)
	assert.Equal(t, ProviderGemini, resp.Metadata.Provider)
	assert.Equal(t, defaultGeminiEmbeddingModel, resp.Metadata.Model)
	assert.Equal(t, "req-gemini-embedding", resp.Metadata.RequestID)

	assert.Equal(t, "models/gemini-embedding-001", captured["model"])
	assert.InDelta(t, 256, captured["outputDimensionality"], 0.0001)
	content, ok := captured["content"].(map[string]any)
	require.True(t, ok)
	parts, ok := content["parts"].([]any)
	require.True(t, ok)
	require.Len(t, parts, 1)
	assert.Equal(t, "hello", parts[0].(map[string]any)["text"])
}

func TestGeminiEmbedderEmbedMapsBatchRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-embedding-001:batchEmbedContents", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[{"values":[0.1]},{"values":[0.2]}]}`))
	}))
	t.Cleanup(srv.Close)

	e, err := NewGeminiEmbedder(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
	})
	require.NoError(t, err)

	resp, err := e.Embed(t.Context(), &EmbeddingRequest{Input: []string{"first", "second"}})
	require.NoError(t, err)

	require.Len(t, resp.Data, 2)
	assert.Equal(t, 0, resp.Data[0].Index)
	assert.Equal(t, 1, resp.Data[1].Index)
	require.Len(t, resp.Data[0].Vector, 1)
	require.Len(t, resp.Data[1].Vector, 1)
	assert.InDelta(t, 0.1, resp.Data[0].Vector[0], 0.0001)
	assert.InDelta(t, 0.2, resp.Data[1].Vector[0], 0.0001)

	requests, ok := captured["requests"].([]any)
	require.True(t, ok)
	require.Len(t, requests, 2)
	first, ok := requests[0].(map[string]any)
	require.True(t, ok)
	second, ok := requests[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "models/gemini-embedding-001", first["model"])
	assert.Equal(t, "models/gemini-embedding-001", second["model"])
	assert.Equal(t, "first", first["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"])
	assert.Equal(t, "second", second["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"])
}

func TestGeminiEmbedderErrorHandling(t *testing.T) {
	t.Parallel()

	_, err := NewGeminiEmbedder(NativeProviderConfig{})
	require.ErrorIs(t, err, ErrInvalidProviderConfig)

	e, err := NewGeminiEmbedder(NativeProviderConfig{
		APIKey: "test-key",
	})
	require.NoError(t, err)

	_, err = e.Embed(t.Context(), nil)
	require.ErrorIs(t, err, ErrNilEmbeddingRequest)

	_, err = e.Embed(t.Context(), &EmbeddingRequest{})
	require.ErrorIs(t, err, ErrEmptyEmbeddingInput)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"bad key","status":"UNAUTHENTICATED"}}`))
	}))
	t.Cleanup(srv.Close)

	e, err = NewGeminiEmbedder(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
	})
	require.NoError(t, err)

	resp, err := e.Embed(t.Context(), &EmbeddingRequest{Input: []string{"hello"}})
	require.Error(t, err)
	assert.Nil(t, resp)

	var providerErr *ProviderError
	require.ErrorAs(t, err, &providerErr)
	assert.Equal(t, ProviderGemini, providerErr.Provider)
	assert.Equal(t, ErrorCodeAuth, providerErr.Code)
	assert.Equal(t, "UNAUTHENTICATED", providerErr.RawType)
	assert.ErrorIs(t, err, ErrAuth)
}

func TestNewProviderFromPresetCreatesNativeProvidersAndXAI(t *testing.T) {
	t.Parallel()

	anthropic, err := NewProviderFromPreset(ProviderAnthropic, "test-key", "")
	require.NoError(t, err)
	assert.Equal(t, ProviderAnthropic, anthropic.Name())
	assert.IsType(t, &anthropicProvider{}, anthropic)

	gemini, err := NewProviderFromPreset(ProviderGemini, "test-key", "")
	require.NoError(t, err)
	assert.Equal(t, ProviderGemini, gemini.Name())
	assert.IsType(t, &geminiProvider{}, gemini)

	xai, err := NewProviderFromPreset(ProviderXAI, "test-key", "")
	require.NoError(t, err)
	assert.Equal(t, ProviderXAI, xai.Name())
	assert.IsType(t, &openaiProvider{}, xai)
}

func TestDoNativeStream(t *testing.T) {
	t.Parallel()

	t.Run("opens stream request with metadata model", func(t *testing.T) {
		t.Parallel()

		client := &recordingNativeStreamClient{
			response: &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString("data: {\"ok\":true}\n\n")),
			},
		}

		reader, _, err := doNativeStream(t.Context(), client, nativeHTTPRequest{
			Method:   http.MethodPost,
			URL:      "https://example.test/v1/messages",
			Body:     map[string]string{"message": "hello"},
			Provider: ProviderAnthropic,
			Model:    "claude-sonnet-4-5",
			SetHeaders: func(req *http.Request) {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("x-test", "yes")
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, reader.Close()) })

		require.NotNil(t, client.request)
		assert.Equal(t, "text/event-stream", client.request.Header.Get("Accept"))
		assert.Equal(t, "application/json", client.request.Header.Get("Content-Type"))
		assert.Equal(t, "yes", client.request.Header.Get("x-test"))

		data, err := reader.Next()
		require.NoError(t, err)
		assert.JSONEq(t, `{"ok":true}`, string(data))
	})

	t.Run("wraps transport error", func(t *testing.T) {
		t.Parallel()

		reader, _, err := doNativeStream(t.Context(), failingHTTPDoer{err: assert.AnError}, nativeHTTPRequest{
			Method:   http.MethodPost,
			URL:      "https://example.test/v1/messages",
			Body:     map[string]string{"message": "hello"},
			Provider: ProviderAnthropic,
		})
		require.Error(t, err)
		assert.Nil(t, reader)
		require.ErrorIs(t, err, assert.AnError)

		var providerErr *ProviderError
		require.ErrorAs(t, err, &providerErr)
		assert.Equal(t, ErrorCodeNetwork, providerErr.Code)
		assert.Equal(t, ProviderAnthropic, providerErr.Provider)
	})

	t.Run("decodes non success and closes body", func(t *testing.T) {
		t.Parallel()

		body := &trackingReadCloser{Buffer: bytes.NewBufferString(`{"error":{"type":"overloaded_error","message":"busy"}}`)}
		client := &recordingNativeStreamClient{
			response: &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Status:     "503 Service Unavailable",
				Header:     make(http.Header),
				Body:       body,
			},
		}

		reader, _, err := doNativeStream(t.Context(), client, nativeHTTPRequest{
			Method:      http.MethodPost,
			URL:         "https://example.test/v1/messages",
			Body:        map[string]string{"message": "hello"},
			Provider:    ProviderAnthropic,
			Model:       "claude-sonnet-4-5",
			DecodeError: decodeAnthropicError,
		})
		require.Error(t, err)
		assert.Nil(t, reader)
		assert.True(t, body.closed)
		require.ErrorIs(t, err, ErrServerError)
	})
}

func TestNativeProviderTransportErrorIsWrapped(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("transport down")
	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:     "test-key",
		BaseURL:    "https://example.invalid",
		Model:      "claude-sonnet-4-5",
		HTTPClient: failingHTTPDoer{err: transportErr},
	})
	require.NoError(t, err)

	resp, err := p.Chat(context.Background(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.Error(t, err)
	assert.Nil(t, resp)
	require.ErrorIs(t, err, transportErr)

	var providerErr *ProviderError
	require.ErrorAs(t, err, &providerErr)
	assert.Equal(t, ErrorCodeNetwork, providerErr.Code)
	assert.True(t, providerErr.Retryable)
}

func TestNativeProviderUnsupportedRoleReturnsError(t *testing.T) {
	t.Parallel()

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: "http://127.0.0.1:1",
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{{Role: Role("developer"), Content: []ContentPart{TextPart("hi")}}},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "developer")
}

func TestGeminiProviderChatMapsToolResultMessages(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role":"model","parts":[{"text":"done"}]},
				"finishReason": "STOP"
			}],
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
		Messages: []Message{
			UserText("call tool"),
			{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{{
					ID:       "call_1",
					Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`},
				}},
			},
			ToolResultMessage("call_1", `{"temperature":20}`),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)

	contents, ok := captured["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contents, 3)
	toolContent, ok := contents[2].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "function", toolContent["role"])
	parts, ok := toolContent["parts"].([]any)
	require.True(t, ok)
	require.Len(t, parts, 1)
	response, ok := parts[0].(map[string]any)["functionResponse"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "call_1", response["id"])
	assert.Equal(t, "call_1", response["name"])
}

func TestSSEReaderSkipsInvalidJSONAndComments(t *testing.T) {
	t.Parallel()

	reader := newSSEReader(io.NopCloser(strings.NewReader(": keepalive\n\ndata: {bad}\n\ndata: {\"ok\":true}\n\n")))
	defer func() { assert.NoError(t, reader.Close()) }()

	data, err := reader.Next()
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(data))
}

func TestAnthropicProviderMapsInlineImageDataURL(t *testing.T) {
	t.Parallel()

	parts, err := anthropicContentParts([]ContentPart{
		ImageURLPart("data:image/png;base64,cmF3LWltYWdl"),
	})
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.NotNil(t, parts[0].Source)
	assert.Equal(t, "base64", parts[0].Source.Type)
	assert.Equal(t, "image/png", parts[0].Source.MediaType)
	assert.Equal(t, "cmF3LWltYWdl", parts[0].Source.Data)
}

func TestGeminiProviderMapsInlineImageDataURL(t *testing.T) {
	t.Parallel()

	parts, err := geminiParts([]ContentPart{
		ImageURLPart("data:image/png;base64,cmF3LWltYWdl"),
	})
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.NotNil(t, parts[0].InlineData)
	assert.Equal(t, "image/png", parts[0].InlineData.MIMEType)
	assert.Equal(t, "cmF3LWltYWdl", parts[0].InlineData.Data)
}

func TestNativeProviderRejectsInvalidImageParts(t *testing.T) {
	t.Parallel()

	_, err := anthropicContentParts([]ContentPart{ImageDataPart([]byte("raw"), "")})
	require.ErrorIs(t, err, ErrInvalidRequest)

	_, err = geminiParts([]ContentPart{ImageURLPart("https://example.com/cat.png")})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

type failingHTTPDoer struct {
	err error
}

func (f failingHTTPDoer) Do(*http.Request) (*http.Response, error) {
	return nil, f.err
}

type recordingNativeStreamClient struct {
	request  *http.Request
	response *http.Response
}

func (c *recordingNativeStreamClient) Do(req *http.Request) (*http.Response, error) {
	c.request = req
	return c.response, nil
}

type trackingReadCloser struct {
	*bytes.Buffer
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}
