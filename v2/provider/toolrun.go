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

	// ToolRetry 配置单个工具调用在 handler 返回错误时的重试。
	// 零值表示不重试（仅执行一次），保持既有行为。
	ToolRetry ToolRetryOptions

	// AccumulateUsage 为 true 时，返回响应的 Usage 为所有轮次 token 消耗的累加值；
	// 默认 false，保持返回最后一轮 Usage 的既有行为。
	AccumulateUsage bool
}

// ToolRetryOptions 配置工具调用的重试行为。
type ToolRetryOptions struct {
	// MaxAttempts 是单个工具调用的最大尝试次数（含首次）。
	// <= 1 表示不重试。
	MaxAttempts int

	// Backoff 返回每次重试前的等待时长。nil 时不等待。
	Backoff BackoffFunc

	// ShouldRetry 判定某个 handler 错误是否值得重试。
	// nil 时默认重试所有非 context 取消错误。
	ShouldRetry func(err error) bool
}

func normalizeToolRetry(opts ToolRetryOptions) ToolRetryOptions {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 1
	}
	if opts.Backoff == nil {
		opts.Backoff = ConstantBackoff(0)
	}
	if opts.ShouldRetry == nil {
		opts.ShouldRetry = func(error) bool { return true }
	}
	return opts
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
// maxRounds <= 0 时使用 20 轮安全上限；推荐显式设为 5-10。
//
// 返回的最终响应默认携带最后一轮的 Usage；如需所有轮次的累加值，
// 改用 RunToolLoopWithOptions 并设置 AccumulateUsage。
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
	retry := normalizeToolRetry(opts.ToolRetry)

	var totalUsage Usage
	rounds := maxIterations(opts.MaxRounds)
	for round := range rounds {
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
		totalUsage = addUsage(totalUsage, resp.Usage)

		// 模型没有请求 tool call，返回最终结果。
		if !resp.HasToolCalls() {
			if opts.AccumulateUsage {
				resp.Usage = totalUsage
			}
			return resp, nil
		}

		// 将模型的 tool_calls 响应追加到对话历史
		messages = append(messages, resp.AssistantMessage())

		toolMessages, err := executeToolCalls(ctx, resp.ToolCalls, handler, encoder, opts.ParallelToolCalls, retry)
		if err != nil {
			return nil, err
		}
		messages = append(messages, toolMessages...)
	}

	return nil, fmt.Errorf("tool loop exceeded max rounds (%d)", rounds)
}

// maxIterations 返回一个用于 for range 的迭代次数。
// 如果 n <= 0，默认使用 20 作为安全上限。
func maxIterations(n int) int {
	if n <= 0 {
		return 20
	}
	return n
}

// addUsage 逐字段累加两个 Usage。
func addUsage(a, b Usage) Usage {
	return Usage{
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
		ReasoningTokens:  a.ReasoningTokens + b.ReasoningTokens,
		TotalTokens:      a.TotalTokens + b.TotalTokens,
	}
}

func executeToolCalls(
	ctx context.Context,
	toolCalls []ToolCall,
	handler ToolHandler,
	encoder ToolErrorEncoder,
	parallel bool,
	retry ToolRetryOptions,
) ([]Message, error) {
	if !parallel || len(toolCalls) <= 1 {
		messages := make([]Message, 0, len(toolCalls))
		for _, call := range toolCalls {
			msg, err := executeToolCall(ctx, call, handler, encoder, retry)
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
			msg, err := executeToolCall(ctx, toolCall, handler, encoder, retry)
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

func executeToolCall(ctx context.Context, call ToolCall, handler ToolHandler, encoder ToolErrorEncoder, retry ToolRetryOptions) (Message, error) {
	var lastErr error
	for attempt := 1; attempt <= retry.MaxAttempts; attempt++ {
		result, err := handler(ctx, call.Function.Name, call.Function.Arguments)
		if err == nil {
			return ToolResultMessage(call.ID, result), nil
		}

		// context 取消：立即上抛，不重试、不编码回模型。
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			return Message{}, fmt.Errorf("tool execution canceled: %w", ctxErr)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Message{}, err
		}

		lastErr = err
		if attempt < retry.MaxAttempts && retry.ShouldRetry(err) {
			if werr := waitBackoff(ctx, retry.Backoff(attempt)); werr != nil {
				return Message{}, werr
			}
			continue
		}
		break
	}

	// 重试耗尽或不可重试：按既有行为将错误编码为 tool 结果回传模型。
	msg, encodeErr := encoder(ctx, call, lastErr)
	if encodeErr != nil {
		return Message{}, fmt.Errorf("encode tool error result: %w", encodeErr)
	}
	return msg, nil
}
