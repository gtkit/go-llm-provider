package provider

// 阿里云百炼（通义千问）的深度思考控制是 Chat Completions 请求体顶层的两个扩展
// 字段（{"enable_thinking": true, "thinking_budget": 1024}），go-openai 的请求结构
// 无法表达它们，改由 extrafields.go 的通用注入通道在请求发出前补上。

const (
	qwenEnableThinkingField = "enable_thinking"
	qwenThinkingBudgetField = "thinking_budget"
)

// qwenExtraFields 产出百炼的顶层思考控制字段。
// 两个字段互相独立：只设置其一时只注入其一，都未设置时不注入任何字段，
// 由百炼按模型自身的行为决定是否深度思考、思考多少 token。
func qwenExtraFields(req *ChatRequest) map[string]any {
	if req == nil || req.Thinking == nil {
		return nil
	}

	fields := make(map[string]any, 2)
	if req.Thinking.Enabled != nil {
		fields[qwenEnableThinkingField] = *req.Thinking.Enabled
	}
	if req.Thinking.BudgetTokens != nil {
		fields[qwenThinkingBudgetField] = *req.Thinking.BudgetTokens
	}
	if len(fields) == 0 {
		return nil
	}

	return fields
}
