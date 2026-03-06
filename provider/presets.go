package provider

import "fmt"

// 预置各平台的 BaseURL 和推荐默认模型。
// 业务侧只需提供 APIKey 即可快速接入。

// Preset 是一组平台预设信息。
type Preset struct {
	BaseURL      string
	DefaultModel string
}

// Presets 收录了国内主流大模型平台的 OpenAI 兼容地址。
// 所有地址均为各平台官方文档公开的 OpenAI 兼容端点。
var Presets = map[ProviderName]Preset{
	ProviderDeepSeek: {
		BaseURL:      "https://api.deepseek.com/v1",
		DefaultModel: "deepseek-chat", // DeepSeek-V3
	},
	ProviderZhipu: {
		BaseURL:      "https://open.bigmodel.cn/api/paas/v4/",
		DefaultModel: "glm-4-plus",
	},
	ProviderQwen: {
		// 阿里百炼 / DashScope OpenAI 兼容端点
		BaseURL:      "https://dashscope.aliyuncs.com/compatible-mode/v1",
		DefaultModel: "qwen-plus",
	},
	ProviderQianfan: {
		// 百度千帆 OpenAI 兼容 V2 接口
		BaseURL:      "https://qianfan.baidubce.com/v2",
		DefaultModel: "ernie-4.0-8k",
	},
	ProviderSiliconFlow: {
		BaseURL:      "https://api.siliconflow.cn/v1",
		DefaultModel: "deepseek-ai/DeepSeek-V3",
	},
	ProviderMoonshot: {
		BaseURL:      "https://api.moonshot.cn/v1",
		DefaultModel: "moonshot-v1-8k",
	},
}

// NewProviderFromPreset 使用预设配置快速创建 Provider，
// 只需要提供 APIKey，其余使用平台默认值。
// model 可选，留空时使用预设的 DefaultModel。
func NewProviderFromPreset(name ProviderName, apiKey string, model string) (Provider, error) {
	preset, ok := Presets[name]
	if !ok {
		return nil, fmt.Errorf("no preset for provider %q", name)
	}

	if model == "" {
		model = preset.DefaultModel
	}

	return NewProvider(ProviderConfig{
		Name:    name,
		BaseURL: preset.BaseURL,
		APIKey:  apiKey,
		Model:   model,
	}), nil
}

// QuickRegistry 是一个便捷函数，根据一组 apiKey 映射快速创建 Registry。
// keys 的 key 是 ProviderName，value 是 APIKey。
// 只会注册提供了非空 APIKey 的平台。
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
	for name, key := range keys {
		if key == "" {
			continue
		}
		p, err := NewProviderFromPreset(name, key, "")
		if err != nil {
			continue // 未知的 preset，跳过
		}
		reg.Register(p)
	}
	return reg
}
