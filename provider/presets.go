package provider

import (
	"fmt"
	"maps"
)

// 预置各平台的 BaseURL 和推荐默认模型。
// 业务侧只需提供 APIKey 即可快速接入。

// Preset 是一组平台预设信息。
type Preset struct {
	BaseURL      string
	DefaultModel string // Chat 默认模型
	// EmbeddingModel 是该平台推荐的默认 embedding 模型。
	// 空字符串表示该平台暂无官方 embedding 接口（如 DeepSeek、Moonshot），
	// NewEmbedderFromPreset 遇到空串会返回错误。
	EmbeddingModel string
}

var presetCatalog = map[ProviderName]Preset{
	ProviderDeepSeek: {
		BaseURL: "https://api.deepseek.com/v1",
		// 旧的 deepseek-chat / deepseek-reasoner 已于 2026-07-24 停用。
		DefaultModel: "deepseek-v4-flash",
		// DeepSeek 官方暂无 embedding 模型
	},
	ProviderZhipu: {
		BaseURL:        "https://open.bigmodel.cn/api/paas/v4/",
		DefaultModel:   "glm-5.3",
		EmbeddingModel: "embedding-3",
	},
	ProviderQwen: {
		// 阿里百炼 / DashScope OpenAI 兼容端点
		BaseURL:        "https://dashscope.aliyuncs.com/compatible-mode/v1",
		DefaultModel:   "qwen3.7-plus",
		EmbeddingModel: "text-embedding-v4",
	},
	ProviderQianfan: {
		// 百度千帆 OpenAI 兼容 V2 接口
		BaseURL:        "https://qianfan.baidubce.com/v2",
		DefaultModel:   "ernie-5.1",
		EmbeddingModel: "embedding-v1",
	},
	ProviderSiliconFlow: {
		BaseURL:        "https://api.siliconflow.cn/v1",
		DefaultModel:   "deepseek-ai/DeepSeek-V4-Flash",
		EmbeddingModel: "BAAI/bge-m3",
	},
	ProviderMoonshot: {
		BaseURL:      "https://api.moonshot.cn/v1",
		DefaultModel: "kimi-k2.6",
		// Moonshot 官方暂无 embedding 模型
	},
	ProviderOpenAI: {
		BaseURL:        "https://api.openai.com/v1",
		DefaultModel:   "gpt-5.6-terra",
		EmbeddingModel: "text-embedding-3-small",
	},
	ProviderAnthropic: {
		BaseURL:      defaultAnthropicBaseURL,
		DefaultModel: defaultAnthropicModel,
	},
	ProviderGemini: {
		BaseURL:      defaultGeminiBaseURL,
		DefaultModel: defaultGeminiModel,
	},
}

// Presets 保留旧版导出变量以兼容既有调用方。
//
// Deprecated: 不要修改此 map。包内逻辑不依赖它，新增代码请改用 AllPresets。
var Presets = maps.Clone(presetCatalog)

// AllPresets 返回平台预设的副本，调用方可读取但不会修改包内全局状态。
func AllPresets() map[ProviderName]Preset {
	return maps.Clone(presetCatalog)
}

// NewProviderFromPreset 使用预设配置快速创建 Provider，
// 只需要提供 APIKey，其余使用平台默认值。
// model 可选，留空时使用预设的 DefaultModel。
func NewProviderFromPreset(name ProviderName, apiKey, model string) (Provider, error) {
	preset, ok := presetCatalog[name]
	if !ok {
		return nil, fmt.Errorf("no preset for provider %q", name)
	}

	if model == "" {
		model = preset.DefaultModel
	}

	switch name {
	case ProviderAnthropic:
		return NewAnthropicProvider(NativeProviderConfig{
			APIKey:  apiKey,
			BaseURL: preset.BaseURL,
			Model:   model,
		})
	case ProviderGemini:
		return NewGeminiProvider(NativeProviderConfig{
			APIKey:  apiKey,
			BaseURL: preset.BaseURL,
			Model:   model,
		})
	default:
		return NewProvider(ProviderConfig{
			Name:    name,
			BaseURL: preset.BaseURL,
			APIKey:  apiKey,
			Model:   model,
		})
	}
}

// NewEmbedderFromPreset 使用预设配置快速创建 Embedder，只需提供 APIKey。
// model 可选，留空时使用预设的 EmbeddingModel。
// 若该平台无官方 embedding 模型（如 DeepSeek、Moonshot），返回错误。
func NewEmbedderFromPreset(name ProviderName, apiKey, model string) (Embedder, error) {
	preset, ok := presetCatalog[name]
	if !ok {
		return nil, fmt.Errorf("no preset for provider %q", name)
	}

	if model == "" {
		model = preset.EmbeddingModel
	}
	if model == "" {
		return nil, fmt.Errorf("provider %q does not support embedding", name)
	}

	return NewEmbedder(EmbedderConfig{
		Name:    name,
		BaseURL: preset.BaseURL,
		APIKey:  apiKey,
		Model:   model,
	})
}
