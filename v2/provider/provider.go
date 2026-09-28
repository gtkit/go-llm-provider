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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gtkit/json/v2"

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
	// ErrToolNotOffered 表示模型请求的工具不在本轮下发的工具集中；工具循环不执行该调用，
	// 而以此错误调用 ToolErrorEncoder 生成工具结果回传模型。
	ErrToolNotOffered = errors.New("tool not offered in this round")
	// ErrToolArgumentsInvalid 表示工具调用参数不是合法 JSON（常见于输出被 max_tokens 截断）；
	// 工具循环不调用 handler，而以此错误调用 ToolErrorEncoder 生成工具结果回传模型。
	ErrToolArgumentsInvalid = errors.New("tool arguments are not valid JSON")
	// ErrToolHandlerPanic 表示工具执行期间的回调（handler、ToolResultTransformer、ToolErrorEncoder
	// 等）发生 panic；工具循环恢复 panic 并以包装此错误的 error 中止，错误文本含工具名、panic 值与堆栈。
	ErrToolHandlerPanic = errors.New("tool execution panicked")
	// ErrInvalidProviderConfig indicates that ProviderConfig is missing required fields.
	ErrInvalidProviderConfig = errors.New("invalid provider config")
	// ErrInvalidToolChoice indicates that ChatRequest.ToolChoice contains an unsupported value.
	ErrInvalidToolChoice = errors.New("invalid tool choice")
	// ErrNilEmbedder indicates that the caller passed a nil Embedder.
	ErrNilEmbedder = errors.New("embedder is nil")
	// ErrNilEmbeddingRequest indicates that the caller passed a nil embedding request.
	ErrNilEmbeddingRequest = errors.New("embedding request is nil")
	// ErrEmptyEmbeddingInput indicates that EmbeddingRequest.Input is empty.
	ErrEmptyEmbeddingInput = errors.New("embedding input is empty")
	// ErrInvalidEmbedderConfig indicates that EmbedderConfig is missing required fields.
	ErrInvalidEmbedderConfig = errors.New("invalid embedder config")
	// ErrNilFileUploadRequest 表示调用方传入了 nil 的文件上传请求。
	ErrNilFileUploadRequest = errors.New("file upload request is nil")
	// ErrNilStructuredTarget indicates that GenerateJSONInto received a nil target.
	ErrNilStructuredTarget = errors.New("structured target is nil")
	// ErrStructuredDecode indicates that structured response content could not be decoded.
	ErrStructuredDecode = errors.New("structured response decode failed")
	// ErrStructuredValidation indicates that decoded structured response content failed caller validation.
	ErrStructuredValidation = errors.New("structured response validation failed")
	// ErrEmptyEmbeddingVector indicates that a vector operation received no vector data.
	ErrEmptyEmbeddingVector = errors.New("embedding vector is empty")
	// ErrZeroEmbeddingVector indicates that cosine similarity received a zero-length magnitude vector.
	ErrZeroEmbeddingVector = errors.New("embedding vector magnitude is zero")
	// ErrEmbeddingDimensionMismatch indicates that vector dimensions differ.
	ErrEmbeddingDimensionMismatch = errors.New("embedding vector dimensions mismatch")
	// ErrUnsupportedCapability 表示所选供应商不支持请求的能力。
	ErrUnsupportedCapability = errors.New("unsupported provider capability")

	// ErrAuth 表示鉴权失败。
	// 与 *ProviderError 互认：errors.Is(err, ErrAuth) 在 Code == ErrorCodeAuth 时返回 true。
	ErrAuth = errors.New("authentication failed")

	// ErrRateLimit 表示请求被平台限流。
	// 与 *ProviderError 互认：errors.Is(err, ErrRateLimit) 在 Code == ErrorCodeRateLimit 时返回 true。
	ErrRateLimit = errors.New("rate limit exceeded")

	// ErrTimeout 表示请求超时。
	// 与 *ProviderError 互认：errors.Is(err, ErrTimeout) 在 Code == ErrorCodeTimeout 时返回 true。
	ErrTimeout = errors.New("provider request timeout")

	// ErrStreamIdleTimeout 表示响应体在 NewIdleTimeoutDoer 设定的空闲超时内未读到任何数据。
	// 读取返回的错误是 Code 为 ErrorCodeTimeout 的 *ProviderError，同时满足 errors.Is(err, ErrTimeout)。
	ErrStreamIdleTimeout = errors.New("response body idle timeout")

	// ErrContextLength 表示输入超过模型上下文长度限制。
	// 与 *ProviderError 互认：errors.Is(err, ErrContextLength) 在 Code == ErrorCodeContextLength 时返回 true。
	ErrContextLength = errors.New("context length exceeded")

	// ErrContentFilter 表示请求被平台内容安全策略拦截。
	// 与 *ProviderError 互认：errors.Is(err, ErrContentFilter) 在 Code == ErrorCodeContentFilter 时返回 true。
	ErrContentFilter = errors.New("content filtered")

	// ErrInvalidRequest 表示请求参数无效或不被平台接受。
	// 与 *ProviderError 互认：errors.Is(err, ErrInvalidRequest) 在 Code == ErrorCodeInvalidRequest 时返回 true。
	ErrInvalidRequest = errors.New("invalid provider request")

	// ErrServerError 表示平台服务端异常。
	// 与 *ProviderError 互认：errors.Is(err, ErrServerError) 在 Code == ErrorCodeServerError 时返回 true。
	ErrServerError = errors.New("provider server error")

	// ErrNetwork 表示发起请求时遇到网络层错误。
	// 与 *ProviderError 互认：errors.Is(err, ErrNetwork) 在 Code == ErrorCodeNetwork 时返回 true。
	ErrNetwork = errors.New("provider network error")

	// ErrModelNotPriced 表示 PricingTable 中没有该模型的费率条目。
	ErrModelNotPriced = errors.New("model not found in pricing table")

	// ErrQuotaExceeded 表示调用方配额已用尽，请求未发往平台。
	ErrQuotaExceeded = errors.New("user quota exceeded")

	// ErrInvalidPricing 表示 PricingTable 中的费率或传入的用量数据非法，
	// 包括负值、费率越界、token 子集关系不成立或最终金额溢出。
	ErrInvalidPricing = errors.New("invalid pricing rate or usage")

	// ErrBreakerOpen 表示熔断器处于打开状态，请求未发往平台。
	ErrBreakerOpen = errors.New("circuit breaker is open")

	// ErrNilBreaker 表示调用方传入了 nil 的熔断器。
	ErrNilBreaker = errors.New("breaker is nil")

	// ErrLocalRateLimited 表示请求被客户端侧限流器拦下，未发往平台。
	// 与平台回传的 ErrRateLimit 区分：后者是平台已经拒绝了请求。
	ErrLocalRateLimited = errors.New("local rate limit exceeded")

	// ErrNilRateLimiter 表示调用方传入了 nil 的限流器。
	ErrNilRateLimiter = errors.New("rate limiter is nil")

	// ErrInvalidBalanceStrategy 表示 BalanceOptions.Strategy 取值不被支持。
	ErrInvalidBalanceStrategy = errors.New("invalid balance strategy")

	// ErrNilPricingRegistry 表示在 nil 的 *PricingRegistry 上执行了替换操作。
	ErrNilPricingRegistry = errors.New("pricing registry is nil")

	// ErrNilReranker 表示调用方传入了 nil 的 Reranker。
	ErrNilReranker = errors.New("reranker is nil")
	// ErrNilRerankRequest 表示调用方传入了 nil 的重排序请求。
	ErrNilRerankRequest = errors.New("rerank request is nil")
	// ErrEmptyRerankQuery 表示 RerankRequest.Query 为空。
	ErrEmptyRerankQuery = errors.New("rerank query is empty")
	// ErrEmptyRerankDocuments 表示 RerankRequest.Documents 为空。
	ErrEmptyRerankDocuments = errors.New("rerank documents are empty")
	// ErrInvalidRerankerConfig 表示 RerankerConfig 缺少必填字段。
	ErrInvalidRerankerConfig = errors.New("invalid reranker config")
)

func providerIsNil(p Provider) bool {
	return p == nil
}

// HTTPDoer is the minimal HTTP client contract used by Provider and Embedder.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultResponseHeaderTimeout 是等待服务端响应头的传输层上限。
//
// 非流式调用下平台要等整段生成完才发响应头，思考模型高强度推理一次可达数分钟，
// 该值过小会把正常的长推理请求误判为网络故障。总时长预算应由调用方 ctx 控制，
// 这里只兜住服务端彻底无响应的极端情况。
const defaultResponseHeaderTimeout = 15 * time.Minute

// DefaultHTTPClient returns the package default reusable HTTP client.
//
// The client intentionally leaves http.Client.Timeout unset so request budgets
// stay under the caller's context, while transport-level timeouts prevent
// hanging dial, TLS, and response-header waits.
func DefaultHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: defaultResponseHeaderTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// ProviderName identifies a registered provider.
type ProviderName string

const (
	ProviderDeepSeek    ProviderName = "deepseek"
	ProviderZhipu       ProviderName = "zhipu"       // 智谱 AI (GLM)
	ProviderGLM         ProviderName = ProviderZhipu // Alias for GLM / Zhipu AI.
	ProviderQwen        ProviderName = "qwen"        // 通义千问 / 阿里百炼 (DashScope)
	ProviderQianfan     ProviderName = "qianfan"     // 百度千帆 (OpenAI 兼容 V2)
	ProviderSiliconFlow ProviderName = "siliconflow" // 硅基流动
	ProviderMoonshot    ProviderName = "moonshot"    // Moonshot / Kimi
	ProviderArk         ProviderName = "ark"         // 火山方舟 / 豆包，OpenAI 兼容
	ProviderKimi        ProviderName = ProviderMoonshot
	ProviderOpenAI      ProviderName = "openai"    // 原版 OpenAI，兼容自部署
	ProviderAnthropic   ProviderName = "anthropic" // Anthropic Claude native Messages API
	ProviderGemini      ProviderName = "gemini"    // Google Gemini native Generative Language API
	ProviderOllama      ProviderName = "ollama"    // Local Ollama native API
	ProviderXAI         ProviderName = "xai"       // xAI Grok，OpenAI 兼容
	ProviderGroq        ProviderName = "groq"      // Groq，OpenAI 兼容
	ProviderMistral     ProviderName = "mistral"   // Mistral AI，OpenAI 兼容
	ProviderCohere      ProviderName = "cohere"    // Cohere 兼容 API
	ProviderAzureOpenAI ProviderName = "azure"     // Azure OpenAI
	ProviderBedrock     ProviderName = "bedrock"   // Amazon Bedrock OpenAI 兼容 API
)

// ProviderConfig 描述一个供应商的连接配置。
type ProviderConfig struct {
	Name       ProviderName
	BaseURL    string // 平台 API 地址，例如 "https://open.bigmodel.cn/api/paas/v4/"
	APIKey     string
	Model      string // 默认模型，如 "glm-4"、"deepseek-chat"
	OrgID      string // 可选，部分平台需要
	HTTPClient HTTPDoer

	// SupportsReasoningEffort 声明该平台接受 OpenAI 标准的 reasoning_effort 字段，
	// 从而允许本次接入使用 Thinking.Effort。
	//
	// 仅对库未收录的平台生效——自定义 Name 接入的 OpenAI 兼容平台、自建推理服务。
	// 库不认识这些平台，无从判断它们接受哪些推理参数，默认一律拒绝 Thinking
	// 以免静默丢弃调用方的意图；知情的调用方用这个字段解除限制。
	// 内置预设（如 ProviderOpenAI、ProviderDeepSeek）的支持范围由库判定，该字段被忽略。
	//
	// Thinking.Enabled 与 Thinking.BudgetTokens 没有对应的开关：各平台把它们落在
	// 互不相同的私有字段上（DeepSeek / 智谱 / Kimi / 火山方舟用顶层 thinking、
	// 百炼用顶层 enable_thinking），库无从代为映射，这两个字段对未收录平台始终返回 ErrInvalidRequest。
	SupportsReasoningEffort bool

	// SupportsReasoningContent 声明该平台接受 assistant 消息上的 reasoning_content
	// 字段回传，从而允许把 Message.Reasoning 随历史一并下发。
	//
	// 同样仅对库未收录的平台生效。思考模型开工具后普遍要求把上一轮的推理内容原样
	// 回传（DeepSeek 缺失直接 400），而 OpenAI 官方端点会拒绝这个非标准字段，
	// 库无从替未知平台判断，默认不下发；知情的调用方用这个字段解除限制。
	SupportsReasoningContent bool
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

	// Seed 在供应商支持时请求确定性采样。
	Seed *int

	// CandidateCount 在供应商支持时请求返回多个候选回复。
	CandidateCount int

	// Thinking controls provider-specific reasoning behavior.
	Thinking *Thinking

	// ResponseFormat requests structured output when supported by the provider.
	ResponseFormat *ResponseFormat

	// StreamUsage 控制流式调用是否请求 token 统计。
	// 仅影响 OpenAI 兼容路径的 stream_options.include_usage 下发：
	// nil 或 true 时下发（默认），false 时不下发（兼容不认识该参数的老网关/代理，
	// 此时 StreamChunk.Usage 恒为零值）。原生路径（Anthropic/Gemini/Ollama）的
	// usage 由响应自带，忽略本字段。
	StreamUsage *bool

	// OutputModalities 声明期望模型输出的内容模态，空表示纯文本（默认）。
	// 当前仅 Gemini 原生 provider 支持图像输出（需选用支持图像生成的模型，
	// 如 gemini-2.0-flash-preview-image-generation）；其他 provider 收到
	// 非文本模态会返回 ErrInvalidRequest，不静默丢弃。
	OutputModalities []Modality
}

// Modality 声明模型输出的内容形态。
type Modality string

const (
	// ModalityText 表示文本输出。
	ModalityText Modality = "text"
	// ModalityImage 表示图像输出，结果通过 ChatResponse.Parts / StreamChunk.Parts 返回。
	ModalityImage Modality = "image"
)

// requireTextOnlyOutput 校验不支持多模态输出的 provider 未被请求非文本模态。
func requireTextOnlyOutput(name ProviderName, modalities []Modality) error {
	for _, modality := range modalities {
		if modality != ModalityText {
			return fmt.Errorf("%w: %s does not support %q output modality", ErrInvalidRequest, name, modality)
		}
	}
	return nil
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
	Content []ContentPart

	// ---------- 推理往返字段（仅 Role == RoleAssistant） ----------

	// Reasoning 是该轮 assistant 的推理文本。思考模型在工具循环中普遍要求把
	// 上一轮的推理内容原样回传（DeepSeek 带 tools 时缺失直接 400，Kimi K3 要求
	// 回传完整 assistant 消息），OpenAI 兼容路径把它映射为 reasoning_content
	// （仅对已知接受该字段的平台下发，见 ProviderConfig.SupportsReasoningContent）。
	// 由 ChatResponse.AssistantMessage 自动填充；不需要回传时可置空以节省输入 token。
	Reasoning string

	// ThinkingBlocks 是 Anthropic 签发的思考块（含签名或加密数据），
	// 开启思考并使用工具时必须原样回传，否则平台拒绝请求；其他平台忽略该字段。
	ThinkingBlocks []ThinkingBlock

	// ---------- Tool Use 相关字段 ----------

	// ToolCalls 仅在 Role == RoleAssistant 时有意义，
	// 表示模型请求调用的工具列表。
	ToolCalls []ToolCall

	// ToolCallID 仅在 Role == RoleTool 时有意义，
	// 用于将工具执行结果关联到对应的 ToolCall。
	ToolCallID string

	// ToolName 仅在 Role == RoleTool 时有意义：被调用的函数名。
	// Gemini 的 functionResponse 要求携带与调用一致的函数名；留空时库会在同一
	// 请求的历史中按 ToolCallID 反查，用 ToolResultMessageFor 构造可免去反查。
	ToolName string
}

// ThinkingBlock 是平台签发的一段思考块，需在后续轮次原样回传（当前仅 Anthropic）。
// 平台按 Thinking 与 Signature 校验一致性，二者不得改写、拆分或合并。
type ThinkingBlock struct {
	// Thinking 是可读的思考文本；平台省略显示时可能为空但 Signature 仍在。
	Thinking string
	// Signature 是平台对该思考块的签名。
	Signature string
	// Redacted 是加密思考块（redacted_thinking）的数据，非空时 Thinking 与 Signature 为空。
	Redacted string
}

// ChatResponse 是非流式对话的完整响应。
type ChatResponse struct {
	Content      string // assistant 回复的文本内容（可能为空，如果模型选择调用工具）
	Reasoning    string // assistant 的推理/思考内容
	FinishReason string // "stop", "length", "tool_calls" 等
	Usage        Usage
	Metadata     ResponseMetadata

	// ThinkingBlocks 是 Anthropic 返回的带签名思考块，语义见 Message.ThinkingBlocks。
	ThinkingBlocks []ThinkingBlock

	// Parts 承载非文本输出（如图像生成结果），复用 ContentPart 载体：
	// 图像为 ImageData + MIMEType。纯文本回复时为 nil，文本始终在 Content。
	// 需配合 ChatRequest.OutputModalities 使用。
	Parts []ContentPart

	// Search 是原生联网搜索的结果元数据（查询与来源），
	// 未启用搜索或未触发搜索时为 nil。
	Search *SearchMetadata

	// ToolCalls 当 FinishReason == "tool_calls" 时，包含模型请求调用的工具列表。
	ToolCalls []ToolCall
}

// HasToolCalls 返回模型是否请求了工具调用。
func (r *ChatResponse) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// AssistantMessage 将本次响应转换为可追加到对话历史的 assistant Message。
// 在 Tool Use 多轮循环中，需要将模型的 tool_calls 响应原样回传，
// 此方法简化了这一步骤。非文本输出（Parts）一并并入消息内容。
//
// 推理内容（Reasoning / ThinkingBlocks）与工具调用上的 ThoughtSignature 一并携带：
// 思考模型在工具循环中要求回传这些内容，各协议映射只对接受它们的平台下发。
// 不需要回传推理文本时，调用方可自行清空返回消息的 Reasoning。
func (r *ChatResponse) AssistantMessage() Message {
	content := make([]ContentPart, 0, 1+len(r.Parts))
	content = append(content, TextPart(r.Content))
	content = append(content, r.Parts...)
	return Message{
		Role:           RoleAssistant,
		Content:        content,
		Reasoning:      r.Reasoning,
		ThinkingBlocks: slices.Clone(r.ThinkingBlocks),
		ToolCalls:      r.ToolCalls,
	}
}

// Usage 记录 token 消耗。
//
// 各字段遵循统一的包含关系，便于跨 provider 按 token 计费：
//   - PromptTokens 为全部输入 token，已包含 CacheReadTokens 与 CacheWriteTokens
//     （Anthropic 原始 input_tokens 不含缓存部分，本库已归一化）；
//   - CompletionTokens 为全部输出 token，已包含 ReasoningTokens；
//   - CacheWriteTokens 已包含 CacheWrite5mTokens 与 CacheWrite1hTokens；
//   - TotalTokens 通常等于 PromptTokens + CompletionTokens；
//     provider 返回了总量时以其返回值为准。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	ReasoningTokens  int

	// CacheReadTokens 是命中提示词缓存的输入 token 数。
	// 对应 Anthropic cache_read_input_tokens、OpenAI prompt_tokens_details.cached_tokens、
	// Gemini cachedContentTokenCount；缓存计价通常低于常规输入。
	CacheReadTokens int

	// CacheWriteTokens 是写入提示词缓存的输入 token 数，
	// 对应 Anthropic cache_creation_input_tokens；缓存写入计价通常高于常规输入。
	CacheWriteTokens int

	// CacheWrite5mTokens 与 CacheWrite1hTokens 是按缓存 TTL 分档的写入 token 数，
	// 对应 Anthropic usage.cache_creation 的 ephemeral_5m_input_tokens 与
	// ephemeral_1h_input_tokens，二者都是 CacheWriteTokens 的子集。
	// 分档的意义在计价：1 小时缓存的写入单价高于 5 分钟档，
	// 只有分档统计才能按各自费率算准（见 ModelRate.CacheWrite5mPer1M / CacheWrite1hPer1M）。
	// 平台未上报明细时二者为 0，CacheWriteTokens 整体按 CacheWritePer1M 计价。
	CacheWrite5mTokens int
	CacheWrite1hTokens int

	TotalTokens int

	// WebSearchRequests 是"按搜索次数"口径的原生联网搜索用量，
	// 不参与 token 包含关系：Anthropic 为平台返回的计费搜索次数
	// （usage.server_tool_use.web_search_requests），Gemini 为响应
	// groundingMetadata.webSearchQueries 的数量（Gemini 3 系按 query 计费的口径）。
	WebSearchRequests int

	// WebSearchGroundedPrompts 是"按触发 grounding 的请求"口径的搜索用量
	// （单次调用为 0 或 1，聚合后可累加）：仅 Gemini 上报（Gemini 2.5 系按
	// grounded prompt 计费的口径），Anthropic 恒为 0。
	// 两种口径在 ModelRate 中按配置的费率二选一计费，见 PricingTable.Cost。
	WebSearchGroundedPrompts int
}

// ============================================================
// Tool Use 类型定义
// ============================================================

// Tool 描述一个可供模型调用的工具。
// WebSearch 为 nil 时表示函数工具（Function 必填）；
// 非 nil 时表示厂商原生联网搜索工具，Function 字段被忽略。
type Tool struct {
	// Function 描述工具的函数签名。
	Function FunctionDef

	// WebSearch 声明厂商原生联网搜索工具（服务端执行，不产生客户端 ToolCall）。
	// 用 WebSearchTool / WebSearchToolWithOptions 构造。
	// 仅 Anthropic 与 Gemini 原生路径支持，其他 provider 返回 ErrInvalidRequest。
	WebSearch *WebSearchOptions
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

	// Strict 请求平台按 schema 严格约束工具入参（OpenAI 兼容路径的 strict、
	// Anthropic 的 strict）。nil 表示不下发、沿用平台默认；不支持该字段的
	// 原生路径（Gemini）忽略。严格模式对 schema 有额外要求（如 OpenAI 要求
	// 全部属性进 required 且 additionalProperties 为 false），由平台校验。
	Strict *bool
}

// ToolCall 是模型返回的一次工具调用请求。
type ToolCall struct {
	// ID 是本次 tool call 的唯一标识，
	// 在将工具执行结果回传时需要通过 Message.ToolCallID 关联。
	ID string

	// Function 包含要调用的函数名和参数。
	Function FunctionCall

	// ThoughtSignature 是 Gemini 3 随 functionCall 签发的不透明签名，
	// 回传该调用时必须原样携带，缺失时平台返回 400；其他平台为空。
	ThoughtSignature string
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
// 有 ToolCall 在手时优先用 ToolResultMessageFor，它会一并带上函数名。
func ToolResultMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		Content:    []ContentPart{TextPart(content)},
		ToolCallID: toolCallID,
	}
}

// ToolResultMessageFor 按对应的 ToolCall 构建工具执行结果消息，
// 同时携带 ToolCallID 与函数名（Gemini functionResponse 必需）。
func ToolResultMessageFor(call ToolCall, content string) Message {
	msg := ToolResultMessage(call.ID, content)
	msg.ToolName = call.Function.Name
	return msg
}

// ToolResultMessageJSON 与 ToolResultMessage 相同，但自动将 result 序列化为 JSON。
func ToolResultMessageJSON(toolCallID string, result any) (Message, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return Message{}, fmt.Errorf("marshal tool result: %w", err)
	}
	return ToolResultMessage(toolCallID, string(data)), nil
}

// ToolResultMessageJSONFor 与 ToolResultMessageFor 相同，但自动将 result 序列化为 JSON。
func ToolResultMessageJSONFor(call ToolCall, result any) (Message, error) {
	msg, err := ToolResultMessageJSON(call.ID, result)
	if err != nil {
		return Message{}, err
	}
	msg.ToolName = call.Function.Name
	return msg, nil
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
	Type                 string                 `json:"type"`
	Description          string                 `json:"description,omitempty"`
	Properties           map[string]ParamSchema `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	Enum                 []string               `json:"enum,omitempty"`
	Items                *ParamSchema           `json:"items,omitempty"` // 用于 type: "array"
	AdditionalProperties *bool                  `json:"additionalProperties,omitempty"`
}

// ============================================================
// StreamReader
// ============================================================

// StreamReader 包装流式响应，逐 chunk 读取。
type StreamReader struct {
	recv     func() (*StreamChunk, error)
	close    func() error
	metadata ResponseMetadata
}

// StreamChunk 是流式响应的一个片段。
type StreamChunk struct {
	Delta          string // 增量文本
	ReasoningDelta string // 增量推理文本
	FinishReason   string // 非空时表示流结束

	// ThinkingSignature 是 Anthropic 对当前思考块的签名，随该块结束时到达一次；
	// 非空表示"此前累积的 ReasoningDelta 构成一个完整思考块"。回传见 Message.ThinkingBlocks。
	ThinkingSignature string

	// RedactedThinking 是 Anthropic 整块到达的加密思考块数据，语义见 ThinkingBlock.Redacted。
	RedactedThinking string

	// Model 是响应侧回传的实际模型名（请求未指定 model 时即 provider 的默认模型），
	// 通常仅部分 chunk 携带非空值；计费按最后一个非空值采用。
	Model string

	// Parts 承载本 chunk 到达的非文本输出（如图像），语义同 ChatResponse.Parts；
	// 非文本内容通常整块到达而非增量。
	Parts []ContentPart

	// Usage 是本次请求的 token 统计，仅在流尾部的 chunk 上非零：
	// 可能与 FinishReason 同 chunk（Anthropic/Gemini/Ollama），
	// 也可能出现在其后、io.EOF 之前的收尾 chunk（OpenAI 兼容端点）。
	// 需要统计用量时应读取至 io.EOF，并采用最后一个非零 Usage。
	Usage Usage

	// Search 是原生联网搜索的结果元数据，仅随 FinishReason 非空的 chunk
	// 给出（流上聚合后的最终值），未触发搜索时为 nil。
	Search *SearchMetadata

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

	// ThoughtSignature 是 Gemini 3 随 functionCall 下发的签名，语义见 ToolCall.ThoughtSignature。
	ThoughtSignature string
}

// FunctionCallDelta 是流式模式下函数调用的增量片段。
type FunctionCallDelta struct {
	Name      string // 仅在首个 chunk 中非空
	Arguments string // 每个 chunk 追加的 arguments 片段
}

// NewStreamReader 基于回调构造一个与底层传输无关的流读取器。
// 可选扩展包可以通过它返回自定义流式结果，同时复用统一的 StreamChunk 抽象。
func NewStreamReader(recv func() (*StreamChunk, error), closeFn func() error) *StreamReader {
	return &StreamReader{
		recv:  recv,
		close: closeFn,
	}
}

// NewStreamReaderWithMetadata 在 NewStreamReader 基础上携带流创建时的
// 响应元数据（RequestID、响应头白名单等），供计费与对账读取。
func NewStreamReaderWithMetadata(recv func() (*StreamChunk, error), closeFn func() error, metadata ResponseMetadata) *StreamReader {
	return &StreamReader{
		recv:     recv,
		close:    closeFn,
		metadata: metadata,
	}
}

// Metadata 返回流创建时的响应元数据；未携带时为零值。
func (r *StreamReader) Metadata() ResponseMetadata {
	if r == nil {
		return ResponseMetadata{}
	}
	return r.metadata
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
	name  ProviderName
	model string
	// supportsReasoningEffort / supportsReasoningContent 是 ProviderConfig 同名字段的副本。
	// 这里只保存调用方的声明、不保存解析结果：内置映射表始终优先，
	// 零值因此等价于"完全按库内映射表判定"，与不使用该声明时的行为一致。
	supportsReasoningEffort  bool
	supportsReasoningContent bool
	client                   *openai.Client
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
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = DefaultHTTPClient()
	}
	if providerCapturesSearchSources(cfg.Name) {
		// 该平台会在响应体里带回内建搜索的来源，而 go-openai 只解码它认识的
		// 字段，需要在 HTTP 层旁路取出（见 respfields.go）。
		httpClient = &searchCaptureDoer{next: httpClient}
	}
	if needsExtraFields(cfg.Name) {
		// 该平台有 go-openai 无法表达的顶层扩展字段（如方舟的 thinking），
		// 通过包装 HTTPDoer 在发送前注入（见 extrafields.go）。
		httpClient = &extraFieldsDoer{next: httpClient}
	}
	ocfg.HTTPClient = httpClient

	return &openaiProvider{
		name:                     cfg.Name,
		model:                    cfg.Model,
		supportsReasoningEffort:  cfg.SupportsReasoningEffort,
		supportsReasoningContent: cfg.SupportsReasoningContent,
		client:                   openai.NewClientWithConfig(ocfg),
	}, nil
}

// DefaultModel 返回构造时配置的默认模型名（实现观测层的可选探测接口）。
func (p *openaiProvider) DefaultModel() string {
	if p == nil {
		return ""
	}
	return p.model
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

	ctx = withExtraFields(ctx, p.name, req)
	var capture *searchCapture
	if needsSearchCapture(p.name, req) {
		capture = newSearchCapture(false)
		ctx = withSearchCapture(ctx, capture)
	}
	resp, err := p.client.CreateChatCompletion(ctx, oReq)
	if err != nil {
		return nil, WrapProviderError(p.name, err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("[%s] empty choices in response", p.name)
	}

	choice := resp.Choices[0]
	chatResp := &ChatResponse{
		Content:      choice.Message.Content,
		Reasoning:    choice.Message.ReasoningContent,
		FinishReason: string(choice.FinishReason),
		Metadata:     metadataFromHeader(p.name, resp.Model, resp.Header()),
		Usage:        usageFromOpenAI(resp.Usage),
		Search:       compatSearchMetadata(p.name, req, capture),
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
	// 让服务端在最终 chunk 返回整次请求的 token 统计（OpenAI 兼容端点普遍支持）；
	// 遇到不认识 stream_options 的老网关，可通过 StreamUsage=false 关闭。
	if req.StreamUsage == nil || *req.StreamUsage {
		oReq.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
	}

	ctx = withExtraFields(ctx, p.name, req)
	var capture *searchCapture
	if needsSearchCapture(p.name, req) {
		capture = newSearchCapture(true)
		ctx = withSearchCapture(ctx, capture)
	}
	stream, err := p.client.CreateChatCompletionStream(ctx, oReq)
	if err != nil {
		return nil, WrapProviderError(p.name, err)
	}

	return NewStreamReaderWithMetadata(func() (*StreamChunk, error) {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if err != nil {
			return nil, WrapProviderError(p.name, err)
		}
		chunk := openaiStreamChunk(resp)
		// 搜索元数据只随终止 chunk 透出，与 StreamChunk.Search 的既有契约一致。
		if chunk.FinishReason != "" {
			chunk.Search = compatSearchMetadata(p.name, req, capture)
		}
		return chunk, nil
	}, stream.Close, metadataFromHeader(p.name, req.Model, stream.Header())), nil
}

// openaiStreamChunk 将 go-openai 的流式响应帧映射为统一 StreamChunk。
func openaiStreamChunk(resp openai.ChatCompletionStreamResponse) *StreamChunk {
	chunk := &StreamChunk{Model: resp.Model}
	if resp.Usage != nil {
		chunk.Usage = usageFromOpenAI(*resp.Usage)
	}
	if len(resp.Choices) == 0 {
		return chunk
	}

	delta := resp.Choices[0].Delta
	chunk.Delta = delta.Content
	chunk.ReasoningDelta = delta.ReasoningContent
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
	return chunk
}

func (p *openaiProvider) buildRequest(req *ChatRequest) (openai.ChatCompletionRequest, error) {
	if req == nil {
		return openai.ChatCompletionRequest{Model: p.model}, nil
	}
	if err := requireTextOnlyOutput(p.name, req.OutputModalities); err != nil {
		return openai.ChatCompletionRequest{}, err
	}

	model := req.Model
	if model == "" {
		model = p.model
	}

	support := resolveThinkingSupport(p.name, thinkingSupport{
		effort:        p.supportsReasoningEffort,
		reasoningEcho: p.supportsReasoningContent,
	})
	msgs := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg, err := buildOpenAIMessage(m, support.reasoningEcho)
		if err != nil {
			return openai.ChatCompletionRequest{}, err
		}
		msgs = append(msgs, msg)
	}

	oReq := openai.ChatCompletionRequest{
		Model:    model,
		Messages: msgs,
	}

	applyMaxTokens(&oReq, p.name, req.MaxTokens)
	if req.Temperature != nil {
		oReq.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		oReq.TopP = *req.TopP
	}
	if len(req.Stop) > 0 {
		oReq.Stop = req.Stop
	}
	if req.Seed != nil {
		oReq.Seed = req.Seed
	}
	if req.CandidateCount > 0 {
		oReq.N = req.CandidateCount
	}

	// 支持内建搜索的兼容平台才走搜索校验：未支持的平台由 buildOpenAITools 以
	// "该 provider 不支持原生搜索"拒绝，那个错误更贴近调用方的实际问题。
	if supportsCompatWebSearch(p.name) {
		if err := validateWebSearchRequest(req); err != nil {
			return openai.ChatCompletionRequest{}, err
		}
		if err := validateCompatWebSearch(p.name, req.Tools); err != nil {
			return openai.ChatCompletionRequest{}, err
		}
	}

	tools, err := buildOpenAITools(p.name, req.Tools)
	if err != nil {
		return openai.ChatCompletionRequest{}, err
	}
	oReq.Tools = tools

	// 构建 tool_choice
	if req.ToolChoice != nil {
		if err := req.ToolChoice.applyToolChoice(&oReq); err != nil {
			return openai.ChatCompletionRequest{}, err
		}
	}

	if req.ParallelToolCalls != nil {
		oReq.ParallelToolCalls = req.ParallelToolCalls
	}

	if err := applyThinking(&oReq, p.name, support, req.Thinking); err != nil {
		return openai.ChatCompletionRequest{}, err
	}
	if err := applyResponseFormat(&oReq, req.ResponseFormat); err != nil {
		return openai.ChatCompletionRequest{}, err
	}

	return oReq, nil
}

func reasoningTokens(usage openai.Usage) int {
	if usage.CompletionTokensDetails == nil {
		return 0
	}
	return usage.CompletionTokensDetails.ReasoningTokens
}

func cachedTokens(usage openai.Usage) int {
	if usage.PromptTokensDetails == nil {
		return 0
	}
	return usage.PromptTokensDetails.CachedTokens
}

// usageFromOpenAI 将 openai.Usage 映射为统一 Usage。
// OpenAI 的 prompt_tokens 已包含 cached_tokens，completion_tokens 已包含 reasoning_tokens，
// 与统一语义一致，直接映射。
func usageFromOpenAI(usage openai.Usage) Usage {
	return Usage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ReasoningTokens:  reasoningTokens(usage),
		CacheReadTokens:  cachedTokens(usage),
		TotalTokens:      usage.TotalTokens,
	}
}

// maxCompletionTokensProviders 是要求用 max_completion_tokens 表达输出上限的平台：
// OpenAI 官方与 Azure 的推理模型已拒绝 max_tokens。其余 OpenAI 兼容平台
// （DeepSeek、百炼、智谱、方舟等）普遍只认 max_tokens，不在此列。
var maxCompletionTokensProviders = map[ProviderName]struct{}{
	ProviderOpenAI:      {},
	ProviderAzureOpenAI: {},
}

func usesMaxCompletionTokens(name ProviderName) bool {
	_, ok := maxCompletionTokensProviders[name]
	return ok
}

// applyMaxTokens 按平台把输出上限写到 max_completion_tokens 或 max_tokens。
// OpenAI 官方（含 Azure）的推理模型已拒绝 max_tokens；其余兼容平台普遍只认 max_tokens。
func applyMaxTokens(req *openai.ChatCompletionRequest, name ProviderName, maxTokens int) {
	if maxTokens <= 0 {
		return
	}
	if usesMaxCompletionTokens(name) {
		req.MaxCompletionTokens = maxTokens
		return
	}
	req.MaxTokens = maxTokens //nolint:staticcheck // 非 OpenAI 官方的兼容平台只认 max_tokens
}

// buildOpenAITools 把统一工具声明映射为 OpenAI 函数工具；原生搜索工具在该协议下没有对应形态。
func buildOpenAITools(name ProviderName, tools []Tool) ([]openai.Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		if t.WebSearch != nil {
			if !supportsCompatWebSearch(name) {
				return nil, fmt.Errorf("%w: %s does not support native web search tools", ErrInvalidRequest, name)
			}
			// 声明位置不在 tools 里的平台（百炼走顶层参数）在这里跳过，
			// 由 extrafields.go 的注入通道下发。
			if searchTool, ok := compatWebSearchTool(name, t.WebSearch); ok {
				out = append(out, searchTool)
			}

			continue
		}
		def := &openai.FunctionDefinition{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		}
		if t.Function.Strict != nil {
			def.Strict = *t.Function.Strict
		}
		out = append(out, openai.Tool{Type: openai.ToolTypeFunction, Function: def})
	}
	return out, nil
}

// applyThinking 把统一 Thinking 映射为 OpenAI 兼容平台的推理参数。
// 无映射的字段被拦下并返回 ErrInvalidRequest，不静默丢弃。
// support 由 resolveThinkingSupport 解析：内置预设取 thinkingSupportByProvider，
// 未收录的平台取调用方经 ProviderConfig 的声明。
//
// Enabled 在各平台落在 go-openai 无法表达的顶层扩展字段上（DeepSeek / 智谱 / Kimi /
// 方舟的 thinking、百炼的 enable_thinking），由 extrafields.go 在发送前注入；
// 这里只处理类型化字段 reasoning_effort。
func applyThinking(req *openai.ChatCompletionRequest, name ProviderName, support thinkingSupport, thinking *Thinking) error {
	if req == nil || thinking == nil {
		return nil
	}
	if err := validateThinkingWith(name, support, thinking); err != nil {
		return err
	}

	// Effort 走 OpenAI 标准的 reasoning_effort，所有支持该字段的平台共用一套映射——
	// 由 support 驱动而非按平台名枚举，自定义接入的平台声明后同样生效。
	if thinking.Effort != "" && support.effort {
		req.ReasoningEffort = thinking.Effort
	}
	return nil
}

func applyResponseFormat(req *openai.ChatCompletionRequest, format *ResponseFormat) error {
	if req == nil || format == nil {
		return nil
	}

	switch format.Type {
	case ResponseFormatText:
		req.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeText,
		}
	case ResponseFormatJSONObject:
		req.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		}
	case ResponseFormatJSONSchema:
		schema, err := marshalResponseFormatSchema(format.Schema)
		if err != nil {
			return fmt.Errorf("marshal response format schema: %w", err)
		}

		strict := false
		if format.Strict != nil {
			strict = *format.Strict
		}

		req.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:   format.Name,
				Schema: schema,
				Strict: strict,
			},
		}
	default:
		return fmt.Errorf("unsupported response format type %q", format.Type)
	}

	return nil
}

// buildOpenAIMessage 把统一 Message 映射为 OpenAI 兼容消息。
// echoReasoning 为 true 时把 assistant 的 Reasoning 写入 reasoning_content——
// 该字段是 DeepSeek / Kimi / 百炼 / 智谱等平台的扩展，OpenAI 官方端点会拒绝，
// 因此只对已知接受它的平台下发。
func buildOpenAIMessage(m Message, echoReasoning bool) (openai.ChatCompletionMessage, error) {
	om := openai.ChatCompletionMessage{
		Role: string(m.Role),
	}
	if m.Role == RoleAssistant && echoReasoning {
		om.ReasoningContent = m.Reasoning
	}

	for _, part := range m.Content {
		if part.Type == ContentTypeFile {
			return openai.ChatCompletionMessage{}, fmt.Errorf("%w: openai compatible chat completions do not support file parts; upload via FileService and reference the file per platform convention (FileIDSystemMessage or FileContent)", ErrInvalidRequest)
		}
	}

	switch {
	case len(m.Content) == 1 && m.Content[0].Type == ContentTypeText:
		om.Content = m.Content[0].Text
	case len(m.Content) > 0:
		om.MultiContent = make([]openai.ChatMessagePart, 0, len(m.Content))
		for _, part := range m.Content {
			switch part.Type {
			case ContentTypeText:
				om.MultiContent = append(om.MultiContent, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeText,
					Text: part.Text,
				})
			case ContentTypeImageURL:
				source, ok := part.preferredImageSource()
				if !ok {
					continue
				}
				om.MultiContent = append(om.MultiContent, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    source,
						Detail: openai.ImageURLDetail(part.ImageDetail),
					},
				})
			case ContentTypeFile:
				continue
			}
		}
	default:
		om.Content = ""
	}

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

	if m.Role == RoleTool && m.ToolCallID != "" {
		om.ToolCallID = m.ToolCallID
	}

	return om, nil
}

// ============================================================
// Registry：多 Provider 注册与切换
// ============================================================

// Registry 管理多个 Provider 与 Embedder 实例，支持按名称获取。
// Provider 与 Embedder 在内部使用两张独立的 map，互不影响。
type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderName]Provider
	fallback  ProviderName // 默认 provider

	embedders        map[ProviderName]Embedder
	fallbackEmbedder ProviderName // 默认 embedder
}

// NewRegistry 创建一个空的注册表。
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[ProviderName]Provider),
		embedders: make(map[ProviderName]Embedder),
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

// ============================================================
// Embedder 注册与切换
// ============================================================

// RegisterEmbedder 注册一个 Embedder。如果是注册表中的第一个 embedder，
// 会自动设为默认 embedder。
//
// 与 Register 对齐：typed nil 的 Embedder 实现需保证 Name() 不 panic，
// 或调用方自行避免 typed nil。
func (r *Registry) RegisterEmbedder(e Embedder) {
	if embedderIsNil(e) {
		return
	}

	name := e.Name()
	if name == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.embedders[name] = e
	if r.fallbackEmbedder == "" {
		r.fallbackEmbedder = name
	}
}

// GetEmbedder 按名称获取 Embedder。
func (r *Registry) GetEmbedder(name ProviderName) (Embedder, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.embedders[name]
	if !ok {
		return nil, fmt.Errorf("embedder %q not registered", name)
	}
	return e, nil
}

// DefaultEmbedder 返回默认的 Embedder。
func (r *Registry) DefaultEmbedder() (Embedder, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.fallbackEmbedder == "" {
		return nil, errors.New("no embedder registered")
	}
	return r.embedders[r.fallbackEmbedder], nil
}

// SetDefaultEmbedder 设置默认 Embedder。
func (r *Registry) SetDefaultEmbedder(name ProviderName) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.embedders[name]; !ok {
		return fmt.Errorf("embedder %q not registered", name)
	}
	r.fallbackEmbedder = name
	return nil
}

// EmbedderNames 返回所有已注册的 Embedder 名称。
func (r *Registry) EmbedderNames() []ProviderName {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]ProviderName, 0, len(r.embedders))
	for name := range r.embedders {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
