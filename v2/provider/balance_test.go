package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingProvider 记录调用次数，并按 chat 回调决定成功或失败。
func countingProvider(name ProviderName, counter *atomic.Int64, err error) *stubProvider {
	return &stubProvider{
		name: name,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			counter.Add(1)
			if err != nil {
				return nil, err
			}
			return &ChatResponse{Content: string(name)}, nil
		},
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			counter.Add(1)
			if err != nil {
				return nil, err
			}
			return NewStreamReader(func() (*StreamChunk, error) {
				return &StreamChunk{Delta: string(name), FinishReason: "stop"}, nil
			}, func() error { return nil }), nil
		},
	}
}

func chatRequest() *ChatRequest {
	return &ChatRequest{Messages: []Message{UserText("hi")}}
}

func TestBalancedProviderSmoothWeightedDistribution(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 3},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	)
	require.NoError(t, err)

	for range 8 {
		_, err := lb.Chat(t.Context(), chatRequest())
		require.NoError(t, err)
	}

	assert.EqualValues(t, 6, a.Load())
	assert.EqualValues(t, 2, bCount.Load())
}

func TestBalancedProviderSmoothWeightedOrderIsSpread(t *testing.T) {
	t.Parallel()

	var order []ProviderName
	var mu sync.Mutex
	record := func(name ProviderName) *stubProvider {
		return &stubProvider{
			name: name,
			chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				return &ChatResponse{Content: string(name)}, nil
			},
		}
	}

	lb, err := NewBalancedProvider(
		BalanceMember{Provider: record(ProviderDeepSeek), Weight: 3},
		BalanceMember{Provider: record(ProviderZhipu), Weight: 1},
	)
	require.NoError(t, err)

	for range 4 {
		_, err := lb.Chat(t.Context(), chatRequest())
		require.NoError(t, err)
	}

	// 平滑加权轮询把低权重成员插在中间，而非攒到最后连续命中。
	assert.Equal(t, []ProviderName{ProviderDeepSeek, ProviderDeepSeek, ProviderZhipu, ProviderDeepSeek}, order)
}

func TestBalancedProviderEqualWeightsAlternate(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, nil)},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil)},
	)
	require.NoError(t, err)

	for range 10 {
		_, err := lb.Chat(t.Context(), chatRequest())
		require.NoError(t, err)
	}

	assert.EqualValues(t, 5, a.Load())
	assert.EqualValues(t, 5, bCount.Load())
}

func TestBalancedProviderFailsOverToNextMember(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, retryableErr()), Weight: 10},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	)
	require.NoError(t, err)

	resp, err := lb.Chat(t.Context(), chatRequest())
	require.NoError(t, err)
	assert.Equal(t, string(ProviderZhipu), resp.Content)
	assert.EqualValues(t, 1, a.Load())
	assert.EqualValues(t, 1, bCount.Load())
}

func TestBalancedProviderDoesNotFailOverOnNonRetryableError(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, nonRetryableErr()), Weight: 10},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	)
	require.NoError(t, err)

	_, err = lb.Chat(t.Context(), chatRequest())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidRequest)
	assert.EqualValues(t, 1, a.Load())
	assert.EqualValues(t, 0, bCount.Load())
}

func TestBalancedProviderAggregatesAllErrors(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, retryableErr())},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, retryableErr())},
	)
	require.NoError(t, err)

	_, err = lb.Chat(t.Context(), chatRequest())
	require.Error(t, err)
	require.ErrorContains(t, err, string(ProviderDeepSeek))
	require.ErrorContains(t, err, string(ProviderZhipu))
	assert.EqualValues(t, 1, a.Load())
	assert.EqualValues(t, 1, bCount.Load())
}

func TestBalancedProviderMaxAttemptsLimitsFailover(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, retryableErr())},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil)},
	}, BalanceOptions{MaxAttempts: 1})
	require.NoError(t, err)

	_, err = lb.Chat(t.Context(), chatRequest())
	require.Error(t, err)
	assert.EqualValues(t, 1, a.Load())
	assert.EqualValues(t, 0, bCount.Load(), "MaxAttempts=1 时不做故障转移")
}

func TestBalancedProviderStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: &stubProvider{
			name: ProviderDeepSeek,
			chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
				a.Add(1)
				return nil, retryableErr()
			},
		}},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil)},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = lb.Chat(ctx, chatRequest())
	require.Error(t, err)
	assert.EqualValues(t, 0, bCount.Load(), "ctx 已取消时不再尝试后续成员")
}

func TestBalancedProviderSkipsTrippedMember(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	breaker := newBreakerWithClock(BreakerOptions{
		Name:             "primary",
		FailureThreshold: 1,
		OpenDuration:     time.Minute,
	}, clock.Now)

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{
			Provider: countingProvider(ProviderDeepSeek, &a, retryableErr()),
			Weight:   9,
			Breaker:  breaker,
		},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	)
	require.NoError(t, err)

	// 第一次调用打到主成员并失败，主成员随即熔断。
	resp, err := lb.Chat(t.Context(), chatRequest())
	require.NoError(t, err)
	require.Equal(t, string(ProviderZhipu), resp.Content)
	require.Equal(t, BreakerOpen, breaker.State())
	require.EqualValues(t, 1, a.Load())

	// 熔断期内主成员不再被真正调用，流量全部落到备用成员。
	for range 5 {
		resp, err := lb.Chat(t.Context(), chatRequest())
		require.NoError(t, err)
		require.Equal(t, string(ProviderZhipu), resp.Content)
	}
	assert.EqualValues(t, 1, a.Load(), "熔断期内不应触达主成员")
	assert.EqualValues(t, 6, bCount.Load())

	// 冷却到期后放行探测。
	clock.Advance(time.Minute)
	_, err = lb.Chat(t.Context(), chatRequest())
	require.NoError(t, err)
	assert.EqualValues(t, 2, a.Load())
}

func TestBalancedProviderAllMembersTrippedReturnsBreakerOpen(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	var a, bCount atomic.Int64
	members := []BalanceMember{
		{
			Provider: countingProvider(ProviderDeepSeek, &a, retryableErr()),
			Breaker:  newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Hour}, clock.Now),
		},
		{
			Provider: countingProvider(ProviderZhipu, &bCount, retryableErr()),
			Breaker:  newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Hour}, clock.Now),
		},
	}
	lb, err := NewBalancedProvider(members...)
	require.NoError(t, err)

	_, err = lb.Chat(t.Context(), chatRequest())
	require.Error(t, err)

	_, err = lb.Chat(t.Context(), chatRequest())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrBreakerOpen)
	assert.EqualValues(t, 1, a.Load())
	assert.EqualValues(t, 1, bCount.Load())
}

func TestBalancedProviderFailsOverOnLocalRateLimit(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	limiter := newRateLimiterWithClock(RateLimitOptions{RequestsPerMinute: 60, RequestBurst: 1}, clock.Now)

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{
			Provider: WithRateLimit(countingProvider(ProviderDeepSeek, &a, nil), limiter),
			Weight:   10,
		},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	)
	require.NoError(t, err)

	resp, err := lb.Chat(t.Context(), chatRequest())
	require.NoError(t, err)
	require.Equal(t, string(ProviderDeepSeek), resp.Content)

	// 主成员额度耗尽，本地限流拦下后应切到备用成员。
	resp, err = lb.Chat(t.Context(), chatRequest())
	require.NoError(t, err)
	assert.Equal(t, string(ProviderZhipu), resp.Content)
	assert.EqualValues(t, 1, a.Load())
	assert.EqualValues(t, 1, bCount.Load())
}

func TestBalancedProviderLeastPendingPrefersIdleMember(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var slow, fast atomic.Int64

	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: &stubProvider{
			name: ProviderDeepSeek,
			chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
				slow.Add(1)
				entered <- struct{}{}
				<-release
				return &ChatResponse{Content: "slow"}, nil
			},
		}},
		{Provider: countingProvider(ProviderZhipu, &fast, nil)},
	}, BalanceOptions{Strategy: BalanceLeastPending})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)

		_, chatErr := lb.Chat(t.Context(), chatRequest())
		assert.NoError(t, chatErr)
	}()

	<-entered
	// 慢成员在途，下一次调用应落到空闲成员。
	resp, err := lb.Chat(t.Context(), chatRequest())
	require.NoError(t, err)
	assert.Equal(t, string(ProviderZhipu), resp.Content)

	close(release)
	<-done
	assert.EqualValues(t, 1, slow.Load())
	assert.EqualValues(t, 1, fast.Load())
}

func TestBalancedProviderWeightedRandomFollowsWeights(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 9},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceWeightedRandom})
	require.NoError(t, err)

	const rounds = 3000
	for range rounds {
		_, err := lb.Chat(t.Context(), chatRequest())
		require.NoError(t, err)
	}

	require.EqualValues(t, rounds, a.Load()+bCount.Load())
	// 期望 9:1，放宽到 [80%, 98%] 以避免偶发抖动导致的 flaky。
	ratio := float64(a.Load()) / float64(rounds)
	assert.Greater(t, ratio, 0.80)
	assert.Less(t, ratio, 0.98)
}

func TestBalancedProviderStreamFailsOver(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, retryableErr()), Weight: 10},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	)
	require.NoError(t, err)

	stream, err := lb.ChatStream(t.Context(), chatRequest())
	require.NoError(t, err)
	defer stream.Close()

	chunk, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, string(ProviderZhipu), chunk.Delta)
}

func TestBalancedProviderStats(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	breaker := newBreakerWithClock(BreakerOptions{Name: "primary", FailureThreshold: 1}, clock.Now)
	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 3, Breaker: breaker},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil)},
	)
	require.NoError(t, err)

	stats := lb.Stats()
	require.Len(t, stats, 2)
	assert.Equal(t, ProviderDeepSeek, stats[0].Provider)
	assert.Equal(t, 3, stats[0].Weight)
	assert.Equal(t, 0, stats[0].Pending)
	assert.Equal(t, "primary", stats[0].Breaker.Name)
	assert.Equal(t, BreakerClosed, stats[0].Breaker.State)
	assert.Equal(t, 1, stats[1].Weight, "未设置权重按 1 计")
	assert.Equal(t, BreakerClosed, stats[1].Breaker.State, "未配置熔断器时报告闭合")
}

func TestBalancedProviderNameAndDefaultModel(t *testing.T) {
	t.Parallel()

	var a atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, nil)},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &a, nil)},
	)
	require.NoError(t, err)

	assert.Equal(t, ProviderDeepSeek, lb.Name())
	assert.Empty(t, lb.DefaultModel(), "stub 未实现 DefaultModel")

	var nilLB *BalancedProvider
	assert.Empty(t, nilLB.Name())
	assert.Empty(t, nilLB.DefaultModel())
	assert.Nil(t, nilLB.Stats())
	_, err = nilLB.Chat(t.Context(), chatRequest())
	require.ErrorIs(t, err, ErrNilProvider)
	_, err = nilLB.ChatStream(t.Context(), chatRequest())
	require.ErrorIs(t, err, ErrNilProvider)
}

func TestBalancedProviderDefaultModelFromMember(t *testing.T) {
	t.Parallel()

	primary, err := NewProvider(ProviderConfig{
		Name:    ProviderDeepSeek,
		APIKey:  "test-key",
		Model:   "deepseek-chat",
		BaseURL: "https://example.invalid/v1",
	})
	require.NoError(t, err)

	lb, err := NewBalancedProvider(BalanceMember{Provider: primary})
	require.NoError(t, err)
	assert.Equal(t, "deepseek-chat", lb.DefaultModel())
}

func TestNewBalancedProviderValidatesInput(t *testing.T) {
	t.Parallel()

	_, err := NewBalancedProvider()
	require.ErrorIs(t, err, ErrNilProvider)

	_, err = NewBalancedProvider(BalanceMember{Provider: nil})
	require.ErrorIs(t, err, ErrNilProvider)

	var typedNil *openaiProvider
	_, err = NewBalancedProvider(BalanceMember{Provider: typedNil})
	require.NoError(t, err, "typed nil 与 Registry 口径一致，由成员自身处理")

	var a atomic.Int64
	_, err = NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil)},
	}, BalanceOptions{Strategy: "round_robin"})
	require.ErrorIs(t, err, ErrInvalidBalanceStrategy)
	require.ErrorContains(t, err, "round_robin")
}

func TestBalancedProviderClampsWeight(t *testing.T) {
	t.Parallel()

	var a atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: maxBalanceWeight + 1},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &a, nil), Weight: -5},
	)
	require.NoError(t, err)

	stats := lb.Stats()
	assert.Equal(t, maxBalanceWeight, stats[0].Weight)
	assert.Equal(t, 1, stats[1].Weight)
}

func TestBalancedProviderCustomShouldFallback(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nonRetryableErr()), Weight: 10},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{
		ShouldFallback: func(err error) bool { return errors.Is(err, ErrInvalidRequest) },
	})
	require.NoError(t, err)

	resp, err := lb.Chat(t.Context(), chatRequest())
	require.NoError(t, err)
	assert.Equal(t, string(ProviderZhipu), resp.Content)
}

func TestBalancedProviderConcurrentChat(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 2},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	)
	require.NoError(t, err)

	const calls = 90
	var wg sync.WaitGroup
	for range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()

			_, chatErr := lb.Chat(t.Context(), chatRequest())
			assert.NoError(t, chatErr)
		}()
	}
	wg.Wait()

	assert.EqualValues(t, calls, a.Load()+bCount.Load())
	assert.EqualValues(t, 60, a.Load())
	assert.EqualValues(t, 30, bCount.Load())
}

func BenchmarkBalancedProviderChat(b *testing.B) {
	var counter atomic.Int64
	lb, err := NewBalancedProvider(
		BalanceMember{Provider: countingProvider(ProviderDeepSeek, &counter, nil), Weight: 3},
		BalanceMember{Provider: countingProvider(ProviderZhipu, &counter, nil), Weight: 1},
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	req := chatRequest()
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, err := lb.Chat(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBalancedProviderChatParallel(b *testing.B) {
	var counter atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &counter, nil), Weight: 3},
		{Provider: countingProvider(ProviderZhipu, &counter, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceLeastPending})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	req := chatRequest()
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := lb.Chat(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestBalancedProviderSessionAffinityIsSticky 是会话粘性的核心反证：
// 同一会话键的多轮调用必须始终命中同一成员，否则上游提示词缓存每轮都是冷的。
func TestBalancedProviderSessionAffinityIsSticky(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 1},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceSessionAffinity})
	require.NoError(t, err)

	ctx := WithConversationID(t.Context(), "conv-1")
	const rounds = 20
	for range rounds {
		_, err := lb.Chat(ctx, chatRequest())
		require.NoError(t, err)
	}

	// 全部落到同一个成员：一个计数为 rounds，另一个为 0。
	total := a.Load() + bCount.Load()
	require.EqualValues(t, rounds, total)
	assert.True(t, a.Load() == rounds || bCount.Load() == rounds,
		"同一会话必须恒定命中同一成员，实际 a=%d b=%d", a.Load(), bCount.Load())
}

// TestBalancedProviderSessionAffinityIsDeterministic 确认哈希不含随机种子：
// 独立构造的两个均衡器对同一会话键必须选出同一位置的成员，
// 否则多副本部署与进程重启都会打散缓存。
func TestBalancedProviderSessionAffinityIsDeterministic(t *testing.T) {
	t.Parallel()

	pick := func(key string) ProviderName {
		var a, bCount, c atomic.Int64
		lb, err := NewBalancedProviderWithOptions([]BalanceMember{
			{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 1},
			{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
			{Provider: countingProvider(ProviderQwen, &c, nil), Weight: 1},
		}, BalanceOptions{Strategy: BalanceSessionAffinity})
		require.NoError(t, err)

		resp, err := lb.Chat(WithConversationID(t.Context(), key), chatRequest())
		require.NoError(t, err)
		return ProviderName(resp.Content)
	}

	for _, key := range []string{"conv-1", "conv-2", "user-42", ""} {
		first := pick(key)
		for range 5 {
			assert.Equal(t, first, pick(key), "会话键 %q 的归属必须稳定", key)
		}
	}
}

// TestBalancedProviderSessionAffinitySpreadsSessions 确认不同会话被分散到各成员，
// 粘性针对的是"同一会话"，不是把所有流量压到一个成员上。
func TestBalancedProviderSessionAffinitySpreadsSessions(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 1},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceSessionAffinity})
	require.NoError(t, err)

	const sessions = 200
	for i := range sessions {
		ctx := WithConversationID(t.Context(), fmt.Sprintf("conv-%d", i))
		_, err := lb.Chat(ctx, chatRequest())
		require.NoError(t, err)
	}

	require.EqualValues(t, sessions, a.Load()+bCount.Load())
	// 等权重下两成员各自应拿到大致一半的会话，放宽到 [25%, 75%] 避免哈希抖动 flaky。
	ratio := float64(a.Load()) / float64(sessions)
	assert.Greater(t, ratio, 0.25)
	assert.Less(t, ratio, 0.75)
}

// TestBalancedProviderSessionAffinityRespectsWeight 确认会话按权重分配：
// 权重高的成员承载更多会话。
func TestBalancedProviderSessionAffinityRespectsWeight(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 9},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceSessionAffinity})
	require.NoError(t, err)

	const sessions = 2000
	for i := range sessions {
		ctx := WithConversationID(t.Context(), fmt.Sprintf("conv-%d", i))
		_, err := lb.Chat(ctx, chatRequest())
		require.NoError(t, err)
	}

	require.EqualValues(t, sessions, a.Load()+bCount.Load())
	ratio := float64(a.Load()) / float64(sessions)
	assert.Greater(t, ratio, 0.80, "权重 9 的成员应承载绝大多数会话")
	assert.Less(t, ratio, 0.98)
}

// TestBalancedProviderSessionAffinityFailsOver 确认粘附成员失败时仍会转移，
// 粘性不能把故障转移堵死。
func TestBalancedProviderSessionAffinityFailsOver(t *testing.T) {
	t.Parallel()

	retryable := &ProviderError{Code: ErrorCodeServerError, Retryable: true}
	var failed, healthy atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &failed, retryable), Weight: 1},
		{Provider: countingProvider(ProviderZhipu, &healthy, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceSessionAffinity})
	require.NoError(t, err)

	// 两个成员各试一遍：无论会话键先粘到哪个，最终都应由健康成员返回成功。
	for _, key := range []string{"conv-1", "conv-2", "conv-3", "conv-4"} {
		resp, err := lb.Chat(WithConversationID(t.Context(), key), chatRequest())
		require.NoError(t, err, "会话 %q 应转移到健康成员", key)
		assert.Equal(t, string(ProviderZhipu), resp.Content)
	}
	assert.Positive(t, healthy.Load())
}

// TestBalancedProviderSessionAffinityWithoutKeyFallsBackToRoundRobin 确认无会话键时
// 退化为平滑加权轮询，而不是把全部流量压到同一个成员。
func TestBalancedProviderSessionAffinityWithoutKeyFallsBackToRoundRobin(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 1},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceSessionAffinity})
	require.NoError(t, err)

	// ctx 不含会话标识，默认 SessionKey 返回空串。
	const rounds = 10
	for range rounds {
		_, err := lb.Chat(t.Context(), chatRequest())
		require.NoError(t, err)
	}

	assert.EqualValues(t, rounds/2, a.Load())
	assert.EqualValues(t, rounds/2, bCount.Load())
}

// TestBalancedProviderSessionAffinityCustomSessionKey 确认自定义 SessionKey 生效，
// 可按业务自己的维度（此处为请求模型名）粘附。
func TestBalancedProviderSessionAffinityCustomSessionKey(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 1},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{
		Strategy: BalanceSessionAffinity,
		SessionKey: func(_ context.Context, req *ChatRequest) string {
			return req.Model
		},
	})
	require.NoError(t, err)

	req := chatRequest()
	req.Model = "model-x"
	const rounds = 8
	for range rounds {
		_, err := lb.Chat(t.Context(), req)
		require.NoError(t, err)
	}

	total := a.Load() + bCount.Load()
	require.EqualValues(t, rounds, total)
	assert.True(t, a.Load() == rounds || bCount.Load() == rounds,
		"同一自定义会话键必须恒定命中同一成员")
}

// TestBalancedProviderSessionAffinityUsesUserIDFallback 确认默认 SessionKey 在
// 没有会话标识时回落到用户标识。
func TestBalancedProviderSessionAffinityUsesUserIDFallback(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 1},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceSessionAffinity})
	require.NoError(t, err)

	ctx := WithUserID(t.Context(), "user-7")
	const rounds = 12
	for range rounds {
		_, err := lb.Chat(ctx, chatRequest())
		require.NoError(t, err)
	}

	assert.True(t, a.Load() == rounds || bCount.Load() == rounds,
		"用户级回落也必须产生稳定粘附，实际 a=%d b=%d", a.Load(), bCount.Load())
}

// TestBalancedProviderSessionAffinityStreamIsSticky 确认流式路径同样走会话粘性。
func TestBalancedProviderSessionAffinityStreamIsSticky(t *testing.T) {
	t.Parallel()

	var a, bCount atomic.Int64
	lb, err := NewBalancedProviderWithOptions([]BalanceMember{
		{Provider: countingProvider(ProviderDeepSeek, &a, nil), Weight: 1},
		{Provider: countingProvider(ProviderZhipu, &bCount, nil), Weight: 1},
	}, BalanceOptions{Strategy: BalanceSessionAffinity})
	require.NoError(t, err)

	ctx := WithConversationID(t.Context(), "conv-stream")
	const rounds = 10
	for range rounds {
		stream, err := lb.ChatStream(ctx, chatRequest())
		require.NoError(t, err)
		require.NoError(t, stream.Close())
	}

	assert.True(t, a.Load() == rounds || bCount.Load() == rounds,
		"流式路径的会话粘性必须与非流式一致")
}
