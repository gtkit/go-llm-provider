package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gtkit/json"
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

func TestAnthropicProviderChatRejectsTools(t *testing.T) {
	t.Parallel()

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: "http://127.0.0.1:1",
		Model:   "claude-sonnet-4-5",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools:    []Tool{{Function: FunctionDef{Name: "get_weather"}}},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
	assert.Nil(t, resp)
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

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
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

func TestGeminiProviderChatRejectsTools(t *testing.T) {
	t.Parallel()

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: "http://127.0.0.1:1/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools:    []Tool{{Function: FunctionDef{Name: "get_weather"}}},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
	assert.Nil(t, resp)
}

func TestGeminiProviderChatStreamMapsSSE(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", r.URL.Path)
		assert.Equal(t, "sse", r.URL.Query().Get("alt"))
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

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: "http://127.0.0.1:1/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages: []Message{ToolResultMessage("call_1", "ok")},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), string(RoleTool))
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
