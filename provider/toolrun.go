package provider

import (
	"context"
	"fmt"
)

// ToolHandler 是工具执行函数的签名。
// 接收函数名和 JSON 格式的参数字符串，返回执行结果字符串。
// 返回的结果会作为 tool 角色消息回传给模型。
type ToolHandler func(ctx context.Context, name string, arguments string) (string, error)

// RunToolLoop 自动执行 Tool Use 的完整循环：
//
//  1. 发送初始请求
//  2. 如果模型返回 tool_calls，调用 handler 执行每个工具
//  3. 将工具结果回传给模型
//  4. 重复步骤 2-3，直到模型返回文本回复（FinishReason != "tool_calls"）
//
// maxRounds 限制最大循环次数，防止模型无限调用工具。
// 推荐值为 5-10，设为 0 表示不限制（不推荐）。
//
// 用法示例：
//
//	resp, err := provider.RunToolLoop(ctx, p, req, 10, func(ctx context.Context, name, args string) (string, error) {
//	    switch name {
//	    case "get_weather":
//	        return getWeather(args)
//	    case "search":
//	        return search(args)
//	    default:
//	        return "", fmt.Errorf("unknown tool: %s", name)
//	    }
//	})
func RunToolLoop(ctx context.Context, p Provider, req *ChatRequest, maxRounds int, handler ToolHandler) (*ChatResponse, error) {
	if handler == nil {
		return nil, fmt.Errorf("tool handler is required")
	}

	// 复制 messages，不修改调用方的原始切片
	messages := make([]Message, len(req.Messages))
	copy(messages, req.Messages)

	for round := range maxIterations(maxRounds) {
		// 构建本轮请求
		roundReq := &ChatRequest{
			Model:             req.Model,
			Messages:          messages,
			MaxTokens:         req.MaxTokens,
			Temperature:       req.Temperature,
			TopP:              req.TopP,
			Stop:              req.Stop,
			Tools:             req.Tools,
			ToolChoice:        req.ToolChoice,
			ParallelToolCalls: req.ParallelToolCalls,
		}

		resp, err := p.Chat(ctx, roundReq)
		if err != nil {
			return nil, fmt.Errorf("round %d: %w", round+1, err)
		}

		// 模型没有请求 tool call，返回最终结果
		if !resp.HasToolCalls() {
			return resp, nil
		}

		// 将模型的 tool_calls 响应追加到对话历史
		messages = append(messages, resp.AssistantMessage())

		// 执行每个 tool call
		for _, tc := range resp.ToolCalls {
			result, err := handler(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				// 工具执行出错时，将错误信息作为结果返回给模型，
				// 让模型有机会纠正或换一种方式处理。
				result = fmt.Sprintf(`{"error": "%s"}`, err.Error())
			}
			messages = append(messages, ToolResultMessage(tc.ID, result))
		}
	}

	return nil, fmt.Errorf("tool loop exceeded max rounds (%d)", maxRounds)
}

// maxIterations 返回一个用于 for range 的迭代次数。
// 如果 n <= 0，默认使用 20 作为安全上限。
func maxIterations(n int) int {
	if n <= 0 {
		return 20
	}
	return n
}
