// Package billingstore 是 UsageRecorder / QuotaChecker 的 Redis + GORM 参考实现。
//
// 定位为 reference 级：Redis 承担热路径累计计数，流水异步批量刷入数据库，
// 刷库为 best-effort（失败通过 OnError 上抛，不重试、不保证不丢）——
// 需要更强一致性时，请以本包为起点按业务要求强化。
package billingstore

import "time"

// UsageRecord 是逐次调用的计量流水（GORM 模型）。
type UsageRecord struct {
	ID               uint   `gorm:"primaryKey"`
	UserID           string `gorm:"size:64;index:idx_user_created,priority:1"`
	ConversationID   string `gorm:"size:64;index"`
	RequestID        string `gorm:"size:128"`
	Provider         string `gorm:"size:32"`
	Model            string `gorm:"size:64"`
	PromptTokens     int
	CompletionTokens int
	ReasoningTokens  int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalTokens      int
	CostMicros       int64  // 按注入的 PricingTable 计算，未配价时为 0
	Currency         string `gorm:"size:8"`
	Streaming        bool
	Terminated       bool      // 流式异常终止（usage 可能缺失），漏账审计用
	TerminateReason  string    `gorm:"size:16"`
	ErrorCode        string    `gorm:"size:32"`
	CreatedAt        time.Time `gorm:"index:idx_user_created,priority:2"`
}

// QuotaPeriod 是配额的统计口径。
type QuotaPeriod string

const (
	// QuotaPeriodTotal 按用户累计总量限额。
	QuotaPeriodTotal QuotaPeriod = "total"
	// QuotaPeriodDaily 按自然日（本地时区）限额。
	QuotaPeriodDaily QuotaPeriod = "daily"
)

// UserQuota 是用户限额配置（GORM 模型）。TokenLimit 与 CostLimitMicros
// 任一非零即生效，两者都配置时任一超限即拦截；均为 0 表示不限额。
type UserQuota struct {
	UserID          string `gorm:"primaryKey;size:64"`
	TokenLimit      int64
	CostLimitMicros int64
	Period          QuotaPeriod `gorm:"size:16;default:total"`
	UpdatedAt       time.Time
}
