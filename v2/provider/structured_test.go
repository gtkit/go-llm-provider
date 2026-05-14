package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateJSONInto(t *testing.T) {
	t.Parallel()

	type citySummary struct {
		City string `json:"city"`
		Temp int    `json:"temp"`
	}

	var seenReq *ChatRequest
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			seenReq = req
			return &ChatResponse{Content: `{"city":"杭州","temp":27}`}, nil
		},
	}

	var out citySummary
	resp, err := GenerateJSONInto(context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("天气")},
	}, &out)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "杭州", out.City)
	assert.Equal(t, 27, out.Temp)
	require.NotNil(t, seenReq)
	require.NotNil(t, seenReq.ResponseFormat)
	assert.Equal(t, ResponseFormatJSONObject, seenReq.ResponseFormat.Type)
}

func TestGenerateJSON(t *testing.T) {
	t.Parallel()

	type citySummary struct {
		City string `json:"city"`
	}

	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: `{"city":"上海"}`}, nil
		},
	}

	got, resp, err := GenerateJSON[citySummary](context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("城市")},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "上海", got.City)
}

func TestGenerateJSONIntoPreservesExistingResponseFormat(t *testing.T) {
	t.Parallel()

	format := JSONSchemaFormatStrict("city", ParamSchema{
		Type: "object",
		Properties: map[string]ParamSchema{
			"city": {Type: "string"},
		},
		Required: []string{"city"},
	})

	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			assert.Same(t, format, req.ResponseFormat)
			return &ChatResponse{Content: `{"city":"北京"}`}, nil
		},
	}

	var out struct {
		City string `json:"city"`
	}
	_, err := GenerateJSONInto(context.Background(), p, &ChatRequest{
		Messages:       []Message{UserText("城市")},
		ResponseFormat: format,
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, "北京", out.City)
}

func TestGenerateJSONIntoWithValidator(t *testing.T) {
	t.Parallel()

	errInvalidCity := errors.New("city is required")
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: `{"city":""}`}, nil
		},
	}

	var out struct {
		City string `json:"city"`
	}
	resp, err := GenerateJSONIntoWithValidator(context.Background(), p, &ChatRequest{}, &out, func(v struct {
		City string `json:"city"`
	}) error {
		if v.City == "" {
			return errInvalidCity
		}
		return nil
	})
	require.ErrorIs(t, err, ErrStructuredValidation)
	require.ErrorIs(t, err, errInvalidCity)
	require.NotNil(t, resp)
}

func TestGenerateJSONIntoValidation(t *testing.T) {
	t.Parallel()

	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()
		var out struct{}
		_, err := GenerateJSONInto(context.Background(), nil, &ChatRequest{}, &out)
		require.ErrorIs(t, err, ErrNilProvider)
	})

	t.Run("nil request", func(t *testing.T) {
		t.Parallel()
		var out struct{}
		_, err := GenerateJSONInto(context.Background(), &stubProvider{name: ProviderOpenAI}, nil, &out)
		require.ErrorIs(t, err, ErrNilChatRequest)
	})

	t.Run("nil target", func(t *testing.T) {
		t.Parallel()
		_, err := GenerateJSONInto[struct{}](context.Background(), &stubProvider{name: ProviderOpenAI}, &ChatRequest{}, nil)
		require.ErrorIs(t, err, ErrNilStructuredTarget)
	})
}

func TestGenerateJSONIntoWrapsChatAndDecodeErrors(t *testing.T) {
	t.Parallel()

	t.Run("chat error", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		p := &stubProvider{
			name: ProviderOpenAI,
			chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
				return nil, boom
			},
		}
		var out struct{}
		_, err := GenerateJSONInto(context.Background(), p, &ChatRequest{}, &out)
		require.ErrorIs(t, err, boom)
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		p := &stubProvider{
			name: ProviderOpenAI,
			chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
				return &ChatResponse{Content: `{bad`}, nil
			},
		}
		var out struct{}
		_, err := GenerateJSONInto(context.Background(), p, &ChatRequest{}, &out)
		require.ErrorIs(t, err, ErrStructuredDecode)
	})
}
