package provider

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// SimpleChat 是最简便的调用方式：一问一答，返回纯文本。
func SimpleChat(ctx context.Context, p Provider, userMessage string) (string, error) {
	resp, err := p.Chat(ctx, &ChatRequest{
		Messages: []Message{
			{Role: RoleUser, Content: userMessage},
		},
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// SimpleChatWithSystem 带 system prompt 的一问一答。
func SimpleChatWithSystem(ctx context.Context, p Provider, system, userMessage string) (string, error) {
	msgs := make([]Message, 0, 2)
	if system != "" {
		msgs = append(msgs, Message{Role: RoleSystem, Content: system})
	}
	msgs = append(msgs, Message{Role: RoleUser, Content: userMessage})

	resp, err := p.Chat(ctx, &ChatRequest{Messages: msgs})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// CollectStream 将流式响应收集为完整的文本字符串。
// onChunk 可选：如果提供，每收到一个 chunk 会回调（用于实时打印等场景）。
func CollectStream(ctx context.Context, p Provider, req *ChatRequest, onChunk func(delta string)) (string, error) {
	stream, err := p.ChatStream(ctx, req)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	var sb strings.Builder
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return sb.String(), fmt.Errorf("stream recv: %w", err)
		}
		sb.WriteString(chunk.Delta)
		if onChunk != nil {
			onChunk(chunk.Delta)
		}
	}
	return sb.String(), nil
}
