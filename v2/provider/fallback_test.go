package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFallbackProviderUsesBackupAfterRetryablePrimaryFailure(t *testing.T) {
	t.Parallel()

	primary := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, &ProviderError{
				Provider:  ProviderOpenAI,
				Code:      ErrorCodeServerError,
				Retryable: true,
			}
		},
	}
	backup := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: "backup"}, nil
		},
	}

	p, err := NewFallbackProvider(primary, backup)
	require.NoError(t, err)

	resp, err := p.Chat(context.Background(), &ChatRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "backup", resp.Content)
	assert.Equal(t, ProviderOpenAI, p.Name())
}

func TestFallbackProviderDoesNotFallbackOnNonRetryableFailure(t *testing.T) {
	t.Parallel()

	var backupCalls int
	primaryErr := &ProviderError{
		Provider:  ProviderOpenAI,
		Code:      ErrorCodeInvalidRequest,
		Retryable: false,
	}
	primary := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, primaryErr
		},
	}
	backup := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			backupCalls++
			return &ChatResponse{Content: "backup"}, nil
		},
	}

	p, err := NewFallbackProvider(primary, backup)
	require.NoError(t, err)

	resp, err := p.Chat(context.Background(), &ChatRequest{})
	require.ErrorIs(t, err, primaryErr)
	assert.Nil(t, resp)
	assert.Equal(t, 0, backupCalls)
}

func TestFallbackProviderReturnsJoinedErrorWhenAllFail(t *testing.T) {
	t.Parallel()

	primaryErr := &ProviderError{Provider: ProviderOpenAI, Code: ErrorCodeServerError, Retryable: true}
	backupErr := &ProviderError{Provider: ProviderDeepSeek, Code: ErrorCodeServerError, Retryable: true}
	primary := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, primaryErr
		},
	}
	backup := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, backupErr
		},
	}

	p, err := NewFallbackProvider(primary, backup)
	require.NoError(t, err)

	resp, err := p.Chat(context.Background(), &ChatRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	require.ErrorIs(t, err, primaryErr)
	require.ErrorIs(t, err, backupErr)
}

func TestFallbackProviderRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	p, err := NewFallbackProvider(nil)
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, p)
}

func TestFallbackProviderDefaultModel(t *testing.T) {
	t.Parallel()

	first, err := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "k", Model: "deepseek-chat"})
	require.NoError(t, err)
	second, err := NewProvider(ProviderConfig{Name: ProviderQwen, APIKey: "k", Model: "qwen-max"})
	require.NoError(t, err)

	fallback, err := NewFallbackProvider(first, second)
	require.NoError(t, err)
	// RequestModel 探测口径取链首默认模型。
	assert.Equal(t, "deepseek-chat", fallback.DefaultModel())

	// 链首未实现探测接口时返回空串。
	plain, err := NewFallbackProvider(&stubProvider{name: ProviderOpenAI})
	require.NoError(t, err)
	assert.Empty(t, plain.DefaultModel())
}

func TestFallbackProviderCustomShouldFallback(t *testing.T) {
	t.Parallel()

	authErr := &ProviderError{Provider: ProviderDeepSeek, Code: ErrorCodeAuth, StatusCode: 401}
	first := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) { return nil, authErr },
	}
	second := &stubProvider{
		name: ProviderQwen,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: "backup ok"}, nil
		},
	}

	// 默认判定：401 不可重试，不切换。
	strict, err := NewFallbackProvider(first, second)
	require.NoError(t, err)
	_, err = strict.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.ErrorIs(t, err, ErrAuth)

	// 自定义判定：key 失效也切换（多供应商冗余场景）。
	relaxed, err := NewFallbackProviderWithOptions([]Provider{first, second}, FallbackOptions{
		ShouldFallback: func(err error) bool {
			return IsRetryableError(err) || errors.Is(err, ErrAuth)
		},
	})
	require.NoError(t, err)
	resp, err := relaxed.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)
	assert.Equal(t, "backup ok", resp.Content)
}

func TestFallbackProviderStopsWhenContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	var secondCalled bool
	first := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			cancel() // 模拟调用期间用户放弃：ctx 被取消
			return nil, &ProviderError{Provider: ProviderDeepSeek, Code: ErrorCodeNetwork, Retryable: true}
		},
	}
	second := &stubProvider{
		name: ProviderQwen,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			secondCalled = true
			return &ChatResponse{Content: "should not reach"}, nil
		},
	}

	fallback, err := NewFallbackProvider(first, second)
	require.NoError(t, err)
	_, err = fallback.Chat(ctx, &ChatRequest{Messages: []Message{UserText("hi")}})
	require.Error(t, err)
	assert.False(t, secondCalled, "ctx 取消后不应继续尝试下一个 provider")
}

// TestFallbackProviderNestedVendorChains 验证两级降级：厂商内先穷尽所有 model，
// 全部失败后再切换到下一个厂商的 model 链。
func TestFallbackProviderNestedVendorChains(t *testing.T) {
	t.Parallel()

	retryableErr := &ProviderError{Provider: ProviderDeepSeek, Code: ErrorCodeServerError, Retryable: true}
	var order []string
	failing := func(name string) *stubProvider {
		return &stubProvider{
			name: ProviderDeepSeek,
			chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
				order = append(order, name)
				return nil, retryableErr
			},
		}
	}

	// 厂商 A：两个 model 全部失败。
	vendorA, err := NewFallbackProvider(failing("A-model1"), failing("A-model2"))
	require.NoError(t, err)
	// 厂商 B：第一个 model 成功。
	vendorB, err := NewFallbackProvider(&stubProvider{
		name: ProviderQwen,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			order = append(order, "B-model1")
			return &ChatResponse{Content: "vendor B ok"}, nil
		},
	})
	require.NoError(t, err)

	top, err := NewFallbackProvider(vendorA, vendorB)
	require.NoError(t, err)

	resp, err := top.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)
	assert.Equal(t, "vendor B ok", resp.Content)
	// 顺序：A 的所有 model 穷尽后才轮到 B。
	assert.Equal(t, []string{"A-model1", "A-model2", "B-model1"}, order)
}
