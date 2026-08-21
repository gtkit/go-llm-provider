package provider

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// ============================================================
// 熔断器（circuit breaker）
// ============================================================

// BreakerState 描述熔断器的当前状态。
type BreakerState string

const (
	// BreakerClosed 表示熔断器闭合，请求正常放行。
	BreakerClosed BreakerState = "closed"
	// BreakerOpen 表示熔断器打开，请求被拦下、不发往平台。
	BreakerOpen BreakerState = "open"
	// BreakerHalfOpen 表示冷却期已过，正在放行少量探测请求试探恢复。
	BreakerHalfOpen BreakerState = "half_open"
)

// 熔断器默认参数。
const (
	defaultBreakerFailureThreshold = 5
	defaultBreakerWindow           = time.Minute
	defaultBreakerOpenDuration     = 30 * time.Second
	defaultBreakerMaxOpenDuration  = 5 * time.Minute
	defaultBreakerBackoffReset     = 10 * time.Minute
	defaultBreakerHalfOpenProbes   = 1

	// maxBreakerFailureThreshold 是失败阈值的硬上限。熔断器为窗口内每一次失败
	// 保留一个时间戳，阈值即这份切片的长度上界；设置超过该上限的阈值会被收敛到
	// 上限值，避免误配置的巨大阈值吃掉内存。
	maxBreakerFailureThreshold = 4096
)

// BreakerOptions 配置熔断器的跳闸与恢复行为。
// 零值可直接使用，全部字段走默认值。
type BreakerOptions struct {
	// Name 是熔断器标识，仅用于错误消息与 Stats 展示，便于定位是哪个上游被熔断。
	// 留空时错误消息不含标识。
	Name string

	// FailureThreshold 是 Window 内触发跳闸所需的失败次数，默认 5。
	// ≤ 0 取默认值，超过 4096 收敛为 4096。
	FailureThreshold int

	// Window 是失败计数的滑动窗口长度，默认 1 分钟。
	// 语义是"最近 Window 内累计 FailureThreshold 次失败即跳闸"，
	// 成功调用不清空窗口内的失败计数，失败记录只随时间滑出窗口。
	Window time.Duration

	// OpenDuration 是首次跳闸后的冷却时长，默认 30 秒。
	OpenDuration time.Duration

	// MaxOpenDuration 是冷却时长的上限，默认 5 分钟。
	// 连续跳闸时冷却时长按 OpenDuration 逐次翻倍，不超过该上限。
	MaxOpenDuration time.Duration

	// BackoffReset 是连续跳闸的判定间隔，默认 10 分钟。
	// 距上次跳闸超过该时长再次跳闸时，冷却时长退回 OpenDuration 重新起算；
	// 否则视为连续故障，冷却时长继续翻倍。
	BackoffReset time.Duration

	// HalfOpenProbes 是半开态允许同时在途的探测请求数，默认 1。
	// 探测成功则闭合，失败则重新打开并延长冷却。
	HalfOpenProbes int

	// ShouldTrip 判定某个错误是否计入失败。nil 时默认 IsRetryableError
	// （限流/超时/5xx/网络才计入），鉴权失败、参数非法等调用方自身的问题不计入——
	// 换一个上游也修不了。需要把 key 失效也纳入熔断时显式传入：
	//
	//	provider.BreakerOptions{ShouldTrip: func(err error) bool {
	//	    return provider.IsRetryableError(err) || errors.Is(err, provider.ErrAuth)
	//	}}
	ShouldTrip func(error) bool
}

// BreakerStats 是熔断器某一时刻的状态快照。
type BreakerStats struct {
	// Name 是 BreakerOptions.Name。
	Name string
	// State 是当前状态。冷却期已过但尚无探测发出时报告 BreakerHalfOpen，
	// 与下一次 Allow 的实际行为保持一致。
	State BreakerState
	// Failures 是当前滑动窗口内的失败次数。
	Failures int
	// Trips 是连续跳闸次数（BackoffReset 内的累计），决定当前冷却时长。
	Trips int
	// OpenUntil 是冷却结束时刻；未处于冷却中时为零值。
	OpenUntil time.Time
}

// Breaker 是按"滑动窗口失败计数 + 指数退避冷却 + 半开探测"工作的熔断器。
//
// 状态机：
//
//	closed    ──窗口内失败达阈值──▶ open
//	open      ──冷却到期───────────▶ half_open
//	half_open ──探测成功───────────▶ closed
//	half_open ──探测失败───────────▶ open（冷却时长翻倍）
//
// 状态保存在进程内存中，不跨进程共享；多副本部署时每个副本独立熔断。
// 所有方法对 nil 接收者安全（等价于"不熔断"），可并发调用。
type Breaker struct {
	opts BreakerOptions
	now  func() time.Time

	mu    sync.Mutex
	state BreakerState
	// failures 是窗口内的失败时刻，长度上界为 opts.FailureThreshold。
	failures     []time.Time
	openUntil    time.Time
	trips        int
	lastTripAt   time.Time
	halfOpenUsed int
}

// NewBreaker 按 opts 创建熔断器。非法或缺省的选项取默认值，不返回错误。
// 返回的 *Breaker 可并发使用。
func NewBreaker(opts BreakerOptions) *Breaker {
	return newBreakerWithClock(opts, time.Now)
}

func newBreakerWithClock(opts BreakerOptions, now func() time.Time) *Breaker {
	if now == nil {
		now = time.Now
	}
	return &Breaker{
		opts:  normalizeBreakerOptions(opts),
		now:   now,
		state: BreakerClosed,
	}
}

func normalizeBreakerOptions(opts BreakerOptions) BreakerOptions {
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = defaultBreakerFailureThreshold
	}
	opts.FailureThreshold = min(opts.FailureThreshold, maxBreakerFailureThreshold)
	if opts.Window <= 0 {
		opts.Window = defaultBreakerWindow
	}
	if opts.OpenDuration <= 0 {
		opts.OpenDuration = defaultBreakerOpenDuration
	}
	if opts.MaxOpenDuration <= 0 {
		opts.MaxOpenDuration = defaultBreakerMaxOpenDuration
	}
	if opts.MaxOpenDuration < opts.OpenDuration {
		opts.MaxOpenDuration = opts.OpenDuration
	}
	if opts.BackoffReset <= 0 {
		opts.BackoffReset = defaultBreakerBackoffReset
	}
	if opts.HalfOpenProbes <= 0 {
		opts.HalfOpenProbes = defaultBreakerHalfOpenProbes
	}
	if opts.ShouldTrip == nil {
		opts.ShouldTrip = IsRetryableError
	}
	return opts
}

// Allow 申请一次调用许可。
//
// 返回 nil 表示放行，调用方必须在该次调用结束后配对调用一次 Report——
// 半开态的探测名额靠 Report 归还，漏调会让熔断器卡在半开态直到下次跳闸。
// 返回非 nil 时请求应被放弃，错误可用 errors.Is(err, ErrBreakerOpen) 判定，
// 此时不要调用 Report。
func (b *Breaker) Allow() error {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	if b.state == BreakerOpen {
		if now.Before(b.openUntil) {
			return b.openErrorLocked(now)
		}
		b.state = BreakerHalfOpen
		b.halfOpenUsed = 0
	}
	if b.state == BreakerHalfOpen {
		if b.halfOpenUsed >= b.opts.HalfOpenProbes {
			return fmt.Errorf("%w%s: half-open probe limit reached", ErrBreakerOpen, b.nameSuffix())
		}
		b.halfOpenUsed++
	}
	return nil
}

// Report 上报一次已放行调用的结果，err 为 nil 表示成功。
// 只应在 Allow 返回 nil 后配对调用一次。
//
// 是否计入失败由 BreakerOptions.ShouldTrip 判定：ctx 取消、参数非法等
// 默认不计入。ShouldTrip 在熔断器的锁之外调用，回调内读取熔断器状态是安全的。
// 半开态的探测成功即闭合熔断器并清空失败窗口。
func (b *Breaker) Report(err error) {
	if b == nil {
		return
	}

	// ShouldTrip 是调用方注入的回调，在锁外调用——回调里读熔断器状态
	// （如 State / Stats）不会死锁。opts 构造后只读，锁外访问安全。
	failed := err != nil && b.opts.ShouldTrip(err)

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()

	if b.state == BreakerHalfOpen {
		if b.halfOpenUsed > 0 {
			b.halfOpenUsed--
		}
		if failed {
			b.tripLocked(now)
			return
		}
		b.closeLocked()
		return
	}

	// 冷却期内的上报（调用方未经 Allow 直接上报）不改变冷却进度。
	if b.state == BreakerOpen {
		return
	}
	if failed {
		b.recordFailureLocked(now)
	}
}

// State 返回当前状态。冷却期已过但尚未发出探测时返回 BreakerHalfOpen。
func (b *Breaker) State() BreakerState {
	if b == nil {
		return BreakerClosed
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	return b.effectiveStateLocked(b.now())
}

// Stats 返回状态快照，供健康检查与监控读取。
func (b *Breaker) Stats() BreakerStats {
	if b == nil {
		return BreakerStats{State: BreakerClosed}
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	stats := BreakerStats{
		Name:     b.opts.Name,
		State:    b.effectiveStateLocked(now),
		Failures: b.countRecentFailuresLocked(now),
		Trips:    b.trips,
	}
	if b.state == BreakerOpen && now.Before(b.openUntil) {
		stats.OpenUntil = b.openUntil
	}
	return stats
}

// Reset 把熔断器恢复到初始的闭合状态，清空失败窗口与退避计数。
// 供上游确认已恢复（如换了新 key）后手动放行使用。
func (b *Breaker) Reset() {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.closeLocked()
	b.trips = 0
	b.lastTripAt = time.Time{}
	b.openUntil = time.Time{}
}

func (b *Breaker) effectiveStateLocked(now time.Time) BreakerState {
	if b.state == BreakerOpen && !now.Before(b.openUntil) {
		return BreakerHalfOpen
	}
	return b.state
}

func (b *Breaker) openErrorLocked(now time.Time) error {
	return fmt.Errorf("%w%s: retry after %s", ErrBreakerOpen, b.nameSuffix(),
		b.openUntil.Sub(now).Round(time.Millisecond))
}

func (b *Breaker) nameSuffix() string {
	if b.opts.Name == "" {
		return ""
	}
	return " [" + b.opts.Name + "]"
}

func (b *Breaker) recordFailureLocked(now time.Time) {
	cutoff := now.Add(-b.opts.Window)
	idx := 0
	for idx < len(b.failures) && !b.failures[idx].After(cutoff) {
		idx++
	}
	if idx > 0 {
		b.failures = slices.Delete(b.failures, 0, idx)
	}
	b.failures = append(b.failures, now)

	if len(b.failures) >= b.opts.FailureThreshold {
		b.tripLocked(now)
	}
}

func (b *Breaker) countRecentFailuresLocked(now time.Time) int {
	cutoff := now.Add(-b.opts.Window)
	count := 0
	for _, at := range b.failures {
		if at.After(cutoff) {
			count++
		}
	}
	return count
}

func (b *Breaker) tripLocked(now time.Time) {
	if !b.lastTripAt.IsZero() && now.Sub(b.lastTripAt) <= b.opts.BackoffReset {
		b.trips++
	} else {
		b.trips = 1
	}
	b.lastTripAt = now
	b.state = BreakerOpen
	b.openUntil = now.Add(ExponentialBackoff(b.opts.OpenDuration, b.opts.MaxOpenDuration)(b.trips))
	b.failures = b.failures[:0]
	b.halfOpenUsed = 0
}

func (b *Breaker) closeLocked() {
	b.state = BreakerClosed
	b.failures = b.failures[:0]
	b.halfOpenUsed = 0
}

// ============================================================
// 中间件与装饰器
// ============================================================

// BreakerMiddleware 用熔断器保护 Chat 调用：冷却期内的请求直接返回
// ErrBreakerOpen，不发往平台。b 为 nil 时中间件为透传。
func BreakerMiddleware(b *Breaker) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
			if err := b.Allow(); err != nil {
				return nil, err
			}
			resp, err := next(ctx, req)
			b.Report(err)
			return resp, err
		}
	}
}

// BreakerStreamMiddleware 是 BreakerMiddleware 的流式对应版本。
//
// 判定口径是"流是否创建成功"：创建成功即上报成功，创建之后读流过程中
// 出现的错误不计入熔断——流已建立说明上游可达，中途断流由调用方按业务
// 需要处理（可在 Recv 出错时自行调用 Report）。
func BreakerStreamMiddleware(b *Breaker) StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
			if err := b.Allow(); err != nil {
				return nil, err
			}
			stream, err := next(ctx, req)
			b.Report(err)
			return stream, err
		}
	}
}

// BreakerEmbedMiddleware 是 BreakerMiddleware 的 Embed 对应版本。
func BreakerEmbedMiddleware(b *Breaker) EmbedMiddleware {
	return func(next EmbedHandler) EmbedHandler {
		return func(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
			if err := b.Allow(); err != nil {
				return nil, err
			}
			resp, err := next(ctx, req)
			b.Report(err)
			return resp, err
		}
	}
}

// WithBreaker 返回被熔断器保护的 Provider。
// 返回的 Provider 在 p 与 b 可并发使用时可并发使用。
// p 为 nil 时返回的 Provider 在调用时报 ErrNilProvider；b 为 nil 时报 ErrNilBreaker。
func WithBreaker(p Provider, b *Breaker) Provider {
	wrapped, err := TryWithBreaker(p, b)
	if err != nil {
		return errorProvider{err: err}
	}
	return wrapped
}

// TryWithBreaker 返回被熔断器保护的 Provider，输入非法时返回错误而不 panic。
func TryWithBreaker(p Provider, b *Breaker) (Provider, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	if b == nil {
		return nil, ErrNilBreaker
	}
	return WithMiddlewares(p, MiddlewareOptions{
		Chat:   []Middleware{BreakerMiddleware(b)},
		Stream: []StreamMiddleware{BreakerStreamMiddleware(b)},
	}), nil
}

// WithEmbedderBreaker 返回被熔断器保护的 Embedder。
// e 为 nil 时返回的 Embedder 在调用时报 ErrNilEmbedder；b 为 nil 时报 ErrNilBreaker。
func WithEmbedderBreaker(e Embedder, b *Breaker) Embedder {
	wrapped, err := TryWithEmbedderBreaker(e, b)
	if err != nil {
		return errorEmbedder{err: err}
	}
	return wrapped
}

// TryWithEmbedderBreaker 返回被熔断器保护的 Embedder，输入非法时返回错误而不 panic。
func TryWithEmbedderBreaker(e Embedder, b *Breaker) (Embedder, error) {
	if embedderIsNil(e) {
		return nil, ErrNilEmbedder
	}
	if b == nil {
		return nil, ErrNilBreaker
	}
	return WithEmbedderMiddlewares(e, BreakerEmbedMiddleware(b)), nil
}
