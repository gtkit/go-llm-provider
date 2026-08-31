package provider

import (
	"context"
	"errors"
	"fmt"
)

// ErrNilTaskSystem indicates that PromptTask.System is nil.
var ErrNilTaskSystem = errors.New("prompt task system is nil")

// PromptTask 把单轮任务（翻译、润色、摘要等）的 system prompt 构造逻辑与常用
// 请求参数绑定在一起，调用时按运行时参数 P 拼装 system prompt，执行一次
// system+user 的 Chat 调用。无需运行时参数的任务可将 P 设为 struct{}。
type PromptTask[P any] struct {
	// System 根据运行时参数构造本次调用的 system prompt，不可为 nil。
	//
	// 安全提示：这个闭包完全由调用方实现，库不对拼接内容做任何检测或转义。
	// 若 params 中的字段来自不可信输入（用户输入、外部数据）并被直接拼进
	// 返回的字符串，等同于把不可信数据混入 system prompt，构成直接提示注入——
	// 且该调用路径是一次性 Chat，不经过 RunToolLoop 的
	// ToolResultTransformer / ResponseValidator 钩子。优先用受限的枚举/白名单
	// 类型约束这类字段的取值；确需接受自由文本时，参照
	// PROMPT_INJECTION_DEFENSE.md 的输入净化规则在闭包内部处理后再拼接。
	System func(params P) string

	// Model 为空时使用 ProviderConfig.Model。
	Model       string
	Temperature *float32
	MaxTokens   int

	// ResponseFormat 需要结构化输出时设置，配合 RunPromptTaskJSON 使用。
	ResponseFormat *ResponseFormat
}

func (t PromptTask[P]) buildRequest(params P, input string) (*ChatRequest, error) {
	if t.System == nil {
		return nil, ErrNilTaskSystem
	}
	return &ChatRequest{
		Model: t.Model,
		Messages: []Message{
			SystemText(t.System(params)),
			UserText(input),
		},
		MaxTokens:      t.MaxTokens,
		Temperature:    t.Temperature,
		ResponseFormat: t.ResponseFormat,
	}, nil
}

// Run 执行一次单轮任务调用，返回纯文本结果。
func (t PromptTask[P]) Run(ctx context.Context, p Provider, params P, input string) (string, error) {
	if providerIsNil(p) {
		return "", ErrNilProvider
	}
	req, err := t.buildRequest(params, input)
	if err != nil {
		return "", err
	}
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("prompt task run: %w", err)
	}
	return resp.Content, nil
}

// RunPromptTaskJSON 执行一次单轮任务调用，并将响应内容解码为 T。
func RunPromptTaskJSON[P, T any](ctx context.Context, t PromptTask[P], p Provider, params P, input string) (T, *ChatResponse, error) {
	var zero T
	if providerIsNil(p) {
		return zero, nil, ErrNilProvider
	}
	req, err := t.buildRequest(params, input)
	if err != nil {
		return zero, nil, err
	}
	return GenerateJSON[T](ctx, p, req)
}
