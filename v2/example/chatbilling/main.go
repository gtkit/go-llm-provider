// 演示"AI 对话产品按用户计费"的端到端组装，覆盖 v2.6 新增能力主线：
//
//  1. 计费切面：NewBillingHook + MemoryUsageStore，业务调用点零统计代码
//  2. 配额与预算：QuotaMiddleware（累计用量拦截）+ CostBudgetMiddleware（余额硬限）
//  3. 流式对话：CollectStreamResult 拿完整文本与 token 统计
//  4. 流式工具循环：RunToolLoopStream（智能体打字机 + 自动执行工具）
//  5. 上下文压缩：CompactMessages 摘要旧历史，CompactResult 供业务缓存
//  6. 账单查询与算钱：UserTotals / ConversationTotals / PricingTable.Cost
//
// 运行前：DEEPSEEK_API_KEY=sk-xxx go run .
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/gtkit/go-llm-provider/v2/provider"
)

const (
	demoUserID         = "user-1001"
	demoConversationID = "conv-20260710-01"
	demoModel          = "deepseek-chat"
)

// pricing 是演示费率（微元 / 1M tokens）；生产从配置中心注入，价格随官方调整维护。
var pricing = provider.PricingTable{
	demoModel: {
		InputPer1M:     2_000_000, // 2 元 / 1M 输入
		OutputPer1M:    8_000_000, // 8 元 / 1M 输出
		CacheReadPer1M: 200_000,   // 0.2 元 / 1M 缓存命中
		Currency:       "CNY",
	},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		return errors.New("set DEEPSEEK_API_KEY before running")
	}

	store := provider.NewMemoryUsageStore()
	guarded, err := assembleProvider(apiKey, store)
	if err != nil {
		return err
	}

	// 请求入口注入身份与余额（生产中在鉴权中间件里做，余额来自账务系统）。
	ctx := provider.WithUserID(context.Background(), demoUserID)
	ctx = provider.WithConversationID(ctx, demoConversationID)
	ctx = provider.WithCostBudget(ctx, 1_000_000) // 剩余余额 1 元

	if err := streamingChat(ctx, guarded); err != nil {
		return err
	}
	if err := streamingToolLoop(ctx, guarded); err != nil {
		return err
	}
	if err := compactHistory(ctx, guarded); err != nil {
		return err
	}
	demoBudgetRejection(ctx, guarded)
	printBill(store)
	return nil
}

// assembleProvider 完成一次性装配：观测 + 计费 hook + 配额/余额中间件。
// 业务各处从这里拿包装后的 provider，调用点不再出现任何统计代码。
func assembleProvider(apiKey string, store *provider.MemoryUsageStore) (provider.Provider, error) {
	base, err := provider.NewProviderFromPreset(provider.ProviderDeepSeek, apiKey, demoModel)
	if err != nil {
		return nil, fmt.Errorf("build provider: %w", err)
	}

	observed := provider.WithObservability(base, provider.ObserveOptions{
		OnEvent: provider.CombineObserveHooks(
			logHook,                        // 观测：打印每次调用的模型与用量
			provider.NewBillingHook(store), // 计费：按 ctx 中的用户/会话归账
		),
	})
	return provider.TryWithMiddlewares(observed, provider.MiddlewareOptions{
		Chat: []provider.Middleware{
			provider.QuotaMiddleware(&tokenQuota{store: store, limit: 200_000}),
			provider.CostBudgetMiddleware(pricing),
		},
		Stream: []provider.StreamMiddleware{
			provider.QuotaStreamMiddleware(&tokenQuota{store: store, limit: 200_000}),
			provider.CostBudgetStreamMiddleware(pricing),
		},
	})
}

// logHook 演示观测切面：与计费 hook 并存，互不干扰。
func logHook(_ context.Context, event provider.ObserveEvent) {
	if event.Operation == provider.ObserveOperationStream {
		return // 流创建事件无 usage，跳过打印
	}
	fmt.Printf("  [观测] op=%s 请求模型=%s 实际模型=%s tokens=%d 终止=%s\n",
		event.Operation, event.RequestModel, event.Model, event.Usage.TotalTokens, event.StreamFinish)
}

// tokenQuota 是 QuotaChecker 的最小实现：按用户累计 token 上限拦截。
// 生产实现见 example/billingstore（Redis + GORM，支持 token/金额、total/daily 口径）。
type tokenQuota struct {
	store *provider.MemoryUsageStore
	limit int
}

func (q *tokenQuota) Allow(_ context.Context, userID, _ string) error {
	totals, ok := q.store.UserTotals(userID)
	if ok && totals.Usage.TotalTokens >= q.limit {
		return fmt.Errorf("%w: user %s used %d tokens (limit %d)",
			provider.ErrQuotaExceeded, userID, totals.Usage.TotalTokens, q.limit)
	}
	return nil
}

// streamingChat 演示流式对话：打字机输出 + 流尾部的完整 token 统计。
func streamingChat(ctx context.Context, p provider.Provider) error {
	fmt.Println("== 场景一：流式对话（自动下发 include_usage）==")
	result, err := provider.CollectStreamResult(ctx, p, &provider.ChatRequest{
		Model: demoModel,
		Messages: []provider.Message{
			provider.SystemText("你是简洁的办公助手。"),
			provider.UserText("用一句话介绍你能做什么"),
		},
		MaxTokens: 128,
	}, func(delta string) { fmt.Print(delta) })
	if err != nil {
		return fmt.Errorf("streaming chat: %w", err)
	}
	fmt.Printf("\n  本次消耗：prompt=%d completion=%d total=%d\n\n",
		result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
	return nil
}

// streamingToolLoop 演示流式智能体：模型调用工具→业务执行→自动回传继续生成，
// 全程 chunk 实时回调，工具增量由库内拼装。
func streamingToolLoop(ctx context.Context, p provider.Provider) error {
	fmt.Println("== 场景二：流式工具循环（智能体）==")
	resp, err := provider.RunToolLoopStream(ctx, p, &provider.ChatRequest{
		Model: demoModel,
		Messages: []provider.Message{
			provider.UserText("北京今天适合户外办公吗？先查天气再回答。"),
		},
		Tools: []provider.Tool{{
			Function: provider.FunctionDef{
				Name:        "get_weather",
				Description: "查询城市当前天气",
				Parameters: provider.ParamSchema{
					Type:       "object",
					Properties: map[string]provider.ParamSchema{"city": {Type: "string"}},
					Required:   []string{"city"},
				},
			},
		}},
		MaxTokens: 256,
	}, 5, func(_ context.Context, name, args string) (string, error) {
		fmt.Printf("\n  [工具执行] %s(%s)\n", name, args)
		return `{"weather":"晴","temperature":"22°C"}`, nil // 业务侧真实实现
	}, func(chunk provider.StreamChunk) {
		fmt.Print(chunk.Delta)
	})
	if err != nil {
		return fmt.Errorf("tool loop: %w", err)
	}
	fmt.Printf("\n  最终轮消耗：total=%d\n\n", resp.Usage.TotalTokens)
	return nil
}

// compactHistory 演示上下文压缩：旧历史摘要为一条消息，
// CompactResult 携带业务缓存所需的摘要正文与覆盖条数。
func compactHistory(ctx context.Context, p provider.Provider) error {
	fmt.Println("== 场景三：多轮历史摘要压缩 ==")
	history := []provider.Message{
		provider.SystemText("你是简洁的办公助手。"),
		provider.UserText("帮我规划周报结构"),
		provider.AssistantText("建议分三块：本周完成、数据亮点、下周计划。"),
		provider.UserText("本周完成了计费模块和流式改造"),
		provider.AssistantText("已记录：计费模块、流式改造。"),
		provider.UserText("下周计划做多模态和知识库调研"),
		provider.AssistantText("已记录下周计划。"),
		provider.UserText("好的，按这些内容生成正式周报"),
	}

	result, err := provider.CompactMessages(ctx, p, history, provider.CompactOptions{
		Model:            demoModel, // 摘要用便宜模型
		KeepRecentGroups: 2,         // 最近 2 组保留原文
		MaxSummaryTokens: 200,
	})
	if err != nil {
		return fmt.Errorf("compact: %w", err)
	}
	if result.Compacted() {
		fmt.Printf("  压缩了 %d 条旧消息，摘要（业务应按会话缓存）：\n    %s\n",
			result.CompactedCount, result.Summary)
		fmt.Printf("  压缩前估算 %d token，压缩后 %d token\n\n",
			provider.EstimateTokens(history), provider.EstimateTokens(result.Messages))
	}
	return nil
}

// demoBudgetRejection 演示余额硬限：余额不足时请求被拦截、不发往平台。
func demoBudgetRejection(ctx context.Context, p provider.Provider) {
	fmt.Println("== 场景四：余额不足拦截 ==")
	poorCtx := provider.WithCostBudget(ctx, 10) // 余额仅 10 微元
	_, err := p.Chat(poorCtx, &provider.ChatRequest{
		Model:    demoModel,
		Messages: []provider.Message{provider.UserText("这条请求不应该发出")},
	})
	if errors.Is(err, provider.ErrQuotaExceeded) {
		fmt.Printf("  已拦截（未产生真实调用）：%v\n\n", err)
	}
}

// printBill 演示账单查询：按用户与会话两级聚合，并按费率换算金额。
func printBill(store *provider.MemoryUsageStore) {
	fmt.Println("== 账单 ==")
	user, _ := store.UserTotals(demoUserID)
	conv, _ := store.ConversationTotals(demoUserID, demoConversationID)
	micros, currency, err := pricing.Cost(demoModel, user.Usage)
	if err != nil {
		fmt.Printf("  算费失败：%v\n", err)
		return
	}
	fmt.Printf("  用户 %s：%d 次调用，%d tokens，费用 %s %s\n",
		demoUserID, user.Calls, user.Usage.TotalTokens, provider.FormatMicros(micros), currency)
	fmt.Printf("  会话 %s：%d 次调用，%d tokens（异常终止 %d 次）\n",
		demoConversationID, conv.Calls, conv.Usage.TotalTokens, conv.TerminatedCalls)
}
