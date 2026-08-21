package provider

import (
	"context"
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

	req := &ChatRequest{Messages: []Message{UserText("hi")}}
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

	req := &ChatRequest{Messages: []Message{UserText("hi")}}
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

	req := &ChatRequest{Messages: []Message{UserText("hi")}}
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

	req := &ChatRequest{Messages: []Message{UserText("hi")}}
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
