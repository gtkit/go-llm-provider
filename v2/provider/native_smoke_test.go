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
			SystemText("只输出简短答案。"),
			UserText("回复 ok"),
		},
		MaxTokens: 16,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Content)
	assert.Equal(t, ProviderAnthropic, resp.Metadata.Provider)
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
			SystemText("只输出简短答案。"),
			UserText("回复 ok"),
		},
		MaxTokens: 16,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Content)
	assert.Equal(t, ProviderGemini, resp.Metadata.Provider)
}

func TestGeminiEmbeddingSmoke(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("set GEMINI_API_KEY to run Gemini embedding smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	e, err := NewGeminiEmbedder(NativeProviderConfig{
		APIKey: apiKey,
		Model:  firstString(os.Getenv("GEMINI_EMBEDDING_MODEL"), defaultGeminiEmbeddingModel),
	})
	require.NoError(t, err)

	resp, err := e.Embed(ctx, &EmbeddingRequest{Input: []string{"hello"}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Data, 1)
	assert.NotEmpty(t, resp.Data[0].Vector)
	assert.Equal(t, ProviderGemini, resp.Metadata.Provider)
}

func TestOllamaSmoke(t *testing.T) {
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		t.Skip("set OLLAMA_MODEL to run Ollama smoke test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	p, err := NewOllamaProvider(OllamaProviderConfig{
		BaseURL: os.Getenv("OLLAMA_BASE_URL"),
		Model:   model,
	})
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
	assert.Equal(t, ProviderOllama, resp.Metadata.Provider)
}
