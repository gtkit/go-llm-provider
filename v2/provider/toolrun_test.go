package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolLoopProvider 在第一轮返回一次工具调用，之后返回最终文本，
// 用于驱动 RunToolLoop 走完一轮工具执行。
func toolLoopProvider() *stubProvider {
	var calls int
	return &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			calls++
			if calls == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{
						{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}},
					},
				}, nil
			}
			return &ChatResponse{Content: "done"}, nil
		},
	}
}

func TestRunToolLoopToolRetrySucceedsAfterFailures(t *testing.T) {
	t.Parallel()

	var attempts int
	handler := func(context.Context, string, string) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("transient")
		}
		return "ok", nil
	}

	resp, err := RunToolLoopWithOptions(context.Background(), toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{
		MaxRounds: 5,
		ToolRetry: ToolRetryOptions{MaxAttempts: 3},
	})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
	assert.Equal(t, 3, attempts)
}

func TestRunToolLoopNoRetryByDefault(t *testing.T) {
	t.Parallel()

	var attempts int
	handler := func(context.Context, string, string) (string, error) {
		attempts++
		return "", errors.New("boom")
	}

	resp, err := RunToolLoopWithOptions(context.Background(), toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{MaxRounds: 5})
	require.NoError(t, err)
	// 默认不重试：handler 仅执行一次，错误编码回模型后流程继续。
	assert.Equal(t, 1, attempts)
	assert.Equal(t, "done", resp.Content)
}

func TestRunToolLoopToolRetryRespectsShouldRetry(t *testing.T) {
	t.Parallel()

	var attempts int
	handler := func(context.Context, string, string) (string, error) {
		attempts++
		return "", errors.New("boom")
	}

	_, err := RunToolLoopWithOptions(context.Background(), toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{
		MaxRounds: 5,
		ToolRetry: ToolRetryOptions{
			MaxAttempts: 3,
			ShouldRetry: func(error) bool { return false },
		},
	})
	require.NoError(t, err)
	// ShouldRetry 返回 false：尽管 MaxAttempts=3，也只尝试一次。
	assert.Equal(t, 1, attempts)
}

func TestRunToolLoopToolRetryStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var attempts int
	handler := func(context.Context, string, string) (string, error) {
		attempts++
		cancel()
		return "", context.Canceled
	}

	_, err := RunToolLoopWithOptions(ctx, toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{
		MaxRounds: 5,
		ToolRetry: ToolRetryOptions{MaxAttempts: 3},
	})
	require.Error(t, err)
	// context 取消不重试。
	assert.Equal(t, 1, attempts)
}

func TestRunToolLoopExceedsMaxRoundsReportsNormalized(t *testing.T) {
	t.Parallel()

	// provider 始终返回工具调用，循环永不自然结束，直至触发轮数上限。
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{
				ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
			}, nil
		},
	}
	handler := func(context.Context, string, string) (string, error) { return "ok", nil }

	// MaxRounds=0 规范化为 20，错误信息应反映规范化后的轮数。
	_, err := RunToolLoopWithOptions(context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{MaxRounds: 0})
	require.Error(t, err)
	assert.ErrorContains(t, err, "max rounds (20)")
}

func TestRunToolLoopAccumulatesUsage(t *testing.T) {
	t.Parallel()

	var calls int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			calls++
			if calls == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
					Usage:     Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				}, nil
			}
			return &ChatResponse{
				Content: "done",
				Usage:   Usage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28},
			}, nil
		},
	}
	handler := func(context.Context, string, string) (string, error) { return "ok", nil }

	resp, err := RunToolLoopWithOptions(context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{MaxRounds: 5, AccumulateUsage: true})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
	// 开启 AccumulateUsage：Usage 为两轮累加。
	assert.Equal(t, 30, resp.Usage.PromptTokens)
	assert.Equal(t, 13, resp.Usage.CompletionTokens)
	assert.Equal(t, 43, resp.Usage.TotalTokens)
}

func TestRunToolLoopUsageDefaultsToLastRound(t *testing.T) {
	t.Parallel()

	var calls int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			calls++
			if calls == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
					Usage:     Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				}, nil
			}
			return &ChatResponse{
				Content: "done",
				Usage:   Usage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28},
			}, nil
		},
	}
	handler := func(context.Context, string, string) (string, error) { return "ok", nil }

	resp, err := RunToolLoopWithOptions(context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{MaxRounds: 5})
	require.NoError(t, err)
	// 默认不累加：保持最后一轮 Usage。
	assert.Equal(t, 28, resp.Usage.TotalTokens)
}

func TestNormalizeToolRetry(t *testing.T) {
	t.Parallel()

	got := normalizeToolRetry(ToolRetryOptions{})
	assert.Equal(t, 1, got.MaxAttempts)
	require.NotNil(t, got.Backoff)
	require.NotNil(t, got.ShouldRetry)
	assert.True(t, got.ShouldRetry(errors.New("x")))

	got = normalizeToolRetry(ToolRetryOptions{MaxAttempts: -2})
	assert.Equal(t, 1, got.MaxAttempts)
}
