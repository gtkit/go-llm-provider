package provider

import (
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/require"
)

// Assert the serialized HTTP request, since the SDK omits scalar zero values.
func TestCompatibleGenerationOptionPresence(t *testing.T) {
	t.Parallel()
	for _, name := range []ProviderName{ProviderOpenAI, ProviderArk, "custom"} {
		for _, streaming := range []bool{false, true} {
			for _, tc := range []struct {
				name  string
				value *float32
			}{
				{name: "unset"},
				{name: "zero", value: new(float32(0))},
				{name: "nonzero", value: new(float32(0.5))},
			} {
				t.Run(string(name)+"/"+tc.name+map[bool]string{true: "/stream", false: "/chat"}[streaming], func(t *testing.T) {
					t.Parallel()
					client := &generationOptionClient{t: t, value: tc.value, streaming: streaming}
					p, err := NewProvider(ProviderConfig{Name: name, APIKey: "test-key", Model: "test-model", HTTPClient: client})
					require.NoError(t, err)
					req := &ChatRequest{Messages: []Message{UserMessage(TextPart("hello"))}, Temperature: tc.value, TopP: tc.value}
					if streaming {
						reader, streamErr := p.ChatStream(t.Context(), req)
						require.NoError(t, streamErr)
						require.NoError(t, reader.Close())
					} else {
						_, err = p.Chat(t.Context(), req)
						require.NoError(t, err)
					}
					require.Equal(t, 1, client.calls)
				})
			}
		}
	}
}

type generationOptionClient struct {
	t         *testing.T
	value     *float32
	streaming bool
	calls     int
}

func (c *generationOptionClient) Do(req *http.Request) (*http.Response, error) {
	c.calls++
	body, err := io.ReadAll(req.Body)
	require.NoError(c.t, err)
	require.NoError(c.t, req.Body.Close())
	var fields map[string]json.RawMessage
	require.NoError(c.t, json.Unmarshal(body, &fields))
	for _, key := range []string{"temperature", "top_p"} {
		raw, exists := fields[key]
		require.Equal(c.t, c.value != nil, exists, "field %s: %s", key, body)
		if c.value != nil {
			var value float32
			require.NoError(c.t, json.Unmarshal(raw, &value))
			require.Equal(c.t, math.Float32bits(*c.value), math.Float32bits(value))
			require.Equal(c.t, 1, strings.Count(string(body), `"`+key+`":`), "no duplicate JSON keys")
		}
	}
	contentType := "application/json"
	response := `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	if c.streaming {
		contentType = "text/event-stream"
		response = "data: [DONE]\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}
