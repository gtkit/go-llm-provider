package provider

import (
	"context"
	"fmt"
	"strings"
)

// defaultCompactPrompt 是 CompactMessages 的默认摘要指令。
const defaultCompactPrompt = "你是对话压缩助手。请将给出的对话历史压缩为一段简明摘要，" +
	"保留：用户的核心诉求与偏好、已确认的事实与结论、未完成的任务与待办、关键实体（名称、文件、数字）。" +
	"用第三人称陈述，不要添加对话中不存在的信息，只输出摘要正文。"

const (
	defaultCompactKeepRecentGroups = 4
	defaultCompactMaxSummaryTokens = 512
)

// CompactOptions 配置 CompactMessages 的压缩行为。零值可用。
type CompactOptions struct {
	// Model 指定生成摘要所用的模型，空串使用 provider 默认模型。
	// 摘要不需要强模型，建议配置便宜的小模型以降低压缩成本。
	Model string

	// KeepRecentGroups 保留最近 N 组消息原文不参与压缩（组语义见
	// TrimMessagesToTokenBudget），<= 0 时默认 4。
	KeepRecentGroups int

	// TriggerTokens 是触发压缩的阈值：历史的 EstimateTokens 估算值
	// 不超过该值时原样返回、不发起摘要调用。<= 0 表示总是压缩。
	// 合理设置阈值可避免短对话被无谓压缩（压缩本身也消耗 token）。
	TriggerTokens int

	// MaxSummaryTokens 限制摘要的输出长度，<= 0 时默认 512。
	MaxSummaryTokens int

	// SummaryPrompt 自定义摘要指令，空串使用内置默认指令。
	SummaryPrompt string
}

// CompactMessages 将较早的对话历史压缩为一条摘要消息，实现多轮对话的
// token 节省：返回的消息序列为 [system 消息…, 摘要消息(user 角色), 最近 N 组原文…]。
//
// 摘要通过一次 LLM 调用生成（消耗 token，也会被计费 hook 正常归账），
// 因此正确用法是业务侧缓存压缩结果、按 TriggerTokens 阈值低频触发，
// 而不是每轮对话都调用。摘要生成失败时返回错误、不静默降级——
// 调用方可回退到 TrimMessagesToTokenBudget 硬裁剪。
//
// 返回值：压缩后的消息序列、摘要调用的响应（含 Usage；未触发压缩时为 nil）。
// 入参 slice 不会被修改。
func CompactMessages(ctx context.Context, p Provider, msgs []Message, opts CompactOptions) ([]Message, *ChatResponse, error) {
	if providerIsNil(p) {
		return nil, nil, ErrNilProvider
	}
	if len(msgs) == 0 {
		return msgs, nil, nil
	}
	if opts.TriggerTokens > 0 && EstimateTokens(msgs) <= opts.TriggerTokens {
		return msgs, nil, nil
	}

	keepRecent := opts.KeepRecentGroups
	if keepRecent <= 0 {
		keepRecent = defaultCompactKeepRecentGroups
	}

	var system, rest []Message
	for _, msg := range msgs {
		if msg.Role == RoleSystem {
			system = append(system, msg)
		} else {
			rest = append(rest, msg)
		}
	}
	groups := splitMessageGroups(rest)
	if len(groups) <= keepRecent {
		return msgs, nil, nil // 没有可压缩的旧历史
	}

	var old []Message
	for _, group := range groups[:len(groups)-keepRecent] {
		old = append(old, group...)
	}

	summaryPrompt := firstString(opts.SummaryPrompt, defaultCompactPrompt)
	maxSummaryTokens := opts.MaxSummaryTokens
	if maxSummaryTokens <= 0 {
		maxSummaryTokens = defaultCompactMaxSummaryTokens
	}
	resp, err := p.Chat(ctx, &ChatRequest{
		Model: opts.Model,
		Messages: []Message{
			SystemText(summaryPrompt),
			UserText(renderTranscript(old)),
		},
		MaxTokens: maxSummaryTokens,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("compact messages: %w", err)
	}

	out := make([]Message, 0, len(system)+1+len(rest)-len(old))
	out = append(out, system...)
	out = append(out, UserText("【此前对话摘要】\n"+resp.Content))
	for _, group := range groups[len(groups)-keepRecent:] {
		out = append(out, group...)
	}
	return out, resp, nil
}

// renderTranscript 将消息序列渲染为供摘要模型阅读的纯文本对话记录。
func renderTranscript(msgs []Message) string {
	var b strings.Builder
	for _, msg := range msgs {
		b.WriteString(string(msg.Role))
		b.WriteString(": ")
		b.WriteString(contentText(msg.Content))
		for _, call := range msg.ToolCalls {
			fmt.Fprintf(&b, "\n[调用工具 %s 参数 %s]", call.Function.Name, call.Function.Arguments)
		}
		b.WriteString("\n")
	}
	return b.String()
}
