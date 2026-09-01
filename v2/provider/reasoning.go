package provider

import (
	"fmt"
	"strings"
)

// Thinking 控制平台侧的推理（思考）行为，零值不下发任何推理参数。
//
// 三个字段对应平台的两套控制口径：Effort 是档位口径（OpenAI 的
// reasoning_effort），BudgetTokens 是预算口径（Anthropic 的
// thinking.budget_tokens、Gemini 的 thinkingConfig.thinkingBudget）。
// 各字段的平台支持范围：
//
//	平台                Enabled  Effort  BudgetTokens
//	OpenAI / Azure         -       ✓          -
//	火山方舟 Ark           ✓       ✓          -
//	DeepSeek               ✓       -          -
//	Anthropic（原生）      ✓       -          ✓
//	Gemini（原生）         ✓       -          ✓
//
// 传入表中未标注的字段时，请求构建阶段返回 ErrInvalidRequest 并在错误信息中
// 列出该平台已映射的字段——推理 token 按输出价计费，静默忽略会让调用方
// 误以为思考已开启并为未发生的思考付费。
// 可用 ModelCapabilities.Supports(CapabilityReasoning) 提前判断平台是否支持推理控制。
type Thinking struct {
	// Enabled 显式开启或关闭平台侧思考；nil 表示不干预平台默认行为。
	Enabled *bool

	// Effort 按档位控制推理深度，取值原样透传给平台的 reasoning_effort，
	// ThinkingEffort* 是便利常量而非取值全集。
	Effort string

	// BudgetTokens 按 token 预算控制推理深度，nil 表示不指定预算。
	// 两个平台对取值的语义不同：
	//   - Anthropic 只接受正数，非正数返回 ErrInvalidRequest；
	//     预算还需小于本次请求的 MaxTokens（未显式设置时为 4096），该上限由平台校验；
	//   - Gemini 以 0 表示禁用思考、-1 表示由模型动态决定预算，取值由平台校验。
	BudgetTokens *int
}

const (
	// ThinkingEffortMinimal 请求最低推理力度。
	ThinkingEffortMinimal = "minimal"
	// ThinkingEffortLow 请求较低推理力度。
	ThinkingEffortLow = "low"
	// ThinkingEffortMedium 请求中等推理力度。
	ThinkingEffortMedium = "medium"
	// ThinkingEffortHigh 请求较高推理力度。
	ThinkingEffortHigh = "high"
)

// thinkingSupport 描述单个平台对 Thinking 各字段的支持情况。
type thinkingSupport struct {
	enabled bool
	effort  bool
	budget  bool
}

// thinkingSupportByProvider 是 Thinking 字段平台支持范围的单一真源，
// 与 Thinking 文档注释中的表格、presets 中的 CapabilityReasoning 声明同步维护。
// 未列出的平台不支持任何推理控制字段。
var thinkingSupportByProvider = map[ProviderName]thinkingSupport{
	ProviderOpenAI:      {effort: true},
	ProviderAzureOpenAI: {effort: true},
	ProviderArk:         {enabled: true, effort: true},
	ProviderDeepSeek:    {enabled: true},
	ProviderAnthropic:   {enabled: true, budget: true},
	ProviderGemini:      {enabled: true, budget: true},
}

// validateThinking 校验 thinking 中每个已设置的字段在 provider 上都有映射，
// 存在无映射的字段时返回 ErrInvalidRequest。thinking 为 nil 时放行。
func validateThinking(provider ProviderName, thinking *Thinking) error {
	if thinking == nil {
		return nil
	}
	support := thinkingSupportByProvider[provider]
	switch {
	case thinking.Enabled != nil && !support.enabled:
		return unsupportedThinkingFieldError(provider, "Thinking.Enabled", support)
	case thinking.Effort != "" && !support.effort:
		return unsupportedThinkingFieldError(provider, "Thinking.Effort", support)
	case thinking.BudgetTokens != nil && !support.budget:
		return unsupportedThinkingFieldError(provider, "Thinking.BudgetTokens", support)
	}
	return nil
}

// unsupportedThinkingFieldError 构造带指引的 ErrInvalidRequest。
// 措辞按"本库是否为该平台映射了该字段"表述，不断言平台自身的能力边界：
// 平台另有可用字段时列出，完全没有映射时直接说明。
func unsupportedThinkingFieldError(provider ProviderName, field string, support thinkingSupport) error {
	var supported []string
	if support.enabled {
		supported = append(supported, "Thinking.Enabled")
	}
	if support.effort {
		supported = append(supported, "Thinking.Effort")
	}
	if support.budget {
		supported = append(supported, "Thinking.BudgetTokens")
	}
	if len(supported) == 0 {
		return fmt.Errorf("%w: no reasoning control mapping for %s, got %s",
			ErrInvalidRequest, provider, field)
	}
	return fmt.Errorf("%w: no %s mapping for %s, mapped fields: %s",
		ErrInvalidRequest, field, provider, strings.Join(supported, ", "))
}
