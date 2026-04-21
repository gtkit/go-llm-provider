package provider

import (
	"context"
	"errors"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// Embedder 是统一的文本向量化调用接口。
//
// 与 Provider 并列存在而非合并的原因：
//   - 部分平台（如 DeepSeek、Moonshot）官方暂无 embedding 模型，
//     合并进 Provider 会强制产生空实现；
//   - embedding 与 chat 在语义上是两类独立能力，应分别抽象。
type Embedder interface {
	// Name 返回供应商标识。
	Name() ProviderName

	// Embed 对输入文本生成向量。
	// Input 支持批量，返回结果按 Index 顺序与 Input 一一对应。
	Embed(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
}

// EmbeddingRequest 是与具体平台无关的向量化请求。
type EmbeddingRequest struct {
	// Model 可选，留空时使用 EmbedderConfig.Model。
	Model string

	// Input 必填，至少 1 条文本。
	Input []string

	// Dimensions 可选，指针类型用于区分"未设置"与"设为 0"。
	// 仅部分模型支持（如 OpenAI text-embedding-3 系列、Qwen text-embedding-v3）。
	Dimensions *int

	// User 可选，OpenAI 兼容字段，用于平台侧风控。
	User string
}

// EmbeddingResponse 是向量化调用的响应。
type EmbeddingResponse struct {
	Data  []Embedding
	Model string
	Usage Usage // 复用 Chat 场景同名类型；embedding 场景 CompletionTokens 通常为 0
}

// Embedding 表示一条文本对应的向量。
type Embedding struct {
	Index  int       // 与请求 Input 中的索引对应
	Vector []float32 // 浮点向量
}

// EmbedderConfig 描述一个 embedder 的连接配置。
type EmbedderConfig struct {
	Name    ProviderName
	BaseURL string
	APIKey  string
	Model   string // embedding 专用默认模型，如 "text-embedding-3-small"
}

// Validate reports missing required EmbedderConfig fields.
func (cfg EmbedderConfig) Validate() error {
	var errs []error

	if cfg.Name == "" {
		errs = append(errs, errors.New("name is required"))
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

	return fmt.Errorf("%w: %w", ErrInvalidEmbedderConfig, errors.Join(errs...))
}

// NewEmbedder 根据配置创建一个 Embedder 实例。
func NewEmbedder(cfg EmbedderConfig) (Embedder, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	ocfg := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		ocfg.BaseURL = cfg.BaseURL
	}

	return &openaiEmbedder{
		name:   cfg.Name,
		model:  cfg.Model,
		client: openai.NewClientWithConfig(ocfg),
	}, nil
}

// embedderIsNil 判断接口值是否为 nil（与 providerIsNil 对齐）。
func embedderIsNil(e Embedder) bool {
	return e == nil
}

// ============================================================
// 基于 go-openai 的通用实现
// ============================================================

// openaiEmbedder 是 Embedder 的通用实现。
// 国内外主流平台的 /v1/embeddings 接口均兼容 OpenAI 协议，单一实现即可覆盖。
type openaiEmbedder struct {
	name   ProviderName
	model  string
	client *openai.Client
}

func (e *openaiEmbedder) Name() ProviderName {
	if e == nil {
		return ""
	}

	return e.name
}

func (e *openaiEmbedder) Embed(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	if e == nil {
		return nil, ErrNilEmbedder
	}
	if req == nil {
		return nil, ErrNilEmbeddingRequest
	}
	if len(req.Input) == 0 {
		return nil, ErrEmptyEmbeddingInput
	}

	model := req.Model
	if model == "" {
		model = e.model
	}

	oReq := openai.EmbeddingRequestStrings{
		Input: req.Input,
		Model: openai.EmbeddingModel(model),
		User:  req.User,
	}
	if req.Dimensions != nil {
		oReq.Dimensions = *req.Dimensions
	}

	resp, err := e.client.CreateEmbeddings(ctx, oReq)
	if err != nil {
		return nil, WrapProviderError(e.name, err)
	}

	out := &EmbeddingResponse{
		Model: string(resp.Model),
		Usage: Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
		Data: make([]Embedding, 0, len(resp.Data)),
	}

	for _, d := range resp.Data {
		out.Data = append(out.Data, Embedding{
			Index:  d.Index,
			Vector: d.Embedding,
		})
	}

	return out, nil
}
