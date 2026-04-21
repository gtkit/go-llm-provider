package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// ToolHandler 是工具执行函数的签名。
// 接收函数名和 JSON 格式的参数字符串，返回执行结果字符串。
// 返回的结果会作为 tool 角色消息回传给模型。
type ToolHandler func(ctx context.Context, name string, arguments string) (string, error)

const defaultToolExecutionError = "tool execution failed"

// ToolErrorEncoder encodes a tool handler failure into a tool result message.
type ToolErrorEncoder func(ctx context.Context, call ToolCall, err error) (Message, error)

// RunToolLoopOptions configures the additive RunToolLoop execution path.
type RunToolLoopOptions struct {
	// MaxRounds limits the maximum number of tool loop rounds.
	// If MaxRounds <= 0, a safe default limit is used.
	MaxRounds int

	// ParallelToolCalls enables concurrent execution for tool calls returned in
	// the same model response. Result messages are still appended in original order.
	ParallelToolCalls bool

	// ToolErrorEncoder customizes how tool handler errors are sent back to the model.
	// If nil, DefaultToolErrorEncoder is used.
	ToolErrorEncoder ToolErrorEncoder
}

// DefaultToolErrorEncoder returns a sanitized JSON tool result message.
func DefaultToolErrorEncoder(_ context.Context, call ToolCall, _ error) (Message, error) {
	return ToolResultMessageJSON(call.ID, map[string]string{"error": defaultToolExecutionError})
}

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
	return RunToolLoopWithOptions(ctx, p, req, handler, RunToolLoopOptions{MaxRounds: maxRounds})
}

// RunToolLoopWithOptions executes the tool loop with additive runtime controls.
func RunToolLoopWithOptions(ctx context.Context, p Provider, req *ChatRequest, handler ToolHandler, opts RunToolLoopOptions) (*ChatResponse, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}
	if handler == nil {
		return nil, ErrToolHandlerRequired
	}

	// 复制 messages，不修改调用方的原始切片
	messages := make([]Message, len(req.Messages))
	copy(messages, req.Messages)
	encoder := opts.ToolErrorEncoder
	if encoder == nil {
		encoder = DefaultToolErrorEncoder
	}

	for round := range maxIterations(opts.MaxRounds) {
		// 浅拷贝基础字段，再为可变 slice 建立独立 header，避免未来实现误修改调用方请求。
		// 注意：这里只隔离了 slice 本身，不会深拷贝 Tool.Function.Parameters 等引用类型字段。
		roundReq := *req
		roundReq.Messages = messages
		roundReq.Stop = slices.Clone(req.Stop)
		roundReq.Tools = slices.Clone(req.Tools)

		resp, err := p.Chat(ctx, &roundReq)
		if err != nil {
			return nil, fmt.Errorf("round %d: %w", round+1, err)
		}

		// 模型没有请求 tool call，返回最终结果
		if !resp.HasToolCalls() {
			return resp, nil
		}

		// 将模型的 tool_calls 响应追加到对话历史
		messages = append(messages, resp.AssistantMessage())

		toolMessages, err := executeToolCalls(ctx, resp.ToolCalls, handler, encoder, opts.ParallelToolCalls)
		if err != nil {
			return nil, err
		}
		messages = append(messages, toolMessages...)
	}

	return nil, fmt.Errorf("tool loop exceeded max rounds (%d)", opts.MaxRounds)
}

// maxIterations 返回一个用于 for range 的迭代次数。
// 如果 n <= 0，默认使用 20 作为安全上限。
func maxIterations(n int) int {
	if n <= 0 {
		return 20
	}
	return n
}

func executeToolCalls(
	ctx context.Context,
	toolCalls []ToolCall,
	handler ToolHandler,
	encoder ToolErrorEncoder,
	parallel bool,
) ([]Message, error) {
	if !parallel || len(toolCalls) <= 1 {
		messages := make([]Message, 0, len(toolCalls))
		for _, call := range toolCalls {
			msg, err := executeToolCall(ctx, call, handler, encoder)
			if err != nil {
				return nil, err
			}
			messages = append(messages, msg)
		}
		return messages, nil
	}

	results := make([]Message, len(toolCalls))
	errCh := make(chan error, len(toolCalls))
	var wg sync.WaitGroup
	wg.Add(len(toolCalls))

	for i, call := range toolCalls {
		go func(index int, toolCall ToolCall) {
			defer wg.Done()
			msg, err := executeToolCall(ctx, toolCall, handler, encoder)
			if err != nil {
				errCh <- err
				return
			}
			results[index] = msg
		}(i, call)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

func executeToolCall(ctx context.Context, call ToolCall, handler ToolHandler, encoder ToolErrorEncoder) (Message, error) {
	result, err := handler(ctx, call.Function.Name, call.Function.Arguments)
	if err == nil {
		return ToolResultMessage(call.ID, result), nil
	}

	if ctxErr := context.Cause(ctx); ctxErr != nil {
		return Message{}, fmt.Errorf("tool execution canceled: %w", ctxErr)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Message{}, err
	}

	msg, encodeErr := encoder(ctx, call, err)
	if encodeErr != nil {
		return Message{}, fmt.Errorf("encode tool error result: %w", encodeErr)
	}
	return msg, nil
}
