package provider

import (
	"context"
	"sync"
	"time"
)

// ============================================================
// 调用方身份 ctx 传递（S-04）
// ============================================================

type userIDCtxKey struct{}

type conversationIDCtxKey struct{}

// WithUserID 将调用方用户标识写入 ctx，供计费/配额中间件在统一切面读取。
// 与具体 Web 框架无关；在请求入口（如鉴权中间件）调用一次即可。
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDCtxKey{}, userID)
}

// UserIDFromContext 读取 WithUserID 写入的用户标识。
// ctx 中不存在时返回 ("", false)。
func UserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDCtxKey{}).(string)
	return userID, ok && userID != ""
}

// WithConversationID 将会话标识写入 ctx，使用量可按"用户 + 会话"两级归账。
func WithConversationID(ctx context.Context, conversationID string) context.Context {
	return context.WithValue(ctx, conversationIDCtxKey{}, conversationID)
}

// ConversationIDFromContext 读取 WithConversationID 写入的会话标识。
// ctx 中不存在时返回 ("", false)。
func ConversationIDFromContext(ctx context.Context) (string, bool) {
	conversationID, ok := ctx.Value(conversationIDCtxKey{}).(string)
	return conversationID, ok && conversationID != ""
}

// ============================================================
// 按用户计量（S-05）
// ============================================================

// RecordEntry 是一次 LLM 调用的计量记录。
type RecordEntry struct {
	UserID         string
	ConversationID string // WithConversationID 注入时非空
	RequestID      string // provider 回传的请求标识，用于对账；流式路径可能为空
	Provider       ProviderName
	Model          string
	Operation      ObserveOperation
	Usage          Usage
	Elapsed        time.Duration
	Streaming      bool

	// Terminated 为 true 表示流式调用未正常读到 io.EOF（收到错误或被提前 Close），
	// 此时 Usage 可能为零值，但 provider 侧仍可能已产生消耗——
	// 收不收钱由 Recorder 按策略决定，漏账事实本身必须可观测。
	Terminated bool
	// TerminateReason 是流的终止方式，非流式调用恒为空。
	TerminateReason StreamFinishReason

	// Err 与 ErrorCode 携带调用失败信息；失败调用是否计费由 Recorder 决定。
	Err       error
	ErrorCode ErrorCode
}

// UsageRecorder 接收计量记录。实现方必须并发安全；
// Record 应快速返回（建议投递到队列后异步落库），其返回的 error 会被计量层忽略，
// 需要感知失败请在实现内部处理（日志、重试、降级）。
type UsageRecorder interface {
	Record(ctx context.Context, entry RecordEntry) error
}

// NewBillingHook 把 UsageRecorder 适配成 ObserveHook：
//
//	billed := provider.WithObservability(p, provider.ObserveOptions{
//	    OnEvent: provider.NewBillingHook(recorder),
//	})
//
// 之后所有 Chat / ChatStream / Embed 调用都会按 ctx 中的 UserID 归账，
// 业务调用点无需任何统计代码。ctx 未携带 UserID 的调用跳过计量；
// 流创建事件（ObserveOperationStream）不含 usage，同样跳过。
// Record 返回的 error 被忽略，计量失败绝不影响主请求。
func NewBillingHook(rec UsageRecorder) ObserveHook {
	if rec == nil {
		return func(context.Context, ObserveEvent) {}
	}
	return func(ctx context.Context, event ObserveEvent) {
		switch event.Operation {
		case ObserveOperationChat, ObserveOperationStreamComplete, ObserveOperationEmbed:
		default:
			return
		}
		userID, ok := UserIDFromContext(ctx)
		if !ok {
			return
		}
		conversationID, _ := ConversationIDFromContext(ctx)
		entry := RecordEntry{
			UserID:          userID,
			ConversationID:  conversationID,
			RequestID:       event.RequestID,
			Provider:        event.Provider,
			Model:           event.Model,
			Operation:       event.Operation,
			Usage:           event.Usage,
			Elapsed:         event.Duration,
			Streaming:       event.Operation == ObserveOperationStreamComplete,
			Terminated:      event.StreamFinish != "" && event.StreamFinish != StreamFinishEOF,
			TerminateReason: event.StreamFinish,
			Err:             event.Err,
			ErrorCode:       event.ErrorCode,
		}
		_ = rec.Record(ctx, entry)
	}
}

// CombineObserveHooks 将多个 ObserveHook 合并为一个，按传入顺序依次调用，
// 便于同时挂载日志观测与计费等多个切面。nil hook 会被跳过。
func CombineObserveHooks(hooks ...ObserveHook) ObserveHook {
	return func(ctx context.Context, event ObserveEvent) {
		for _, hook := range hooks {
			if hook != nil {
				hook(ctx, event)
			}
		}
	}
}

// ============================================================
// 内存计量存储（开箱即用的 UsageRecorder 参考实现）
// ============================================================

// UsageTotals 是按用户或会话聚合后的用量。
type UsageTotals struct {
	Usage           Usage // 各字段累加值
	Calls           int   // 计入的调用次数
	TerminatedCalls int   // 其中流式异常终止（usage 可能缺失）的次数
}

// MemoryUsageStore 是 UsageRecorder 的进程内实现：按用户与"用户+会话"两级聚合，
// 并发安全，零外部依赖。适合单实例部署与测试；多实例或需要持久化时，
// 换用共享存储（如 Redis/DB）实现的 UsageRecorder。
type MemoryUsageStore struct {
	mu            sync.RWMutex
	users         map[string]UsageTotals
	conversations map[string]UsageTotals
}

// NewMemoryUsageStore 构造一个空的 MemoryUsageStore。
func NewMemoryUsageStore() *MemoryUsageStore {
	return &MemoryUsageStore{
		users:         make(map[string]UsageTotals),
		conversations: make(map[string]UsageTotals),
	}
}

// Record 累加一条计量记录。总是返回 nil。
func (s *MemoryUsageStore) Record(_ context.Context, entry RecordEntry) error {
	if s == nil || entry.UserID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[entry.UserID] = accumulateTotals(s.users[entry.UserID], entry)
	if entry.ConversationID != "" {
		key := conversationKey(entry.UserID, entry.ConversationID)
		s.conversations[key] = accumulateTotals(s.conversations[key], entry)
	}
	return nil
}

// UserTotals 返回某用户的累计用量；用户无记录时返回零值与 false。
func (s *MemoryUsageStore) UserTotals(userID string) (UsageTotals, bool) {
	if s == nil {
		return UsageTotals{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	totals, ok := s.users[userID]
	return totals, ok
}

// ConversationTotals 返回某用户单个会话的累计用量；会话无记录时返回零值与 false。
func (s *MemoryUsageStore) ConversationTotals(userID, conversationID string) (UsageTotals, bool) {
	if s == nil {
		return UsageTotals{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	totals, ok := s.conversations[conversationKey(userID, conversationID)]
	return totals, ok
}

func accumulateTotals(totals UsageTotals, entry RecordEntry) UsageTotals {
	totals.Usage = addUsage(totals.Usage, entry.Usage)
	totals.Calls++
	if entry.Terminated {
		totals.TerminatedCalls++
	}
	return totals
}

func conversationKey(userID, conversationID string) string {
	return userID + "\x00" + conversationID
}
