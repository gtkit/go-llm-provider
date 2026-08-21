package provider

import (
	"context"
	"io"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rateLimitHeaders(remainingRequests, remainingTokens string) ResponseMetadata {
	header := http.Header{}
	if remainingRequests != "" {
		header.Set(rateLimitHeaderRemainingRequests, remainingRequests)
	}
	if remainingTokens != "" {
		header.Set(rateLimitHeaderRemainingTokens, remainingTokens)
	}
	return ResponseMetadata{Headers: header}
}

func TestRateLimiterPassthroughWhenUnset(t *testing.T) {
	t.Parallel()

	l := NewRateLimiter(RateLimitOptions{})
	require.NoError(t, l.Acquire(t.Context(), 1_000_000))
	assert.Equal(t, RateLimiterStats{}, l.Stats())

	// nil 接收者等价于不限流。
	var nilLimiter *RateLimiter
	require.NoError(t, nilLimiter.Acquire(t.Context(), 1))
	nilLimiter.Settle(10, 20)
	nilLimiter.Observe(rateLimitHeaders("1", "1"))
	assert.Equal(t, RateLimiterStats{}, nilLimiter.Stats())
}

func TestRateLimiterFallsBackToWallClock(t *testing.T) {
	t.Parallel()

	l := newRateLimiterWithClock(RateLimitOptions{RequestsPerMinute: 60, RequestBurst: 1}, nil)

	require.NoError(t, l.Acquire(t.Context(), 0))
	require.ErrorIs(t, l.Acquire(t.Context(), 0), ErrLocalRateLimited)
}

func TestRateLimiterNonBlockingRejectsWhenExhausted(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{
		Name:              "qwen",
		RequestsPerMinute: 60,
		RequestBurst:      1,
	}, clock.Now)

	require.NoError(t, l.Acquire(t.Context(), 0))

	err := l.Acquire(t.Context(), 0)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrLocalRateLimited)
	require.ErrorContains(t, err, "[qwen]")

	clock.Advance(time.Second)
	require.NoError(t, l.Acquire(t.Context(), 0))
}

func TestRateLimiterDefaultRequestBurstIsOneSecondQuota(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{RequestsPerMinute: 600}, clock.Now)

	stats := l.Stats()
	assert.Equal(t, 10, stats.RequestCapacity)
	assert.Equal(t, 10, stats.AvailableRequests)
	assert.Equal(t, 0, stats.TokenCapacity)
}

func TestRateLimiterDefaultTokenBurstIsFullMinuteQuota(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{TokensPerMinute: 100_000}, clock.Now)

	stats := l.Stats()
	assert.Equal(t, 100_000, stats.TokenCapacity)
	assert.Equal(t, 100_000, stats.AvailableTokens)
	assert.Equal(t, 0, stats.RequestCapacity)
}

func TestRateLimiterWaitBlocksUntilRefill(t *testing.T) {
	t.Parallel()

	// 6000 RPM = 100 次/秒，桶容量 1 时第二次需等约 10ms。
	l := NewRateLimiter(RateLimitOptions{
		RequestsPerMinute: 6000,
		RequestBurst:      1,
		Wait:              true,
	})

	require.NoError(t, l.Acquire(t.Context(), 0))

	start := time.Now()
	require.NoError(t, l.Acquire(t.Context(), 0))
	assert.GreaterOrEqual(t, time.Since(start), 5*time.Millisecond)
}

func TestRateLimiterMaxWaitRejectsAndReleasesReservation(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{
		RequestsPerMinute: 60,
		RequestBurst:      1,
		TokensPerMinute:   600,
		TokenBurst:        10,
		Wait:              true,
		MaxWait:           time.Millisecond,
	}, clock.Now)

	require.NoError(t, l.Acquire(t.Context(), 10))
	require.Equal(t, 0, l.Stats().AvailableRequests)
	require.Equal(t, 0, l.Stats().AvailableTokens)

	err := l.Acquire(t.Context(), 10)
	require.ErrorIs(t, err, ErrLocalRateLimited)
	require.ErrorContains(t, err, "exceeds max wait")

	// 被拒绝的预约必须归还，否则桶会一直透支。
	stats := l.Stats()
	assert.Equal(t, 0, stats.AvailableRequests)
	assert.Equal(t, 0, stats.AvailableTokens)
}

func TestRateLimiterAcquireRespectsCanceledContext(t *testing.T) {
	t.Parallel()

	l := NewRateLimiter(RateLimitOptions{RequestsPerMinute: 60, RequestBurst: 1, Wait: true})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := l.Acquire(ctx, 0)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, l.Stats().AvailableRequests, "取消不应消耗额度")
}

func TestRateLimiterAcquireCanceledWhileWaiting(t *testing.T) {
	t.Parallel()

	l := NewRateLimiter(RateLimitOptions{RequestsPerMinute: 6, RequestBurst: 1, Wait: true})
	require.NoError(t, l.Acquire(t.Context(), 0))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	err := l.Acquire(ctx, 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRateLimiterSettleRefundsAndCharges(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{TokensPerMinute: 1000, TokenBurst: 1000}, clock.Now)

	require.NoError(t, l.Acquire(t.Context(), 500))
	require.Equal(t, 500, l.Stats().AvailableTokens)

	// 实际用量少于预扣：返还差额。
	l.Settle(500, 200)
	assert.Equal(t, 800, l.Stats().AvailableTokens)

	// 实际用量多于预扣：补扣差额。
	require.NoError(t, l.Acquire(t.Context(), 300))
	l.Settle(300, 900)
	assert.Equal(t, -100, l.Stats().AvailableTokens)

	// 相等时不动，负值参数按 0 处理。
	l.Settle(100, 100)
	assert.Equal(t, -100, l.Stats().AvailableTokens)
	l.Settle(-5, -5)
	assert.Equal(t, -100, l.Stats().AvailableTokens)
}

func TestRateLimiterSettleNeverExceedsCapacity(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{TokensPerMinute: 100, TokenBurst: 100}, clock.Now)

	l.Settle(1000, 0)
	assert.Equal(t, 100, l.Stats().AvailableTokens)
}

func TestRateLimiterObserveClampsDownOnly(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{
		RequestsPerMinute: 600,
		RequestBurst:      10,
		TokensPerMinute:   1000,
		TokenBurst:        1000,
	}, clock.Now)

	l.Observe(rateLimitHeaders("3", "250"))
	stats := l.Stats()
	assert.Equal(t, 3, stats.AvailableRequests)
	assert.Equal(t, 250, stats.AvailableTokens)

	// 平台报告更宽松时不上调。
	l.Observe(rateLimitHeaders("9", "900"))
	stats = l.Stats()
	assert.Equal(t, 3, stats.AvailableRequests)
	assert.Equal(t, 250, stats.AvailableTokens)

	// 缺失或非法的头不做调整。
	l.Observe(ResponseMetadata{})
	l.Observe(rateLimitHeaders("abc", "-1"))
	stats = l.Stats()
	assert.Equal(t, 3, stats.AvailableRequests)
	assert.Equal(t, 250, stats.AvailableTokens)
}

func TestRateLimiterClampsOversizedTokenRequest(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{TokensPerMinute: 100, TokenBurst: 100}, clock.Now)

	// 单请求需求超过桶容量时按容量收敛，不会永久饿死。
	require.NoError(t, l.Acquire(t.Context(), 1_000_000))
	assert.Equal(t, 0, l.Stats().AvailableTokens)
}

func TestEstimateChatRequestTokens(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, EstimateChatRequestTokens(nil, 100))

	req := &ChatRequest{Messages: []Message{UserText("hello world")}}
	base := EstimateTokens(req.Messages)
	assert.Equal(t, base+128, EstimateChatRequestTokens(req, 128))
	assert.Equal(t, base, EstimateChatRequestTokens(req, -10))

	req.MaxTokens = 64
	assert.Equal(t, base+64, EstimateChatRequestTokens(req, 128), "MaxTokens 优先于预留值")
}

func TestRateLimitMiddlewareSettlesRealUsage(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{
		TokensPerMinute:  10_000,
		TokenBurst:       10_000,
		AdaptFromHeaders: true,
	}, clock.Now)

	req := &ChatRequest{Messages: []Message{UserText("hello")}, MaxTokens: 1000}
	estimated := EstimateChatRequestTokens(req, 0)

	p := WithRateLimit(&stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{
				Content:  "hi",
				Usage:    Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
				Metadata: rateLimitHeaders("", "400"),
			}, nil
		},
	}, l)

	_, err := p.Chat(t.Context(), req)
	require.NoError(t, err)

	// 预扣 estimated、实扣 10，再被响应头下调到 400。
	require.Positive(t, estimated)
	assert.Equal(t, 400, l.Stats().AvailableTokens)
}

func TestRateLimitMiddlewareKeepsReservationOnFailure(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{TokensPerMinute: 1000, TokenBurst: 1000}, clock.Now)

	req := &ChatRequest{Messages: []Message{UserText("hello")}, MaxTokens: 100}
	estimated := EstimateChatRequestTokens(req, 0)

	p := WithRateLimit(&stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, retryableErr()
		},
	}, l)

	_, err := p.Chat(t.Context(), req)
	require.Error(t, err)
	assert.Equal(t, 1000-estimated, l.Stats().AvailableTokens, "失败请求保留预扣，不返还")
}

func TestRateLimitMiddlewareRejectsBeforeProvider(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{RequestsPerMinute: 60, RequestBurst: 1}, clock.Now)

	var calls atomic.Int64
	p := WithRateLimit(&stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			calls.Add(1)
			return &ChatResponse{Content: "ok"}, nil
		},
	}, l)

	req := &ChatRequest{Messages: []Message{UserText("hi")}}
	_, err := p.Chat(t.Context(), req)
	require.NoError(t, err)

	_, err = p.Chat(t.Context(), req)
	require.ErrorIs(t, err, ErrLocalRateLimited)
	assert.EqualValues(t, 1, calls.Load(), "限流拦下的请求不该触达 provider")
}

func TestRateLimitStreamMiddlewareSettlesOnStreamEnd(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{TokensPerMinute: 5000, TokenBurst: 5000}, clock.Now)

	req := &ChatRequest{Messages: []Message{UserText("hello")}, MaxTokens: 1000}

	p := WithRateLimit(&stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			chunks := []*StreamChunk{
				{Delta: "a"},
				{FinishReason: "stop", Usage: Usage{PromptTokens: 3, CompletionTokens: 7, TotalTokens: 10}},
			}
			idx := 0
			return NewStreamReader(func() (*StreamChunk, error) {
				if idx >= len(chunks) {
					return nil, io.EOF
				}
				chunk := chunks[idx]
				idx++
				return chunk, nil
			}, func() error { return nil }), nil
		},
	}, l)

	stream, err := p.ChatStream(t.Context(), req)
	require.NoError(t, err)

	estimated := EstimateChatRequestTokens(req, 0)
	assert.Equal(t, 5000-estimated, l.Stats().AvailableTokens, "流创建后仅预扣")

	for {
		_, recvErr := stream.Recv()
		if recvErr != nil {
			require.ErrorIs(t, recvErr, io.EOF)
			break
		}
	}
	require.NoError(t, stream.Close())

	assert.Equal(t, 4990, l.Stats().AvailableTokens, "流结束后按真实 Usage 结算")
}

func TestRateLimitStreamMiddlewareSettlesOnceOnEarlyClose(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{TokensPerMinute: 5000, TokenBurst: 5000}, clock.Now)

	req := &ChatRequest{Messages: []Message{UserText("hello")}, MaxTokens: 1000}
	estimated := EstimateChatRequestTokens(req, 0)

	p := WithRateLimit(&stubProvider{
		name: ProviderOpenAI,
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) {
				return &StreamChunk{Delta: "a"}, nil
			}, func() error { return nil }), nil
		},
	}, l)

	stream, err := p.ChatStream(t.Context(), req)
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	require.NoError(t, stream.Close())

	// 没拿到 Usage 时保留预扣，且不会重复结算。
	assert.Equal(t, 5000-estimated, l.Stats().AvailableTokens)
}

func TestRateLimitEmbedMiddleware(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiterWithClock(RateLimitOptions{TokensPerMinute: 1000, TokenBurst: 1000}, clock.Now)

	e := WithEmbedderRateLimit(&stubEmbedder{
		name: ProviderOpenAI,
		embed: func(context.Context, *EmbeddingRequest) (*EmbeddingResponse, error) {
			return &EmbeddingResponse{Usage: Usage{PromptTokens: 12, TotalTokens: 12}}, nil
		},
	}, l)

	_, err := e.Embed(t.Context(), &EmbeddingRequest{Input: []string{"hello world", "另一段中文文本"}})
	require.NoError(t, err)
	assert.Equal(t, 1000-12, l.Stats().AvailableTokens)
}

func TestWithRateLimitValidatesInput(t *testing.T) {
	t.Parallel()

	l := NewRateLimiter(RateLimitOptions{RequestsPerMinute: 10})

	_, err := TryWithRateLimit(nil, l)
	require.ErrorIs(t, err, ErrNilProvider)

	_, err = TryWithRateLimit(&stubProvider{name: ProviderOpenAI}, nil)
	require.ErrorIs(t, err, ErrNilRateLimiter)

	_, err = WithRateLimit(nil, l).Chat(t.Context(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNilProvider)

	_, err = WithRateLimit(&stubProvider{name: ProviderOpenAI}, nil).Chat(t.Context(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNilRateLimiter)

	_, err = TryWithEmbedderRateLimit(nil, l)
	require.ErrorIs(t, err, ErrNilEmbedder)

	_, err = TryWithEmbedderRateLimit(&stubEmbedder{name: ProviderOpenAI}, nil)
	require.ErrorIs(t, err, ErrNilRateLimiter)

	_, err = WithEmbedderRateLimit(nil, l).Embed(t.Context(), &EmbeddingRequest{Input: []string{"x"}})
	require.ErrorIs(t, err, ErrNilEmbedder)

	_, err = WithEmbedderRateLimit(&stubEmbedder{name: ProviderOpenAI}, nil).
		Embed(t.Context(), &EmbeddingRequest{Input: []string{"x"}})
	require.ErrorIs(t, err, ErrNilRateLimiter)
}

func TestRateLimiterConcurrentAcquire(t *testing.T) {
	t.Parallel()

	l := NewRateLimiter(RateLimitOptions{
		RequestsPerMinute: 60_000,
		TokensPerMinute:   1_000_000,
		Wait:              true,
	})

	var wg sync.WaitGroup
	var granted atomic.Int64
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := l.Acquire(t.Context(), 100); err == nil {
				granted.Add(1)
				l.Settle(100, 50)
			}
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 64, granted.Load())
}

func TestCeilDiv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b, want int
	}{
		{a: 10, b: 60, want: 1},
		{a: 60, b: 60, want: 1},
		{a: 61, b: 60, want: 2},
		{a: 600, b: 60, want: 10},
		{a: 7, b: 0, want: 7},
		// (a+b-1)/b 写法会在此处溢出为负；余数写法不会。
		{a: math.MaxInt, b: 60, want: math.MaxInt/60 + 1},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, ceilDiv(tt.a, tt.b))
	}
}

func BenchmarkRateLimiterAcquire(b *testing.B) {
	l := NewRateLimiter(RateLimitOptions{RequestsPerMinute: 1 << 30, TokensPerMinute: 1 << 30})
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = l.Acquire(ctx, 100)
	}
}

func BenchmarkRateLimiterAcquireParallel(b *testing.B) {
	l := NewRateLimiter(RateLimitOptions{RequestsPerMinute: 1 << 30, TokensPerMinute: 1 << 30})
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = l.Acquire(ctx, 100)
		}
	})
}
