package provider

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelCapabilitiesFromPreset(t *testing.T) {
	t.Parallel()

	caps, ok := ModelCapabilitiesFromPreset(ProviderOpenAI)
	require.True(t, ok)
	assert.Equal(t, ProviderOpenAI, caps.Provider)
	assert.Equal(t, "gpt-5.4-mini", caps.ChatModel)
	assert.Equal(t, "text-embedding-3-small", caps.EmbeddingModel)
	assert.True(t, caps.Supports(CapabilityChat))
	assert.True(t, caps.Supports(CapabilityStreaming))
	assert.True(t, caps.Supports(CapabilityTools))
	assert.True(t, caps.Supports(CapabilityStructuredOutput))
	assert.True(t, caps.Supports(CapabilityReasoning))
	assert.True(t, caps.Supports(CapabilityEmbedding))
}

func TestModelCapabilitiesFromPresetUnknown(t *testing.T) {
	t.Parallel()

	caps, ok := ModelCapabilitiesFromPreset("missing")
	assert.False(t, ok)
	assert.Empty(t, caps.Provider)
}

func TestAllModelCapabilitiesReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	caps := AllModelCapabilities()
	require.Contains(t, caps, ProviderOpenAI)

	openaiCaps := caps[ProviderOpenAI]
	openaiCaps.Capabilities[0] = Capability("mutated")
	caps[ProviderOpenAI] = openaiCaps

	fresh := AllModelCapabilities()
	assert.NotEqual(t, Capability("mutated"), fresh[ProviderOpenAI].Capabilities[0])
}

func TestModelCapabilitiesByCapability(t *testing.T) {
	t.Parallel()

	embedders := ModelCapabilitiesByCapability(CapabilityEmbedding)
	assert.Contains(t, embedders, ProviderOpenAI)
	assert.Contains(t, embedders, ProviderQwen)
	assert.Contains(t, embedders, ProviderGemini)
	assert.NotContains(t, embedders, ProviderDeepSeek)

	for name, caps := range embedders {
		assert.True(t, caps.Supports(CapabilityEmbedding), "provider=%s", name)
	}
}

func TestPresetIncludesCapabilities(t *testing.T) {
	t.Parallel()

	presets := AllPresets()
	preset := presets[ProviderOpenAI]
	assert.Equal(t, "gpt-5.4-mini", preset.Capabilities.ChatModel)
	assert.True(t, preset.Capabilities.Supports(CapabilityChat))
}

func TestAllPresetsReturnsDefensiveCapabilityCopy(t *testing.T) {
	t.Parallel()

	presets := AllPresets()
	preset := presets[ProviderOpenAI]
	preset.Capabilities.Capabilities[0] = Capability("mutated")
	presets[ProviderOpenAI] = preset

	fresh := AllPresets()
	assert.NotEqual(t, Capability("mutated"), fresh[ProviderOpenAI].Capabilities.Capabilities[0])
}

func TestDeprecatedPresetsDoesNotShareCapabilitySliceWithCatalog(t *testing.T) {
	t.Parallel()

	original := Presets[ProviderOpenAI]
	t.Cleanup(func() {
		Presets[ProviderOpenAI] = original
	})

	preset := Presets[ProviderOpenAI]
	preset.Capabilities.Capabilities[0] = Capability("mutated")
	Presets[ProviderOpenAI] = preset

	fresh, ok := ModelCapabilitiesFromPreset(ProviderOpenAI)
	require.True(t, ok)
	assert.NotEqual(t, Capability("mutated"), fresh.Capabilities[0])
}

func TestProviderConfigHTTPClientValidate(t *testing.T) {
	t.Parallel()

	err := (ProviderConfig{
		Name:       ProviderOpenAI,
		APIKey:     "k",
		Model:      "m",
		HTTPClient: http.DefaultClient,
	}).Validate()
	require.NoError(t, err)
}
