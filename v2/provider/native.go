package provider

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gtkit/json/v2"
)

var errSkipNativeStreamEvent = errors.New("skip native stream event")

const (
	defaultAnthropicBaseURL     = "https://api.anthropic.com"
	defaultAnthropicVersion     = "2023-06-01"
	defaultAnthropicModel       = "claude-sonnet-4-5"
	defaultGeminiBaseURL        = "https://generativelanguage.googleapis.com/v1beta"
	defaultGeminiModel          = "gemini-2.5-flash"
	defaultGeminiEmbeddingModel = "gemini-embedding-001"
	defaultNativeMaxTokens      = 4096
	maxNativeErrorBody          = 1 << 20
	maxSSETokenSize             = 1 << 20
)

// NativeProviderConfig configures native HTTP providers such as Anthropic and Gemini.
type NativeProviderConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient HTTPDoer
}

func (cfg NativeProviderConfig) validate(defaultModel string) error {
	var errs []error
	if cfg.APIKey == "" {
		errs = append(errs, errors.New("api key is required"))
	}
	if cfg.Model == "" && defaultModel == "" {
		errs = append(errs, errors.New("model is required"))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalidProviderConfig, errors.Join(errs...))
}

func normalizeNativeConfig(cfg NativeProviderConfig, defaultBaseURL, defaultModel string) (NativeProviderConfig, error) {
	if err := cfg.validate(defaultModel); err != nil {
		return NativeProviderConfig{}, err
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = DefaultHTTPClient()
	}
	return cfg, nil
}

type anthropicProvider struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient HTTPDoer
}

// NewAnthropicProvider creates a native Anthropic Claude Provider.
// The returned Provider is safe for concurrent use when cfg.HTTPClient is safe for concurrent use.
func NewAnthropicProvider(cfg NativeProviderConfig) (Provider, error) {
	normalized, err := normalizeNativeConfig(cfg, defaultAnthropicBaseURL, defaultAnthropicModel)
	if err != nil {
		return nil, err
	}
	return &anthropicProvider{
		apiKey:     normalized.APIKey,
		baseURL:    normalized.BaseURL,
		model:      normalized.Model,
		httpClient: normalized.HTTPClient,
	}, nil
}

func (p *anthropicProvider) Name() ProviderName {
	if p == nil {
		return ""
	}
	return ProviderAnthropic
}

func (p *anthropicProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	nativeReq, model, err := p.buildRequest(req, false)
	if err != nil {
		return nil, err
	}

	resp, metadata, err := doNativeJSON[anthropicResponse](ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         p.baseURL + "/v1/messages",
		Body:        nativeReq,
		Provider:    ProviderAnthropic,
		Model:       model,
		SetHeaders:  p.setHeaders,
		DecodeError: decodeAnthropicError,
	})
	if err != nil {
		return nil, err
	}

	parsed, err := anthropicResponseContent(resp)
	if err != nil {
		return nil, err
	}
	if resp.StopReason == "pause_turn" {
		// 暂停前服务端已执行的搜索会计费：错误携带真实用量与搜索元数据，
		// 供观测/计费层提取，不得按零消耗处理。
		return nil, &PauseTurnError{Usage: usageFromAnthropic(resp.Usage), Search: parsed.Search}
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type != ResponseFormatText {
		if structured, ok, err := anthropicStructuredContent(resp); err != nil {
			return nil, err
		} else if ok {
			parsed.Text = structured
			parsed.FinishReason = "stop"
			parsed.ToolCalls = nil
		}
	}
	if metadata.Model == "" {
		metadata.Model = firstString(resp.Model, model)
	}

	return &ChatResponse{
		Content:      parsed.Text,
		Reasoning:    parsed.Reasoning,
		FinishReason: parsed.FinishReason,
		Usage:        usageFromAnthropic(resp.Usage),
		Metadata:     metadata,
		Search:       parsed.Search,
		ToolCalls:    parsed.ToolCalls,
	}, nil
}

// DefaultModel 返回构造时配置的默认模型名（实现观测层的可选探测接口）。
func (p *anthropicProvider) DefaultModel() string {
	if p == nil {
		return ""
	}
	return p.model
}

func (p *anthropicProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	nativeReq, model, err := p.buildRequest(req, true)
	if err != nil {
		return nil, err
	}

	reader, metadata, err := doNativeStream(ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         p.baseURL + "/v1/messages",
		Body:        nativeReq,
		Provider:    ProviderAnthropic,
		Model:       model,
		SetHeaders:  p.setHeaders,
		DecodeError: decodeAnthropicError,
	})
	if err != nil {
		return nil, err
	}
	// usage 分散在 message_start（输入侧）与 message_delta（输出侧）事件中，
	// 在读取过程中累积，最终随 FinishReason 非空的 chunk 一次性给出。
	state := &anthropicStreamState{}
	return NewStreamReaderWithMetadata(func() (*StreamChunk, error) {
		return recvAnthropicStreamChunk(reader, state)
	}, reader.Close, metadata), nil
}

// CountTokens 调用 Anthropic /v1/messages/count_tokens 统计请求的输入 token 数
// （实现 TokenCounter 接口）。该端点免费；注意 Anthropic 官方将结果定义为
// 估算值，与实际计费 token 可能有少量偏差——适合摘要压缩阈值与额度预检，
// 不应作为无安全余量的硬额度判定，结算一律以响应 Usage 为准。
func (p *anthropicProvider) CountTokens(ctx context.Context, req *ChatRequest) (*TokenCountResponse, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	nativeReq, model, err := p.buildRequest(req, false)
	if err != nil {
		return nil, err
	}
	countReq := anthropicCountTokensRequest{
		Model:      nativeReq.Model,
		Messages:   nativeReq.Messages,
		System:     nativeReq.System,
		Tools:      nativeReq.Tools,
		ToolChoice: nativeReq.ToolChoice,
	}

	resp, metadata, err := doNativeJSON[anthropicCountTokensResponse](ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         p.baseURL + "/v1/messages/count_tokens",
		Body:        countReq,
		Provider:    ProviderAnthropic,
		Model:       model,
		SetHeaders:  p.setHeaders,
		DecodeError: decodeAnthropicError,
	})
	if err != nil {
		return nil, err
	}
	return &TokenCountResponse{
		Model:       model,
		TotalTokens: resp.InputTokens,
		Metadata:    metadata,
	}, nil
}

func (p *anthropicProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", defaultAnthropicVersion)
}

// anthropicMaxTokens 决定本次请求下发的 max_tokens。
//
// 思考预算需小于 max_tokens。调用方只给了预算、没设 MaxTokens 时，沿用本库的
// 默认上限会让请求被平台拒绝，而 max_tokens 根本不是调用方设过的值、错误无从
// 理解——这种情况下把上限抬到"预算 + 默认余量"，让只设预算的请求本身就成立。
// 显式设置过 MaxTokens 的一律尊重原值，与预算的冲突交由平台裁决。
func anthropicMaxTokens(requested int, thinking *anthropicThinking) int {
	if requested > 0 {
		return requested
	}
	if thinking != nil && thinking.BudgetTokens >= defaultNativeMaxTokens {
		return thinking.BudgetTokens + defaultNativeMaxTokens
	}
	return defaultNativeMaxTokens
}

func (p *anthropicProvider) buildRequest(req *ChatRequest, stream bool) (anthropicRequest, string, error) {
	if len(req.Messages) == 0 {
		return anthropicRequest{}, "", fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}
	if err := requireTextOnlyOutput(ProviderAnthropic, req.OutputModalities); err != nil {
		return anthropicRequest{}, "", err
	}
	if err := validateWebSearchRequest(req); err != nil {
		return anthropicRequest{}, "", err
	}
	if err := validateThinking(ProviderAnthropic, req.Thinking); err != nil {
		return anthropicRequest{}, "", err
	}
	thinking, err := anthropicThinkingParam(req.Thinking)
	if err != nil {
		return anthropicRequest{}, "", err
	}
	toolChoice, err := buildAnthropicToolChoice(req)
	if err != nil {
		return anthropicRequest{}, "", err
	}
	structuredTool, structuredChoice, err := anthropicStructuredTool(req.ResponseFormat)
	if err != nil {
		return anthropicRequest{}, "", err
	}
	// 结构化输出通过内部工具 + 强制 tool_choice 实现，与服务端搜索工具
	// 属于同一类不可往返的组合，禁止而非静默失效。
	if structuredTool != nil && hasWebSearchTool(req.Tools) {
		return anthropicRequest{}, "", fmt.Errorf(
			"%w: anthropic web search cannot be combined with structured output", ErrInvalidRequest)
	}
	tools, err := anthropicTools(req.Tools)
	if err != nil {
		return anthropicRequest{}, "", err
	}

	model := firstString(req.Model, p.model)
	out := anthropicRequest{
		Model:      model,
		MaxTokens:  anthropicMaxTokens(req.MaxTokens, thinking),
		Stream:     stream,
		Tools:      tools,
		ToolChoice: toolChoice,
		Thinking:   thinking,
	}
	if structuredTool != nil {
		out.Tools = append(out.Tools, *structuredTool)
		out.ToolChoice = structuredChoice
	}
	if req.Temperature != nil {
		out.Temperature = req.Temperature
	}
	if req.TopP != nil {
		out.TopP = req.TopP
	}
	if len(req.Stop) > 0 {
		out.StopSequences = append([]string(nil), req.Stop...)
	}
	if req.Seed != nil {
		return anthropicRequest{}, "", fmt.Errorf("%w: anthropic does not support seed", ErrInvalidRequest)
	}
	if req.CandidateCount > 0 {
		return anthropicRequest{}, "", fmt.Errorf("%w: anthropic does not support candidate count", ErrInvalidRequest)
	}

	for _, msg := range req.Messages {
		if err := appendAnthropicMessage(&out, msg); err != nil {
			return anthropicRequest{}, "", err
		}
	}
	return out, model, nil
}

func appendAnthropicMessage(out *anthropicRequest, msg Message) error {
	switch msg.Role {
	case RoleSystem:
		appendSystemText(&out.System, contentText(msg.Content))
	case RoleUser, RoleAssistant:
		parts, err := anthropicContentParts(msg.Content)
		if msg.Role == RoleAssistant {
			parts, err = anthropicAssistantParts(msg)
		}
		if err != nil {
			return err
		}
		out.Messages = append(out.Messages, anthropicMessage{
			Role:    anthropicRole(msg.Role),
			Content: parts,
		})
	case RoleTool:
		out.Messages = append(out.Messages, anthropicMessage{
			Role:    "user",
			Content: []anthropicContentPart{anthropicToolResultPart(msg)},
		})
	default:
		return fmt.Errorf("%w: unsupported anthropic role %q", ErrInvalidRequest, msg.Role)
	}
	return nil
}

type geminiProvider struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient HTTPDoer
}

// NewGeminiProvider creates a native Google Gemini Provider.
// The returned Provider is safe for concurrent use when cfg.HTTPClient is safe for concurrent use.
func NewGeminiProvider(cfg NativeProviderConfig) (Provider, error) {
	normalized, err := normalizeNativeConfig(cfg, defaultGeminiBaseURL, defaultGeminiModel)
	if err != nil {
		return nil, err
	}
	return &geminiProvider{
		apiKey:     normalized.APIKey,
		baseURL:    normalized.BaseURL,
		model:      normalized.Model,
		httpClient: normalized.HTTPClient,
	}, nil
}

func (p *geminiProvider) Name() ProviderName {
	if p == nil {
		return ""
	}
	return ProviderGemini
}

func (p *geminiProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	nativeReq, model, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}

	resp, metadata, err := doNativeJSON[geminiResponse](ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         p.modelURL(model, "generateContent"),
		Body:        nativeReq,
		Provider:    ProviderGemini,
		Model:       model,
		SetHeaders:  p.setHeaders,
		DecodeError: decodeGeminiError,
	})
	if err != nil {
		return nil, err
	}

	parsed, err := geminiResponseContent(resp)
	if err != nil {
		return nil, err
	}
	if metadata.Model == "" {
		metadata.Model = firstString(resp.ModelVersion, model)
	}
	usage := usageFromGemini(resp.UsageMetadata)
	search := applyGeminiGrounding(geminiGrounding(resp), &usage)
	return &ChatResponse{
		Content:      parsed.Text,
		Reasoning:    parsed.Reasoning,
		FinishReason: parsed.FinishReason,
		Usage:        usage,
		Metadata:     metadata,
		Parts:        parsed.Parts,
		Search:       search,
		ToolCalls:    parsed.ToolCalls,
	}, nil
}

// DefaultModel 返回构造时配置的默认模型名（实现观测层的可选探测接口）。
func (p *geminiProvider) DefaultModel() string {
	if p == nil {
		return ""
	}
	return p.model
}

func (p *geminiProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	nativeReq, model, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}

	streamURL, err := url.Parse(p.modelURL(model, "streamGenerateContent"))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidProviderConfig, err)
	}
	query := streamURL.Query()
	query.Set("alt", "sse")
	streamURL.RawQuery = query.Encode()

	reader, metadata, err := doNativeStream(ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         streamURL.String(),
		Body:        nativeReq,
		Provider:    ProviderGemini,
		Model:       model,
		SetHeaders:  p.setHeaders,
		DecodeError: decodeGeminiError,
	})
	if err != nil {
		return nil, err
	}
	state := &geminiStreamState{}
	return NewStreamReaderWithMetadata(func() (*StreamChunk, error) {
		return recvGeminiStreamChunk(reader, state)
	}, reader.Close, metadata), nil
}

func (p *geminiProvider) CountTokens(ctx context.Context, req *ChatRequest) (*TokenCountResponse, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}

	model := firstString(req.Model, p.model)
	out := geminiRequest{}
	if err := fillGeminiContents(&out, req.Messages); err != nil {
		return nil, err
	}
	nativeReq := geminiCountTokensRequest{
		Contents:          out.Contents,
		SystemInstruction: out.SystemInstruction,
	}

	resp, metadata, err := doNativeJSON[geminiCountTokensResponse](ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         p.modelURL(model, "countTokens"),
		Body:        nativeReq,
		Provider:    ProviderGemini,
		Model:       model,
		SetHeaders:  p.setHeaders,
		DecodeError: decodeGeminiError,
	})
	if err != nil {
		return nil, err
	}
	return &TokenCountResponse{
		Model:       model,
		TotalTokens: resp.TotalTokens,
		Metadata:    metadata,
	}, nil
}

func (p *geminiProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.apiKey)
}

func (p *geminiProvider) modelURL(model, action string) string {
	return p.baseURL + "/models/" + url.PathEscape(model) + ":" + action
}

func (p *geminiProvider) buildRequest(req *ChatRequest) (geminiRequest, string, error) {
	if len(req.Messages) == 0 {
		return geminiRequest{}, "", fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}
	generation, err := applyGeminiResponseFormat(geminiGeneration(req), req.ResponseFormat)
	if err != nil {
		return geminiRequest{}, "", err
	}
	modalities, err := geminiResponseModalities(req.OutputModalities)
	if err != nil {
		return geminiRequest{}, "", err
	}
	if len(modalities) > 0 {
		if generation == nil {
			generation = &geminiGenerationConfig{}
		}
		generation.ResponseModalities = modalities
	}
	if err := validateWebSearchRequest(req); err != nil {
		return geminiRequest{}, "", err
	}
	if err := validateThinking(ProviderGemini, req.Thinking); err != nil {
		return geminiRequest{}, "", err
	}
	thinkingConfig, err := geminiThinkingParam(req.Thinking)
	if err != nil {
		return geminiRequest{}, "", err
	}
	if thinkingConfig != nil {
		if generation == nil {
			generation = &geminiGenerationConfig{}
		}
		generation.ThinkingConfig = thinkingConfig
	}
	// 多候选与 grounding 的组合下各 candidate 的搜索归属不明确，
	// 无法给出可靠的计费与元数据归一，拒绝而非按首个 candidate 近似。
	webSearch := hasWebSearchTool(req.Tools)
	if req.CandidateCount > 1 && webSearch {
		return geminiRequest{}, "", fmt.Errorf(
			"%w: gemini web search does not support candidate count > 1", ErrInvalidRequest)
	}
	// 纯搜索场景（validateWebSearchRequest 已保证只剩 nil / Auto）不下发
	// functionCallingConfig——它只对函数工具有意义，对 google_search 无意义。
	var toolConfig *geminiToolConfig
	if !webSearch {
		toolConfig, err = buildGeminiToolConfig(req)
		if err != nil {
			return geminiRequest{}, "", err
		}
	}
	tools, err := geminiTools(req.Tools)
	if err != nil {
		return geminiRequest{}, "", err
	}

	model := firstString(req.Model, p.model)
	out := geminiRequest{
		GenerationConfig: generation,
		Tools:            tools,
		ToolConfig:       toolConfig,
	}
	if err := fillGeminiContents(&out, req.Messages); err != nil {
		return geminiRequest{}, "", err
	}
	return out, model, nil
}

type nativeHTTPRequest struct {
	Method      string
	URL         string
	Body        any
	Provider    ProviderName
	Model       string
	SetHeaders  func(*http.Request)
	DecodeError func(ProviderName, int, string, []byte) error
}

func doNativeJSON[T any](ctx context.Context, client HTTPDoer, cfg nativeHTTPRequest) (T, ResponseMetadata, error) {
	var zero T
	req, err := buildNativeHTTPRequest(ctx, cfg)
	if err != nil {
		return zero, ResponseMetadata{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return zero, ResponseMetadata{}, wrapNativeTransportError(cfg.Provider, err)
	}
	defer resp.Body.Close()

	metadata := metadataFromHeader(cfg.Provider, cfg.Model, resp.Header)
	metadata.StatusCode = resp.StatusCode
	metadata.Status = resp.Status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, metadata, decodeNativeHTTPError(cfg.Provider, resp, cfg.DecodeError)
	}

	if err := json.NewDecoder(resp.Body).Decode(&zero); err != nil {
		return zero, metadata, fmt.Errorf("[%s] decode response: %w", cfg.Provider, err)
	}
	return zero, metadata, nil
}

func doNativeStream(ctx context.Context, client HTTPDoer, cfg nativeHTTPRequest) (*sseReader, ResponseMetadata, error) {
	req, err := buildNativeHTTPRequest(ctx, cfg)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return nil, ResponseMetadata{}, wrapNativeTransportError(cfg.Provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, ResponseMetadata{}, decodeNativeHTTPError(cfg.Provider, resp, cfg.DecodeError)
	}

	return newSSEReader(resp.Body), metadataFromHeader(cfg.Provider, cfg.Model, resp.Header), nil
}

func buildNativeHTTPRequest(ctx context.Context, cfg nativeHTTPRequest) (*http.Request, error) {
	body, err := json.Marshal(cfg.Body)
	if err != nil {
		return nil, fmt.Errorf("[%s] marshal request: %w", cfg.Provider, err)
	}

	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidProviderConfig, err)
	}
	if cfg.SetHeaders != nil {
		cfg.SetHeaders(req)
	}
	return req, nil
}

func decodeNativeHTTPError(provider ProviderName, resp *http.Response, decode func(ProviderName, int, string, []byte) error) error {
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNativeErrorBody))
	if err != nil {
		return withRetryAfter(&ProviderError{
			Provider:   provider,
			Code:       CodeFromHTTPStatus(resp.StatusCode),
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Retryable:  RetryableByCode(CodeFromHTTPStatus(resp.StatusCode)),
			Message:    "read error response failed",
			Cause:      err,
		}, retryAfter)
	}
	if decode != nil {
		return withRetryAfter(decode(provider, resp.StatusCode, resp.Status, body), retryAfter)
	}
	code := CodeFromHTTPStatus(resp.StatusCode)
	return withRetryAfter(&ProviderError{
		Provider:   provider,
		Code:       code,
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Retryable:  RetryableByCode(code),
		Message:    string(body),
	}, retryAfter)
}

// parseRetryAfter 解析 Retry-After 头，支持「秒数」与「HTTP 日期」两种格式；
// 缺失、非法或已过期一律返回 0。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// withRetryAfter 在 err 链中存在 *ProviderError 时填充其 RetryAfter；d<=0 时原样返回。
func withRetryAfter(err error, d time.Duration) error {
	if d <= 0 {
		return err
	}
	if pe, ok := errors.AsType[*ProviderError](err); ok {
		pe.RetryAfter = d
	}
	return err
}

func wrapNativeTransportError(provider ProviderName, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &ProviderError{
		Provider:  provider,
		Code:      ErrorCodeNetwork,
		Retryable: RetryableByCode(ErrorCodeNetwork),
		Message:   err.Error(),
		Cause:     err,
	}
}

type sseReader struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
}

func newSSEReader(body io.ReadCloser) *sseReader {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSETokenSize)
	return &sseReader{body: body, scanner: scanner}
}

func (r *sseReader) Next() ([]byte, error) {
	var data strings.Builder
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			raw := strings.TrimSpace(data.String())
			if raw == "" || raw == "[DONE]" {
				return nil, io.EOF
			}
			var valid any
			if err := json.Unmarshal([]byte(raw), &valid); err != nil {
				data.Reset()
				continue
			}
			return []byte(raw), nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := r.scanner.Err(); err != nil {
		return nil, fmt.Errorf("sse stream: %w", err)
	}
	return nil, io.EOF
}

func (r *sseReader) Close() error {
	if r == nil || r.body == nil {
		return nil
	}
	if err := r.body.Close(); err != nil {
		return fmt.Errorf("close sse stream: %w", err)
	}
	return nil
}

func recvAnthropicStreamChunk(reader *sseReader, state *anthropicStreamState) (*StreamChunk, error) {
	for {
		data, err := reader.Next()
		if err != nil {
			if errors.Is(err, errSkipNativeStreamEvent) {
				continue
			}
			return nil, err
		}
		chunk, ok, err := anthropicStreamChunk(data, state)
		if err != nil {
			return nil, err
		}
		if ok {
			return chunk, nil
		}
	}
}

// geminiStreamState 保存流式解析的跨事件状态：usage 随流累计更新，
// grounding 对流上出现的各次 groundingMetadata 做去重合并
// （Gemini 未承诺快照的累积语义，合并可兼容累积与增量两种形态）。
type geminiStreamState struct {
	usage     geminiUsage
	grounding *geminiGroundingMetadata
}

func recvGeminiStreamChunk(reader *sseReader, state *geminiStreamState) (*StreamChunk, error) {
	for {
		data, err := reader.Next()
		if err != nil {
			return nil, err
		}
		chunk, ok, err := geminiStreamChunk(data, state)
		if errors.Is(err, errSkipNativeStreamEvent) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if ok {
			return chunk, nil
		}
	}
}

func geminiStreamChunk(data []byte, state *geminiStreamState) (*StreamChunk, bool, error) {
	var event geminiResponse
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, false, errSkipNativeStreamEvent
	}
	// usageMetadata 随流累计更新，最终值随 FinishReason 非空的 chunk 给出。
	if event.UsageMetadata != (geminiUsage{}) {
		state.usage = event.UsageMetadata
	}
	if gm := geminiGrounding(event); gm != nil {
		state.grounding = mergeGeminiGrounding(state.grounding, gm)
	}
	parsed, err := geminiResponseContent(event)
	if err != nil {
		return nil, false, err
	}
	if len(parsed.ToolCalls) > 0 {
		parsed.FinishReason = "tool_calls"
	}
	// 只携带思考摘要的事件也必须下发，否则 ReasoningDelta 会被整块丢弃。
	if parsed.Text == "" && parsed.Reasoning == "" && parsed.FinishReason == "" && len(parsed.Parts) == 0 {
		return nil, false, errSkipNativeStreamEvent
	}
	chunk := &StreamChunk{
		Delta:          parsed.Text,
		ReasoningDelta: parsed.Reasoning,
		FinishReason:   parsed.FinishReason,
		Model:          event.ModelVersion,
		Parts:          parsed.Parts,
		ToolCalls:      toolCallDeltas(parsed.ToolCalls),
	}
	if parsed.FinishReason != "" {
		chunk.Usage = usageFromGemini(state.usage)
		chunk.Search = applyGeminiGrounding(state.grounding, &chunk.Usage)
	}
	return chunk, true, nil
}

func anthropicStreamChunk(data []byte, state *anthropicStreamState) (*StreamChunk, bool, error) {
	var event anthropicStreamEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, false, errSkipNativeStreamEvent
	}
	switch event.Type {
	case "message_start":
		mergeAnthropicStreamUsage(&state.usage, event.Message.Usage)
		return &StreamChunk{Model: event.Message.Model}, true, nil
	case "content_block_start":
		// 服务端工具块（如原生 web search）由平台执行，其增量不外露为客户端
		// 工具调用；记录索引以抑制后续 input_json_delta。搜索结果块随
		// start 事件完整到达，就地提取来源。
		if anthropicServerToolBlock(event.ContentBlock.Type) {
			state.markServerToolBlock(event.Index)
			if event.ContentBlock.Type == "web_search_tool_result" {
				sources, searchErr := anthropicSearchResultContent(event.ContentBlock.Content)
				state.search = appendSearchSources(state.search, sources)
				state.search = appendSearchError(state.search, searchErr)
			}
			return nil, false, nil
		}
		if event.ContentBlock.Type == "tool_use" {
			return &StreamChunk{ToolCalls: []ToolCallDelta{{
				Index: event.Index,
				ID:    event.ContentBlock.ID,
				Function: FunctionCallDelta{
					Name: event.ContentBlock.Name,
				},
			}}}, true, nil
		}
	case "content_block_delta":
		return anthropicStreamDeltaChunk(event, state)
	case "message_delta":
		mergeAnthropicStreamUsage(&state.usage, event.Usage)
		if event.Delta.StopReason == "pause_turn" {
			return nil, false, &PauseTurnError{
				Usage:  usageFromAnthropic(state.usage),
				Search: state.finalizeSearch(),
			}
		}
		if event.Delta.StopReason != "" {
			finishReason := event.Delta.StopReason
			if finishReason == "tool_use" {
				finishReason = "tool_calls"
			}
			return &StreamChunk{
				FinishReason: finishReason,
				Usage:        usageFromAnthropic(state.usage),
				Search:       state.finalizeSearch(),
			}, true, nil
		}
	case "message_stop":
		return nil, false, io.EOF
	case "error":
		return nil, false, anthropicStreamProviderError(event)
	}
	return nil, false, nil
}

// anthropicStreamDeltaChunk 处理 content_block_delta 事件：正文、思考增量与
// 工具入参增量各归其位。未识别的 delta 类型不产出 chunk。
func anthropicStreamDeltaChunk(event anthropicStreamEvent, state *anthropicStreamState) (*StreamChunk, bool, error) {
	switch {
	case event.Delta.Type == "text_delta" && event.Delta.Text != "":
		return &StreamChunk{Delta: event.Delta.Text}, true, nil

	// 思考增量走 ReasoningDelta，不混入正文；随 thinking 块到达的
	// signature_delta 只用于把思考块回传给平台，本库不回传，忽略。
	case event.Delta.Type == "thinking_delta" && event.Delta.Thinking != "":
		return &StreamChunk{ReasoningDelta: event.Delta.Thinking}, true, nil

	case event.Delta.Type == "input_json_delta" && event.Delta.PartialJSON != "":
		if state.isServerToolBlock(event.Index) {
			state.appendServerToolInput(event.Index, event.Delta.PartialJSON)
			return nil, false, nil
		}
		return &StreamChunk{ToolCalls: []ToolCallDelta{{
			Index: event.Index,
			Function: FunctionCallDelta{
				Arguments: event.Delta.PartialJSON,
			},
		}}}, true, nil
	}
	return nil, false, nil
}

func toolCallDeltas(calls []ToolCall) []ToolCallDelta {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCallDelta, 0, len(calls))
	for i, call := range calls {
		out = append(out, ToolCallDelta{
			Index: i,
			ID:    call.ID,
			Function: FunctionCallDelta{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}
