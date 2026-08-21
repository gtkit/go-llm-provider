package provider

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"
)

// ============================================================
// 客户端侧速率限制（RPM / TPM）
// ============================================================

// secondsPerMinute 把"每分钟"配额换算为令牌桶的每秒补充速率。
const secondsPerMinute = 60

// rateLimitHeaderRemainingRequests / rateLimitHeaderRemainingTokens 是
// OpenAI 兼容平台回传剩余配额的响应头，已在 metadata 白名单内。
const (
	rateLimitHeaderRemainingRequests = "x-ratelimit-remaining-requests"
	//nolint:gosec // G101 误报：这是响应头名称，不是凭据
	rateLimitHeaderRemainingTokens = "x-ratelimit-remaining-tokens"
)

// RateLimitOptions 配置客户端侧速率限制。
// 零值表示不限流，此时限流器为透传。
type RateLimitOptions struct {
	// Name 是限流器标识，仅用于错误消息与 Stats 展示。
	Name string

	// RequestsPerMinute 是每分钟请求数上限（RPM），≤ 0 表示不限制请求数。
	RequestsPerMinute int

	// TokensPerMinute 是每分钟 token 数上限（TPM），≤ 0 表示不限制 token。
	TokensPerMinute int

	// RequestBurst 是请求令牌桶容量，即允许的瞬时并发上限。
	// ≤ 0 时取"1 秒额度"（RequestsPerMinute/60 向上取整，至少 1），
	// 让流量匀速铺开，避免整分钟额度被瞬间打出去触发平台的秒级限流。
	RequestBurst int

	// TokenBurst 是 token 令牌桶容量。≤ 0 时取整分钟额度
	// （TokensPerMinute）——平台的 TPM 按分钟统计，单次长上下文请求
	// 需要的额度可能远超"1 秒额度"，桶容量过小会让请求永远拿不到令牌。
	TokenBurst int

	// Wait 为 true 时额度不足会阻塞等待（受 ctx 与 MaxWait 约束）；
	// 为 false 时立即返回 ErrLocalRateLimited。
	Wait bool

	// MaxWait 是 Wait 模式下的单次最长等待时长，≤ 0 表示只受 ctx 约束。
	// 需要等待超过该时长时放弃本次预约并返回 ErrLocalRateLimited。
	MaxWait time.Duration

	// ReserveOutputTokens 是请求未显式设置 MaxTokens 时为输出预留的 token 数，
	// 默认 0（只按输入估算预扣，实际用量在响应返回后补扣）。
	// 希望限流更早生效、避免短暂超额时，按模型的典型输出长度设置。
	ReserveOutputTokens int

	// AdaptFromHeaders 为 true 时，中间件用响应头回传的剩余配额
	// （x-ratelimit-remaining-requests / -tokens）校准本地桶：平台报告的
	// 剩余量比本地估计更少时按平台的来。默认关闭。
	AdaptFromHeaders bool
}

// RateLimiterStats 是限流器某一时刻的快照。
// 可用额度为负表示已透支，需要等待补充。
type RateLimiterStats struct {
	Name string
	// AvailableRequests / RequestCapacity 是请求桶的可用额度与容量；
	// 未启用请求限流时均为 0。
	AvailableRequests int
	RequestCapacity   int
	// AvailableTokens / TokenCapacity 是 token 桶的可用额度与容量；
	// 未启用 token 限流时均为 0。
	AvailableTokens int
	TokenCapacity   int
}

// RateLimiter 是基于令牌桶的客户端侧限流器，同时约束 RPM 与 TPM。
//
// 与平台侧限流的关系：这里把超额请求挡在发出之前，减少 429；
// 平台真的返回 429 时仍应配合 WithRetry 与 Breaker 处理。
//
// token 额度采用"预扣 + 结算"：请求前按 EstimateTokens 口径预扣
// （误差 ±30%），响应返回后用真实 Usage 结算差额。请求失败时保留预扣不返还，
// 宁可保守也不放大超额风险。
//
// 状态保存在进程内存中，不跨进程共享；多副本部署时按副本数拆分配额。
// 所有方法对 nil 接收者安全（等价于"不限流"），可并发调用。
type RateLimiter struct {
	opts RateLimitOptions
	now  func() time.Time

	mu       sync.Mutex
	requests *tokenBucket
	tokens   *tokenBucket
}

// NewRateLimiter 按 opts 创建限流器。RequestsPerMinute 与 TokensPerMinute
// 都未设置时返回的限流器为透传。返回的 *RateLimiter 可并发使用。
func NewRateLimiter(opts RateLimitOptions) *RateLimiter {
	return newRateLimiterWithClock(opts, time.Now)
}

func newRateLimiterWithClock(opts RateLimitOptions, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	l := &RateLimiter{opts: opts, now: now}
	start := now()
	if opts.RequestsPerMinute > 0 {
		burst := opts.RequestBurst
		if burst <= 0 {
			burst = max(1, ceilDiv(opts.RequestsPerMinute, secondsPerMinute))
		}
		l.requests = newTokenBucket(float64(burst), float64(opts.RequestsPerMinute)/secondsPerMinute, start)
	}
	if opts.TokensPerMinute > 0 {
		burst := opts.TokenBurst
		if burst <= 0 {
			burst = opts.TokensPerMinute
		}
		l.tokens = newTokenBucket(float64(burst), float64(opts.TokensPerMinute)/secondsPerMinute, start)
	}
	return l
}

// Acquire 申请一次调用额度：请求桶固定消耗 1，token 桶消耗 tokens。
//
// 额度充足立即返回 nil。不足时按 RateLimitOptions.Wait 决定阻塞等待还是
// 立即返回 ErrLocalRateLimited；等待期间 ctx 取消会返回 ctx 的错误。
//
// tokens 为负按 0 处理；tokens 超过 token 桶容量时按容量收敛——
// 否则单次请求会因永远凑不齐额度而饿死。
func (l *RateLimiter) Acquire(ctx context.Context, tokens int) error {
	if l == nil || (l.requests == nil && l.tokens == nil) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("rate limit acquire canceled: %w", err)
	}

	need := max(tokens, 0)
	wait, ok := l.reserve(need)
	if !ok {
		return l.limitedError("insufficient quota")
	}
	if wait <= 0 {
		return nil
	}
	if l.opts.MaxWait > 0 && wait > l.opts.MaxWait {
		l.release(need)
		return l.limitedError(fmt.Sprintf("wait %s exceeds max wait %s",
			wait.Round(time.Millisecond), l.opts.MaxWait))
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		l.release(need)
		return fmt.Errorf("rate limit wait canceled: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// Settle 用真实用量结算一次已预扣的 token 额度：
// 实际少于预扣则返还差额，多于预扣则补扣（可能让桶透支，由后续请求承担等待）。
// 只影响 token 桶，请求桶的额度已随请求发出而消耗，不返还。
func (l *RateLimiter) Settle(estimated, actual int) {
	if l == nil || l.tokens == nil {
		return
	}
	delta := max(actual, 0) - max(estimated, 0)
	if delta == 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if delta < 0 {
		l.tokens.giveBack(now, float64(-delta))
		return
	}
	l.tokens.take(now, float64(delta))
}

// Observe 用响应头回传的剩余配额校准本地桶。
//
// 平台报告的剩余量比本地估计更少时把本地可用额度下调到平台口径，
// 反之不上调——本地估计偏保守是安全的，跟着平台放宽则可能立刻超额。
// 头缺失或无法解析时不做任何调整。
func (l *RateLimiter) Observe(metadata ResponseMetadata) {
	if l == nil || (l.requests == nil && l.tokens == nil) {
		return
	}

	remainingRequests, hasRequests := parseRateLimitRemaining(metadata.Header(rateLimitHeaderRemainingRequests))
	remainingTokens, hasTokens := parseRateLimitRemaining(metadata.Header(rateLimitHeaderRemainingTokens))
	if !hasRequests && !hasTokens {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if hasRequests && l.requests != nil {
		l.requests.clampTo(now, remainingRequests)
	}
	if hasTokens && l.tokens != nil {
		l.tokens.clampTo(now, remainingTokens)
	}
}

// Stats 返回限流器快照，供监控与容量排查读取。
func (l *RateLimiter) Stats() RateLimiterStats {
	if l == nil {
		return RateLimiterStats{}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	stats := RateLimiterStats{Name: l.opts.Name}
	if l.requests != nil {
		stats.AvailableRequests = l.requests.availableAt(now)
		stats.RequestCapacity = int(l.requests.capacity)
	}
	if l.tokens != nil {
		stats.AvailableTokens = l.tokens.availableAt(now)
		stats.TokenCapacity = int(l.tokens.capacity)
	}
	return stats
}

// reserve 在一把锁内同时处理请求桶与 token 桶，保证两者要么都预约成功、
// 要么都不动，返回需要等待的时长。
func (l *RateLimiter) reserve(tokens int) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	need := l.tokenNeedLocked(tokens)

	if !l.opts.Wait {
		if l.requests != nil && !l.requests.hasAt(now, 1) {
			return 0, false
		}
		if l.tokens != nil && !l.tokens.hasAt(now, need) {
			return 0, false
		}
		if l.requests != nil {
			l.requests.take(now, 1)
		}
		if l.tokens != nil {
			l.tokens.take(now, need)
		}
		return 0, true
	}

	var wait time.Duration
	if l.requests != nil {
		wait = l.requests.take(now, 1)
	}
	if l.tokens != nil {
		wait = max(wait, l.tokens.take(now, need))
	}
	return wait, true
}

// release 撤销一次预约。撤销后其他并发等待者的等待时长不会随之缩短
// （它们已按扣减后的水位算好唤醒时刻），方向上偏保守、不会超额。
func (l *RateLimiter) release(tokens int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if l.requests != nil {
		l.requests.giveBack(now, 1)
	}
	if l.tokens != nil {
		l.tokens.giveBack(now, l.tokenNeedLocked(tokens))
	}
}

// tokenNeedLocked 把请求需要的 token 数收敛到桶容量内。
func (l *RateLimiter) tokenNeedLocked(tokens int) float64 {
	if l.tokens == nil {
		return 0
	}
	return math.Min(float64(max(tokens, 0)), l.tokens.capacity)
}

func (l *RateLimiter) limitedError(reason string) error {
	if l.opts.Name == "" {
		return fmt.Errorf("%w: %s", ErrLocalRateLimited, reason)
	}
	return fmt.Errorf("%w [%s]: %s", ErrLocalRateLimited, l.opts.Name, reason)
}

// ceilDiv 是向上取整的整除。用余数判定而非 (a+b-1)/b，后者在 a 接近
// int 上界时会溢出为负、把桶容量算成负数。
func ceilDiv(a, b int) int {
	if b == 0 {
		return a
	}
	quotient := a / b
	if a%b != 0 {
		quotient++
	}
	return quotient
}

func parseRateLimitRemaining(value string) (float64, bool) {
	if value == "" {
		return 0, false
	}
	remaining, err := strconv.ParseFloat(value, 64)
	if err != nil || remaining < 0 || math.IsNaN(remaining) || math.IsInf(remaining, 0) {
		return 0, false
	}
	return remaining, true
}

// ============================================================
// 令牌桶
// ============================================================

// tokenBucket 是按固定速率补充的令牌桶，允许 available 透支为负
// （预约模式：先扣减，再按透支量等待）。非并发安全，由 RateLimiter 的锁保护。
type tokenBucket struct {
	capacity  float64
	perSecond float64
	available float64
	last      time.Time
}

func newTokenBucket(capacity, perSecond float64, now time.Time) *tokenBucket {
	return &tokenBucket{
		capacity:  capacity,
		perSecond: perSecond,
		available: capacity,
		last:      now,
	}
}

func (b *tokenBucket) refill(now time.Time) {
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return
	}
	b.last = now
	b.available = math.Min(b.capacity, b.available+elapsed.Seconds()*b.perSecond)
}

// take 扣减 n 个令牌，返回需要等待的时长（余额充足时为 0）。
func (b *tokenBucket) take(now time.Time, n float64) time.Duration {
	b.refill(now)
	b.available -= n
	if b.available >= 0 {
		return 0
	}
	return time.Duration(-b.available / b.perSecond * float64(time.Second))
}

// hasAt 报告当前余额是否足够扣减 n 个令牌。
func (b *tokenBucket) hasAt(now time.Time, n float64) bool {
	b.refill(now)
	return b.available >= n
}

// giveBack 归还 n 个令牌，不超过桶容量。
func (b *tokenBucket) giveBack(now time.Time, n float64) {
	b.refill(now)
	b.available = math.Min(b.capacity, b.available+n)
}

// clampTo 把可用额度下调到 limit，不上调。
func (b *tokenBucket) clampTo(now time.Time, limit float64) {
	b.refill(now)
	b.available = math.Min(b.available, limit)
}

func (b *tokenBucket) availableAt(now time.Time) int {
	b.refill(now)
	return int(math.Floor(b.available))
}

// ============================================================
// 中间件与装饰器
// ============================================================

// EstimateChatRequestTokens 按 EstimateTokens 口径估算一次 Chat 请求
// 需要预扣的 token 数：输入估算 + 输出预留（req.MaxTokens 优先，
// 未设置时用 reserveOutput）。req 为 nil 返回 0。
//
// 与 EstimateTokens 同为启发式估算（误差 ±30%），用于限流预扣，
// 不可用于计费——计费一律以响应返回的 Usage 为准。
func EstimateChatRequestTokens(req *ChatRequest, reserveOutput int) int {
	if req == nil {
		return 0
	}
	total := EstimateTokens(req.Messages)
	if req.MaxTokens > 0 {
		return total + req.MaxTokens
	}
	return total + max(reserveOutput, 0)
}

// RateLimitMiddleware 用限流器约束 Chat 调用的 RPM 与 TPM。
// l 为 nil 时中间件为透传。
func RateLimitMiddleware(l *RateLimiter) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
			estimated := l.estimateChat(req)
			if err := l.Acquire(ctx, estimated); err != nil {
				return nil, err
			}
			resp, err := next(ctx, req)
			if err != nil || resp == nil {
				return resp, err
			}
			l.Settle(estimated, resp.Usage.TotalTokens)
			l.adapt(resp.Metadata)
			return resp, nil
		}
	}
}

// RateLimitStreamMiddleware 是 RateLimitMiddleware 的流式对应版本。
// 流式的真实 Usage 只在流尾部到达，因此结算延后到流终止
// （读到 io.EOF、读出错或提前 Close）时进行，整个流生命周期内至多一次。
func RateLimitStreamMiddleware(l *RateLimiter) StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
			estimated := l.estimateChat(req)
			if err := l.Acquire(ctx, estimated); err != nil {
				return nil, err
			}
			stream, err := next(ctx, req)
			if err != nil || stream == nil {
				return stream, err
			}
			l.adapt(stream.Metadata())
			settling := &settlingStream{inner: stream, limiter: l, estimated: estimated}
			return NewStreamReaderWithMetadata(settling.recv, settling.close, stream.Metadata()), nil
		}
	}
}

// RateLimitEmbedMiddleware 用限流器约束 Embed 调用。
// token 预扣按输入文本的启发式估算，响应返回后用真实 Usage 结算。
func RateLimitEmbedMiddleware(l *RateLimiter) EmbedMiddleware {
	return func(next EmbedHandler) EmbedHandler {
		return func(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
			estimated := l.estimateEmbed(req)
			if err := l.Acquire(ctx, estimated); err != nil {
				return nil, err
			}
			resp, err := next(ctx, req)
			if err != nil || resp == nil {
				return resp, err
			}
			l.Settle(estimated, resp.Usage.TotalTokens)
			l.adapt(resp.Metadata)
			return resp, nil
		}
	}
}

func (l *RateLimiter) estimateChat(req *ChatRequest) int {
	if l == nil || l.tokens == nil {
		return 0
	}
	return EstimateChatRequestTokens(req, l.opts.ReserveOutputTokens)
}

func (l *RateLimiter) estimateEmbed(req *EmbeddingRequest) int {
	if l == nil || l.tokens == nil || req == nil {
		return 0
	}
	total := 0
	for _, input := range req.Input {
		total += estimateTextTokens(input)
	}
	return total
}

func (l *RateLimiter) adapt(metadata ResponseMetadata) {
	if l == nil || !l.opts.AdaptFromHeaders {
		return
	}
	l.Observe(metadata)
}

// settlingStream 在流终止时用真实 Usage 结算一次预扣额度。
type settlingStream struct {
	inner     *StreamReader
	limiter   *RateLimiter
	estimated int

	mu      sync.Mutex
	usage   Usage
	settled bool
}

func (s *settlingStream) recv() (*StreamChunk, error) {
	chunk, err := s.inner.Recv()
	if chunk != nil && chunk.Usage != (Usage{}) {
		s.mu.Lock()
		s.usage = chunk.Usage
		s.mu.Unlock()
	}
	if err != nil {
		s.settle()
	}
	return chunk, err
}

func (s *settlingStream) close() error {
	err := s.inner.Close()
	// 未读到流尾就 Close 时也结算，用量以已读到的为准（可能为零值）。
	s.settle()
	return err
}

// settle 结算一次，整个流生命周期内至多执行一次。
func (s *settlingStream) settle() {
	s.mu.Lock()
	if s.settled {
		s.mu.Unlock()
		return
	}
	s.settled = true
	usage := s.usage
	s.mu.Unlock()

	// 未拿到用量（提前 Close、中途报错）时保留预扣不返还，避免低估超额。
	if usage.TotalTokens <= 0 {
		return
	}
	s.limiter.Settle(s.estimated, usage.TotalTokens)
}

// WithRateLimit 返回受限流器约束的 Provider。
// 返回的 Provider 在 p 与 l 可并发使用时可并发使用。
// p 为 nil 时返回的 Provider 在调用时报 ErrNilProvider；l 为 nil 时报 ErrNilRateLimiter。
func WithRateLimit(p Provider, l *RateLimiter) Provider {
	wrapped, err := TryWithRateLimit(p, l)
	if err != nil {
		return errorProvider{err: err}
	}
	return wrapped
}

// TryWithRateLimit 返回受限流器约束的 Provider，输入非法时返回错误而不 panic。
func TryWithRateLimit(p Provider, l *RateLimiter) (Provider, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	if l == nil {
		return nil, ErrNilRateLimiter
	}
	return WithMiddlewares(p, MiddlewareOptions{
		Chat:   []Middleware{RateLimitMiddleware(l)},
		Stream: []StreamMiddleware{RateLimitStreamMiddleware(l)},
	}), nil
}

// WithEmbedderRateLimit 返回受限流器约束的 Embedder。
// e 为 nil 时返回的 Embedder 在调用时报 ErrNilEmbedder；l 为 nil 时报 ErrNilRateLimiter。
func WithEmbedderRateLimit(e Embedder, l *RateLimiter) Embedder {
	wrapped, err := TryWithEmbedderRateLimit(e, l)
	if err != nil {
		return errorEmbedder{err: err}
	}
	return wrapped
}

// TryWithEmbedderRateLimit 返回受限流器约束的 Embedder，输入非法时返回错误而不 panic。
func TryWithEmbedderRateLimit(e Embedder, l *RateLimiter) (Embedder, error) {
	if embedderIsNil(e) {
		return nil, ErrNilEmbedder
	}
	if l == nil {
		return nil, ErrNilRateLimiter
	}
	return WithEmbedderMiddlewares(e, RateLimitEmbedMiddleware(l)), nil
}
