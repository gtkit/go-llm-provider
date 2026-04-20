// Package provider 提供统一的多模型 LLM 调用抽象。
//
// 设计思路：
//   - 国内主流大模型（千问/百炼、智谱、DeepSeek、百度千帆、硅基流动等）
//     目前都兼容 OpenAI Chat Completions API，因此底层统一使用 go-openai 客户端。
//   - 上层通过 Provider 接口 + Registry 注册表实现多模型切换，
//     业务代码只需关心 ProviderName，不需要记各平台的 BaseURL 和细节差异。
//   - 支持流式和非流式两种调用模式，支持 Tool Use / Function Calling。
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

// Common provider validation and runtime errors.
var (
	// ErrNilProvider indicates that the caller passed a nil Provider.
	ErrNilProvider = errors.New("provider is nil")
	// ErrNilChatRequest indicates that the caller passed a nil chat request.
	ErrNilChatRequest = errors.New("chat request is nil")
	// ErrStreamNotInitialized indicates that the stream reader has no underlying stream.
	ErrStreamNotInitialized = errors.New("stream is not initialized")
	// ErrToolHandlerRequired indicates that RunToolLoop requires a tool handler.
	ErrToolHandlerRequired = errors.New("tool handler is required")
	// ErrInvalidProviderConfig indicates that ProviderConfig is missing required fields.
	ErrInvalidProviderConfig = errors.New("invalid provider config")
	// ErrInvalidToolChoice indicates that ChatRequest.ToolChoice contains an unsupported value.
	ErrInvalidToolChoice = errors.New("invalid tool choice")
	// ErrUnsupportedThinking indicates that EnableThinking is not supported by the selected provider.
	ErrUnsupportedThinking = errors.New("enable thinking is only supported for deepseek")
)

func providerIsNil(p Provider) bool {
	return p == nil
}

// ProviderName identifies a registered provider.
type ProviderName string

const (
	ProviderDeepSeek    ProviderName = "deepseek"
	ProviderZhipu       ProviderName = "zhipu"       // 智谱 AI (GLM)
	ProviderQwen        ProviderName = "qwen"        // 通义千问 / 阿里百炼 (DashScope)
	ProviderQianfan     ProviderName = "qianfan"     // 百度千帆 (OpenAI 兼容 V2)
	ProviderSiliconFlow ProviderName = "siliconflow" // 硅基流动
	ProviderMoonshot    ProviderName = "moonshot"    // Moonshot / Kimi
	ProviderOpenAI      ProviderName = "openai"      // 原版 OpenAI，兼容自部署
)

// ProviderConfig 描述一个供应商的连接配置。
type ProviderConfig struct {
	Name    ProviderName
	BaseURL string // 平台 API 地址，例如 "https://open.bigmodel.cn/api/paas/v4/"
	APIKey  string
	Model   string // 默认模型，如 "glm-4"、"deepseek-chat"
	OrgID   string // 可选，部分平台需要
}

// Provider 是统一的大模型调用接口。
type Provider interface {
	// Name 返回供应商标识。
	Name() ProviderName

	// Chat 发起一次非流式对话，返回完整的 assistant 回复。
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatStream 发起一次流式对话，返回一个 StreamReader。
	// 调用方需负责调用 StreamReader.Close()。
	ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error)
}

// ============================================================
// 请求 / 响应
// ============================================================

// ChatRequest 是与具体平台无关的对话请求。
type ChatRequest struct {
	Model       string // 可选，留空时使用 ProviderConfig.Model
	Messages    []Message
	MaxTokens   int
	Temperature *float32
	TopP        *float32
	Stop        []string

	// ---------- Tool Use ----------

	// Tools 声明本次请求可用的工具列表。
	// 如果为空，模型不会触发 tool call。
	Tools []Tool

	// ToolChoice 控制模型如何选择工具。
	// 可选值：
	//   - nil / ToolChoiceAuto：模型自行决定是否调用工具（默认）
	//   - ToolChoiceNone：禁止调用工具
	//   - ToolChoiceRequired：强制调用工具
	//   - ToolChoiceFunction{Name: "xxx"}：强制调用指定函数
	ToolChoice ToolChoiceOption

	// ParallelToolCalls 控制模型是否可以在一次回复中并行调用多个工具。
	// nil 表示使用平台默认行为（通常为 true）。
	ParallelToolCalls *bool

	// ---------- 其他 ----------

	// EnableThinking 用于支持 DeepSeek-R1 等思考模式模型。
	EnableThinking bool
}

// Role 定义消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // 工具执行结果的角色
)

// Message 是一条对话消息。
type Message struct {
	Role    Role
	Content string

	// ---------- Tool Use 相关字段 ----------

	// ToolCalls 仅在 Role == RoleAssistant 时有意义，
	// 表示模型请求调用的工具列表。
	ToolCalls []ToolCall

	// ToolCallID 仅在 Role == RoleTool 时有意义，
	// 用于将工具执行结果关联到对应的 ToolCall。
	ToolCallID string
}

// ChatResponse 是非流式对话的完整响应。
type ChatResponse struct {
	Content      string // assistant 回复的文本内容（可能为空，如果模型选择调用工具）
	FinishReason string // "stop", "length", "tool_calls" 等
	Usage        Usage

	// ToolCalls 当 FinishReason == "tool_calls" 时，包含模型请求调用的工具列表。
	ToolCalls []ToolCall
}

// HasToolCalls 返回模型是否请求了工具调用。
func (r *ChatResponse) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// AssistantMessage 将本次响应转换为可追加到对话历史的 assistant Message。
// 在 Tool Use 多轮循环中，需要将模型的 tool_calls 响应原样回传，
// 此方法简化了这一步骤。
func (r *ChatResponse) AssistantMessage() Message {
	return Message{
		Role:      RoleAssistant,
		Content:   r.Content,
		ToolCalls: r.ToolCalls,
	}
}

// Usage 记录 token 消耗。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ============================================================
// Tool Use 类型定义
// ============================================================

// Tool 描述一个可供模型调用的工具。
type Tool struct {
	// Function 描述工具的函数签名。
	Function FunctionDef
}

// FunctionDef 定义一个函数的名称、描述和参数 schema。
type FunctionDef struct {
	// Name 是函数名称，模型在 tool_call 中通过此名称引用。
	// 建议使用 snake_case，如 "get_weather"、"query_database"。
	Name string

	// Description 描述函数的用途，帮助模型判断何时该调用。
	// 描述越清晰，模型的调用决策越准确。
	Description string

	// Parameters 定义函数的参数 JSON Schema。
	// 推荐使用 ParamSchema 构建，也可直接传入 map[string]any / json.RawMessage。
	Parameters any
}

// ToolCall 是模型返回的一次工具调用请求。
type ToolCall struct {
	// ID 是本次 tool call 的唯一标识，
	// 在将工具执行结果回传时需要通过 Message.ToolCallID 关联。
	ID string

	// Function 包含要调用的函数名和参数。
	Function FunctionCall
}

// FunctionCall 是模型请求调用的具体函数信息。
type FunctionCall struct {
	// Name 是函数名称，对应 Tool.Function.Name。
	Name string

	// Arguments 是模型生成的 JSON 格式参数字符串。
	// 调用方需要自行 json.Unmarshal 到目标结构体。
	Arguments string
}

// ParseArguments 将 Arguments JSON 字符串解析到目标结构体。
func (fc *FunctionCall) ParseArguments(v any) error {
	if fc.Arguments == "" {
		return errors.New("empty arguments")
	}
	if err := json.Unmarshal([]byte(fc.Arguments), v); err != nil {
		return fmt.Errorf("parse arguments: %w", err)
	}

	return nil
}

// ToolChoiceOption 表示一个合法的 tool choice 值。
type ToolChoiceOption interface {
	applyToolChoice(*openai.ChatCompletionRequest) error
}

// ToolChoiceMode 是字符串形式的 tool choice。
type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
)

func (m ToolChoiceMode) applyToolChoice(req *openai.ChatCompletionRequest) error {
	switch m {
	case ToolChoiceAuto, ToolChoiceNone, ToolChoiceRequired:
		req.ToolChoice = string(m)
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidToolChoice, m)
	}
}

// ToolChoiceFunction 用于强制模型调用指定函数。
type ToolChoiceFunction struct {
	Name string
}

func (f ToolChoiceFunction) applyToolChoice(req *openai.ChatCompletionRequest) error {
	if f.Name == "" {
		return fmt.Errorf("%w: function name is required", ErrInvalidToolChoice)
	}

	req.ToolChoice = openai.ToolChoice{
		Type: openai.ToolTypeFunction,
		Function: openai.ToolFunction{
			Name: f.Name,
		},
	}

	return nil
}

// ToolResultMessage 是构建工具执行结果消息的便捷函数。
// toolCallID 必须与对应 ToolCall.ID 一致。
func ToolResultMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		Content:    content,
		ToolCallID: toolCallID,
	}
}

// ToolResultMessageJSON 与 ToolResultMessage 相同，但自动将 result 序列化为 JSON。
func ToolResultMessageJSON(toolCallID string, result any) (Message, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return Message{}, fmt.Errorf("marshal tool result: %w", err)
	}
	return ToolResultMessage(toolCallID, string(data)), nil
}

// ============================================================
// ParamSchema — 便捷的 JSON Schema 构建器
// ============================================================

// ParamSchema 用于构建工具参数的 JSON Schema。
//
// 用法示例：
//
//	provider.ParamSchema{
//	    Type: "object",
//	    Properties: map[string]provider.ParamSchema{
//	        "city": {Type: "string", Description: "城市名称"},
//	        "unit": {Type: "string", Enum: []string{"celsius", "fahrenheit"}},
//	    },
//	    Required: []string{"city"},
//	}
type ParamSchema struct {
	Type        string                 `json:"type"`
	Description string                 `json:"description,omitempty"`
	Properties  map[string]ParamSchema `json:"properties,omitempty"`
	Required    []string               `json:"required,omitempty"`
	Enum        []string               `json:"enum,omitempty"`
	Items       *ParamSchema           `json:"items,omitempty"` // 用于 type: "array"
}

// ============================================================
// StreamReader
// ============================================================

// StreamReader 包装流式响应，逐 chunk 读取。
type StreamReader struct {
	recv  func() (*StreamChunk, error)
	close func() error
}

// StreamChunk 是流式响应的一个片段。
type StreamChunk struct {
	Delta        string // 增量文本
	FinishReason string // 非空时表示流结束

	// ToolCalls 流式模式下的增量 tool call 数据。
	// 每个 chunk 可能只包含部分 tool call 信息（如部分 arguments），
	// 调用方需自行累积拼装。对于不涉及 tool call 的 chunk，此字段为 nil。
	ToolCalls []ToolCallDelta
}

// ToolCallDelta 是流式模式下 tool call 的增量片段。
type ToolCallDelta struct {
	Index    int    // tool call 在列表中的索引
	ID       string // 仅在首个 chunk 中非空
	Function FunctionCallDelta
}

// FunctionCallDelta 是流式模式下函数调用的增量片段。
type FunctionCallDelta struct {
	Name      string // 仅在首个 chunk 中非空
	Arguments string // 每个 chunk 追加的 arguments 片段
}

// NewStreamReader 基于回调构造一个与底层传输无关的流读取器。
// 可选扩展包可以通过它返回自定义流式结果，同时复用统一的 StreamChunk 抽象。
func NewStreamReader(recv func() (*StreamChunk, error), close func() error) *StreamReader {
	return &StreamReader{
		recv:  recv,
		close: close,
	}
}

// Recv 读取下一个 chunk。当流结束时返回 io.EOF。
func (r *StreamReader) Recv() (*StreamChunk, error) {
	if r == nil || r.recv == nil {
		return nil, ErrStreamNotInitialized
	}

	return r.recv()
}

// Close 关闭底层流。
func (r *StreamReader) Close() error {
	if r == nil || r.close == nil {
		return nil
	}

	return r.close()
}

// ============================================================
// 基于 go-openai 的通用实现
// ============================================================

// openaiProvider 是 Provider 的通用实现。
// 因为国内主流平台都兼容 OpenAI API，所以只需要一个实现类。
type openaiProvider struct {
	name   ProviderName
	model  string
	client *openai.Client
}

// Validate reports missing required ProviderConfig fields.
func (cfg ProviderConfig) Validate() error {
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

	return fmt.Errorf("%w: %w", ErrInvalidProviderConfig, errors.Join(errs...))
}

// NewProvider 根据配置创建一个 Provider 实例。
func NewProvider(cfg ProviderConfig) (Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	ocfg := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		ocfg.BaseURL = cfg.BaseURL
	}
	if cfg.OrgID != "" {
		ocfg.OrgID = cfg.OrgID
	}

	return &openaiProvider{
		name:   cfg.Name,
		model:  cfg.Model,
		client: openai.NewClientWithConfig(ocfg),
	}, nil
}

func (p *openaiProvider) Name() ProviderName {
	if p == nil {
		return ""
	}

	return p.name
}

func (p *openaiProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	oReq, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.CreateChatCompletion(ctx, oReq)
	if err != nil {
		return nil, fmt.Errorf("[%s] chat completion: %w", p.name, err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("[%s] empty choices in response", p.name)
	}

	choice := resp.Choices[0]
	chatResp := &ChatResponse{
		Content:      choice.Message.Content,
		FinishReason: string(choice.FinishReason),
		Usage: Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	// 映射 tool calls
	if len(choice.Message.ToolCalls) > 0 {
		chatResp.ToolCalls = make([]ToolCall, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
				ID: tc.ID,
				Function: FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	return chatResp, nil
}

func (p *openaiProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	oReq, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}
	oReq.Stream = true

	stream, err := p.client.CreateChatCompletionStream(ctx, oReq)
	if err != nil {
		return nil, fmt.Errorf("[%s] chat stream: %w", p.name, err)
	}

	return NewStreamReader(func() (*StreamChunk, error) {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if err != nil {
			return nil, fmt.Errorf("stream recv: %w", err)
		}

		chunk := &StreamChunk{}
		if len(resp.Choices) > 0 {
			delta := resp.Choices[0].Delta
			chunk.Delta = delta.Content
			chunk.FinishReason = string(resp.Choices[0].FinishReason)

			// 映射流式 tool call delta
			if len(delta.ToolCalls) > 0 {
				chunk.ToolCalls = make([]ToolCallDelta, 0, len(delta.ToolCalls))
				for _, tc := range delta.ToolCalls {
					d := ToolCallDelta{
						ID: tc.ID,
						Function: FunctionCallDelta{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}
					if tc.Index != nil {
						d.Index = *tc.Index
					}
					chunk.ToolCalls = append(chunk.ToolCalls, d)
				}
			}
		}

		return chunk, nil
	}, stream.Close), nil
}

func (p *openaiProvider) buildRequest(req *ChatRequest) (openai.ChatCompletionRequest, error) {
	if req == nil {
		return openai.ChatCompletionRequest{Model: p.model}, nil
	}

	model := req.Model
	if model == "" {
		model = p.model
	}

	msgs := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		om := openai.ChatCompletionMessage{
			Role:    string(m.Role),
			Content: m.Content,
		}

		// 传递 assistant 消息中的 tool calls（多轮 tool use 场景需要）
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
			om.ToolCalls = make([]openai.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}

		// 传递 tool 角色消息的 ToolCallID
		if m.Role == RoleTool && m.ToolCallID != "" {
			om.ToolCallID = m.ToolCallID
		}

		msgs = append(msgs, om)
	}

	oReq := openai.ChatCompletionRequest{
		Model:    model,
		Messages: msgs,
	}

	if req.MaxTokens > 0 {
		oReq.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		oReq.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		oReq.TopP = *req.TopP
	}
	if len(req.Stop) > 0 {
		oReq.Stop = req.Stop
	}

	// 构建 tools
	if len(req.Tools) > 0 {
		oReq.Tools = make([]openai.Tool, 0, len(req.Tools))
		for _, t := range req.Tools {
			oReq.Tools = append(oReq.Tools, openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			})
		}
	}

	// 构建 tool_choice
	if req.ToolChoice != nil {
		if err := req.ToolChoice.applyToolChoice(&oReq); err != nil {
			return openai.ChatCompletionRequest{}, err
		}
	}

	if req.ParallelToolCalls != nil {
		oReq.ParallelToolCalls = req.ParallelToolCalls
	}

	if req.EnableThinking {
		if p.name != ProviderDeepSeek {
			return openai.ChatCompletionRequest{}, fmt.Errorf("%w: provider %q", ErrUnsupportedThinking, p.name)
		}

		oReq.ChatTemplateKwargs = map[string]any{
			"enable_thinking": true,
		}
	}

	return oReq, nil
}

// ============================================================
// Registry：多 Provider 注册与切换
// ============================================================

// Registry 管理多个 Provider 实例，支持按名称获取。
type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderName]Provider
	fallback  ProviderName // 默认 provider
}

// NewRegistry 创建一个空的注册表。
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[ProviderName]Provider),
	}
}

// Register 注册一个 Provider。如果是注册表中的第一个，会自动设为 fallback。
//
// Register 仅保证忽略接口值本身为 nil 的 Provider。对于 typed nil 的接口值，
// 它会继续调用 p.Name()；自定义 Provider 实现应保证 Name 在 nil receiver 下不会 panic，
// 或者在传入 Register 前由调用方自行避免 typed nil。
func (r *Registry) Register(p Provider) {
	if providerIsNil(p) {
		return
	}

	name := p.Name()
	if name == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers[name] = p
	if r.fallback == "" {
		r.fallback = name
	}
}

// SetDefault 设置默认 Provider。
func (r *Registry) SetDefault(name ProviderName) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.providers[name]; !ok {
		return fmt.Errorf("provider %q not registered", name)
	}
	r.fallback = name
	return nil
}

// Get 按名称获取 Provider。
func (r *Registry) Get(name ProviderName) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	return p, nil
}

// Default 返回默认的 Provider。
func (r *Registry) Default() (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.fallback == "" {
		return nil, errors.New("no provider registered")
	}
	return r.providers[r.fallback], nil
}

// Names 返回所有已注册的 Provider 名称。
func (r *Registry) Names() []ProviderName {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]ProviderName, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
