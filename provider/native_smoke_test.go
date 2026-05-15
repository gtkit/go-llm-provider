package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("set ANTHROPIC_API_KEY to run Anthropic smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey: apiKey,
		Model:  firstString(os.Getenv("ANTHROPIC_MODEL"), defaultAnthropicModel),
	})
	require.NoError(t, err)

	resp, err := p.Chat(ctx, &ChatRequest{
		Messages: []Message{
			{Role: RoleSystem, Content: "只输出简短答案。"},
			{Role: RoleUser, Content: "回复 ok"},
		},
		MaxTokens: 16,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Content)
}

func TestGeminiSmoke(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("set GEMINI_API_KEY to run Gemini smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewGeminiProvider(NativeProviderConfig{
		APIKey: apiKey,
		Model:  firstString(os.Getenv("GEMINI_MODEL"), defaultGeminiModel),
	})
	require.NoError(t, err)

	resp, err := p.Chat(ctx, &ChatRequest{
		Messages: []Message{
			{Role: RoleSystem, Content: "只输出简短答案。"},
			{Role: RoleUser, Content: "回复 ok"},
		},
		MaxTokens: 16,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Content)
}
