// Package websearch 是"给 Agent 接联网搜索"的参考实现。
//
// 定位：go-llm-provider 只负责与大模型平台交互，搜索服务属于应用层选型，
// 因此不进核心包。本模块用独立 go.mod 演示推荐接法：
//
//	SearchClient 接口（应用层契约）
//	  └─ 博查 / Serper / Tavily … 任选实现
//	       └─ 包装成 provider.Tool + provider.ToolHandler
//	            └─ 交给 provider.RunToolLoop，并叠加提示注入防护
//
// 换搜索服务商只需换 SearchClient 的实现，工具定义、防护策略、工具循环都不用动。
package websearch

import (
	"context"
	"errors"
	"strings"
	"time"
)

// 搜索客户端的通用错误。具体实现可用 errors.Is 让调用方统一判定。
var (
	// ErrEmptyQuery 表示查询词为空。
	ErrEmptyQuery = errors.New("websearch: query is empty")
	// ErrMissingAPIKey 表示未配置 API Key。
	ErrMissingAPIKey = errors.New("websearch: api key is required")
)

// SearchHit 是跨搜索服务归一化的一条结果。
//
// 各家返回结构差异很大（博查兼容 Bing 格式、Serper 是 Google SERP、Tavily 已做清洗），
// 归一到同一个结构后，上层的排序、截断、拼 prompt、引用标注才能与服务商解耦。
type SearchHit struct {
	Title string
	URL   string
	// Snippet 是短摘要，各家都有。
	Snippet string
	// Summary 是长摘要或正文抽取，仅部分服务提供（博查需显式请求，Tavily 默认返回）。
	// 为空时上层应回退到 Snippet。
	Summary string
	// Site 是站点名，可为空。
	Site string
	// Published 是发布时间，零值表示服务未提供或无法解析。
	Published time.Time
}

// Text 返回该条结果用于喂给模型的正文：优先长摘要，回退短摘要。
func (h SearchHit) Text() string {
	if text := strings.TrimSpace(h.Summary); text != "" {
		return text
	}
	return strings.TrimSpace(h.Snippet)
}

// SearchClient 是本模块与搜索服务之间的唯一契约。
//
// limit 是期望的结果条数上限，实现方按各自平台的允许范围裁剪；
// 返回条数可能少于 limit，调用方不应假设长度。
// 超时与取消一律由 ctx 控制，实现方不得自行设置固定的整体超时。
type SearchClient interface {
	Search(ctx context.Context, query string, limit int) ([]SearchHit, error)
}

// SearchFunc 让普通函数直接满足 SearchClient，便于测试与临时适配。
type SearchFunc func(ctx context.Context, query string, limit int) ([]SearchHit, error)

// Search 实现 SearchClient。
func (f SearchFunc) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	return f(ctx, query, limit)
}

// RetryableError 供搜索实现标注"这次失败值得重试"（限流、超时、5xx）。
// 配合 provider.RunToolLoopOptions.ToolRetry.ShouldRetry 使用，见 ToolRetry。
type RetryableError interface {
	error
	Retryable() bool
}

// IsRetryable 报告 err 链中是否有实现 RetryableError 且判定可重试的错误。
func IsRetryable(err error) bool {
	var retryable RetryableError
	if errors.As(err, &retryable) {
		return retryable.Retryable()
	}
	return false
}

// clampLimit 把调用方给的条数约束到 [1, maxLimit]，0 或负数取 defaultLimit。
func clampLimit(limit, defaultLimit, maxLimit int) int {
	if limit <= 0 {
		limit = defaultLimit
	}
	return min(limit, maxLimit)
}
