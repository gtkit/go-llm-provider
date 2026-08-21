package provider

import (
	"errors"
	"fmt"
	"slices"
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
	// RerankModel 是该平台推荐的默认 rerank 模型。
	// 空字符串表示预设未覆盖该平台的 rerank 端点，
	// NewRerankerFromPreset 遇到空串会返回错误；显式传入 model 即可绕过。
	RerankModel string
	// Capabilities 描述预设默认模型的已知能力。
	Capabilities ModelCapabilities
}

var presetCatalog = map[ProviderName]Preset{
	ProviderDeepSeek: {
		BaseURL:      "https://api.deepseek.com/v1",
		DefaultModel: "deepseek-chat", // DeepSeek-V3.2 非思考模式
		// DeepSeek 官方暂无 embedding 模型
		Capabilities: ModelCapabilities{
			Provider:  ProviderDeepSeek,
			ChatModel: "deepseek-chat",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityReasoning,
			},
		},
	},
	ProviderZhipu: {
		BaseURL:        "https://open.bigmodel.cn/api/paas/v4/",
		DefaultModel:   "glm-5.1",
		EmbeddingModel: "embedding-3",
		Capabilities: ModelCapabilities{
			Provider:       ProviderZhipu,
			ChatModel:      "glm-5.1",
			EmbeddingModel: "embedding-3",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityEmbedding,
				CapabilityFileUpload,
			},
		},
	},
	ProviderQwen: {
		// 阿里百炼 / DashScope OpenAI 兼容端点
		BaseURL:        "https://dashscope.aliyuncs.com/compatible-mode/v1",
		DefaultModel:   "qwen3.6-plus",
		EmbeddingModel: "text-embedding-v3",
		Capabilities: ModelCapabilities{
			Provider:       ProviderQwen,
			ChatModel:      "qwen3.6-plus",
			EmbeddingModel: "text-embedding-v3",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityEmbedding,
				CapabilityFileUpload,
			},
		},
	},
	ProviderQianfan: {
		// 百度千帆 OpenAI 兼容 V2 接口
		BaseURL:        "https://qianfan.baidubce.com/v2",
		DefaultModel:   "ernie-4.5-turbo-32k",
		EmbeddingModel: "embedding-v1",
		Capabilities: ModelCapabilities{
			Provider:       ProviderQianfan,
			ChatModel:      "ernie-4.5-turbo-32k",
			EmbeddingModel: "embedding-v1",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityEmbedding,
			},
		},
	},
	ProviderSiliconFlow: {
		BaseURL:        "https://api.siliconflow.cn/v1",
		DefaultModel:   "deepseek-ai/DeepSeek-V3",
		EmbeddingModel: "BAAI/bge-m3",
		RerankModel:    "BAAI/bge-reranker-v2-m3",
		Capabilities: ModelCapabilities{
			Provider:       ProviderSiliconFlow,
			ChatModel:      "deepseek-ai/DeepSeek-V3",
			EmbeddingModel: "BAAI/bge-m3",
			RerankModel:    "BAAI/bge-reranker-v2-m3",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityEmbedding,
				CapabilityRerank,
			},
		},
	},
	ProviderMoonshot: {
		BaseURL:      "https://api.moonshot.cn/v1",
		DefaultModel: "kimi-k2-turbo-preview",
		// Moonshot 官方暂无 embedding 模型
		Capabilities: ModelCapabilities{
			Provider:  ProviderMoonshot,
			ChatModel: "kimi-k2-turbo-preview",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityFileUpload,
			},
		},
	},
	ProviderArk: {
		// 火山方舟 OpenAI 兼容端点（豆包等模型）
		BaseURL:        "https://ark.cn-beijing.volces.com/api/v3",
		DefaultModel:   "doubao-seed-2-0-pro-260215",
		EmbeddingModel: "doubao-embedding-text-240515",
		Capabilities: ModelCapabilities{
			Provider:       ProviderArk,
			ChatModel:      "doubao-seed-2-0-pro-260215",
			EmbeddingModel: "doubao-embedding-text-240515",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityReasoning,
				CapabilityVision,
				CapabilityEmbedding,
			},
		},
	},
	ProviderOpenAI: {
		BaseURL:        "https://api.openai.com/v1",
		DefaultModel:   "gpt-5.4-mini",
		EmbeddingModel: "text-embedding-3-small",
		Capabilities: ModelCapabilities{
			Provider:       ProviderOpenAI,
			ChatModel:      "gpt-5.4-mini",
			EmbeddingModel: "text-embedding-3-small",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityReasoning,
				CapabilityEmbedding,
				CapabilityFileUpload,
			},
		},
	},
	ProviderAnthropic: {
		BaseURL:      defaultAnthropicBaseURL,
		DefaultModel: defaultAnthropicModel,
		Capabilities: ModelCapabilities{
			Provider:  ProviderAnthropic,
			ChatModel: defaultAnthropicModel,
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityVision,
				CapabilityFile,
				CapabilityWebSearch,
			},
		},
	},
	ProviderGemini: {
		BaseURL:        defaultGeminiBaseURL,
		DefaultModel:   defaultGeminiModel,
		EmbeddingModel: defaultGeminiEmbeddingModel,
		Capabilities: ModelCapabilities{
			Provider:       ProviderGemini,
			ChatModel:      defaultGeminiModel,
			EmbeddingModel: defaultGeminiEmbeddingModel,
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityVision,
				CapabilityFile,
				CapabilityEmbedding,
				CapabilityWebSearch,
			},
		},
	},
	ProviderOllama: {
		BaseURL: defaultOllamaBaseURL,
		Capabilities: ModelCapabilities{
			Provider: ProviderOllama,
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
			},
		},
	},
	ProviderXAI: {
		BaseURL:      "https://api.x.ai/v1",
		DefaultModel: "grok-4-1-fast-non-reasoning",
		Capabilities: ModelCapabilities{
			Provider:  ProviderXAI,
			ChatModel: "grok-4-1-fast-non-reasoning",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
			},
		},
	},
	ProviderGroq: {
		BaseURL:      "https://api.groq.com/openai/v1",
		DefaultModel: "llama-3.3-70b-versatile",
		Capabilities: ModelCapabilities{
			Provider:  ProviderGroq,
			ChatModel: "llama-3.3-70b-versatile",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
			},
		},
	},
	ProviderMistral: {
		BaseURL:        "https://api.mistral.ai/v1",
		DefaultModel:   "mistral-large-latest",
		EmbeddingModel: "mistral-embed",
		Capabilities: ModelCapabilities{
			Provider:       ProviderMistral,
			ChatModel:      "mistral-large-latest",
			EmbeddingModel: "mistral-embed",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityEmbedding,
			},
		},
	},
	ProviderCohere: {
		BaseURL:        "https://api.cohere.ai/compatibility/v1",
		DefaultModel:   "command-a-03-2025",
		EmbeddingModel: "embed-v4.0",
		Capabilities: ModelCapabilities{
			Provider:       ProviderCohere,
			ChatModel:      "command-a-03-2025",
			EmbeddingModel: "embed-v4.0",
			Capabilities: []Capability{
				CapabilityChat,
				CapabilityStreaming,
				CapabilityTools,
				CapabilityStructuredOutput,
				CapabilityEmbedding,
			},
		},
	},
}

// Presets 保留旧版导出变量以兼容既有调用方。
//
// Deprecated: 不要修改此 map。包内逻辑不依赖它，新增代码请改用 AllPresets。
var Presets = AllPresets()

// AllPresets 返回平台预设的副本，调用方可读取但不会修改包内全局状态。
func AllPresets() map[ProviderName]Preset {
	out := make(map[ProviderName]Preset, len(presetCatalog))
	for name, preset := range presetCatalog {
		out[name] = clonePreset(preset)
	}
	return out
}

func clonePreset(preset Preset) Preset {
	preset.Capabilities = cloneModelCapabilities(preset.Capabilities)
	return preset
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
	case ProviderOllama:
		return NewOllamaProvider(OllamaProviderConfig{
			BaseURL: preset.BaseURL,
			Model:   model,
		})
	}

	return NewProvider(ProviderConfig{
		Name:    name,
		BaseURL: preset.BaseURL,
		APIKey:  apiKey,
		Model:   model,
	})
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

// NewRerankerFromPreset 使用预设配置快速创建 Reranker，只需提供 APIKey。
// model 可选，留空时使用预设的 RerankModel。
// 若预设未覆盖该平台的 rerank 端点，返回错误——此时用 NewReranker 显式给出
// BaseURL 与模型名即可接入。
func NewRerankerFromPreset(name ProviderName, apiKey, model string) (Reranker, error) {
	preset, ok := presetCatalog[name]
	if !ok {
		return nil, fmt.Errorf("no preset for provider %q", name)
	}

	if model == "" {
		model = preset.RerankModel
	}
	if model == "" {
		return nil, fmt.Errorf("provider %q does not have a rerank preset", name)
	}

	return NewReranker(RerankerConfig{
		Name:    name,
		BaseURL: preset.BaseURL,
		APIKey:  apiKey,
		Model:   model,
	})
}

// QuickRegistry 是一个便捷函数，根据一组 apiKey 映射快速创建 Registry。
// keys 的 key 是 ProviderName，value 是 APIKey。
// 只会注册提供了非空 APIKey 的平台。
// 同一个 APIKey 会同时尝试注册 Chat Provider 与 Embedder（若该平台有 embedding 预设）。
// 注册失败的条目会被静默跳过；如果需要显式错误，请使用 QuickRegistryStrict。
//
// 用法示例：
//
//	reg := provider.QuickRegistry(map[provider.ProviderName]string{
//	    provider.ProviderDeepSeek: os.Getenv("DEEPSEEK_API_KEY"),
//	    provider.ProviderQwen:     os.Getenv("QWEN_API_KEY"),
//	    provider.ProviderZhipu:    os.Getenv("ZHIPU_API_KEY"),
//	})
func QuickRegistry(keys map[ProviderName]string) *Registry {
	reg := NewRegistry()
	names := make([]ProviderName, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		key := keys[name]
		if key == "" {
			continue
		}

		if p, err := NewProviderFromPreset(name, key, ""); err == nil {
			reg.Register(p)
		}

		// 若该平台有 embedding 预设，同时注册 embedder；没有则静默跳过
		if e, err := NewEmbedderFromPreset(name, key, ""); err == nil {
			reg.RegisterEmbedder(e)
		}
	}
	return reg
}

// QuickRegistryStrict 与 QuickRegistry 类似，但会返回所有注册失败的错误。
// Provider 与 Embedder 的注册错误都会累积到返回值中；
// 平台明确无 embedding 模型（如 DeepSeek、Moonshot）不视为错误。
func QuickRegistryStrict(keys map[ProviderName]string) (*Registry, error) {
	reg := NewRegistry()
	names := make([]ProviderName, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	slices.Sort(names)

	var errs []error
	for _, name := range names {
		key := keys[name]
		if key == "" {
			continue
		}

		p, err := NewProviderFromPreset(name, key, "")
		if err != nil {
			errs = append(errs, fmt.Errorf("register provider %q: %w", name, err))
		} else {
			reg.Register(p)
		}

		// 该平台有 embedding 预设才尝试注册；无预设跳过不报错
		preset, ok := presetCatalog[name]
		if !ok || preset.EmbeddingModel == "" {
			continue
		}

		e, err := NewEmbedderFromPreset(name, key, "")
		if err != nil {
			errs = append(errs, fmt.Errorf("register embedder %q: %w", name, err))
			continue
		}

		reg.RegisterEmbedder(e)
	}

	if len(errs) > 0 {
		return reg, errors.Join(errs...)
	}

	return reg, nil
}
