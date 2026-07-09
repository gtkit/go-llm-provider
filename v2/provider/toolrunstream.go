package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// RunToolLoopStream 是 RunToolLoop 的流式版本：每轮通过 ChatStream 发起请求，
// 收到的每个 chunk 实时回调 onChunk（文本增量可直接透传给前端做打字机效果），
// 工具调用的增量片段由库内拼装完整后执行 handler，结果自动回传进入下一轮，
// 直到模型给出最终文本回复。
//
// onChunk 会收到所有轮次的全部 chunk（含工具调用轮），可按需过滤；传 nil 表示不回调。
// 返回的 ChatResponse 语义与 RunToolLoop 一致：Content/Reasoning/FinishReason
// 来自最后一轮，Usage 默认为最后一轮的值。
func RunToolLoopStream(ctx context.Context, p Provider, req *ChatRequest, maxRounds int, handler ToolHandler, onChunk func(StreamChunk)) (*ChatResponse, error) {
	return RunToolLoopStreamWithOptions(ctx, p, req, handler, onChunk, RunToolLoopOptions{MaxRounds: maxRounds})
}

// RunToolLoopStreamWithOptions 以 RunToolLoopOptions 控制流式工具循环，
// 选项语义与 RunToolLoopWithOptions 一致（含 AccumulateUsage 的跨轮 usage 累加）。
func RunToolLoopStreamWithOptions(ctx context.Context, p Provider, req *ChatRequest, handler ToolHandler, onChunk func(StreamChunk), opts RunToolLoopOptions) (*ChatResponse, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}
	if handler == nil {
		return nil, ErrToolHandlerRequired
	}

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
		roundReq := *req
		roundReq.Messages = messages
		roundReq.Stop = slices.Clone(req.Stop)
		roundReq.Tools = slices.Clone(req.Tools)

		stream, err := p.ChatStream(ctx, &roundReq)
		if err != nil {
			return nil, fmt.Errorf("round %d: %w", round+1, err)
		}
		result, err := consumeToolLoopStream(stream, onChunk)
		if err != nil {
			return nil, fmt.Errorf("round %d: %w", round+1, err)
		}
		totalUsage = addUsage(totalUsage, result.usage)

		// 模型没有请求 tool call，返回最终结果。
		if len(result.toolCalls) == 0 {
			resp := &ChatResponse{
				Content:      result.content,
				Reasoning:    result.reasoning,
				FinishReason: result.finishReason,
				Usage:        result.usage,
			}
			if opts.AccumulateUsage {
				resp.Usage = totalUsage
			}
			return resp, nil
		}

		messages = append(messages, Message{
			Role:      RoleAssistant,
			Content:   []ContentPart{TextPart(result.content)},
			ToolCalls: result.toolCalls,
		})
		toolMessages, err := executeToolCalls(ctx, result.toolCalls, handler, encoder, opts.ParallelToolCalls, retry)
		if err != nil {
			return nil, err
		}
		messages = append(messages, toolMessages...)
	}

	return nil, fmt.Errorf("tool loop exceeded max rounds (%d)", rounds)
}

// streamRoundResult 是流式工具循环单轮消费完毕后的聚合结果。
type streamRoundResult struct {
	content      string
	reasoning    string
	finishReason string
	usage        Usage
	toolCalls    []ToolCall
}

func consumeToolLoopStream(stream *StreamReader, onChunk func(StreamChunk)) (result streamRoundResult, err error) {
	defer func() {
		err = errors.Join(err, stream.Close())
	}()

	var content, reasoning strings.Builder
	assembler := newToolCallAssembler()
	for {
		chunk, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			return result, recvErr
		}
		if onChunk != nil {
			onChunk(*chunk)
		}
		content.WriteString(chunk.Delta)
		reasoning.WriteString(chunk.ReasoningDelta)
		if chunk.FinishReason != "" {
			result.finishReason = chunk.FinishReason
		}
		if chunk.Usage != (Usage{}) {
			result.usage = chunk.Usage
		}
		for _, delta := range chunk.ToolCalls {
			assembler.add(delta)
		}
	}
	result.content = content.String()
	result.reasoning = reasoning.String()
	result.toolCalls = assembler.result()
	return result, nil
}

// toolCallAssembler 将流式 ToolCallDelta 片段按 Index 拼装为完整 ToolCall。
type toolCallAssembler struct {
	calls map[int]*pendingToolCall
	order []int
}

type pendingToolCall struct {
	id        string
	name      string
	arguments strings.Builder
}

func newToolCallAssembler() *toolCallAssembler {
	return &toolCallAssembler{calls: make(map[int]*pendingToolCall)}
}

func (a *toolCallAssembler) add(delta ToolCallDelta) {
	pending, ok := a.calls[delta.Index]
	if !ok {
		pending = &pendingToolCall{}
		a.calls[delta.Index] = pending
		a.order = append(a.order, delta.Index)
	}
	if delta.ID != "" {
		pending.id = delta.ID
	}
	if delta.Function.Name != "" {
		pending.name = delta.Function.Name
	}
	pending.arguments.WriteString(delta.Function.Arguments)
}

func (a *toolCallAssembler) result() []ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(a.order))
	for _, index := range a.order {
		pending := a.calls[index]
		out = append(out, ToolCall{
			ID: pending.id,
			Function: FunctionCall{
				Name:      pending.name,
				Arguments: pending.arguments.String(),
			},
		})
	}
	return out
}
