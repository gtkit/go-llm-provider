package websearch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gtkit/json/v2"
)

// 博查（Bocha）Web Search API：国内直连、中文内容优化，响应结构兼容 Bing Search。
// 文档与 API Key：https://open.bochaai.com/
const (
	bochaDefaultBaseURL = "https://api.bochaai.com"
	bochaSearchPath     = "/v1/web-search"

	// bochaMaxCount 是平台允许的单次最大返回条数。
	bochaMaxCount = 50
	// bochaDefaultCount 是未指定条数时的默认值：够用且不至于把 context 撑爆。
	bochaDefaultCount = 8
	// bochaSuccessCode 是响应体内层的成功码（HTTP 200 不代表业务成功）。
	bochaSuccessCode = 200
	// bochaMaxResponseBytes 限制响应体读取上限，避免异常响应撑爆内存。
	bochaMaxResponseBytes = 8 << 20
)

// BochaFreshness 是时效性过滤取值。
//
// 官方建议默认用 FreshnessNoLimit：搜索算法会自行做时间范围改写，
// 强行限定区间反而可能因该区间内无相关网页而搜不到结果。
type BochaFreshness string

// 时效性过滤的预置取值；也接受 "YYYY-MM-DD..YYYY-MM-DD" 或 "YYYY-MM-DD" 形式的自定义值。
const (
	FreshnessNoLimit  BochaFreshness = "noLimit"
	FreshnessOneDay   BochaFreshness = "oneDay"
	FreshnessOneWeek  BochaFreshness = "oneWeek"
	FreshnessOneMonth BochaFreshness = "oneMonth"
	FreshnessOneYear  BochaFreshness = "oneYear"
)

// HTTPDoer 是最小 HTTP 客户端契约，便于注入自定义 transport 或测试替身。
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// BochaConfig 配置博查搜索客户端。
type BochaConfig struct {
	// APIKey 必填，从 https://open.bochaai.com/api-keys 获取。
	APIKey string
	// BaseURL 可选，留空使用官方地址；走企业网关时改这里。
	BaseURL string
	// Freshness 可选，留空即 noLimit。
	Freshness BochaFreshness
	// Summary 请求长文本摘要。强烈建议开启（默认开启）：
	// 关闭时只能拿到几十字的 snippet，模型据此作答容易空泛。
	// 显式关闭请设 DisableSummary。
	DisableSummary bool
	// HTTPClient 可选，留空使用带传输层超时的默认客户端。
	// 整体超时由调用方 ctx 控制，这里不设 http.Client.Timeout。
	HTTPClient HTTPDoer
}

// BochaClient 是博查 Web Search API 的 SearchClient 实现。
// 并发安全性取决于注入的 HTTPClient，默认客户端并发安全。
type BochaClient struct {
	apiKey     string
	baseURL    string
	freshness  string
	summary    bool
	httpClient HTTPDoer
}

// NewBocha 创建博查搜索客户端。
func NewBocha(cfg BochaConfig) (*BochaClient, error) {
	if cfg.APIKey == "" {
		return nil, ErrMissingAPIKey
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = bochaDefaultBaseURL
	}
	freshness := string(cfg.Freshness)
	if freshness == "" {
		freshness = string(FreshnessNoLimit)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}
	return &BochaClient{
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		freshness:  freshness,
		summary:    !cfg.DisableSummary,
		httpClient: httpClient,
	}, nil
}

// defaultHTTPClient 返回带传输层超时的默认客户端：
// 不设 Client.Timeout，请求总预算交给 ctx；只兜住 dial / TLS / 响应头的无限等待。
func defaultHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// Search 实现 SearchClient。
func (c *BochaClient) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptyQuery
	}

	body, err := json.Marshal(bochaRequest{
		Query:     query,
		Freshness: c.freshness,
		Summary:   c.summary,
		Count:     clampLimit(limit, bochaDefaultCount, bochaMaxCount),
	})
	if err != nil {
		return nil, fmt.Errorf("websearch: encode bocha request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+bochaSearchPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("websearch: build bocha request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// 传输层失败（连接被拒、超时）通常是瞬时的，标记为可重试；
		// ctx 取消由调用方判定，不在这里改写语义。
		return nil, &BochaError{Message: err.Error(), transport: true, cause: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, bochaMaxResponseBytes))
	if err != nil {
		return nil, &BochaError{StatusCode: resp.StatusCode, Message: "read response body: " + err.Error(), transport: true, cause: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &BochaError{StatusCode: resp.StatusCode, Message: truncateForError(string(raw))}
	}

	var decoded bochaResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("websearch: decode bocha response: %w", err)
	}
	// HTTP 200 不代表业务成功：博查在响应体内层用 code 表示结果。
	if decoded.Code != bochaSuccessCode {
		return nil, &BochaError{StatusCode: resp.StatusCode, Code: decoded.Code, Message: decoded.Msg, LogID: decoded.LogID}
	}

	return decoded.hits(), nil
}

type bochaRequest struct {
	Query     string `json:"query"`
	Freshness string `json:"freshness,omitempty"`
	Summary   bool   `json:"summary"`
	Count     int    `json:"count,omitempty"`
}

type bochaResponse struct {
	Code  int    `json:"code"`
	LogID string `json:"log_id"`
	Msg   string `json:"msg"`
	Data  *struct {
		WebPages *struct {
			Value []bochaWebPage `json:"value"`
		} `json:"webPages"`
	} `json:"data"`
}

type bochaWebPage struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	SiteName      string `json:"siteName"`
	Snippet       string `json:"snippet"`
	Summary       string `json:"summary"`
	DatePublished string `json:"datePublished"`
}

// hits 把博查响应归一为 SearchHit；缺字段与空结果都按空切片处理，不 panic。
func (r bochaResponse) hits() []SearchHit {
	if r.Data == nil || r.Data.WebPages == nil {
		return nil
	}
	pages := r.Data.WebPages.Value
	out := make([]SearchHit, 0, len(pages))
	for _, page := range pages {
		if page.URL == "" {
			continue
		}
		out = append(out, SearchHit{
			Title:     page.Name,
			URL:       page.URL,
			Snippet:   page.Snippet,
			Summary:   page.Summary,
			Site:      page.SiteName,
			Published: parseBochaTime(page.DatePublished),
		})
	}
	return out
}

// parseBochaTime 解析发布时间，无法解析时返回零值而不是报错：
// 时间只是展示与排序的辅助信息，不该让整次搜索失败。
func parseBochaTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05", time.DateOnly} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// BochaError 是博查接口返回的失败，携带 HTTP 状态码与平台业务码。
type BochaError struct {
	StatusCode int
	Code       int
	Message    string
	LogID      string

	transport bool
	cause     error
}

func (e *BochaError) Error() string {
	var b strings.Builder
	b.WriteString("websearch: bocha search failed")
	if e.StatusCode > 0 {
		fmt.Fprintf(&b, " (http %d)", e.StatusCode)
	}
	if e.Code > 0 {
		fmt.Fprintf(&b, " (code %d)", e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.LogID != "" {
		b.WriteString(" [log_id=" + e.LogID + "]")
	}
	return b.String()
}

func (e *BochaError) Unwrap() error { return e.cause }

// Retryable 实现 RetryableError：传输层失败、限流与 5xx 值得重试，
// 鉴权失败与参数错误换几次也一样，不重试。
func (e *BochaError) Retryable() bool {
	if e.transport {
		return true
	}
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// truncateForError 限制进入错误信息的响应体长度，避免把整页 HTML 灌进日志。
func truncateForError(text string) string {
	const maxLen = 256
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "…"
}
