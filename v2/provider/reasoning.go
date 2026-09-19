package provider

import (
	"fmt"
	"strings"
)

// Thinking 控制平台侧的推理（思考）行为，零值不下发任何推理参数。
//
// 三个字段对应平台的两套控制口径：Effort 是档位口径（OpenAI 的 reasoning_effort、
// Anthropic 的 output_config.effort、Gemini 的 thinkingLevel），BudgetTokens 是
// 预算口径（Anthropic 旧模型的 thinking.budget_tokens、Gemini 2.5 的 thinkingBudget、
// 百炼的 thinking_budget）。各字段的平台支持范围：
//
//	平台                     Enabled  Effort  BudgetTokens
//	OpenAI / Azure              -       ✓          -
//	DeepSeek                    ✓       ✓          -
//	智谱 GLM                    ✓       ✓          -
//	Moonshot / Kimi             ✓       ✓          -
//	火山方舟 Ark                ✓       ✓          -
//	阿里云百炼 Qwen             ✓       -          ✓
//	Anthropic（原生）           ✓       ✓          ✓（仅 4.5 及更早模型）
//	Gemini（原生）              ✓       ✓          ✓（与 Effort 互斥）
//	Ollama（原生）              ✓       ✓          -
//
// 传入表中未标注的字段时，请求构建阶段返回 ErrInvalidRequest 并在错误信息中
// 列出该平台已映射的字段——推理 token 按输出价计费，静默忽略会让调用方
// 误以为思考已开启并为未发生的思考付费。
// 可用 ModelCapabilities.Supports(CapabilityReasoning) 提前判断平台是否支持推理控制。
type Thinking struct {
	// Enabled 显式开启或关闭平台侧思考；nil 表示不干预平台默认行为。
	// 注意部分模型不允许关闭思考（GLM-5.3、Kimi K3、Gemini 3 系），传 false 由平台报错。
	Enabled *bool

	// Effort 按档位控制推理深度，取值原样透传给平台的档位字段，
	// ThinkingEffort* 是便利常量而非取值全集，各平台接受的档位集合不同：
	//   - OpenAI：none / minimal / low / medium / high / xhigh / max（按模型而异）；
	//   - DeepSeek / 智谱 / Kimi K3：low / high / max；
	//   - Anthropic：low / medium / high / xhigh / max（按模型而异）；
	//   - Gemini：minimal / low / medium / high（按模型而异）；
	//   - Ollama：low / medium / high（gpt-oss 等支持档位的模型，透传为 think 字符串）。
	Effort string

	// BudgetTokens 按 token 预算控制推理深度，nil 表示不指定预算。
	// 各平台对取值的语义不同：
	//   - Anthropic 只接受正数，非正数返回 ErrInvalidRequest；下发为旧式
	//     thinking.type=enabled + budget_tokens，Claude 4.6 起该口径已废弃、
	//     4.7 起平台直接拒绝，新模型请改用 Enabled + Effort（自适应思考）；
	//     预算还需小于本次请求的 MaxTokens（未显式设置时为 4096），该上限由平台校验；
	//   - Gemini 以 0 表示禁用思考、-1 表示由模型动态决定预算，取值由平台校验；
	//     Gemini 3 系推荐改用 Effort（thinkingLevel），两者同时设置返回 ErrInvalidRequest；
	//   - 百炼原样透传为顶层 thinking_budget，取值由平台校验。
	BudgetTokens *int
}

const (
	// ThinkingEffortNone 请求关闭推理（OpenAI gpt-5.1 及之后、Mistral、Cohere 等接受）。
	ThinkingEffortNone = "none"
	// ThinkingEffortMinimal 请求最低推理力度。
	ThinkingEffortMinimal = "minimal"
	// ThinkingEffortLow 请求较低推理力度。
	ThinkingEffortLow = "low"
	// ThinkingEffortMedium 请求中等推理力度。
	ThinkingEffortMedium = "medium"
	// ThinkingEffortHigh 请求较高推理力度。
	ThinkingEffortHigh = "high"
	// ThinkingEffortXHigh 请求极高推理力度（OpenAI gpt-5.2 及之后、Anthropic）。
	ThinkingEffortXHigh = "xhigh"
	// ThinkingEffortMax 请求最高推理力度（DeepSeek V4、GLM-5.3、Kimi K3、OpenAI gpt-5.6、Anthropic）。
	ThinkingEffortMax = "max"
)

// thinkingSupport 描述单个平台对 Thinking 各字段的支持情况，
// 以及是否接受 assistant 消息上的 reasoning_content 回传。
type thinkingSupport struct {
	enabled bool
	effort  bool
	budget  bool
	// reasoningEcho 为 true 时把 Message.Reasoning 映射为 reasoning_content 下发。
	// 该字段是 OpenAI 兼容协议的扩展，OpenAI 官方端点会拒绝未知字段，只对已知
	// 接受它的平台开启。
	reasoningEcho bool
}

// thinkingSupportByProvider 是 Thinking 字段平台支持范围的单一真源，
// 与 Thinking 文档注释中的表格、presets 中的 CapabilityReasoning 声明同步维护。
// 未列出的平台不支持任何推理控制字段。
var thinkingSupportByProvider = map[ProviderName]thinkingSupport{
	ProviderOpenAI:      {effort: true},
	ProviderAzureOpenAI: {effort: true},
	ProviderDeepSeek:    {enabled: true, effort: true, reasoningEcho: true},
	ProviderZhipu:       {enabled: true, effort: true, reasoningEcho: true},
	ProviderMoonshot:    {enabled: true, effort: true, reasoningEcho: true},
	ProviderArk:         {enabled: true, effort: true, reasoningEcho: true},
	ProviderQwen:        {enabled: true, budget: true, reasoningEcho: true},
	ProviderAnthropic:   {enabled: true, effort: true, budget: true},
	ProviderGemini:      {enabled: true, effort: true, budget: true},
	ProviderOllama:      {enabled: true, effort: true},
}

// Thinking 各字段在错误信息中的名字，集中一处以免措辞漂移。
const (
	thinkingFieldEnabled = "Thinking.Enabled"
	thinkingFieldEffort  = "Thinking.Effort"
	thinkingFieldBudget  = "Thinking.BudgetTokens"
)

// resolveThinkingSupport 决定某个平台的推理字段支持情况。
//
// 内置预设以库内的映射表为准——它们的映射由库实现，调用方无需也不应覆盖。
// 映射表未收录的平台（用 NewProvider 自定义接入的 OpenAI 兼容平台、自建推理
// 服务）以调用方的显式声明为准：库不认识这个平台，无从判断它接受哪些参数，
// 与其一律拒绝，不如让知情的调用方声明。
func resolveThinkingSupport(name ProviderName, declared thinkingSupport) thinkingSupport {
	if support, ok := thinkingSupportByProvider[name]; ok {
		return support
	}
	return declared
}

// validateThinking 按库内映射表校验，用于协议固定的原生路径。
func validateThinking(provider ProviderName, thinking *Thinking) error {
	return validateThinkingWith(provider, thinkingSupportByProvider[provider], thinking)
}

// validateThinkingWith 校验 thinking 中每个已设置的字段在该平台上都有映射，
// 存在无映射的字段时返回 ErrInvalidRequest。thinking 为 nil 时放行。
func validateThinkingWith(provider ProviderName, support thinkingSupport, thinking *Thinking) error {
	if thinking == nil {
		return nil
	}
	switch {
	case thinking.Enabled != nil && !support.enabled:
		return unsupportedThinkingFieldError(provider, thinkingFieldEnabled, support)
	case thinking.Effort != "" && !support.effort:
		return unsupportedThinkingFieldError(provider, thinkingFieldEffort, support)
	case thinking.BudgetTokens != nil && !support.budget:
		return unsupportedThinkingFieldError(provider, thinkingFieldBudget, support)
	}
	return nil
}

// unsupportedThinkingFieldError 构造带指引的 ErrInvalidRequest。
// 措辞按"本库是否为该平台映射了该字段"表述，不断言平台自身的能力边界：
// 平台另有可用字段时列出，完全没有映射时直接说明。
func unsupportedThinkingFieldError(provider ProviderName, field string, support thinkingSupport) error {
	var supported []string
	if support.enabled {
		supported = append(supported, thinkingFieldEnabled)
	}
	if support.effort {
		supported = append(supported, thinkingFieldEffort)
	}
	if support.budget {
		supported = append(supported, thinkingFieldBudget)
	}
	if len(supported) == 0 {
		// 自定义接入的 OpenAI 兼容平台只有 Effort 有通用映射（reasoning_effort
		// 是 OpenAI 的标准字段），指出声明入口；Enabled 与 BudgetTokens 各家
		// 落在不同的私有字段上，库无从代为映射，不给出误导性的指引。
		if field == thinkingFieldEffort {
			return fmt.Errorf(
				"%w: no reasoning control mapping for %s, got %s"+
					" (set ProviderConfig.SupportsReasoningEffort if this platform accepts reasoning_effort)",
				ErrInvalidRequest, provider, field)
		}
		return fmt.Errorf("%w: no reasoning control mapping for %s, got %s",
			ErrInvalidRequest, provider, field)
	}
	return fmt.Errorf("%w: no %s mapping for %s, mapped fields: %s",
		ErrInvalidRequest, field, provider, strings.Join(supported, ", "))
}
