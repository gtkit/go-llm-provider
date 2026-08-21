package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gtkit/json/v2"
)

// ============================================================
// 重排序（rerank）
// ============================================================

// Reranker 是统一的重排序调用接口。
//
// 典型用途是 RAG 的召回后精排：向量检索先粗筛出几十条候选，
// 再由 rerank 模型按与 query 的相关性重新打分排序，只把最相关的几条送进上下文。
//
// 与 Provider / Embedder 并列存在而非合并的原因：rerank 是独立的平台端点，
// 且只有部分平台提供（如硅基流动），合并进 Embedder 会强制产生空实现。
type Reranker interface {
	// Name 返回供应商标识。
	Name() ProviderName

	// Rerank 按与 Query 的相关性对 Documents 重新排序。
	// 返回结果按相关性从高到低排列，Index 指回请求中 Documents 的下标。
	Rerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error)
}

// RerankRequest 是与具体平台无关的重排序请求。
type RerankRequest struct {
	// Model 可选，留空时使用 RerankerConfig.Model。
	Model string

	// Query 必填，用于衡量相关性的查询文本。
	Query string

	// Documents 必填，至少 1 条候选文本。
	Documents []string

	// TopN 可选，只返回相关性最高的前 N 条。
	// 指针类型用于区分"未设置"与"设为 0"；≤ 0 时按未设置处理（返回全部）。
	TopN *int

	// ReturnDocuments 可选，是否让平台在结果中回传文档原文。
	// 未设置时不发送该字段（走平台默认）。多数场景不必开启——
	// 用 RerankResult.Index 回查本地的 Documents 即可，能省一份回传流量。
	ReturnDocuments *bool
}

func (r *RerankRequest) validate() error {
	if r == nil {
		return ErrNilRerankRequest
	}
	if strings.TrimSpace(r.Query) == "" {
		return ErrEmptyRerankQuery
	}
	if len(r.Documents) == 0 {
		return ErrEmptyRerankDocuments
	}
	return nil
}

// RerankResult 是单条候选文档的重排序结果。
type RerankResult struct {
	// Index 是该文档在请求 Documents 中的下标。
	Index int

	// RelevanceScore 是平台给出的相关性得分，越大越相关。
	// 取值范围由模型决定（多数模型输出 [0,1]），只应用于同一次调用内的排序比较，
	// 不同模型、不同调用之间的绝对值不可直接比较。
	RelevanceScore float64

	// Document 是文档原文，仅在请求设置 ReturnDocuments 为 true
	// 且平台回传时非空；否则为空串，用 Index 回查请求中的 Documents。
	Document string
}

// RerankResponse 是重排序调用的响应。
type RerankResponse struct {
	// Model 是响应侧回传的实际模型名，平台未回传时为请求使用的模型名。
	Model string

	// Results 按相关性从高到低排列。
	Results []RerankResult

	// Usage 是 token 用量。各平台字段口径不同（有的只给总量），
	// 缺失的项为 0；rerank 场景 CompletionTokens 通常为 0。
	Usage Usage

	// Metadata 是响应诊断信息（RequestID、白名单响应头等）。
	Metadata ResponseMetadata
}

// SortedDocuments 按 Results 的顺序返回 documents 中对应的原文，
// 用于把重排结果直接接回本地候选集。
//
// documents 应是发起请求时使用的那份切片；下标越界的结果会被跳过
// （传入的切片与请求不一致时不会 panic）。
func (r *RerankResponse) SortedDocuments(documents []string) []string {
	if r == nil || len(r.Results) == 0 || len(documents) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Results))
	for _, result := range r.Results {
		if result.Index < 0 || result.Index >= len(documents) {
			continue
		}
		out = append(out, documents[result.Index])
	}
	return out
}

// RerankerConfig 描述一个 reranker 的连接配置。
type RerankerConfig struct {
	// Name 必填，供应商标识。
	Name ProviderName
	// BaseURL 必填，平台 API 根地址，实际请求路径为 {BaseURL}/rerank，
	// 例如 "https://api.siliconflow.cn/v1"。
	BaseURL string
	// APIKey 必填，以 Authorization: Bearer 发送。
	APIKey string
	// Model 必填，rerank 专用模型，如 "BAAI/bge-reranker-v2-m3"。
	Model string
	// HTTPClient 可选，留空时使用 DefaultHTTPClient。
	HTTPClient HTTPDoer
}

// Validate reports missing required RerankerConfig fields.
func (cfg RerankerConfig) Validate() error {
	var errs []error

	if cfg.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if cfg.BaseURL == "" {
		errs = append(errs, errors.New("base url is required"))
	}
	if cfg.APIKey == "" {
		errs = append(errs, errors.New("api key is required"))
	}
	if cfg.Model == "" {
		errs = append(errs, errors.New("model is required"))
	}

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %w", ErrInvalidRerankerConfig, errors.Join(errs...))
}

// NewReranker 根据配置创建一个 Reranker 实例。
//
// 走 OpenAI 兼容风格的 POST {BaseURL}/rerank：请求体为
// model/query/documents/top_n/return_documents，响应体为
// results[].index / results[].relevance_score。硅基流动、Jina、Cohere 等
// 平台的 rerank 端点均是这一形态，用量字段的差异由本实现统一归一。
//
// 返回的 Reranker 在 cfg.HTTPClient 可并发使用时可并发使用。
func NewReranker(cfg RerankerConfig) (Reranker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = DefaultHTTPClient()
	}

	return &httpReranker{
		name:       cfg.Name,
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		httpClient: httpClient,
	}, nil
}

// rerankerIsNil 判断接口值是否为 nil（与 providerIsNil 口径一致）。
func rerankerIsNil(r Reranker) bool {
	return r == nil
}

type httpReranker struct {
	name       ProviderName
	baseURL    string
	apiKey     string
	model      string
	httpClient HTTPDoer
}

func (r *httpReranker) Name() ProviderName {
	if r == nil {
		return ""
	}
	return r.name
}

func (r *httpReranker) Rerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error) {
	if r == nil {
		return nil, ErrNilReranker
	}
	if err := req.validate(); err != nil {
		return nil, err
	}

	model := firstString(req.Model, r.model)
	apiReq := rerankAPIRequest{
		Model:           model,
		Query:           req.Query,
		Documents:       req.Documents,
		ReturnDocuments: req.ReturnDocuments,
	}
	if req.TopN != nil && *req.TopN > 0 {
		topN := *req.TopN
		apiReq.TopN = &topN
	}

	apiResp, metadata, err := doNativeJSON[rerankAPIResponse](ctx, r.httpClient, nativeHTTPRequest{
		Method:     http.MethodPost,
		URL:        r.baseURL + "/rerank",
		Body:       apiReq,
		Provider:   r.name,
		Model:      model,
		SetHeaders: r.setHeaders,
	})
	if err != nil {
		return nil, err
	}

	results, err := rerankResults(r.name, apiResp.Results, len(req.Documents))
	if err != nil {
		return nil, err
	}
	if metadata.Model == "" {
		metadata.Model = model
	}

	return &RerankResponse{
		Model:    firstString(apiResp.Model, model),
		Results:  results,
		Usage:    apiResp.usage(),
		Metadata: metadata,
	}, nil
}

func (r *httpReranker) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
}

// rerankResults 把平台返回的条目归一为 RerankResult，并校验下标落在请求范围内——
// 越界的下标一旦透给调用方，调用方拿它索引自己的候选集就会越界 panic。
func rerankResults(name ProviderName, items []rerankAPIResult, documents int) ([]RerankResult, error) {
	out := make([]RerankResult, 0, len(items))
	for _, item := range items {
		if item.Index < 0 || item.Index >= documents {
			return nil, fmt.Errorf("[%s] invalid rerank response: index %d out of range for %d documents",
				name, item.Index, documents)
		}
		text, err := rerankDocumentText(item.Document)
		if err != nil {
			return nil, fmt.Errorf("[%s] invalid rerank response: %w", name, err)
		}
		out = append(out, RerankResult{
			Index:          item.Index,
			RelevanceScore: item.RelevanceScore,
			Document:       text,
		})
	}
	return out, nil
}

// ============================================================
// 平台协议映射
// ============================================================

type rerankAPIRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            *int     `json:"top_n,omitempty"`
	ReturnDocuments *bool    `json:"return_documents,omitempty"`
}

type rerankAPIResponse struct {
	Model   string            `json:"model"`
	Results []rerankAPIResult `json:"results"`

	// Tokens 是硅基流动口径的用量。
	Tokens *rerankAPITokens `json:"tokens"`
	// Usage 是 Jina 口径的用量。
	Usage *rerankAPIUsage `json:"usage"`
}

// usage 把各平台的用量字段归一到统一的 Usage。
// 两种口径都缺失时返回零值，不猜测用量。
func (r rerankAPIResponse) usage() Usage {
	switch {
	case r.Tokens != nil:
		usage := Usage{
			PromptTokens:     r.Tokens.InputTokens,
			CompletionTokens: r.Tokens.OutputTokens,
		}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		return usage
	case r.Usage != nil:
		usage := Usage{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens:      r.Usage.TotalTokens,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		return usage
	default:
		return Usage{}
	}
}

type rerankAPITokens struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type rerankAPIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type rerankAPIResult struct {
	Index          int             `json:"index"`
	RelevanceScore float64         `json:"relevance_score"`
	Document       json.RawMessage `json:"document"`
}

// rerankDocumentText 兼容两种 document 形态：
// 对象 {"text": "..."}（硅基流动、Cohere）与裸字符串 "..."（部分自建端点）。
// 字段缺失或为 null 时返回空串。
func rerankDocumentText(document json.RawMessage) (string, error) {
	raw := strings.TrimSpace(string(document))
	if raw == "" || raw == "null" {
		return "", nil
	}

	switch raw[0] {
	case '"':
		var text string
		if err := json.Unmarshal([]byte(raw), &text); err != nil {
			return "", fmt.Errorf("decode document text: %w", err)
		}
		return text, nil
	case '{':
		var doc struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", fmt.Errorf("decode document object: %w", err)
		}
		return doc.Text, nil
	default:
		return "", fmt.Errorf("unsupported document form %q", raw)
	}
}
