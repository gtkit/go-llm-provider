package provider

import (
	"context"
	"fmt"
)

// TokenCounter 统计对话请求在供应商侧的 token 数量。
type TokenCounter interface {
	CountTokens(ctx context.Context, req *ChatRequest) (*TokenCountResponse, error)
}

// TokenCountResponse 表示供应商侧 token 统计结果。
type TokenCountResponse struct {
	Model       string
	TotalTokens int
	Metadata    ResponseMetadata
}

// estimateTokensPerAttachment 是图片/文件等非文本内容的固定估算预算。
// 各平台对多媒体的 token 折算差异很大，这里取保守中间值。
const estimateTokensPerAttachment = 800

// EstimateTokens 在不依赖 tokenizer 的前提下粗略估算消息列表的 token 数。
//
// 启发式规则：CJK 字符按 1 token/字，其余文本按 4 字符/token，
// 图片/文件附件按固定预算计，tool call 的函数名与参数按文本计。
// 误差可达 ±30%，适用于上下文预算裁剪与额度预检，不可用于计费结算——
// 结算一律以响应返回的 Usage 为准。需要更精确的请求前计数时，
// 使用支持 TokenCounter 的 provider（如 Gemini 与 Anthropic 的 CountTokens，
// 供应商侧计数同样是估算口径，只是精度远高于本地启发式）。
func EstimateTokens(msgs []Message) int {
	total := 0
	for _, msg := range msgs {
		total += 4 // 每条消息的角色与分隔开销
		for _, part := range msg.Content {
			switch part.Type {
			case ContentTypeText:
				total += estimateTextTokens(part.Text)
			default:
				total += estimateTokensPerAttachment
			}
		}
		for _, call := range msg.ToolCalls {
			total += 8 + estimateTextTokens(call.Function.Name) + estimateTextTokens(call.Function.Arguments)
		}
		if msg.ToolCallID != "" {
			total += 4
		}
	}
	return total
}

// cjkRuneStart 起始码点之上视为 CJK 及其他全宽字符，按 1 token/字估算。
const cjkRuneStart = 0x2E80

func estimateTextTokens(s string) int {
	cjk, other := 0, 0
	for _, r := range s {
		if r >= cjkRuneStart {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// TrimMessagesToTokenBudget 将对话历史裁剪到 budget（EstimateTokens 口径）之内：
// 保留全部 system 消息，其余消息从最新往前保留，超出预算的旧消息被丢弃。
// 裁剪以"消息组"为单位——assistant 的 tool_calls 与其后续 tool 结果消息不可拆分，
// 避免裁出非法请求序列。
//
// 无论预算多小，至少保留最新的一组非 system 消息（否则请求必然非法），
// 因此返回结果的估算值可能超出 budget，调用方可用 EstimateTokens 复核。
// 入参 slice 不会被修改。
func TrimMessagesToTokenBudget(msgs []Message, budget int) []Message {
	if len(msgs) == 0 {
		return nil
	}

	var system, rest []Message
	for _, msg := range msgs {
		if msg.Role == RoleSystem {
			system = append(system, msg)
		} else {
			rest = append(rest, msg)
		}
	}

	remaining := budget - EstimateTokens(system)

	// 从尾部按组回收，至少保留最新一组。
	groups := splitMessageGroups(rest)
	kept := len(groups)
	for kept > 0 {
		cost := EstimateTokens(groups[kept-1])
		if cost > remaining {
			break
		}
		remaining -= cost
		kept--
	}
	if kept == len(groups) && len(groups) > 0 {
		kept = len(groups) - 1 // 保底最新一组
	}

	out := make([]Message, 0, len(msgs))
	out = append(out, system...)
	for _, group := range groups[kept:] {
		out = append(out, group...)
	}
	return out
}

// splitMessageGroups 将非 system 消息切分为不可拆分的消息组并按原顺序返回：
// assistant 的 tool_calls 消息与其后续紧邻的 tool 结果消息构成一组，
// 其余消息各自成组。裁剪与摘要压缩均以组为单位，避免产生非法请求序列。
func splitMessageGroups(msgs []Message) [][]Message {
	var groups [][]Message
	for i := 0; i < len(msgs); {
		end := i + 1
		for end < len(msgs) && msgs[end].Role == RoleTool {
			end++
		}
		groups = append(groups, msgs[i:end])
		i = end
	}
	return groups
}

// CountTokens 在 p 实现 TokenCounter 时统计 token 数量。
func CountTokens(ctx context.Context, p Provider, req *ChatRequest) (*TokenCountResponse, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	counter, ok := p.(TokenCounter)
	if !ok {
		return nil, fmt.Errorf("%w: %s token counting", ErrUnsupportedCapability, p.Name())
	}
	resp, err := counter.CountTokens(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("count tokens: %w", err)
	}
	return resp, nil
}
