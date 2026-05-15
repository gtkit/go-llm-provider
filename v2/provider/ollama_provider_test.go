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

func TestNewOllamaProviderRejectsMissingModel(t *testing.T) {
	t.Parallel()

	p, err := NewOllamaProvider(OllamaProviderConfig{})
	require.ErrorIs(t, err, ErrInvalidProviderConfig)
	assert.Nil(t, p)
}

func TestOllamaProviderChatMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/chat", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "llama3.2",
			"message": {"role":"assistant","content":"hello from ollama"},
			"done": true,
			"done_reason": "stop",
			"prompt_eval_count": 7,
			"eval_count": 5
		}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewOllamaProvider(OllamaProviderConfig{
		BaseURL: srv.URL,
		Model:   "llama3.2",
	})
	require.NoError(t, err)

	resp, err := p.Chat(t.Context(), &ChatRequest{
		Messages:  []Message{SystemText("be brief"), UserText("hi")},
		MaxTokens: 64,
	})
	require.NoError(t, err)
	assert.Equal(t, "hello from ollama", resp.Content)
	assert.Equal(t, "stop", resp.FinishReason)
	assert.Equal(t, Usage{PromptTokens: 7, CompletionTokens: 5, TotalTokens: 12}, resp.Usage)
	assert.Equal(t, ProviderOllama, resp.Metadata.Provider)
	assert.Equal(t, "llama3.2", resp.Metadata.Model)

	assert.Equal(t, "llama3.2", captured["model"])
	assert.Equal(t, false, captured["stream"])
	assert.InDelta(t, 64, captured["options"].(map[string]any)["num_predict"], 0.0001)
	messages, ok := captured["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 2)
	assert.Equal(t, "system", messages[0].(map[string]any)["role"])
	assert.Equal(t, "be brief", messages[0].(map[string]any)["content"])
}

func TestOllamaProviderChatStreamMapsNDJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/chat", r.URL.Path)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"hello"},"done":false}`)
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":" world"},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":4}`)
	}))
	t.Cleanup(srv.Close)

	p, err := NewOllamaProvider(OllamaProviderConfig{
		BaseURL: srv.URL,
		Model:   "llama3.2",
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
	assert.Equal(t, "stop", second.FinishReason)
	assert.Equal(t, Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}, second.Usage)

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}
