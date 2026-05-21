package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAzureOpenAIProviderRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	p, err := NewAzureOpenAIProvider(AzureOpenAIConfig{})

	require.ErrorIs(t, err, ErrInvalidProviderConfig)
	assert.Nil(t, p)
}

func TestNewAzureOpenAIProvider(t *testing.T) {
	t.Parallel()

	p, err := NewAzureOpenAIProvider(AzureOpenAIConfig{
		APIKey:     "test-key",
		Endpoint:   "https://example.openai.azure.com",
		Deployment: "gpt-4o-mini",
	})

	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, ProviderAzureOpenAI, p.Name())
}

func TestNewBedrockOpenAIProviderRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	p, err := NewBedrockOpenAIProvider(BedrockOpenAIConfig{})

	require.ErrorIs(t, err, ErrInvalidProviderConfig)
	assert.Nil(t, p)
}

func TestNewBedrockOpenAIProvider(t *testing.T) {
	t.Parallel()

	p, err := NewBedrockOpenAIProvider(BedrockOpenAIConfig{
		APIKey: "test-key",
		Region: "us-east-1",
		Model:  "anthropic.claude-sonnet-4-5-20250929-v1:0",
	})

	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, ProviderBedrock, p.Name())
}
