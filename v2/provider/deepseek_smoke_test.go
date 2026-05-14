package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekSmoke(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run DeepSeek smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewProviderFromPreset(ProviderDeepSeek, apiKey, "deepseek-chat")
	require.NoError(t, err)

	resp, err := p.Chat(ctx, &ChatRequest{
		Messages: []Message{
			SystemText("只输出简短答案。"),
			UserText("回复 ok"),
		},
		MaxTokens: 16,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Content)
	assert.NotZero(t, resp.Usage.TotalTokens)
}

func TestDeepSeekStructuredSmoke(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run DeepSeek structured smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewProviderFromPreset(ProviderDeepSeek, apiKey, "deepseek-chat")
	require.NoError(t, err)

	type smokeResult struct {
		Status string `json:"status"`
	}

	out, resp, err := GenerateJSONWithValidator[smokeResult](ctx, p, &ChatRequest{
		Messages: []Message{
			SystemText("只输出 JSON 对象，不要输出 Markdown。"),
			UserText(`返回 {"status":"ok"}`),
		},
		MaxTokens: 32,
	}, func(v smokeResult) error {
		if v.Status != "ok" {
			return ErrStructuredValidation
		}
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ok", out.Status)
	assert.NotZero(t, resp.Usage.TotalTokens)
}
