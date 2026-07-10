package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubQuotaChecker struct {
	err       error
	lastUser  string
	lastModel string
	calls     int
}

func (c *stubQuotaChecker) Allow(_ context.Context, userID, model string) error {
	c.calls++
	c.lastUser = userID
	c.lastModel = model
	return c.err
}

func newQuotaWrappedProvider(t *testing.T, qc QuotaChecker, chatCalled *bool) Provider {
	t.Helper()
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			*chatCalled = true
			return &ChatResponse{Content: "ok"}, nil
		},
	}
	wrapped, err := TryWithMiddlewares(p, MiddlewareOptions{
		Chat: []Middleware{QuotaMiddleware(qc)},
	})
	require.NoError(t, err)
	return wrapped
}

func TestQuotaMiddlewareAllowsAndRejects(t *testing.T) {
	t.Parallel()

	t.Run("额度可用时放行并透传 userID 与 model", func(t *testing.T) {
		t.Parallel()
		qc := &stubQuotaChecker{}
		var chatCalled bool
		wrapped := newQuotaWrappedProvider(t, qc, &chatCalled)

		ctx := WithUserID(t.Context(), "u1")
		_, err := wrapped.Chat(ctx, &ChatRequest{Model: "gpt-4o", Messages: []Message{UserText("hi")}})
		require.NoError(t, err)
		assert.True(t, chatCalled)
		assert.Equal(t, "u1", qc.lastUser)
		assert.Equal(t, "gpt-4o", qc.lastModel)
	})

	t.Run("超限时拦截且不调用下游", func(t *testing.T) {
		t.Parallel()
		qc := &stubQuotaChecker{err: ErrQuotaExceeded}
		var chatCalled bool
		wrapped := newQuotaWrappedProvider(t, qc, &chatCalled)

		_, err := wrapped.Chat(WithUserID(t.Context(), "u1"), &ChatRequest{Messages: []Message{UserText("hi")}})
		require.ErrorIs(t, err, ErrQuotaExceeded)
		assert.False(t, chatCalled)
	})

	t.Run("无 userID 默认放行且不查配额", func(t *testing.T) {
		t.Parallel()
		qc := &stubQuotaChecker{err: ErrQuotaExceeded}
		var chatCalled bool
		wrapped := newQuotaWrappedProvider(t, qc, &chatCalled)

		_, err := wrapped.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
		require.NoError(t, err)
		assert.True(t, chatCalled)
		assert.Zero(t, qc.calls)
	})
}

func TestQuotaStreamMiddlewareRejects(t *testing.T) {
	t.Parallel()

	qc := &stubQuotaChecker{err: ErrQuotaExceeded}
	var streamCalled bool
	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			streamCalled = true
			return nil, nil
		},
	}
	wrapped, err := TryWithMiddlewares(p, MiddlewareOptions{
		Stream: []StreamMiddleware{QuotaStreamMiddleware(qc)},
	})
	require.NoError(t, err)

	_, err = wrapped.ChatStream(WithUserID(t.Context(), "u1"), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.ErrorIs(t, err, ErrQuotaExceeded)
	assert.False(t, streamCalled)
}

func TestTokenBudgetContextRoundTrip(t *testing.T) {
	t.Parallel()

	budget, ok := TokenBudgetFromContext(WithTokenBudget(t.Context(), 1000))
	assert.True(t, ok)
	assert.Equal(t, 1000, budget)

	_, ok = TokenBudgetFromContext(t.Context())
	assert.False(t, ok)
}

func TestApplyTokenBudget(t *testing.T) {
	t.Parallel()

	msgs := []Message{UserText("hello world, this is a test message")}
	estimated := EstimateTokens(msgs)
	require.Positive(t, estimated)

	t.Run("无预算透传原请求", func(t *testing.T) {
		t.Parallel()
		req := &ChatRequest{Messages: msgs}
		out, err := applyTokenBudget(t.Context(), req)
		require.NoError(t, err)
		assert.Same(t, req, out)
	})

	t.Run("预算耗尽直接拒绝", func(t *testing.T) {
		t.Parallel()
		_, err := applyTokenBudget(WithTokenBudget(t.Context(), 0), &ChatRequest{Messages: msgs})
		require.ErrorIs(t, err, ErrQuotaExceeded)
	})

	t.Run("输入估算已达预算时拒绝", func(t *testing.T) {
		t.Parallel()
		_, err := applyTokenBudget(WithTokenBudget(t.Context(), estimated), &ChatRequest{Messages: msgs})
		require.ErrorIs(t, err, ErrQuotaExceeded)
	})

	t.Run("收缩 MaxTokens 且不修改原请求", func(t *testing.T) {
		t.Parallel()
		req := &ChatRequest{Messages: msgs, MaxTokens: 100_000}
		budget := estimated + 50
		out, err := applyTokenBudget(WithTokenBudget(t.Context(), budget), req)
		require.NoError(t, err)
		assert.Equal(t, 50, out.MaxTokens)
		assert.Equal(t, 100_000, req.MaxTokens, "调用方请求不被修改")
	})

	t.Run("未设 MaxTokens 时补设输出上限", func(t *testing.T) {
		t.Parallel()
		out, err := applyTokenBudget(WithTokenBudget(t.Context(), estimated+80), &ChatRequest{Messages: msgs})
		require.NoError(t, err)
		assert.Equal(t, 80, out.MaxTokens)
	})

	t.Run("原 MaxTokens 更小时保持不变", func(t *testing.T) {
		t.Parallel()
		req := &ChatRequest{Messages: msgs, MaxTokens: 10}
		out, err := applyTokenBudget(WithTokenBudget(t.Context(), estimated+100), req)
		require.NoError(t, err)
		assert.Same(t, req, out)
	})

	t.Run("预算充足时不强设 MaxTokens", func(t *testing.T) {
		t.Parallel()
		req := &ChatRequest{Messages: msgs}
		out, err := applyTokenBudget(WithTokenBudget(t.Context(), tokenBudgetUnlimitedThreshold*2), req)
		require.NoError(t, err)
		assert.Same(t, req, out)
		assert.Zero(t, out.MaxTokens)
	})
}

func TestTokenBudgetStreamMiddlewareRejects(t *testing.T) {
	t.Parallel()

	var streamCalled bool
	p := &stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			streamCalled = true
			return nil, nil
		},
	}
	wrapped, err := TryWithMiddlewares(p, MiddlewareOptions{
		Stream: []StreamMiddleware{TokenBudgetStreamMiddleware()},
	})
	require.NoError(t, err)

	_, err = wrapped.ChatStream(WithTokenBudget(t.Context(), 0), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.ErrorIs(t, err, ErrQuotaExceeded)
	assert.False(t, streamCalled)
}

func TestApplyCostBudget(t *testing.T) {
	t.Parallel()

	table := PricingTable{
		"m": {InputPer1M: 2_000_000, OutputPer1M: 8_000_000, Currency: "CNY"}, // 输入 2 元/1M，输出 8 元/1M
	}
	msgs := []Message{UserText("hello world, this is a test message")}
	estimatedInput := int64(EstimateTokens(msgs))
	inputCost := estimatedInput * 2_000_000 / 1_000_000

	t.Run("无预算透传原请求", func(t *testing.T) {
		t.Parallel()
		req := &ChatRequest{Model: "m", Messages: msgs}
		out, err := applyCostBudget(t.Context(), table, req)
		require.NoError(t, err)
		assert.Same(t, req, out)
	})

	t.Run("余额耗尽直接拒绝", func(t *testing.T) {
		t.Parallel()
		_, err := applyCostBudget(WithCostBudget(t.Context(), 0), table, &ChatRequest{Model: "m", Messages: msgs})
		require.ErrorIs(t, err, ErrQuotaExceeded)
	})

	t.Run("输入估算费用已达余额时拒绝", func(t *testing.T) {
		t.Parallel()
		_, err := applyCostBudget(WithCostBudget(t.Context(), inputCost), table, &ChatRequest{Model: "m", Messages: msgs})
		require.ErrorIs(t, err, ErrQuotaExceeded)
	})

	t.Run("金额反推输出上限并收缩 MaxTokens", func(t *testing.T) {
		t.Parallel()
		// 余额 = 输入成本 + 800 微元；输出 8 微元/token → 允许 100 token 输出。
		budget := inputCost + 800
		req := &ChatRequest{Model: "m", Messages: msgs, MaxTokens: 100_000}
		out, err := applyCostBudget(WithCostBudget(t.Context(), budget), table, req)
		require.NoError(t, err)
		assert.Equal(t, 100, out.MaxTokens)
		assert.Equal(t, 100_000, req.MaxTokens, "调用方请求不被修改")
	})

	t.Run("原 MaxTokens 更小时保持不变", func(t *testing.T) {
		t.Parallel()
		req := &ChatRequest{Model: "m", Messages: msgs, MaxTokens: 10}
		out, err := applyCostBudget(WithCostBudget(t.Context(), inputCost+800), table, req)
		require.NoError(t, err)
		assert.Same(t, req, out)
	})

	t.Run("未配价模型显式报错", func(t *testing.T) {
		t.Parallel()
		_, err := applyCostBudget(WithCostBudget(t.Context(), 1_000_000), table, &ChatRequest{Model: "unknown", Messages: msgs})
		require.ErrorIs(t, err, ErrModelNotPriced)
	})

	t.Run("输出免费的模型只做输入预检不收缩", func(t *testing.T) {
		t.Parallel()
		free := PricingTable{"m": {InputPer1M: 1_000_000, Currency: "CNY"}}
		req := &ChatRequest{Model: "m", Messages: msgs}
		out, err := applyCostBudget(WithCostBudget(t.Context(), inputCost+1), free, req)
		require.NoError(t, err)
		assert.Same(t, req, out)
	})

	t.Run("余额充足时不强设 MaxTokens", func(t *testing.T) {
		t.Parallel()
		req := &ChatRequest{Model: "m", Messages: msgs}
		out, err := applyCostBudget(WithCostBudget(t.Context(), 100_000_000_000), table, req) // 10 万元
		require.NoError(t, err)
		assert.Same(t, req, out)
	})
}

func TestCostBudgetMiddlewareEndToEnd(t *testing.T) {
	t.Parallel()

	table := PricingTable{"m": {InputPer1M: 2_000_000, OutputPer1M: 8_000_000, Currency: "CNY"}}
	var gotMaxTokens int
	p := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			gotMaxTokens = req.MaxTokens
			return &ChatResponse{Content: "ok"}, nil
		},
	}
	wrapped, err := TryWithMiddlewares(p, MiddlewareOptions{
		Chat: []Middleware{CostBudgetMiddleware(table)},
	})
	require.NoError(t, err)

	msgs := []Message{UserText("hi")}
	budget := int64(EstimateTokens(msgs))*2 + 80 // 输入成本 + 80 微元 → 10 token 输出
	_, err = wrapped.Chat(WithCostBudget(t.Context(), budget), &ChatRequest{Model: "m", Messages: msgs})
	require.NoError(t, err)
	assert.Equal(t, 10, gotMaxTokens)

	var streamCalled bool
	sp := &stubProvider{
		name: ProviderDeepSeek,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			streamCalled = true
			return nil, nil
		},
	}
	wrappedStream, err := TryWithMiddlewares(sp, MiddlewareOptions{
		Stream: []StreamMiddleware{CostBudgetStreamMiddleware(table)},
	})
	require.NoError(t, err)
	_, err = wrappedStream.ChatStream(WithCostBudget(t.Context(), 0), &ChatRequest{Model: "m", Messages: msgs})
	require.ErrorIs(t, err, ErrQuotaExceeded)
	assert.False(t, streamCalled)
}

func TestTokenBudgetMiddlewareEndToEnd(t *testing.T) {
	t.Parallel()

	var gotMaxTokens int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			gotMaxTokens = req.MaxTokens
			return &ChatResponse{Content: "ok"}, nil
		},
	}
	wrapped, err := TryWithMiddlewares(p, MiddlewareOptions{
		Chat: []Middleware{TokenBudgetMiddleware()},
	})
	require.NoError(t, err)

	msgs := []Message{UserText("hi")}
	budget := EstimateTokens(msgs) + 30
	_, err = wrapped.Chat(WithTokenBudget(t.Context(), budget), &ChatRequest{Messages: msgs})
	require.NoError(t, err)
	assert.Equal(t, 30, gotMaxTokens)
}
