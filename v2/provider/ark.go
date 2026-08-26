package provider

// 火山方舟（Ark）的深度思考开关是 Chat Completions 请求体顶层的 thinking
// 扩展字段（{"thinking": {"type": "enabled"|"disabled"}}），go-openai 的请求
// 结构无法表达该字段，改由 extrafields.go 的通用注入通道在请求发出前补上。

const (
	arkThinkingField        = "thinking"
	arkThinkingTypeEnabled  = "enabled"
	arkThinkingTypeDisabled = "disabled"
)

// arkExtraFields 在显式设置了 Thinking.Enabled 时产出方舟的顶层 thinking 字段。
// Enabled 为 nil 时不注入该字段，由方舟按模型的默认行为决定是否深度思考。
func arkExtraFields(req *ChatRequest) map[string]any {
	if req == nil || req.Thinking == nil || req.Thinking.Enabled == nil {
		return nil
	}

	thinkingType := arkThinkingTypeDisabled
	if *req.Thinking.Enabled {
		thinkingType = arkThinkingTypeEnabled
	}

	return map[string]any{
		arkThinkingField: map[string]any{"type": thinkingType},
	}
}
