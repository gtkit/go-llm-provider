package provider

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock 是可手动推进的测试时钟。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

func retryableErr() error {
	return &ProviderError{Provider: ProviderOpenAI, Code: ErrorCodeServerError, Retryable: true}
}

func nonRetryableErr() error {
	return &ProviderError{Provider: ProviderOpenAI, Code: ErrorCodeInvalidRequest}
}

func TestBreakerTripsAfterThresholdWithinWindow(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		Name:             "openai",
		FailureThreshold: 3,
		Window:           time.Minute,
		OpenDuration:     30 * time.Second,
	}, clock.Now)

	for range 2 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
	}
	assert.Equal(t, BreakerClosed, b.State())
	assert.Equal(t, 2, b.Stats().Failures)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())

	assert.Equal(t, BreakerOpen, b.State())
	err := b.Allow()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrBreakerOpen)
	require.ErrorContains(t, err, "[openai]")

	stats := b.Stats()
	assert.Equal(t, 1, stats.Trips)
	assert.Equal(t, clock.Now().Add(30*time.Second), stats.OpenUntil)
}

func TestBreakerIgnoresNonTrippingErrors(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 2}, clock.Now)

	for range 5 {
		require.NoError(t, b.Allow())
		b.Report(nonRetryableErr())
	}

	assert.Equal(t, BreakerClosed, b.State())
	assert.Equal(t, 0, b.Stats().Failures)
}

func TestBreakerFailuresSlideOutOfWindow(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		FailureThreshold: 3,
		Window:           time.Minute,
	}, clock.Now)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	clock.Advance(40 * time.Second)
	require.NoError(t, b.Allow())
	b.Report(retryableErr())

	// 第一次失败已滑出窗口，窗口内只剩 1 次，不该跳闸。
	clock.Advance(40 * time.Second)
	assert.Equal(t, 1, b.Stats().Failures)
	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	assert.Equal(t, BreakerClosed, b.State())

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	assert.Equal(t, BreakerOpen, b.State())
}

func TestBreakerHalfOpenProbeSuccessCloses(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		FailureThreshold: 1,
		OpenDuration:     time.Minute,
	}, clock.Now)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	require.Equal(t, BreakerOpen, b.State())

	clock.Advance(time.Minute)
	assert.Equal(t, BreakerHalfOpen, b.State())

	require.NoError(t, b.Allow())
	// 半开态只允许一个在途探测。
	require.ErrorIs(t, b.Allow(), ErrBreakerOpen)

	b.Report(nil)
	assert.Equal(t, BreakerClosed, b.State())
	require.NoError(t, b.Allow())
}

func TestBreakerHalfOpenProbeFailureDoublesCooldown(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		FailureThreshold: 1,
		OpenDuration:     time.Minute,
		MaxOpenDuration:  10 * time.Minute,
		BackoffReset:     time.Hour,
	}, clock.Now)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	require.Equal(t, 1, b.Stats().Trips)
	require.Equal(t, clock.Now().Add(time.Minute), b.Stats().OpenUntil)

	clock.Advance(time.Minute)
	require.NoError(t, b.Allow())
	b.Report(retryableErr())

	stats := b.Stats()
	assert.Equal(t, BreakerOpen, stats.State)
	assert.Equal(t, 2, stats.Trips)
	assert.Equal(t, clock.Now().Add(2*time.Minute), stats.OpenUntil)

	clock.Advance(2 * time.Minute)
	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	assert.Equal(t, clock.Now().Add(4*time.Minute), b.Stats().OpenUntil)
}

func TestBreakerCooldownCappedByMaxOpenDuration(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		FailureThreshold: 1,
		OpenDuration:     time.Minute,
		MaxOpenDuration:  2 * time.Minute,
		BackoffReset:     time.Hour,
	}, clock.Now)

	for range 5 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
		cooldown := b.Stats().OpenUntil.Sub(clock.Now())
		assert.LessOrEqual(t, cooldown, 2*time.Minute)
		clock.Advance(cooldown)
	}
}

func TestBreakerBackoffResetsAfterQuietPeriod(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		FailureThreshold: 1,
		OpenDuration:     time.Minute,
		MaxOpenDuration:  time.Hour,
		BackoffReset:     10 * time.Minute,
	}, clock.Now)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	clock.Advance(time.Minute)
	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	require.Equal(t, 2, b.Stats().Trips)

	// 恢复后长时间无故障，再次跳闸应从最短冷却重新起算。
	clock.Advance(2 * time.Minute)
	require.NoError(t, b.Allow())
	b.Report(nil)
	require.Equal(t, BreakerClosed, b.State())

	clock.Advance(11 * time.Minute)
	require.NoError(t, b.Allow())
	b.Report(retryableErr())

	stats := b.Stats()
	assert.Equal(t, 1, stats.Trips)
	assert.Equal(t, clock.Now().Add(time.Minute), stats.OpenUntil)
}

func TestBreakerResetClearsState(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Hour}, clock.Now)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	require.Equal(t, BreakerOpen, b.State())

	b.Reset()

	stats := b.Stats()
	assert.Equal(t, BreakerClosed, stats.State)
	assert.Equal(t, 0, stats.Trips)
	assert.Zero(t, stats.OpenUntil)
	require.NoError(t, b.Allow())
}

func TestBreakerReportInOpenStateIsIgnored(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Minute}, clock.Now)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	openUntil := b.Stats().OpenUntil

	// 未经 Allow 的越界上报不应延长或缩短冷却。
	b.Report(retryableErr())
	b.Report(nil)

	stats := b.Stats()
	assert.Equal(t, BreakerOpen, stats.State)
	assert.Equal(t, openUntil, stats.OpenUntil)
	assert.Equal(t, 1, stats.Trips)
}

func TestBreakerNilReceiverIsPassthrough(t *testing.T) {
	t.Parallel()

	var b *Breaker

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	b.Reset()
	assert.Equal(t, BreakerClosed, b.State())
	assert.Equal(t, BreakerStats{State: BreakerClosed}, b.Stats())
}

func TestBreakerFallsBackToWallClock(t *testing.T) {
	t.Parallel()

	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Hour}, nil)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	assert.Equal(t, BreakerOpen, b.State())
	assert.WithinDuration(t, time.Now().Add(time.Hour), b.Stats().OpenUntil, time.Minute)
}

func TestBreakerOptionsNormalization(t *testing.T) {
	t.Parallel()

	b := NewBreaker(BreakerOptions{
		FailureThreshold: -1,
		Window:           -time.Second,
		OpenDuration:     -time.Second,
		MaxOpenDuration:  -time.Second,
		BackoffReset:     -time.Second,
		HalfOpenProbes:   -1,
	})

	assert.Equal(t, defaultBreakerFailureThreshold, b.opts.FailureThreshold)
	assert.Equal(t, defaultBreakerWindow, b.opts.Window)
	assert.Equal(t, defaultBreakerOpenDuration, b.opts.OpenDuration)
	assert.Equal(t, defaultBreakerMaxOpenDuration, b.opts.MaxOpenDuration)
	assert.Equal(t, defaultBreakerBackoffReset, b.opts.BackoffReset)
	assert.Equal(t, defaultBreakerHalfOpenProbes, b.opts.HalfOpenProbes)
	assert.NotNil(t, b.opts.ShouldTrip)

	clamped := NewBreaker(BreakerOptions{FailureThreshold: maxBreakerFailureThreshold + 1})
	assert.Equal(t, maxBreakerFailureThreshold, clamped.opts.FailureThreshold)

	swapped := NewBreaker(BreakerOptions{OpenDuration: time.Hour, MaxOpenDuration: time.Second})
	assert.Equal(t, time.Hour, swapped.opts.MaxOpenDuration)
}

func TestBreakerMiddlewareShortCircuitsChat(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Minute}, clock.Now)

	var calls atomic.Int64
	p := WithBreaker(&stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			calls.Add(1)
			return nil, retryableErr()
		},
	}, b)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	_, err := p.Chat(t.Context(), req)
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load())

	_, err = p.Chat(t.Context(), req)
	require.ErrorIs(t, err, ErrBreakerOpen)
	assert.EqualValues(t, 1, calls.Load(), "熔断期内不应再触达 provider")

	clock.Advance(time.Minute)
	_, err = p.Chat(t.Context(), req)
	require.Error(t, err)
	assert.EqualValues(t, 2, calls.Load(), "冷却到期应放行一个探测")
}

func TestBreakerStreamMiddlewareCountsCreationOnly(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Minute}, clock.Now)

	p := WithBreaker(&stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) {
				return nil, retryableErr()
			}, nil), nil
		},
	}, b)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	stream, err := p.ChatStream(t.Context(), req)
	require.NoError(t, err)
	_, recvErr := stream.Recv()
	require.Error(t, recvErr)
	require.NoError(t, stream.Close())

	// 创建成功即视为成功，读流失败不计入熔断。
	assert.Equal(t, BreakerClosed, b.State())
	assert.Equal(t, 0, b.Stats().Failures)
}

func TestBreakerStreamMiddlewareTripsOnCreationFailure(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Minute}, clock.Now)

	p := WithBreaker(&stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return nil, retryableErr()
		},
	}, b)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	_, err := p.ChatStream(t.Context(), req)
	require.Error(t, err)

	_, err = p.ChatStream(t.Context(), req)
	require.ErrorIs(t, err, ErrBreakerOpen)
}

func TestWithBreakerValidatesInput(t *testing.T) {
	t.Parallel()

	_, err := TryWithBreaker(nil, NewBreaker(BreakerOptions{}))
	require.ErrorIs(t, err, ErrNilProvider)

	_, err = TryWithBreaker(&stubProvider{name: ProviderOpenAI}, nil)
	require.ErrorIs(t, err, ErrNilBreaker)

	p := WithBreaker(nil, nil)
	_, err = p.Chat(t.Context(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNilProvider)

	_, err = WithBreaker(&stubProvider{name: ProviderOpenAI}, nil).Chat(t.Context(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNilBreaker)
}

func TestEmbedderBreaker(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Minute}, clock.Now)

	var calls atomic.Int64
	e := WithEmbedderBreaker(&stubEmbedder{
		name: ProviderOpenAI,
		embed: func(context.Context, *EmbeddingRequest) (*EmbeddingResponse, error) {
			calls.Add(1)
			return nil, retryableErr()
		},
	}, b)

	req := &EmbeddingRequest{Input: []string{"hi"}}
	_, err := e.Embed(t.Context(), req)
	require.Error(t, err)
	_, err = e.Embed(t.Context(), req)
	require.ErrorIs(t, err, ErrBreakerOpen)
	assert.EqualValues(t, 1, calls.Load())

	_, err = TryWithEmbedderBreaker(nil, b)
	require.ErrorIs(t, err, ErrNilEmbedder)
	_, err = TryWithEmbedderBreaker(&stubEmbedder{name: ProviderOpenAI}, nil)
	require.ErrorIs(t, err, ErrNilBreaker)
	_, err = WithEmbedderBreaker(nil, nil).Embed(t.Context(), req)
	require.ErrorIs(t, err, ErrNilEmbedder)
}

// TestBreakerShouldTripCanInspectBreaker 是"ShouldTrip 在锁外调用"这条契约的
// 反证测试：把回调移回锁内，本测试会因自死锁而超时失败。
func TestBreakerShouldTripCanInspectBreaker(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	var b *Breaker
	b = newBreakerWithClock(BreakerOptions{
		FailureThreshold: 1,
		OpenDuration:     time.Minute,
		ShouldTrip: func(err error) bool {
			// 回调内读取熔断器状态：锁内调用会自死锁。
			_ = b.State()
			_ = b.Stats()
			return IsRetryableError(err)
		},
	}, clock.Now)

	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	assert.Equal(t, BreakerOpen, b.State())
}

func TestBreakerConcurrentUse(t *testing.T) {
	t.Parallel()

	b := NewBreaker(BreakerOptions{FailureThreshold: 4, Window: time.Hour, OpenDuration: time.Millisecond})

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			if err := b.Allow(); err != nil {
				return
			}
			if i%2 == 0 {
				b.Report(retryableErr())
				return
			}
			b.Report(nil)
		}(i)
	}
	wg.Wait()

	// 只要求并发下不 panic、不 data race，终态由调度顺序决定。
	assert.Contains(t, []BreakerState{BreakerClosed, BreakerOpen, BreakerHalfOpen}, b.State())
}

func TestFallbackSwitchesOnBreakerOpen(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 1, OpenDuration: time.Hour}, clock.Now)
	primary := WithBreaker(&stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, retryableErr()
		},
	}, b)
	secondary := &stubProvider{
		name: ProviderZhipu,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: "from secondary"}, nil
		},
	}

	chain, err := NewFallbackProvider(primary, secondary)
	require.NoError(t, err)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	resp, err := chain.Chat(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "from secondary", resp.Content)

	// 此时链首已熔断，默认判定必须让 ErrBreakerOpen 也切换。
	require.Equal(t, BreakerOpen, b.State())
	resp, err = chain.Chat(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, "from secondary", resp.Content)
}

func BenchmarkBreakerAllowReport(b *testing.B) {
	br := NewBreaker(BreakerOptions{FailureThreshold: 1 << 20, Window: time.Hour})
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if err := br.Allow(); err == nil {
			br.Report(nil)
		}
	}
}

func BenchmarkBreakerAllowReportParallel(b *testing.B) {
	br := NewBreaker(BreakerOptions{FailureThreshold: 1 << 20, Window: time.Hour})
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := br.Allow(); err == nil {
				br.Report(nil)
			}
		}
	})
}

// ============================================================
// 失败率跳闸（ReadyToTrip）
// ============================================================

// TestBreakerFailureRateDoesNotTripHealthyUpstream 是引入 ReadyToTrip 的核心动因：
// 绝对失败次数阈值与流量规模无关——1 分钟窗口配 5 次失败，在高 QPS 下
// 只要上游有千分之一的偶发错误率就会持续跳闸，把本可成功的请求挡在本地。
// 失败率判定必须让这种健康上游保持闭合。
func TestBreakerFailureRateDoesNotTripHealthyUpstream(t *testing.T) {
	t.Parallel()

	const (
		total     = 10_000
		failEvery = 1_000 // 0.1% 错误率，成功率 99.9%
	)

	t.Run("失败次数阈值会误跳闸", func(t *testing.T) {
		t.Parallel()

		clock := newFakeClock()
		b := newBreakerWithClock(BreakerOptions{Name: "count"}, clock.Now)

		tripped := false
		for i := range total {
			if err := b.Allow(); err != nil {
				tripped = true
				break
			}
			if i%failEvery == failEvery-1 {
				b.Report(retryableErr())
			} else {
				b.Report(nil)
			}
		}
		assert.True(t, tripped, "默认阈值下 99.9% 健康的上游会被熔断（这是要解决的问题）")
	})

	t.Run("失败率判定保持闭合", func(t *testing.T) {
		t.Parallel()

		clock := newFakeClock()
		b := newBreakerWithClock(BreakerOptions{
			Name:        "rate",
			ReadyToTrip: FailureRateTrip(20, 0.5),
		}, clock.Now)

		failures := 0
		for i := range total {
			require.NoError(t, b.Allow(), "第 %d 次请求不应被熔断", i)
			if i%failEvery == failEvery-1 {
				b.Report(retryableErr())
				failures++
			} else {
				b.Report(nil)
			}
		}

		assert.Equal(t, BreakerClosed, b.State())
		assert.Positive(t, failures, "用例必须真的产生过失败，否则不构成反证")
	})
}

// TestBreakerFailureRateTripsBrokenUpstream 确认失败率判定不会放过真正坏掉的上游。
func TestBreakerFailureRateTripsBrokenUpstream(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		ReadyToTrip: FailureRateTrip(20, 0.5),
	}, clock.Now)

	// 前 19 次全失败也不跳闸：样本未达 minSamples。
	for range 19 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
	}
	assert.Equal(t, BreakerClosed, b.State(), "样本不足时不得跳闸")

	// 第 20 次达到样本下限，失败率 100% > 50%，跳闸。
	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	assert.Equal(t, BreakerOpen, b.State())
	require.ErrorIs(t, b.Allow(), ErrBreakerOpen)
}

// TestBreakerFailureRateWindowSlides 确认过期样本会滑出窗口：
// 若旧样本不过期，一次故障后的失败率会永久拉高、熔断器再也不闭合。
func TestBreakerFailureRateWindowSlides(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		Window:      time.Minute,
		ReadyToTrip: FailureRateTrip(4, 0.5),
	}, clock.Now)

	// 3 次失败：样本不足，未跳闸。
	for range 3 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
	}
	require.Equal(t, 3, b.Stats().Failures)

	// 整窗过去，旧样本全部过期。
	clock.Advance(2 * time.Minute)
	assert.Equal(t, 0, b.Stats().Failures, "过期样本必须滑出窗口")
	assert.Equal(t, 0, b.Stats().Successes)

	// 窗口已空，再来 3 次失败仍不足 minSamples，保持闭合。
	for range 3 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
	}
	assert.Equal(t, BreakerClosed, b.State())
}

// TestBreakerStatsReportsCounts 确认 Stats 如实反映窗口内成功与失败数，
// 供健康检查与监控读取。
func TestBreakerStatsReportsCounts(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		ReadyToTrip: FailureRateTrip(100, 0.9),
	}, clock.Now)

	for range 7 {
		require.NoError(t, b.Allow())
		b.Report(nil)
	}
	for range 3 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
	}

	stats := b.Stats()
	assert.Equal(t, 7, stats.Successes)
	assert.Equal(t, 3, stats.Failures)
}

// TestBreakerWithoutReadyToTripKeepsLegacyBehavior 是向后兼容的反证：
// 未配置 ReadyToTrip 时不得记录成功样本，也不得改变既有的失败次数判定。
func TestBreakerWithoutReadyToTripKeepsLegacyBehavior(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{FailureThreshold: 3}, clock.Now)

	require.Nil(t, b.buckets, "未启用失败率判定时不应分配分桶，避免为统计成功付出开销")

	for range 10 {
		require.NoError(t, b.Allow())
		b.Report(nil)
	}
	assert.Equal(t, 0, b.Stats().Successes, "未配置 ReadyToTrip 时成功数恒为 0")

	// 失败次数阈值仍按原样生效。
	for range 2 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
	}
	require.Equal(t, BreakerClosed, b.State())
	require.NoError(t, b.Allow())
	b.Report(retryableErr())
	assert.Equal(t, BreakerOpen, b.State(), "第 3 次失败应按 FailureThreshold 跳闸")
}

// TestBreakerReadyToTripReplacesFailureThreshold 确认两者是替代关系而非叠加：
// 配置了 ReadyToTrip 后，即使失败次数远超 FailureThreshold，
// 只要失败率未越线就不跳闸。
func TestBreakerReadyToTripReplacesFailureThreshold(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		FailureThreshold: 2, // 会被 ReadyToTrip 取代
		ReadyToTrip:      FailureRateTrip(10, 0.9),
	}, clock.Now)

	// 9 成功 + 1 失败：失败率 10%，失败次数已超过 FailureThreshold=2 的两倍。
	for range 9 {
		require.NoError(t, b.Allow())
		b.Report(nil)
	}
	for range 5 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
	}

	assert.Equal(t, BreakerClosed, b.State(),
		"失败率未越线时不得因失败次数跳闸")
	assert.Equal(t, 5, b.Stats().Failures)
}

// TestBreakerFailureRateResetsWindowOnTrip 确认跳闸后窗口重新起算，
// 半开探测成功闭合后也不残留旧样本——否则恢复后会立刻被旧失败率再次拉闸。
func TestBreakerFailureRateResetsWindowOnTrip(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		OpenDuration: 10 * time.Second,
		ReadyToTrip:  FailureRateTrip(4, 0.5),
	}, clock.Now)

	for range 4 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr())
	}
	require.Equal(t, BreakerOpen, b.State())
	assert.Equal(t, 0, b.Stats().Failures, "跳闸后窗口应清空")

	// 冷却到期 -> 半开探测成功 -> 闭合，窗口仍是空的。
	clock.Advance(11 * time.Second)
	require.NoError(t, b.Allow())
	b.Report(nil)
	require.Equal(t, BreakerClosed, b.State())
	assert.Equal(t, 0, b.Stats().Failures)
	assert.Equal(t, 0, b.Stats().Successes)
}

// TestFailureRateTripArgumentGuards 覆盖 helper 的参数边界：
// 非法参数不得让熔断器静默失效（永不跳闸）。
func TestFailureRateTripArgumentGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		minSamples     int
		maxFailureRate float64
		counts         BreakerCounts
		wantTrip       bool
	}{
		{
			name:       "minSamples 为 0 按 1 计：单次失败即可判定",
			minSamples: 0, maxFailureRate: 0.5,
			counts: BreakerCounts{Failures: 1}, wantTrip: true,
		},
		{
			name:       "minSamples 为负按 1 计",
			minSamples: -10, maxFailureRate: 0.5,
			counts: BreakerCounts{Failures: 1}, wantTrip: true,
		},
		{
			name:       "NaN 阈值按 0 处理：任何失败都跳闸，不静默失效",
			minSamples: 1, maxFailureRate: math.NaN(),
			counts: BreakerCounts{Successes: 99, Failures: 1}, wantTrip: true,
		},
		{
			name:       "负阈值收敛到 0",
			minSamples: 1, maxFailureRate: -1,
			counts: BreakerCounts{Successes: 99, Failures: 1}, wantTrip: true,
		},
		{
			name:       "超过 1 的阈值收敛到 1：全失败也不跳闸（失败率不可能大于 1）",
			minSamples: 1, maxFailureRate: 5,
			counts: BreakerCounts{Failures: 10}, wantTrip: false,
		},
		{
			name:       "样本不足不跳闸",
			minSamples: 10, maxFailureRate: 0.1,
			counts: BreakerCounts{Failures: 9}, wantTrip: false,
		},
		{
			name:       "零样本不跳闸",
			minSamples: 1, maxFailureRate: 0,
			counts: BreakerCounts{}, wantTrip: false,
		},
		{
			name:       "失败率等于阈值不跳闸（严格大于才跳）",
			minSamples: 2, maxFailureRate: 0.5,
			counts: BreakerCounts{Successes: 1, Failures: 1}, wantTrip: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantTrip, FailureRateTrip(tc.minSamples, tc.maxFailureRate)(tc.counts))
		})
	}
}

// TestBreakerCountsHelpers 覆盖统计量的除零与取值范围。
func TestBreakerCountsHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, BreakerCounts{}.Total())
	assert.InDelta(t, 0.0, BreakerCounts{}.FailureRate(), 1e-9, "零样本时不得除零")
	assert.Equal(t, 10, BreakerCounts{Successes: 7, Failures: 3}.Total())
	assert.InDelta(t, 0.3, BreakerCounts{Successes: 7, Failures: 3}.FailureRate(), 1e-9)
	assert.InDelta(t, 1.0, BreakerCounts{Failures: 5}.FailureRate(), 1e-9)
}

// TestBreakerFailureRateExtremeWindow 覆盖极小窗口：
// Window 小于分桶数时整除会得 0，桶宽必须有下限，否则计算桶序号时除零 panic。
func TestBreakerFailureRateExtremeWindow(t *testing.T) {
	t.Parallel()

	for _, window := range []time.Duration{time.Nanosecond, 5 * time.Nanosecond, time.Microsecond} {
		clock := newFakeClock()
		b := newBreakerWithClock(BreakerOptions{
			Window:      window,
			ReadyToTrip: FailureRateTrip(1, 0.5),
		}, clock.Now)

		require.Positive(t, b.bucketWidth, "桶宽必须为正，window=%s", window)
		require.NoError(t, b.Allow())
		b.Report(retryableErr()) // 不得 panic
		_ = b.Stats()
	}
}

// TestBreakerFailureRateZeroTimeClock 覆盖负时间戳：零值 time.Time 的 UnixNano
// 是负数，取模会得到负下标，未补正会索引越界 panic。
func TestBreakerFailureRateZeroTimeClock(t *testing.T) {
	t.Parallel()

	b := newBreakerWithClock(BreakerOptions{
		ReadyToTrip: FailureRateTrip(2, 0.5),
	}, func() time.Time { return time.Time{} })

	// 时钟恒为零值：两次失败即达样本下限且失败率 100%，应跳闸。
	for range 2 {
		require.NoError(t, b.Allow())
		b.Report(retryableErr()) // 不得 panic
	}
	assert.Equal(t, BreakerOpen, b.State(), "负时间戳下判定仍应生效")

	// 时钟不前进，冷却永不到期，后续请求被拦下且不 panic。
	require.ErrorIs(t, b.Allow(), ErrBreakerOpen)
	_ = b.Stats()
}

// TestBreakerFailureRateClockRewind 覆盖系统时钟回拨：
// 环形数组里可能残留序号大于当前时间格的桶，计入会让判定失真。
func TestBreakerFailureRateClockRewind(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		Window:      time.Minute,
		ReadyToTrip: FailureRateTrip(2, 0.5),
	}, clock.Now)

	for range 3 {
		require.NoError(t, b.Allow())
		b.Report(nil)
	}
	require.Equal(t, 3, b.Stats().Successes)

	// 时钟回拨到窗口之前：此前记录的桶序号变成"未来"，不应计入。
	clock.Advance(-10 * time.Minute)
	stats := b.Stats()
	assert.Equal(t, 0, stats.Successes, "时钟回拨后不得把未来的样本算进当前窗口")
	assert.Equal(t, 0, stats.Failures)

	// 回拨后仍能正常工作，不 panic。
	require.NoError(t, b.Allow())
	b.Report(retryableErr())
}

// TestBreakerFailureRateMemoryIsBounded 确认内存占用与 QPS 无关：
// 分桶数固定，10 万次上报后桶数组长度不变。
func TestBreakerFailureRateMemoryIsBounded(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	b := newBreakerWithClock(BreakerOptions{
		Window:      time.Minute,
		ReadyToTrip: FailureRateTrip(1_000_000, 0.5), // 阈值极高，保持闭合
	}, clock.Now)

	for i := range 100_000 {
		require.NoError(t, b.Allow())
		b.Report(nil)
		if i%1000 == 0 {
			clock.Advance(time.Second)
		}
	}

	assert.Len(t, b.buckets, breakerBucketCount, "桶数固定，不随样本数增长")
	assert.Empty(t, b.failures, "失败率模式下不应再往失败时刻切片追加")
}

// TestBreakerFailureRateConcurrent 并发上报，配合 -race 检查分桶读写的数据竞争。
func TestBreakerFailureRateConcurrent(t *testing.T) {
	t.Parallel()

	b := NewBreaker(BreakerOptions{
		Window:      time.Minute,
		ReadyToTrip: FailureRateTrip(50, 0.9),
	})

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := range 200 {
				if b.Allow() != nil {
					continue
				}
				if (worker+j)%10 == 0 {
					b.Report(retryableErr())
				} else {
					b.Report(nil)
				}
				_ = b.Stats()
			}
		}(i)
	}
	wg.Wait()
}
