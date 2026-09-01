package provider

import (
	"context"
	"fmt"
	"math"
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

	// breakerBucketCount 是 ReadyToTrip 模式下滑动窗口的分桶数。
	// 分桶而非逐条记录样本，使内存占用只与桶数相关、与 QPS 无关：
	// 按时间戳逐条记录时，1000 QPS 配 1 分钟窗口需要保留 6 万个样本。
	// 32 个桶在默认 1 分钟窗口下约 1.9 秒一格，足够支撑失败率判定的精度。
	breakerBucketCount = 32
)

// BreakerCounts 是熔断器滑动窗口内的样本统计，作为 ReadyToTrip 的判定输入。
type BreakerCounts struct {
	// Successes 与 Failures 是当前滑动窗口内的成功、失败次数。
	// 失败的口径由 BreakerOptions.ShouldTrip 决定：未计入失败的错误
	// （如 ctx 取消、参数非法）算作成功。
	Successes int
	Failures  int
}

// Total 返回窗口内的样本总数。
func (c BreakerCounts) Total() int {
	return c.Successes + c.Failures
}

// FailureRate 返回窗口内的失败率，取值 [0, 1]；无样本时返回 0。
func (c BreakerCounts) FailureRate() float64 {
	total := c.Total()
	if total <= 0 {
		return 0
	}
	return float64(c.Failures) / float64(total)
}

// FailureRateTrip 构造按失败率判定的 ReadyToTrip：窗口内样本数达到 minSamples、
// 且失败率超过 maxFailureRate 时跳闸。
//
// minSamples 是必需的下限保护——样本太少时失败率没有统计意义
// （只有一次调用且失败就是 100%），样本不足时一律不跳闸。
// minSamples ≤ 0 按 1 计，maxFailureRate 收敛到 [0, 1]，
// 传入 NaN 按 0 处理（任何失败都跳闸），不会退化成永不跳闸。
//
//	// 窗口内至少 20 次调用、失败率超过 50% 才熔断
//	provider.BreakerOptions{ReadyToTrip: provider.FailureRateTrip(20, 0.5)}
func FailureRateTrip(minSamples int, maxFailureRate float64) func(BreakerCounts) bool {
	minSamples = max(minSamples, 1)
	if math.IsNaN(maxFailureRate) {
		maxFailureRate = 0
	}
	maxFailureRate = min(max(maxFailureRate, 0), 1)
	return func(counts BreakerCounts) bool {
		if counts.Total() < minSamples {
			return false
		}
		return counts.FailureRate() > maxFailureRate
	}
}

// breakerBucket 是一个时间格内的样本计数。
// epoch 是该格的时间序号，用于判断环形数组的槽位是否属于当前窗口。
type breakerBucket struct {
	epoch     int64
	successes int
	failures  int
}

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

	// ReadyToTrip 按滑动窗口内的成功/失败统计判定是否跳闸，非 nil 时**替代**
	// FailureThreshold（后者不再参与判定）。用 FailureRateTrip 构造常见的
	// 失败率判定，也可自行实现更复杂的条件。
	//
	// 为什么需要它：FailureThreshold 是绝对次数，与流量规模无关——1 分钟窗口配
	// 5 次失败，在 1000 QPS 下只要上游有 0.1% 的偶发错误率就会持续跳闸，
	// 把 99.9% 本可成功的请求一起挡在本地。失败率判定与 QPS 解耦，
	// 高流量服务应优先使用。
	//
	// 与 ShouldTrip 的加锁语义不同，注意区分：ShouldTrip 在锁外调用，
	// 回调内可以读熔断器状态；**ReadyToTrip 在锁内调用，回调内不得访问
	// 该熔断器（调用 State / Stats / Report 会死锁）**。判定所需的信息
	// 已全部通过 counts 传入，回调应当是不访问外部状态的纯函数。
	//
	// 启用后熔断器会同时记录成功与失败样本（分桶计数，内存与 QPS 无关）；
	// 为 nil 时只记录失败时刻，与不使用该选项时的行为完全一致。
	ReadyToTrip func(counts BreakerCounts) bool
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
	// Successes 是当前滑动窗口内的成功次数，仅在配置了
	// BreakerOptions.ReadyToTrip 时统计；未配置时恒为 0
	// （此时熔断器只记录失败，不为统计成功付出开销）。
	Successes int
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

	// buckets 是 ReadyToTrip 模式下的环形分桶计数，nil 表示未启用该模式；
	// bucketWidth 是单桶时长，恒为正。二者构造后长度/取值不变，
	// 桶内计数的读写受 mu 保护。
	buckets     []breakerBucket
	bucketWidth time.Duration

	mu    sync.Mutex
	state BreakerState
	// failures 是窗口内的失败时刻，长度上界为 opts.FailureThreshold。
	// 仅在未配置 ReadyToTrip 时使用。
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
	normalized := normalizeBreakerOptions(opts)
	b := &Breaker{
		opts:  normalized,
		now:   now,
		state: BreakerClosed,
	}
	if normalized.ReadyToTrip != nil {
		b.buckets = make([]breakerBucket, breakerBucketCount)
		// Window 已被 normalize 保证为正；极小窗口下整除可能得 0，
		// 下限取 1ns 以杜绝后续的除零。
		b.bucketWidth = max(normalized.Window/breakerBucketCount, time.Nanosecond)
	}
	return b
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
	if b.buckets != nil {
		// 成功与失败都要记：失败率判定需要分母。
		b.recordSampleLocked(now, failed)
		// ReadyToTrip 在锁内调用，回调内不得访问该熔断器（见选项文档）。
		if b.opts.ReadyToTrip(b.bucketCountsLocked(now)) {
			b.tripLocked(now)
		}
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
	counts := b.countsLocked(now)
	stats := BreakerStats{
		Name:      b.opts.Name,
		State:     b.effectiveStateLocked(now),
		Failures:  counts.Failures,
		Successes: counts.Successes,
		Trips:     b.trips,
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

// countsLocked 返回当前窗口内的样本统计。
// 未启用 ReadyToTrip 时只有失败被记录，成功数恒为 0。
func (b *Breaker) countsLocked(now time.Time) BreakerCounts {
	if b.buckets != nil {
		return b.bucketCountsLocked(now)
	}
	return BreakerCounts{Failures: b.countRecentFailuresLocked(now)}
}

// bucketEpoch 返回 now 所属的时间格序号。bucketWidth 恒为正，不会除零。
func (b *Breaker) bucketEpoch(now time.Time) int64 {
	return now.UnixNano() / int64(b.bucketWidth)
}

// bucketSlot 把时间格序号映射到环形数组下标。
// epoch 可能为负（1970 之前的时刻，如零值 time.Time），
// Go 的取模会保留负号，这里补正回非负区间，避免索引越界。
func (b *Breaker) bucketSlot(epoch int64) int {
	slot := epoch % int64(len(b.buckets))
	if slot < 0 {
		slot += int64(len(b.buckets))
	}
	return int(slot)
}

// recordSampleLocked 把一次调用结果记入当前时间格。
// 槽位被环形复用时先清零，避免把一个窗口之前的旧计数算作当前窗口。
func (b *Breaker) recordSampleLocked(now time.Time, failed bool) {
	epoch := b.bucketEpoch(now)
	slot := b.bucketSlot(epoch)
	if b.buckets[slot].epoch != epoch {
		b.buckets[slot] = breakerBucket{epoch: epoch}
	}
	if failed {
		b.buckets[slot].failures++
		return
	}
	b.buckets[slot].successes++
}

// bucketCountsLocked 汇总落在当前窗口内的分桶计数。
// 同时排除过期的槽位与"未来"的槽位——系统时钟回拨后，
// 环形数组里可能残留序号大于当前时间格的桶，计入会让判定失真。
func (b *Breaker) bucketCountsLocked(now time.Time) BreakerCounts {
	current := b.bucketEpoch(now)
	oldest := current - int64(len(b.buckets)) + 1

	var counts BreakerCounts
	for _, bucket := range b.buckets {
		if bucket.epoch < oldest || bucket.epoch > current {
			continue
		}
		counts.Successes += bucket.successes
		counts.Failures += bucket.failures
	}
	return counts
}

// resetBucketsLocked 清空全部分桶，用于跳闸与闭合后让窗口重新起算，
// 与失败时刻切片被清空的语义保持一致。
func (b *Breaker) resetBucketsLocked() {
	for i := range b.buckets {
		b.buckets[i] = breakerBucket{}
	}
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
	b.resetBucketsLocked()
	b.halfOpenUsed = 0
}

func (b *Breaker) closeLocked() {
	b.state = BreakerClosed
	b.failures = b.failures[:0]
	b.resetBucketsLocked()
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
