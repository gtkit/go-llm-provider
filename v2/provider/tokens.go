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
