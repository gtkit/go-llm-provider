package provider

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
)

// ============================================================
// 加权负载均衡
// ============================================================

// BalanceStrategy 描述均衡器的成员选择策略。
type BalanceStrategy string

// maxBalanceWeight 是成员权重上限。平滑加权轮询按候选权重之和推进、
// 加权最少在途按权重交叉相乘比较，上限保证这些累加与乘法不会溢出。
const maxBalanceWeight = 1_000_000

const (
	// BalanceWeightedRoundRobin 是平滑加权轮询：按权重比例把请求均匀铺开，
	// 同权重成员严格交替，不会出现连续命中同一成员的尖峰。默认策略。
	BalanceWeightedRoundRobin BalanceStrategy = "weighted_round_robin"

	// BalanceWeightedRandom 是加权随机：按权重比例随机选择，长期分布与权重一致，
	// 短期可能连续命中同一成员。
	BalanceWeightedRandom BalanceStrategy = "weighted_random"

	// BalanceLeastPending 是加权最少在途：选择"在途请求数 / 权重"最小的成员，
	// 适合各成员响应时延差异大的场景（慢的成员自然少分到流量）。
	BalanceLeastPending BalanceStrategy = "least_pending"
)

// BalanceMember 是均衡器的一个成员，通常对应一个 key、一个地域或一个平台。
type BalanceMember struct {
	// Provider 必填，成员实际承载调用的 Provider。
	Provider Provider

	// Weight 是相对权重，≤ 0 按 1 计，超过 1000000 按 1000000 计。
	// 权重 3 与权重 1 的成员长期流量比为 3:1。
	Weight int

	// Breaker 可选，为该成员单独计数熔断。设置后由均衡器负责申请与上报，
	// 不要再用 WithBreaker 包装 Provider——那会造成双重计数。
	Breaker *Breaker
}

// BalanceOptions 配置均衡器行为。零值可用：默认平滑加权轮询、
// 默认切换判定、最多尝试全部成员。
type BalanceOptions struct {
	// Strategy 是成员选择策略，留空取 BalanceWeightedRoundRobin。
	Strategy BalanceStrategy

	// ShouldFallback 判定某个错误是否应换下一个成员重试。
	// nil 时默认切换平台侧可重试错误、熔断打开与本地限流拦下。
	ShouldFallback func(error) bool

	// MaxAttempts 是单次调用最多尝试的成员数，≤ 0 表示最多尝试全部成员。
	// 设为 1 表示只打一个成员、不做故障转移。
	MaxAttempts int
}

// BalanceMemberStats 是单个成员的状态快照。
type BalanceMemberStats struct {
	Provider ProviderName
	Weight   int
	// Pending 是该成员当前在途的调用数。流式调用只统计到流创建完成为止。
	Pending int
	// Breaker 是该成员熔断器的快照；未配置熔断器时为零值（State 为 closed）。
	Breaker BreakerStats
}

// BalancedProvider 把请求按权重分摊到多个成员上，并在成员失败时转移到下一个成员。
//
// 与 FallbackProvider 的区别：降级链是固定顺序、链首承担全部流量，
// 只有链首失败才会用到后续成员；均衡器按权重分摊流量，适合多 key 分摊配额、
// 多地域就近、按成本比例混流等场景。故障转移语义与降级链一致。
//
// 在成员与各自的熔断器可并发使用时，BalancedProvider 可并发使用。
type BalancedProvider struct {
	members        []*balanceMember
	strategy       BalanceStrategy
	shouldFallback func(error) bool
	maxAttempts    int

	// mu 保护平滑加权轮询的动态权重。
	mu sync.Mutex
}

type balanceMember struct {
	provider Provider
	weight   int
	breaker  *Breaker
	pending  atomic.Int64
	current  int
}

// NewBalancedProvider 用默认选项构造加权均衡器。
func NewBalancedProvider(members ...BalanceMember) (*BalancedProvider, error) {
	return NewBalancedProviderWithOptions(members, BalanceOptions{})
}

// NewBalancedProviderWithOptions 按 opts 构造加权均衡器。
// members 为空或含 nil Provider 时返回 ErrNilProvider；
// Strategy 取值非法时返回 ErrInvalidBalanceStrategy。
func NewBalancedProviderWithOptions(members []BalanceMember, opts BalanceOptions) (*BalancedProvider, error) {
	if len(members) == 0 {
		return nil, ErrNilProvider
	}
	strategy := opts.Strategy
	if strategy == "" {
		strategy = BalanceWeightedRoundRobin
	}
	switch strategy {
	case BalanceWeightedRoundRobin, BalanceWeightedRandom, BalanceLeastPending:
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidBalanceStrategy, string(strategy))
	}

	out := make([]*balanceMember, 0, len(members))
	for _, m := range members {
		if providerIsNil(m.Provider) {
			return nil, ErrNilProvider
		}
		out = append(out, &balanceMember{
			provider: m.Provider,
			weight:   min(max(m.Weight, 1), maxBalanceWeight),
			breaker:  m.Breaker,
		})
	}

	shouldFallback := opts.ShouldFallback
	if shouldFallback == nil {
		shouldFallback = defaultShouldFallback
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 || maxAttempts > len(out) {
		maxAttempts = len(out)
	}

	return &BalancedProvider{
		members:        out,
		strategy:       strategy,
		shouldFallback: shouldFallback,
		maxAttempts:    maxAttempts,
	}, nil
}

// Name 返回首个成员的供应商标识。
// 均衡器本身不代表单一平台，该值仅用于日志与观测的兜底展示；
// 实际服务的平台以响应侧的 ResponseMetadata.Provider 为准。
func (b *BalancedProvider) Name() ProviderName {
	if b == nil || len(b.members) == 0 {
		return ""
	}
	return b.members[0].provider.Name()
}

// Chat 按策略选择成员发起非流式对话，失败时按 ShouldFallback 转移到下一个成员。
func (b *BalancedProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if b == nil || len(b.members) == 0 {
		return nil, ErrNilProvider
	}
	return balanceCall(ctx, b, func(ctx context.Context, p Provider) (*ChatResponse, error) {
		return p.Chat(ctx, req)
	})
}

// ChatStream 按策略选择成员创建流，创建失败时按 ShouldFallback 转移到下一个成员。
// 判定口径是"流是否创建成功"，与 FallbackProvider 一致。
func (b *BalancedProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if b == nil || len(b.members) == 0 {
		return nil, ErrNilProvider
	}
	return balanceCall(ctx, b, func(ctx context.Context, p Provider) (*StreamReader, error) {
		return p.ChatStream(ctx, req)
	})
}

// Stats 返回各成员的状态快照，顺序与构造时的 members 一致。
func (b *BalancedProvider) Stats() []BalanceMemberStats {
	if b == nil {
		return nil
	}
	stats := make([]BalanceMemberStats, 0, len(b.members))
	for _, m := range b.members {
		stats = append(stats, BalanceMemberStats{
			Provider: m.provider.Name(),
			Weight:   m.weight,
			Pending:  int(m.pending.Load()),
			Breaker:  m.breaker.Stats(),
		})
	}
	return stats
}

// balanceCall 是 Chat 与 ChatStream 共用的选择—调用—转移流程。
func balanceCall[T any](ctx context.Context, b *BalancedProvider, call func(context.Context, Provider) (T, error)) (T, error) {
	var zero T

	tried := make([]bool, len(b.members))
	var errs []error
	for range b.maxAttempts {
		m, ok := b.pick(tried)
		if !ok {
			break
		}

		result, err := invokeBalanceMember(ctx, m, call)
		if err == nil {
			return result, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", m.provider.Name(), err))
		// 调用方已取消/超时：继续尝试没有意义，立即返回。
		if ctx.Err() != nil {
			return zero, errors.Join(errs...)
		}
		if !b.shouldFallback(err) {
			return zero, errors.Join(errs...)
		}
	}

	if len(errs) == 0 {
		return zero, ErrNilProvider
	}
	return zero, errors.Join(errs...)
}

// invokeBalanceMember 在单个成员上执行一次调用，负责熔断申请与上报、在途计数。
func invokeBalanceMember[T any](ctx context.Context, m *balanceMember, call func(context.Context, Provider) (T, error)) (T, error) {
	var zero T

	if err := m.breaker.Allow(); err != nil {
		return zero, err
	}

	m.pending.Add(1)
	defer m.pending.Add(-1)

	result, err := call(ctx, m.provider)
	m.breaker.Report(err)
	if err != nil {
		return zero, err
	}
	return result, nil
}

func (b *BalancedProvider) pick(tried []bool) (*balanceMember, bool) {
	switch b.strategy {
	case BalanceWeightedRandom:
		return b.pickWeightedRandom(tried)
	case BalanceLeastPending:
		return b.pickLeastPending(tried)
	default:
		return b.pickSmoothWeighted(tried)
	}
}

// pickSmoothWeighted 是 Nginx 的平滑加权轮询：每轮给候选成员各加一份权重，
// 选当前值最大的，再从选中者身上扣掉候选权重总和。
func (b *BalancedProvider) pickSmoothWeighted(tried []bool) (*balanceMember, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	total := 0
	var best *balanceMember
	bestIdx := -1
	for i, m := range b.members {
		if tried[i] {
			continue
		}
		total += m.weight
		m.current += m.weight
		if best == nil || m.current > best.current {
			best = m
			bestIdx = i
		}
	}
	if best == nil {
		return nil, false
	}
	best.current -= total
	tried[bestIdx] = true
	return best, true
}

func (b *BalancedProvider) pickWeightedRandom(tried []bool) (*balanceMember, bool) {
	total := 0
	for i, m := range b.members {
		if tried[i] {
			continue
		}
		total += m.weight
	}
	if total <= 0 {
		return nil, false
	}

	target := rand.IntN(total)
	for i, m := range b.members {
		if tried[i] {
			continue
		}
		target -= m.weight
		if target < 0 {
			tried[i] = true
			return m, true
		}
	}
	return nil, false
}

// pickLeastPending 选择在途负载相对权重最低的成员：
// 比较 pending_i / weight_i，用交叉相乘避免浮点除法。
func (b *BalancedProvider) pickLeastPending(tried []bool) (*balanceMember, bool) {
	var best *balanceMember
	bestIdx := -1
	var bestPending int64
	for i, m := range b.members {
		if tried[i] {
			continue
		}
		pending := m.pending.Load()
		if best == nil || pending*int64(best.weight) < bestPending*int64(m.weight) {
			best = m
			bestIdx = i
			bestPending = pending
		}
	}
	if best == nil {
		return nil, false
	}
	tried[bestIdx] = true
	return best, true
}
