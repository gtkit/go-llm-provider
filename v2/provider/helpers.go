package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SimpleChat 是最简便的调用方式：一问一答，返回纯文本。
func SimpleChat(ctx context.Context, p Provider, userMessage string) (string, error) {
	if providerIsNil(p) {
		return "", ErrNilProvider
	}

	resp, err := p.Chat(ctx, &ChatRequest{
		Messages: []Message{
			UserText(userMessage),
		},
	})
	if err != nil {
		return "", fmt.Errorf("simple chat: %w", err)
	}
	return resp.Content, nil
}

// SimpleChatWithSystem 带 system prompt 的一问一答。
func SimpleChatWithSystem(ctx context.Context, p Provider, system, userMessage string) (string, error) {
	if providerIsNil(p) {
		return "", ErrNilProvider
	}

	msgs := make([]Message, 0, 2)
	if system != "" {
		msgs = append(msgs, SystemText(system))
	}
	msgs = append(msgs, UserText(userMessage))

	resp, err := p.Chat(ctx, &ChatRequest{Messages: msgs})
	if err != nil {
		return "", fmt.Errorf("simple chat with system: %w", err)
	}
	return resp.Content, nil
}

// CollectStream 将流式响应收集为完整的文本字符串。
// onChunk 可选：如果提供，每收到一个 chunk 会回调（用于实时打印等场景）。
func CollectStream(ctx context.Context, p Provider, req *ChatRequest, onChunk func(delta string)) (result string, err error) {
	if providerIsNil(p) {
		return "", ErrNilProvider
	}
	if req == nil {
		return "", ErrNilChatRequest
	}

	stream, err := p.ChatStream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("collect stream: %w", err)
	}
	defer func() {
		err = errors.Join(err, stream.Close())
	}()

	var sb strings.Builder
	for {
		select {
		case <-ctx.Done():
			return sb.String(), context.Cause(ctx)
		default:
		}

		chunk, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return sb.String(), fmt.Errorf("collect stream recv: %w", err)
		}
		sb.WriteString(chunk.Delta)
		if onChunk != nil {
			onChunk(chunk.Delta)
		}
	}
	return sb.String(), nil
}

// StreamResult 是 CollectStreamResult 收集完一条流后的完整结果。
type StreamResult struct {
	Content      string // 累积的回复文本
	Reasoning    string // 累积的推理文本（模型支持时）
	FinishReason string // 最后一个非空 FinishReason
	Usage        Usage  // 流尾部给出的完整 token 统计；provider 未回传时为零值
}

// CollectStreamResult 与 CollectStream 类似，但消费到流尾后返回完整的
// StreamResult（含 Usage 与推理文本），适合需要按 token 计费或展示用量的场景。
// onChunk 为 nil 时只做收集。
func CollectStreamResult(ctx context.Context, p Provider, req *ChatRequest, onChunk func(delta string)) (result *StreamResult, err error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	stream, err := p.ChatStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("collect stream result: %w", err)
	}
	defer func() {
		err = errors.Join(err, stream.Close())
	}()

	var content, reasoning strings.Builder
	out := &StreamResult{}
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("collect stream result: %w", context.Cause(ctx))
		default:
		}

		chunk, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("collect stream result recv: %w", err)
		}
		content.WriteString(chunk.Delta)
		reasoning.WriteString(chunk.ReasoningDelta)
		if chunk.FinishReason != "" {
			out.FinishReason = chunk.FinishReason
		}
		if chunk.Usage != (Usage{}) {
			out.Usage = chunk.Usage
		}
		if onChunk != nil && chunk.Delta != "" {
			onChunk(chunk.Delta)
		}
	}
	out.Content = content.String()
	out.Reasoning = reasoning.String()
	return out, nil
}
