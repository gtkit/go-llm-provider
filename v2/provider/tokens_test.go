package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiProviderCountTokensMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash:countTokens", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalTokens": 17}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1beta",
		Model:   "gemini-2.5-flash",
	})
	require.NoError(t, err)

	counter, ok := p.(TokenCounter)
	require.True(t, ok)

	resp, err := counter.CountTokens(t.Context(), &ChatRequest{
		Messages: []Message{
			SystemText("be concise"),
			UserText("hello"),
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "gemini-2.5-flash", resp.Model)
	assert.Equal(t, 17, resp.TotalTokens)
	contents, ok := captured["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contents, 1)
	systemInstruction, ok := captured["systemInstruction"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, systemInstruction)
}

func TestCountTokensReturnsUnsupportedForProviderWithoutCounter(t *testing.T) {
	t.Parallel()

	resp, err := CountTokens(t.Context(), tokenTestProvider{name: ProviderOpenAI}, &ChatRequest{
		Messages: []Message{UserText("hello")},
	})

	require.ErrorIs(t, err, ErrUnsupportedCapability)
	assert.Nil(t, resp)
}

type tokenTestProvider struct {
	name ProviderName
}

func (p tokenTestProvider) Name() ProviderName {
	return p.name
}

func (p tokenTestProvider) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return nil, nil
}

func (p tokenTestProvider) ChatStream(context.Context, *ChatRequest) (*StreamReader, error) {
	return nil, nil
}
