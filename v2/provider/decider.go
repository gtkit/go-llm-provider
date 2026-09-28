package provider

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
)

// ============================================================
// 结构化判断（decide）
// ============================================================

// Decider 是结构化判断的统一调用接口：对程序传入的状态与预定义问题，
// 返回类型受限的答案（是/否、选择、评分）及概率，不生成文本。
//
// 典型用途是 Agent 流程中的高频判断：工具路由（配合 ToolSelector）、高风险操作前的护栏、
// 工单分流、检索结果相关性判断、基于置信度的人工兜底。
type Decider interface {
	// Name 返回供应商标识。
	Name() ProviderName

	// Decide 在一次请求内对 State 并行评估全部 Questions，答案按问题 key 返回。
	Decide(ctx context.Context, req *DecideRequest) (*DecideResponse, error)
}

// DecisionType 是问题（及答案）的类型。
type DecisionType string

const (
	// DecisionNoul 是是/否判断，答案为「是」的 0–1 概率。
	DecisionNoul DecisionType = "noul"
	// DecisionChoice 是从一组选项中选出一个。
	DecisionChoice DecisionType = "choice"
	// DecisionScore 是按有序等级评分。
	DecisionScore DecisionType = "score"
)

// DecideRequest 是结构化判断请求。
type DecideRequest struct {
	// Model 可选，留空时使用 DeciderConfig.Model。
	Model string

	// State 必填，待判断的内容：字符串，或可 JSON 序列化的对象、数组（如对话记录、应用状态）。
	// 已有的结构化数据建议直接传入，不必先拼成字符串。
	State any

	// Questions 必填，key 由调用方自定，答案按相同 key 返回；key 不参与模型推理。
	Questions map[string]DecisionQuestion
}

// DecisionQuestion 是一个待判断的问题。Instructions 与 Criteria 的取值都可以是
// 字符串，或可 JSON 序列化的对象、数组，原样发送给平台。
type DecisionQuestion struct {
	// Type 必填。
	Type DecisionType

	// Instructions 是问题本身，可在其中用反引号引用 State 中的字段名。
	Instructions any

	// Criteria 是判断标准，形态由 Type 决定：
	//   - noul：可选，形如 map[string]any{"true": ..., "false": ...}，分别描述「是」与「否」
	//   - choice：必填，选项名到描述的映射，描述可为 nil；答案中的 Choice 为选项名
	//   - score：必填，由低到高的等级描述数组，下标即分值
	Criteria any
}

// DecisionAnswer 是单个问题的答案，按 Type 读取对应字段。
type DecisionAnswer struct {
	// Type 与问题的 Type 一致。
	Type DecisionType `json:"type"`

	// Noul 是 noul 答案：答案为「是」的 0–1 概率。
	Noul float64 `json:"noul"`

	// Choice 是 choice 答案：概率最高的选项名。
	Choice string `json:"choice"`

	// Score 是 score 答案：按概率加权的等级值，可能落在两个等级之间。
	Score float64 `json:"score"`

	// Legend 是 score 答案的等级序号（字符串形式，如 "0"）到等级描述的映射。
	Legend map[string]any `json:"legend"`

	// Probabilities 是 choice 与 score 答案的概率分布，总和为 1：
	// choice 以选项名为 key，score 以等级序号字符串为 key。
	Probabilities map[string]float64 `json:"probabilities"`

	// Confidence 是 choice 与 score 答案由概率分布推导出的 0–1 置信度，分布越集中越高。
	Confidence float64 `json:"confidence"`
}

// DecideResponse 是结构化判断的响应。
type DecideResponse struct {
	// Model 是实际作答的具体版本 ID（如 "jev-1.13.0"），请求使用别名时也返回具体版本。
	Model string

	// Answers 按问题 key 索引，与请求的 Questions 一一对应。
	Answers map[string]DecisionAnswer

	// Usage 是 token 用量：PromptTokens 为输入 token，CompletionTokens 为输出 token。
	Usage Usage

	// Metadata 是响应诊断信息（RequestID、白名单响应头等）。
	Metadata ResponseMetadata
}

// DeciderConfig 描述一个 Decider 的连接配置。
type DeciderConfig struct {
	// Name 可选，默认 ProviderTypeSafe。
	Name ProviderName
	// BaseURL 可选，默认 "https://api.typesafe.ai/v1"，实际请求路径为 {BaseURL}/systemone。
	BaseURL string
	// APIKey 必填，以 Authorization: Bearer 发送。
	APIKey string
	// Model 可选，默认 "jev-latest"。
	Model string
	// HTTPClient 可选，留空时使用 DefaultHTTPClient。
	HTTPClient HTTPDoer
}

// NewDecider 根据配置创建一个对接 TypeSafe AI 结构化判断 API（Jev 模型）的 Decider。
//
// 非 2xx 响应返回 *ProviderError：401 为 ErrorCodeAuth，422 为 ErrorCodeInvalidRequest，
// 429 为 ErrorCodeRateLimit，529（服务过载）为可重试的 ErrorCodeServerError，
// 平台下发的 Retry-After 写入 ProviderError.RetryAfter。
//
// 返回的 Decider 在 cfg.HTTPClient 可并发使用时可并发使用。
func NewDecider(cfg DeciderConfig) (Decider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: api key is required", ErrInvalidDeciderConfig)
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = DefaultHTTPClient()
	}

	return &httpDecider{
		name:       cmp.Or(cfg.Name, ProviderTypeSafe),
		baseURL:    strings.TrimRight(firstString(cfg.BaseURL, "https://api.typesafe.ai/v1"), "/"),
		apiKey:     cfg.APIKey,
		model:      firstString(cfg.Model, "jev-latest"),
		httpClient: httpClient,
	}, nil
}

type httpDecider struct {
	name       ProviderName
	baseURL    string
	apiKey     string
	model      string
	httpClient HTTPDoer
}

func (d *httpDecider) Name() ProviderName {
	if d == nil {
		return ""
	}
	return d.name
}

func (d *httpDecider) Decide(ctx context.Context, req *DecideRequest) (*DecideResponse, error) {
	if d == nil {
		return nil, ErrNilDecider
	}
	if err := req.validate(); err != nil {
		return nil, err
	}

	model := firstString(req.Model, d.model)
	apiReq := decideAPIRequest{
		Model:     model,
		State:     req.State,
		Questions: make(map[string]decideAPIQuestion, len(req.Questions)),
	}
	for key, q := range req.Questions {
		apiReq.Questions[key] = decideAPIQuestion(q)
	}

	apiResp, metadata, err := doNativeJSON[decideAPIResponse](ctx, d.httpClient, nativeHTTPRequest{
		Method:     http.MethodPost,
		URL:        d.baseURL + "/systemone",
		Body:       apiReq,
		Provider:   d.name,
		Model:      model,
		SetHeaders: d.setHeaders,
	})
	if err != nil {
		return nil, err
	}

	if err := checkDecisionAnswers(req.Questions, apiResp.Answers); err != nil {
		return nil, fmt.Errorf("[%s] invalid decide response: %w", d.name, err)
	}
	respModel := firstString(apiResp.Model, model)
	metadata.Model = respModel

	return &DecideResponse{
		Model:   respModel,
		Answers: apiResp.Answers,
		Usage: Usage{
			PromptTokens:     apiResp.Usage.InputTokens,
			CompletionTokens: apiResp.Usage.OutputTokens,
			TotalTokens:      apiResp.Usage.InputTokens + apiResp.Usage.OutputTokens,
		},
		Metadata: metadata,
	}, nil
}

func (d *httpDecider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
}

// validate 只校验 TypeSafe 官方各文档一致的约束；选项数、等级数等仅由服务端判定的规则
// 交给服务端以 422 返回，避免客户端比服务端更严。
func (r *DecideRequest) validate() error {
	if r == nil {
		return ErrNilDecideRequest
	}
	if isNilJSON(r.State) {
		return fmt.Errorf("%w: state is required", ErrInvalidDecideRequest)
	}
	if len(r.Questions) == 0 {
		return fmt.Errorf("%w: questions are required", ErrInvalidDecideRequest)
	}
	for _, key := range slices.Sorted(maps.Keys(r.Questions)) {
		if key == "" {
			return fmt.Errorf("%w: question key is empty", ErrInvalidDecideRequest)
		}
		q := r.Questions[key]
		switch q.Type {
		case DecisionNoul:
		case DecisionChoice, DecisionScore:
			if isNilJSON(q.Criteria) {
				return fmt.Errorf("%w: question %q: criteria is required for %s", ErrInvalidDecideRequest, key, q.Type)
			}
		default:
			return fmt.Errorf("%w: question %q: unknown type %q", ErrInvalidDecideRequest, key, q.Type)
		}
	}
	return nil
}

// isNilJSON 报告 v 是否会被序列化为 JSON null：包括 nil 接口与 typed-nil 的指针、map、slice。
func isNilJSON(v any) bool {
	if v == nil {
		return true
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}

// checkDecisionAnswers 保证每个问题都有类型一致的答案——调用方按问题 key 读取答案，
// 缺题或类型错位时交还残缺结果会让调用方读到零值并当作有效判断。
func checkDecisionAnswers(questions map[string]DecisionQuestion, answers map[string]DecisionAnswer) error {
	for _, key := range slices.Sorted(maps.Keys(questions)) {
		answer, ok := answers[key]
		if !ok {
			return fmt.Errorf("missing answer for question %q", key)
		}
		if want := questions[key].Type; answer.Type != want {
			return fmt.Errorf("question %q: answer type %q, want %q", key, answer.Type, want)
		}
	}
	return nil
}

// ============================================================
// 平台协议映射
// ============================================================

type decideAPIRequest struct {
	Model     string                       `json:"model"`
	State     any                          `json:"state"`
	Questions map[string]decideAPIQuestion `json:"questions"`
}

type decideAPIQuestion struct {
	Type         DecisionType `json:"type"`
	Instructions any          `json:"instructions,omitzero"`
	Criteria     any          `json:"criteria,omitzero"`
}

type decideAPIResponse struct {
	Model   string                    `json:"model"`
	Answers map[string]DecisionAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}
