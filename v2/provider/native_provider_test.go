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
	// system 以内容块数组下发，才能承载片段级 cache_control。
	system, ok := captured["system"].([]any)
	require.True(t, ok)
	require.Len(t, system, 1)
	assert.Equal(t, "be concise", system[0].(map[string]any)["text"])
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

// TestAnthropicProviderChatMapsJSONSchemaResponseFormatToOutputConfig 锁定结构化输出走
// 平台原生 output_config.format 而非"强制调用假工具"：新模型拒绝强制工具调用，
// 且原生格式不占用 tools / tool_choice，可与真正的工具调用并存。
func TestAnthropicProviderChatMapsJSONSchemaResponseFormatToOutputConfig(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-5",
			"stop_reason": "end_turn",
			"content": [{"type":"text","text":"{\"city\":\"Paris\"}"}],
			"usage": {"input_tokens": 12, "output_tokens": 8}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-5",
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
	assert.Equal(t, "end_turn", resp.FinishReason)
	assert.Empty(t, resp.ToolCalls)

	assert.NotContains(t, captured, "tools")
	assert.NotContains(t, captured, "tool_choice")
	outputConfig, ok := captured["output_config"].(map[string]any)
	require.True(t, ok)
	format, ok := outputConfig["format"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "json_schema", format["type"])
	schema, ok := format["schema"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, []any{"city"}, schema["required"])
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
	// 走标准 JSON Schema 字段，而非只认 OpenAPI 子集的 responseSchema。
	assert.NotContains(t, generation, "responseSchema")
	schema, ok := generation["responseJsonSchema"].(map[string]any)
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
	// 工具结果未带 ToolName 时，从同一请求内的 assistant 工具调用按 ID 反查函数名：
	// Gemini 同时校验 id 与 name，name 填成调用 ID 会被拒绝。
	assert.Equal(t, "get_weather", response["name"])
}

// TestGeminiToolResponseNameResolution 覆盖 functionResponse.name 的三种来源：
// 显式 ToolName 优先，其次按 ToolCallID 反查，都没有时拒绝而非发出平台必拒的请求。
func TestGeminiToolResponseNameResolution(t *testing.T) {
	t.Parallel()

	call := ToolCall{ID: "call_1", Function: FunctionCall{Name: "get_weather", Arguments: `{}`}}
	assistant := Message{Role: RoleAssistant, ToolCalls: []ToolCall{call}}

	t.Run("显式 ToolName 优先", func(t *testing.T) {
		t.Parallel()
		out := geminiRequest{}
		msg := ToolResultMessageFor(call, `{"ok":true}`)
		require.NoError(t, fillGeminiContents(&out, []Message{msg}))
		require.Len(t, out.Contents, 1)
		assert.Equal(t, "get_weather", out.Contents[0].Parts[0].FunctionResponse.Name)
	})

	t.Run("按 ToolCallID 反查", func(t *testing.T) {
		t.Parallel()
		out := geminiRequest{}
		require.NoError(t, fillGeminiContents(&out, []Message{assistant, ToolResultMessage("call_1", "ok")}))
		require.Len(t, out.Contents, 2)
		assert.Equal(t, "get_weather", out.Contents[1].Parts[0].FunctionResponse.Name)
	})

	t.Run("无法确定函数名时拒绝", func(t *testing.T) {
		t.Parallel()
		out := geminiRequest{}
		err := fillGeminiContents(&out, []Message{ToolResultMessage("call_unknown", "ok")})
		require.ErrorIs(t, err, ErrInvalidRequest)
		assert.Contains(t, err.Error(), "ToolResultMessageFor")
	})
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

// TestUsageFromAnthropicCacheWriteTiers 覆盖缓存写入分档明细的归一化。
// 归一化层的契约是"产出自洽数据"：无论平台给的总数与明细是否一致，
// 输出都必须满足 CacheWrite5m + CacheWrite1h <= CacheWriteTokens，
// 调用方可直接使用这些原始用量事实。
func TestUsageFromAnthropicCacheWriteTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantPrompt int
		wantWrite  int
		want5m     int
		want1h     int
		wantRead   int
	}{
		{
			name:       "总数与明细一致",
			body:       `{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":2000,"cache_read_input_tokens":500,"cache_creation":{"ephemeral_5m_input_tokens":1500,"ephemeral_1h_input_tokens":500}}`,
			wantPrompt: 2600, // 100 + 500 + 2000
			wantWrite:  2000,
			want5m:     1500,
			want1h:     500,
			wantRead:   500,
		},
		{
			name:       "只有明细未给总数：总数由明细之和兜底",
			body:       `{"input_tokens":100,"output_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":1500,"ephemeral_1h_input_tokens":500}}`,
			wantPrompt: 2100, // 100 + 0 + 2000
			wantWrite:  2000,
			want5m:     1500,
			want1h:     500,
		},
		{
			name:       "只有总数未给明细（旧模型）：两档为 0，整体按总档计价",
			body:       `{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":2000,"cache_read_input_tokens":500}`,
			wantPrompt: 2600,
			wantWrite:  2000,
			wantRead:   500,
		},
		{
			name:       "总数小于明细之和：取较大值，保证子集关系不破裂",
			body:       `{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":1500,"ephemeral_1h_input_tokens":500}}`,
			wantPrompt: 2100,
			wantWrite:  2000,
			want5m:     1500,
			want1h:     500,
		},
		{
			name:       "cache_creation 为空对象",
			body:       `{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":300,"cache_creation":{}}`,
			wantPrompt: 400,
			wantWrite:  300,
		},
		{
			name:       "无缓存",
			body:       `{"input_tokens":100,"output_tokens":20}`,
			wantPrompt: 100,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var raw anthropicUsage
			require.NoError(t, json.Unmarshal([]byte(tc.body), &raw))

			usage := usageFromAnthropic(raw)
			assert.Equal(t, tc.wantPrompt, usage.PromptTokens)
			assert.Equal(t, tc.wantWrite, usage.CacheWriteTokens)
			assert.Equal(t, tc.want5m, usage.CacheWrite5mTokens)
			assert.Equal(t, tc.want1h, usage.CacheWrite1hTokens)
			assert.Equal(t, tc.wantRead, usage.CacheReadTokens)

			// 分档明细不能超过写入总量。
			require.LessOrEqual(t, usage.CacheWrite5mTokens+usage.CacheWrite1hTokens, usage.CacheWriteTokens,
				"归一化输出必须自洽：分档需为写入总量的子集")
		})
	}
}

// TestMergeAnthropicStreamUsageCacheWriteTiers 是"后续事件不清空已累积明细"的反证：
// 若改为无条件覆盖，只带 output_tokens 的 message_delta 会把分档明细清零，
// 导致流式调用按总档计价、少算长 TTL 写入成本。
func TestMergeAnthropicStreamUsageCacheWriteTiers(t *testing.T) {
	t.Parallel()

	var acc anthropicUsage

	// message_start 携带输入侧统计与分档明细
	mergeAnthropicStreamUsage(&acc, anthropicUsage{
		InputTokens:              100,
		CacheCreationInputTokens: 2000,
		CacheReadInputTokens:     500,
		CacheCreation: &anthropicCacheCreation{
			Ephemeral5mInputTokens: 1500,
			Ephemeral1hInputTokens: 500,
		},
	})
	usage := usageFromAnthropic(acc)
	require.Equal(t, 1500, usage.CacheWrite5mTokens)
	require.Equal(t, 500, usage.CacheWrite1hTokens)

	// message_delta 只携带累计输出，不得清空已累积的分档明细
	mergeAnthropicStreamUsage(&acc, anthropicUsage{OutputTokens: 30})
	usage = usageFromAnthropic(acc)
	assert.Equal(t, 30, usage.CompletionTokens)
	assert.Equal(t, 1500, usage.CacheWrite5mTokens)
	assert.Equal(t, 500, usage.CacheWrite1hTokens)
	assert.Equal(t, 2000, usage.CacheWriteTokens)

	// 携带新明细的事件整体覆盖两档，不与旧值逐档混合
	mergeAnthropicStreamUsage(&acc, anthropicUsage{
		CacheCreation: &anthropicCacheCreation{Ephemeral1hInputTokens: 900},
	})
	usage = usageFromAnthropic(acc)
	assert.Equal(t, 0, usage.CacheWrite5mTokens, "整体覆盖后 5m 档应随新快照归零")
	assert.Equal(t, 900, usage.CacheWrite1hTokens)
}

// TestMergeAnthropicStreamUsageDoesNotAliasEventCacheCreation 确认累积值不与事件对象
// 共享底层数据：若直接复用事件指针，事件对象被复用或修改时累积值会被连带改写。
func TestMergeAnthropicStreamUsageDoesNotAliasEventCacheCreation(t *testing.T) {
	t.Parallel()

	event := anthropicUsage{
		CacheCreation: &anthropicCacheCreation{Ephemeral5mInputTokens: 1500},
	}
	var acc anthropicUsage
	mergeAnthropicStreamUsage(&acc, event)

	event.CacheCreation.Ephemeral5mInputTokens = 999
	assert.Equal(t, 1500, usageFromAnthropic(acc).CacheWrite5mTokens)
}

// TestAnthropicProviderChatThinkingEndToEnd 端到端验证推理控制整条链路：
// thinking 参数确实被序列化下发（单元测试只验证到请求结构体，
// 字段若被标成不序列化，各段单测仍会全绿而功能已失效），
// 且响应中的 thinking 块归位到 Reasoning 而非正文。
func TestAnthropicProviderChatThinkingEndToEnd(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// handler 跑在另一个 goroutine：用 assert 而非 require，
		// require 的 FailNow 只允许在测试 goroutine 中调用。
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": [
				{"type":"thinking","thinking":"先分解问题","signature":"sig-abc"},
				{"type":"redacted_thinking","data":"ENCRYPTED"},
				{"type":"text","text":"答案是 42"}
			],
			"usage": {"input_tokens": 10, "output_tokens": 7}
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	budget := 2048
	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages:  []Message{UserText("hi")},
		MaxTokens: 8192,
		Thinking:  &Thinking{BudgetTokens: &budget},
	})
	require.NoError(t, err)

	// 请求侧：thinking 参数真的出现在 HTTP 请求体里。
	thinking, ok := captured["thinking"].(map[string]any)
	require.True(t, ok, "请求体应携带 thinking 参数，实际 body=%v", captured)
	assert.Equal(t, "enabled", thinking["type"])
	assert.EqualValues(t, 2048, thinking["budget_tokens"])

	// 响应侧：思考内容归位到 Reasoning，正文不含思考内容与加密块。
	assert.Equal(t, "答案是 42", resp.Content)
	assert.Equal(t, "先分解问题", resp.Reasoning)
	assert.NotContains(t, resp.Content, "先分解问题")
	assert.NotContains(t, resp.Content, "ENCRYPTED")
	assert.NotContains(t, resp.Reasoning, "ENCRYPTED")
	// 签名块与加密块原样保留，供工具循环回传。
	assert.Equal(t, []ThinkingBlock{
		{Thinking: "先分解问题", Signature: "sig-abc"},
		{Redacted: "ENCRYPTED"},
	}, resp.ThinkingBlocks)
}

// TestAnthropicProviderChatCacheWriteTiersEndToEnd 端到端验证缓存写入分档：
// HTTP 响应里的 cache_creation 明细一路到达 Usage，并能直接用于分档计价。
func TestAnthropicProviderChatCacheWriteTiersEndToEnd(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": [{"type":"text","text":"ok"}],
			"usage": {
				"input_tokens": 100,
				"output_tokens": 7,
				"cache_read_input_tokens": 300,
				"cache_creation_input_tokens": 2000,
				"cache_creation": {"ephemeral_5m_input_tokens": 1500, "ephemeral_1h_input_tokens": 500}
			}
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

	assert.Equal(t, Usage{
		PromptTokens:       2400, // 100 + 300 + 2000
		CompletionTokens:   7,
		CacheReadTokens:    300,
		CacheWriteTokens:   2000,
		CacheWrite5mTokens: 1500,
		CacheWrite1hTokens: 500,
		TotalTokens:        2407,
	}, resp.Usage)
}

// TestAnthropicStreamThinkingBlockEvents 锁定思考块签名与加密块在流上的透出：
// 开启思考并使用工具时平台要求把思考块原样回传，签名与加密数据缺一不可。
func TestAnthropicStreamThinkingBlockEvents(t *testing.T) {
	t.Parallel()

	chunk, ok, err := anthropicStreamChunk(
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc123"}}`),
		&anthropicStreamState{})
	require.NoError(t, err)
	require.True(t, ok, "signature_delta 必须产出 chunk，否则调用方无法拼出可回传的思考块")
	assert.Equal(t, "abc123", chunk.ThinkingSignature)
	assert.Empty(t, chunk.ReasoningDelta)

	chunk, ok, err = anthropicStreamChunk(
		[]byte(`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"ENC"}}`),
		&anthropicStreamState{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "ENC", chunk.RedactedThinking)

	// 普通 thinking 块的 start 事件没有可透出的内容，不产出 chunk。
	chunk, ok, err = anthropicStreamChunk(
		[]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`),
		&anthropicStreamState{})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, chunk)
}
