package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingHTTPClient struct {
	requests []*http.Request
	response string
}

func (c *recordingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.requests = append(c.requests, req)
	if c.response == "" {
		c.response = `{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(c.response)),
		Request:    req,
	}, nil
}

func TestNewProviderUsesCustomHTTPClient(t *testing.T) {
	t.Parallel()

	client := &recordingHTTPClient{}
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
	assert.Equal(t, "ok", resp.Content)

	require.Len(t, client.requests, 1)
	assert.Equal(t, "example.test", client.requests[0].URL.Host)
	assert.Equal(t, "/v1/chat/completions", client.requests[0].URL.Path)
}

func TestDefaultHTTPClientHasTransportTimeouts(t *testing.T) {
	t.Parallel()

	client := DefaultHTTPClient()
	require.NotNil(t, client)
	assert.Zero(t, client.Timeout)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.DialContext)
	assert.GreaterOrEqual(t, transport.TLSHandshakeTimeout, 5*time.Second)
	assert.GreaterOrEqual(t, transport.ResponseHeaderTimeout, 30*time.Second)
	assert.GreaterOrEqual(t, transport.IdleConnTimeout, 30*time.Second)
}

func TestNewEmbedderUsesCustomHTTPClient(t *testing.T) {
	t.Parallel()

	client := &recordingHTTPClient{
		response: `{"object":"list","model":"text-embedding-3-small","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`,
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
	require.Len(t, resp.Data, 1)
	assert.Equal(t, []float32{0.1, 0.2}, resp.Data[0].Vector)

	require.Len(t, client.requests, 1)
	assert.Equal(t, "example.test", client.requests[0].URL.Host)
	assert.Equal(t, "/v1/embeddings", client.requests[0].URL.Path)
}
