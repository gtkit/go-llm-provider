package provider

import (
	"errors"
	"fmt"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// BenchmarkIsRetryableError 基准化重试判定：WithRetry 每次失败都会执行。
func BenchmarkIsRetryableError(b *testing.B) {
	err := fmt.Errorf("call failed: %w",
		&ProviderError{Provider: ProviderDeepSeek, Code: ErrorCodeRateLimit, Retryable: true})
	b.ReportAllocs()
	for b.Loop() {
		if !IsRetryableError(err) {
			b.Fatal("expected retryable")
		}
	}
}

// BenchmarkFallbackErrorJoin 基准化降级链全失败时的错误聚合。
func BenchmarkFallbackErrorJoin(b *testing.B) {
	errA := &ProviderError{Provider: ProviderDeepSeek, Code: ErrorCodeServerError, Retryable: true}
	errB := &ProviderError{Provider: ProviderQwen, Code: ErrorCodeTimeout, Retryable: true}
	b.ReportAllocs()
	for b.Loop() {
		joined := errors.Join(
			fmt.Errorf("deepseek: %w", errA),
			fmt.Errorf("qwen: %w", errB),
		)
		if joined == nil {
			b.Fatal("expected error")
		}
	}
}

// BenchmarkOpenAIStreamChunk 基准化流式帧映射：流式响应每帧执行一次。
func BenchmarkOpenAIStreamChunk(b *testing.B) {
	usage := &openai.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	resp := openai.ChatCompletionStreamResponse{
		Model: "deepseek-chat",
		Choices: []openai.ChatCompletionStreamChoice{{
			Delta: openai.ChatCompletionStreamChoiceDelta{Content: "根据季度数据，营收环比增长约 12%。"},
		}},
		Usage: usage,
	}
	b.ReportAllocs()
	for b.Loop() {
		if chunk := openaiStreamChunk(resp); chunk == nil {
			b.Fatal("nil chunk")
		}
	}
}
