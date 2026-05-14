package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatResponseIncludesMetadata(t *testing.T) {
	t.Parallel()

	client := &recordingHTTPClient{
		header: http.Header{
			"X-Request-Id":           []string{"req-chat"},
			"X-Ratelimit-Limit-Reqs": []string{"100"},
			"Set-Cookie":             []string{"secret"},
		},
	}
	p, err := NewProvider(ProviderConfig{
		Name:       ProviderOpenAI,
		BaseURL:    "https://example.test/v1",
		APIKey:     "sk-test",
		Model:      "test-model",
		HTTPClient: client,
	})
	require.NoError(t, err)

	resp, err := p.Chat(context.Background(), &ChatRequest{
		Messages: []Message{UserText("hello")},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, ProviderOpenAI, resp.Metadata.Provider)
	assert.Equal(t, "test-model", resp.Metadata.Model)
	assert.Equal(t, "req-chat", resp.Metadata.RequestID)
	assert.Equal(t, "100", resp.Metadata.Header("x-ratelimit-limit-reqs"))
	assert.Empty(t, resp.Metadata.Header("set-cookie"))
}

func TestEmbeddingResponseIncludesMetadata(t *testing.T) {
	t.Parallel()

	client := &recordingHTTPClient{
		response: `{"object":"list","model":"text-embedding-3-small","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`,
		header: http.Header{
			"X-Request-Id": []string{"req-embedding"},
		},
	}
	e, err := NewEmbedder(EmbedderConfig{
		Name:       ProviderOpenAI,
		BaseURL:    "https://example.test/v1",
		APIKey:     "sk-test",
		Model:      "text-embedding-3-small",
		HTTPClient: client,
	})
	require.NoError(t, err)

	resp, err := e.Embed(context.Background(), &EmbeddingRequest{
		Input: []string{"hello"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, ProviderOpenAI, resp.Metadata.Provider)
	assert.Equal(t, "text-embedding-3-small", resp.Metadata.Model)
	assert.Equal(t, "req-embedding", resp.Metadata.RequestID)
}
