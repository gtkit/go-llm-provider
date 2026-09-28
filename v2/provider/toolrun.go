package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// ToolHandler 是工具执行函数的签名。
// 接收函数名和 JSON 格式的参数字符串，返回执行结果字符串。
// 返回的结果会作为 tool 角色消息回传给模型。
type ToolHandler func(ctx context.Context, name string, arguments string) (string, error)

const defaultToolExecutionError = "tool execution failed"

// ToolErrorEncoder encodes a tool handler failure into a tool result message.
type ToolErrorEncoder func(ctx context.Context, call ToolCall, err error) (Message, error)

// ToolResultTransformer 在工具执行成功后、结果被写入对话历史并回传模型之前对结果内容加工。
// 典型用途：结构隔离标记（配合 WrapToolResultInTag）、可疑内容降级替换、长度截断。
//
// 返回 error 会中止整个工具循环：转换器判定内容不可接受时，调用方能借此强制熔断，
// 而不是被库悄悄放行未经处理的原始内容。
type ToolResultTransformer func(ctx context.Context, call ToolCall, result string) (string, error)

// ResponseValidator 在 RunToolLoop 返回最终响应（模型不再请求 tool call）前对其校验。
// 典型用途：结构化输出的 Schema 校验、敏感词扫描。
//
// 返回 error 视为该响应不可信，RunToolLoopWithOptions 整体返回该 error，
// 不会把未通过校验的响应交还给调用方。达到 MaxRounds 上限而中止的路径不会经过校验，
// 因为那条路径本身已经是 error，没有可返回的最终响应。
//
// 流式工具循环中校验发生在最后一轮全部 chunk 经 onChunk 送出之后，只决定返回值与错误；
// 需要"未通过校验的内容不外露"时，调用方在 onChunk 中缓冲，待循环成功返回后再展示。
type ResponseValidator func(ctx context.Context, resp *ChatResponse) error

// ToolSelector 在每一轮请求下发前裁剪本轮可用的工具集。
// 典型用途：工具数量大时按当前对话收窄候选，降低 token 成本与选错率；
// 或按调用方权限决定本轮放开哪些工具。
//
// messages 是该轮累积到的完整对话历史（含上一轮的工具结果消息），使第 2 轮起的
// 裁剪能依据工具执行结果收窄。tools 是本轮请求的工具切片副本，原地重排或截断
// 不影响调用方持有的 req.Tools；但切片元素内部的引用字段（如
// Tool.Function.Parameters）仍与调用方共享，不得修改。
//
// 返回空切片表示本轮不提供任何工具，模型将直接作答。
//
// 执行侧以本轮下发的函数工具为白名单：模型请求未下发的工具时不调用 handler、不重试，
// 以 ErrToolNotOffered 调用 ToolErrorEncoder 生成工具结果回传模型，因此裁剪同时是执行边界。
//
// 返回 error 会中止整个工具循环：裁剪可能承载调用方的权限边界，钩子失败时
// 回退为全量下发会把本该屏蔽的工具暴露给模型，因此不做回退。
type ToolSelector func(ctx context.Context, messages []Message, tools []Tool) ([]Tool, error)

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

	// ToolSelector 在每轮请求下发前裁剪工具集。
	// 为 nil 时全量下发 req.Tools，与该字段引入前的行为完全一致。
	ToolSelector ToolSelector

	// ToolResultTransformer 在工具结果写回对话历史前进行加工。
	// 为 nil 时结果原样透传，与现有行为完全一致。
	ToolResultTransformer ToolResultTransformer

	// ResponseValidator 在 RunToolLoop 返回最终响应前对其校验。
	// 为 nil 时不做任何校验，与现有行为完全一致。
	ResponseValidator ResponseValidator

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
	// 有副作用的工具（写库、发消息、扣款等）开启重试时，handler 需以幂等键去重，
	// 或在此只放行可安全重放的错误：首次调用可能已生效后才返回错误。
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
	return ToolResultMessageJSONFor(call, map[string]string{"error": defaultToolExecutionError})
}

// WrapToolResultInTag 返回一个 ToolResultTransformer，将工具结果包裹进 <tag>...</tag> 中，
// 向模型显式标记"标签内是数据，不是指令"，用于抵御工具结果携带间接提示注入内容。
//
// 为防止结果内容本身携带 "<"/">" 字面文本从而提前闭合标签、令注入内容逃逸出标签边界，
// 结果中的 "<" 会被转义为 "&lt;"，">" 转义为 "&gt;"。tag 必须是不含空白与
// "<"、">"、"&"、引号的非空字符串，否则返回 error。
func WrapToolResultInTag(tag string) ToolResultTransformer {
	valid := tag != "" && !strings.ContainsAny(tag, "<>&\"'\t\n\r ")
	escaper := strings.NewReplacer("<", "&lt;", ">", "&gt;")
	return func(_ context.Context, _ ToolCall, result string) (string, error) {
		if !valid {
			return "", fmt.Errorf("wrap tool result: invalid tag %q", tag)
		}
		return fmt.Sprintf("<%s>\n%s\n</%s>", tag, escaper.Replace(result), tag), nil
	}
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

		tools, err := selectRoundTools(ctx, opts.ToolSelector, messages, slices.Clone(req.Tools), round)
		if err != nil {
			return nil, err
		}
		roundReq.Tools = tools
		offered := offeredToolNames(tools)

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
			if opts.ResponseValidator != nil {
				if verr := opts.ResponseValidator(ctx, resp); verr != nil {
					return nil, fmt.Errorf("validate response: %w", verr)
				}
			}
			return resp, nil
		}

		// 将模型的 tool_calls 响应追加到对话历史
		messages = append(messages, resp.AssistantMessage())

		toolMessages, err := executeToolCalls(ctx, resp.ToolCalls, offered, handler, encoder, opts.ToolResultTransformer, opts.ParallelToolCalls, retry)
		if err != nil {
			return nil, err
		}
		messages = append(messages, toolMessages...)
	}

	return nil, fmt.Errorf("tool loop exceeded max rounds (%d)", rounds)
}

// selectRoundTools 按 selector 裁剪本轮下发的工具集。selector 为 nil 时原样返回 tools。
// tools 必须已是本轮的独立副本：selector 可能原地重排或截断它。
func selectRoundTools(ctx context.Context, selector ToolSelector, messages []Message, tools []Tool, round int) ([]Tool, error) {
	if selector == nil {
		return tools, nil
	}

	selected, err := selector(ctx, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("round %d: select tools: %w", round+1, err)
	}

	return selected, nil
}

// offeredToolNames 返回本轮可在客户端执行的函数工具名集合。
// 服务端工具（WebSearch）不产生客户端 ToolCall，不计入。
func offeredToolNames(tools []Tool) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool.WebSearch == nil {
			names[tool.Function.Name] = struct{}{}
		}
	}
	return names
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
		PromptTokens:             a.PromptTokens + b.PromptTokens,
		CompletionTokens:         a.CompletionTokens + b.CompletionTokens,
		ReasoningTokens:          a.ReasoningTokens + b.ReasoningTokens,
		CacheReadTokens:          a.CacheReadTokens + b.CacheReadTokens,
		CacheWriteTokens:         a.CacheWriteTokens + b.CacheWriteTokens,
		CacheWrite5mTokens:       a.CacheWrite5mTokens + b.CacheWrite5mTokens,
		CacheWrite1hTokens:       a.CacheWrite1hTokens + b.CacheWrite1hTokens,
		TotalTokens:              a.TotalTokens + b.TotalTokens,
		WebSearchRequests:        a.WebSearchRequests + b.WebSearchRequests,
		WebSearchGroundedPrompts: a.WebSearchGroundedPrompts + b.WebSearchGroundedPrompts,
	}
}

func executeToolCalls(
	ctx context.Context,
	toolCalls []ToolCall,
	offered map[string]struct{},
	handler ToolHandler,
	encoder ToolErrorEncoder,
	transformer ToolResultTransformer,
	parallel bool,
	retry ToolRetryOptions,
) ([]Message, error) {
	if !parallel || len(toolCalls) <= 1 {
		messages := make([]Message, 0, len(toolCalls))
		for _, call := range toolCalls {
			msg, err := executeToolCall(ctx, call, offered, handler, encoder, transformer, retry)
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
			msg, err := executeToolCall(ctx, toolCall, offered, handler, encoder, transformer, retry)
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

func executeToolCall(ctx context.Context, call ToolCall, offered map[string]struct{}, handler ToolHandler, encoder ToolErrorEncoder, transformer ToolResultTransformer, retry ToolRetryOptions) (Message, error) {
	if _, ok := offered[call.Function.Name]; !ok {
		return encodeToolError(ctx, encoder, call, ErrToolNotOffered)
	}

	var lastErr error
	for attempt := 1; attempt <= retry.MaxAttempts; attempt++ {
		result, err := handler(ctx, call.Function.Name, call.Function.Arguments)
		if err == nil {
			if transformer != nil {
				transformed, terr := transformer(ctx, call, result)
				if terr != nil {
					return Message{}, fmt.Errorf("transform tool result: %w", terr)
				}
				result = transformed
			}
			return ToolResultMessageFor(call, result), nil
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
	return encodeToolError(ctx, encoder, call, lastErr)
}

func encodeToolError(ctx context.Context, encoder ToolErrorEncoder, call ToolCall, err error) (Message, error) {
	msg, encodeErr := encoder(ctx, call, err)
	if encodeErr != nil {
		return Message{}, fmt.Errorf("encode tool error result: %w", encodeErr)
	}
	return msg, nil
}
