package provider

import (
	"context"
	"fmt"
)

// ============================================================
// 配额拦截（S-07）
// ============================================================

// QuotaChecker 在请求发出前判断调用方是否仍有可用额度。
// 返回 nil 放行；返回非 nil error（惯例为 ErrQuotaExceeded 或其包装）时请求被拦截，
// 不会发往平台。基于累计真实用量判断，语义是"已超额则拦下一次调用"，
// 存在最后一次调用的滞后。存储不可用时 fail-open（返回 nil）还是
// fail-close（返回 error）由实现方决定。实现必须并发安全。
type QuotaChecker interface {
	// Allow 判断 userID 是否可以调用 model。
	// model 为请求指定的模型名，可能为空串（使用 provider 默认模型）；
	// 不需要按模型限额的实现可忽略该参数。
	Allow(ctx context.Context, userID, model string) error
}

// QuotaMiddleware 在 Chat 请求前执行配额预检。
// ctx 未携带 UserID 的请求默认放行——要求全部请求受控时，
// 在服务入口保证 WithUserID 必被调用。qc 为 nil 时中间件为透传。
func QuotaMiddleware(qc QuotaChecker) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
			if err := quotaPrecheck(ctx, qc, req); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}

// QuotaStreamMiddleware 是 QuotaMiddleware 的流式对应版本。
func QuotaStreamMiddleware(qc QuotaChecker) StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
			if err := quotaPrecheck(ctx, qc, req); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}

func quotaPrecheck(ctx context.Context, qc QuotaChecker, req *ChatRequest) error {
	if qc == nil || req == nil {
		return nil
	}
	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return nil
	}
	if err := qc.Allow(ctx, userID, req.Model); err != nil {
		return fmt.Errorf("quota check: %w", err)
	}
	return nil
}

// ============================================================
// 剩余 token 预算硬限
// ============================================================

type tokenBudgetCtxKey struct{}

// WithTokenBudget 将调用方剩余可用 token 数写入 ctx。
// 业务从自己的账务系统查出用户剩余额度后注入，配合 TokenBudgetMiddleware
// 保证单次调用的消耗不超出该值。
func WithTokenBudget(ctx context.Context, remainingTokens int) context.Context {
	return context.WithValue(ctx, tokenBudgetCtxKey{}, remainingTokens)
}

// TokenBudgetFromContext 读取 WithTokenBudget 写入的剩余额度。
// ctx 中不存在时返回 (0, false)。
func TokenBudgetFromContext(ctx context.Context) (int, bool) {
	budget, ok := ctx.Value(tokenBudgetCtxKey{}).(int)
	return budget, ok
}

// tokenBudgetUnlimitedThreshold 之上的输出余量视为"预算充足"，
// 不再强设 MaxTokens——没有模型的单次输出超过 1M token，
// 强设一个巨大值反而可能被平台判为非法参数。
const tokenBudgetUnlimitedThreshold = 1 << 20

// TokenBudgetMiddleware 依据 ctx 中的剩余 token 预算对单次调用做硬限：
//
//  1. 预算 ≤ 0，或输入的估算 token（EstimateTokens 口径）已达预算 → 返回
//     ErrQuotaExceeded，请求不发往平台；
//  2. 否则将 MaxTokens 收缩到 预算-输入估算 之内，从输出侧硬性保证本次
//     消耗不超出预算（原 MaxTokens 更小时保持不变）。
//
// 输入侧是启发式估算（误差 ±30%），极端情况下实际输入可能略超预算——
// 需要零误差的额度控制时，结合响应 Usage 做事后结算与追偿。
// ctx 未携带预算的请求不受影响。
func TokenBudgetMiddleware() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
			clamped, err := applyTokenBudget(ctx, req)
			if err != nil {
				return nil, err
			}
			return next(ctx, clamped)
		}
	}
}

// TokenBudgetStreamMiddleware 是 TokenBudgetMiddleware 的流式对应版本。
func TokenBudgetStreamMiddleware() StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
			clamped, err := applyTokenBudget(ctx, req)
			if err != nil {
				return nil, err
			}
			return next(ctx, clamped)
		}
	}
}

func applyTokenBudget(ctx context.Context, req *ChatRequest) (*ChatRequest, error) {
	if req == nil {
		return nil, ErrNilChatRequest
	}
	budget, ok := TokenBudgetFromContext(ctx)
	if !ok {
		return req, nil
	}
	if budget <= 0 {
		return nil, fmt.Errorf("%w: token budget %d", ErrQuotaExceeded, budget)
	}

	estimatedInput := EstimateTokens(req.Messages)
	allowedOutput := budget - estimatedInput
	if allowedOutput <= 0 {
		return nil, fmt.Errorf("%w: estimated input %d tokens reaches budget %d",
			ErrQuotaExceeded, estimatedInput, budget)
	}

	if allowedOutput >= tokenBudgetUnlimitedThreshold {
		return req, nil
	}
	if req.MaxTokens > 0 && req.MaxTokens <= allowedOutput {
		return req, nil
	}
	// 浅拷贝后收缩输出上限，不修改调用方持有的请求。
	clamped := *req
	clamped.MaxTokens = allowedOutput
	return &clamped, nil
}
