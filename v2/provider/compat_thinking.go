package provider

// DeepSeek、智谱 GLM、Moonshot Kimi 与火山方舟的深度思考开关形态一致：
// Chat Completions 请求体顶层的 thinking 扩展字段（{"thinking": {"type": "enabled"|"disabled"}}），
// 推理强度则走 OpenAI 标准的 reasoning_effort（由 applyThinking 以类型化字段下发）。
// go-openai 的请求结构无法表达顶层 thinking，改由 extrafields.go 的通用注入通道
// 在请求发出前补上。

const (
	thinkingTypeField    = "thinking"
	thinkingTypeEnabled  = "enabled"
	thinkingTypeDisabled = "disabled"
)

// thinkingTypeExtraFields 在显式设置了 Thinking.Enabled 时产出顶层 thinking 字段。
// Enabled 为 nil 时不注入该字段，由平台按模型的默认行为决定是否深度思考
// （DeepSeek V4、GLM-5、Kimi K2.6 默认开启）。
func thinkingTypeExtraFields(req *ChatRequest) map[string]any {
	if req == nil || req.Thinking == nil || req.Thinking.Enabled == nil {
		return nil
	}

	thinkingType := thinkingTypeDisabled
	if *req.Thinking.Enabled {
		thinkingType = thinkingTypeEnabled
	}

	return map[string]any{
		thinkingTypeField: map[string]any{"type": thinkingType},
	}
}
