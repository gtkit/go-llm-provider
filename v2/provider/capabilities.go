package provider

import "slices"

// Capability describes a model or provider feature exposed through presets.
type Capability string

const (
	// CapabilityChat indicates non-streaming chat completion support.
	CapabilityChat Capability = "chat"
	// CapabilityStreaming indicates streaming chat completion support.
	CapabilityStreaming Capability = "streaming"
	// CapabilityTools indicates function/tool calling support.
	CapabilityTools Capability = "tools"
	// CapabilityStructuredOutput indicates response_format or equivalent structured output support.
	CapabilityStructuredOutput Capability = "structured_output"
	// CapabilityVision indicates image input support.
	CapabilityVision Capability = "vision"
	// CapabilityReasoning indicates provider-side reasoning or thinking controls.
	CapabilityReasoning Capability = "reasoning"
	// CapabilityEmbedding indicates text embedding support.
	CapabilityEmbedding Capability = "embedding"
)

// ModelCapabilities describes the default models and known capabilities for a preset.
type ModelCapabilities struct {
	Provider       ProviderName
	ChatModel      string
	EmbeddingModel string
	ContextWindow  int
	Capabilities   []Capability
}

// Supports reports whether caps includes capability.
func (caps ModelCapabilities) Supports(capability Capability) bool {
	return slices.Contains(caps.Capabilities, capability)
}

// ModelCapabilitiesFromPreset returns capability metadata for a preset provider.
func ModelCapabilitiesFromPreset(name ProviderName) (ModelCapabilities, bool) {
	preset, ok := presetCatalog[name]
	if !ok {
		return ModelCapabilities{}, false
	}
	return cloneModelCapabilities(preset.Capabilities), true
}

// AllModelCapabilities returns a defensive copy of all preset capability metadata.
func AllModelCapabilities() map[ProviderName]ModelCapabilities {
	out := make(map[ProviderName]ModelCapabilities, len(presetCatalog))
	for name, preset := range presetCatalog {
		out[name] = cloneModelCapabilities(preset.Capabilities)
	}
	return out
}

// ModelCapabilitiesByCapability returns presets that advertise capability.
func ModelCapabilitiesByCapability(capability Capability) map[ProviderName]ModelCapabilities {
	out := make(map[ProviderName]ModelCapabilities)
	for name, preset := range presetCatalog {
		caps := preset.Capabilities
		if !caps.Supports(capability) {
			continue
		}
		out[name] = cloneModelCapabilities(caps)
	}
	return out
}

func cloneModelCapabilities(caps ModelCapabilities) ModelCapabilities {
	caps.Capabilities = slices.Clone(caps.Capabilities)
	return caps
}
