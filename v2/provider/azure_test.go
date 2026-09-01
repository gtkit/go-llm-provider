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

// TestNewBedrockOpenAIProviderCarriesReasoningEffortDeclaration 确认 Bedrock 的
// 推理声明真的透传到底层 provider。ProviderBedrock 不在库内的推理映射表中
// （Bedrock 上能否使用 reasoning_effort 取决于所选底层模型，库不代为断言），
// 因此这个声明是该构造函数使用 Thinking.Effort 的唯一入口，漏传即等于功能不可用。
func TestNewBedrockOpenAIProviderCarriesReasoningEffortDeclaration(t *testing.T) {
	t.Parallel()

	t.Run("声明后 Effort 可用", func(t *testing.T) {
		t.Parallel()

		p, err := NewBedrockOpenAIProvider(BedrockOpenAIConfig{
			APIKey:                  "test-key",
			Region:                  "us-east-1",
			Model:                   "anthropic.claude-sonnet-4-5",
			SupportsReasoningEffort: true,
		})
		require.NoError(t, err)

		op, ok := p.(*openaiProvider)
		require.True(t, ok)
		req, err := op.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{Effort: ThinkingEffortHigh},
		})
		require.NoError(t, err)
		assert.Equal(t, "high", req.ReasoningEffort)
	})

	t.Run("未声明时 Effort 被拒", func(t *testing.T) {
		t.Parallel()

		p, err := NewBedrockOpenAIProvider(BedrockOpenAIConfig{
			APIKey: "test-key",
			Region: "us-east-1",
			Model:  "anthropic.claude-sonnet-4-5",
		})
		require.NoError(t, err)

		op, ok := p.(*openaiProvider)
		require.True(t, ok)
		_, err = op.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{Effort: ThinkingEffortHigh},
		})
		require.ErrorIs(t, err, ErrInvalidRequest)
	})
}
