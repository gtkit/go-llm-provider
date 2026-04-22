package provider

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestThinkingDeepSeekEnabled(t *testing.T) {
	t.Parallel()

	enabled := true
	p := &openaiProvider{name: ProviderDeepSeek, model: "deepseek-chat"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: &enabled},
	})
	require.NoError(t, err)
	require.NotNil(t, req.ChatTemplateKwargs)
	assert.Equal(t, true, req.ChatTemplateKwargs["enable_thinking"])
}

func TestBuildRequestThinkingDeepSeekDisabled(t *testing.T) {
	t.Parallel()

	enabled := false
	p := &openaiProvider{name: ProviderDeepSeek, model: "deepseek-chat"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: &enabled},
	})
	require.NoError(t, err)
	require.NotNil(t, req.ChatTemplateKwargs)
	assert.Equal(t, false, req.ChatTemplateKwargs["enable_thinking"])
}

func TestBuildRequestThinkingOpenAIEffort(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{name: ProviderOpenAI, model: "o4-mini"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Effort: ThinkingEffortHigh},
	})
	require.NoError(t, err)
	assert.Equal(t, "high", req.ReasoningEffort)
}

func TestBuildRequestThinkingUnsupportedProviderIgnored(t *testing.T) {
	t.Parallel()

	enabled := true
	p := &openaiProvider{name: ProviderQwen, model: "qwen-plus"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{
			Enabled: &enabled,
			Effort:  ThinkingEffortLow,
		},
	})
	require.NoError(t, err)
	assert.Nil(t, req.ChatTemplateKwargs)
	assert.Empty(t, req.ReasoningEffort)
}

func TestStreamChunkReasoningDeltaField(t *testing.T) {
	t.Parallel()

	chunk := StreamChunk{ReasoningDelta: "thinking..."}
	assert.Equal(t, "thinking...", chunk.ReasoningDelta)
}

func TestUsageReasoningTokensField(t *testing.T) {
	t.Parallel()

	usage := Usage{ReasoningTokens: 12}
	assert.Equal(t, 12, usage.ReasoningTokens)
}

func TestOpenAIReasoningContentFieldAvailableInSDK(t *testing.T) {
	t.Parallel()

	msg := openai.ChatCompletionMessage{ReasoningContent: "trace"}
	assert.Equal(t, "trace", msg.ReasoningContent)
}
