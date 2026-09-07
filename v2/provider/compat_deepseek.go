package provider

import (
	openai "github.com/sashabaranov/go-openai"
)

// DeepSeek 的思考开关落在 Chat Completions 请求的 chat_template_kwargs 上
// （{"chat_template_kwargs": {"enable_thinking": true}}）。该字段 go-openai 的请求
// 结构可以表达，因此直接写类型化字段，不走 extrafields.go 的字节注入通道。

const deepseekEnableThinkingKey = "enable_thinking"

// applyDeepSeekThinking 在显式设置了 Thinking.Enabled 时写入 DeepSeek 的思考开关。
// Enabled 为 nil 时不写入该键，由 DeepSeek 按模型自身的行为决定是否思考。
func applyDeepSeekThinking(req *openai.ChatCompletionRequest, thinking *Thinking) {
	if req == nil || thinking == nil || thinking.Enabled == nil {
		return
	}

	if req.ChatTemplateKwargs == nil {
		req.ChatTemplateKwargs = make(map[string]any, 1)
	}
	req.ChatTemplateKwargs[deepseekEnableThinkingKey] = *thinking.Enabled
}
