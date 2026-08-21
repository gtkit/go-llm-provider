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
	// CapabilityFile indicates document/file input support (Message ContentTypeFile parts).
	CapabilityFile Capability = "file"
	// CapabilityFileUpload 表示平台提供 OpenAI 兼容 Files API（上传/抽取/删除，
	// 即 FileService 接口对应的平台端点）。注意区分：CapabilityFile 描述消息内
	// 文件片段的协议映射，CapabilityFileUpload 描述独立的文件管理端点。
	CapabilityFileUpload Capability = "file_upload"
	// CapabilityReasoning indicates provider-side reasoning or thinking controls.
	CapabilityReasoning Capability = "reasoning"
	// CapabilityEmbedding indicates text embedding support.
	CapabilityEmbedding Capability = "embedding"
	// CapabilityWebSearch indicates built-in provider-native web search mapping
	// (WebSearchTool). Providers without it can still perform web search through
	// a regular function tool executed by the caller's ToolHandler (see README).
	CapabilityWebSearch Capability = "web_search"
	// CapabilityRerank 表示平台提供 OpenAI 兼容的 /rerank 端点（Reranker 接口）。
	CapabilityRerank Capability = "rerank"
)

// ModelCapabilities describes the default models and known capabilities for a preset.
type ModelCapabilities struct {
	Provider       ProviderName
	ChatModel      string
	EmbeddingModel string
	// RerankModel 是该平台推荐的默认 rerank 模型，空串表示预设未覆盖 rerank。
	RerankModel   string
	ContextWindow int
	Capabilities  []Capability
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
