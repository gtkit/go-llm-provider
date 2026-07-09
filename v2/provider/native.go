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

	content, finishReason, toolCalls, err := anthropicResponseContent(resp)
	if err != nil {
		return nil, err
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type != ResponseFormatText {
		if structured, ok, err := anthropicStructuredContent(resp); err != nil {
			return nil, err
		} else if ok {
			content = structured
			finishReason = "stop"
			toolCalls = nil
		}
	}
	if metadata.Model == "" {
		metadata.Model = firstString(resp.Model, model)
	}

	return &ChatResponse{
		Content:      content,
		FinishReason: finishReason,
		Usage:        usageFromAnthropic(resp.Usage),
		Metadata:     metadata,
		ToolCalls:    toolCalls,
	}, nil
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

	reader, err := doNativeStream(ctx, p.httpClient, nativeHTTPRequest{
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
	var usageAcc anthropicUsage
	return NewStreamReader(func() (*StreamChunk, error) {
		return recvAnthropicStreamChunk(reader, &usageAcc)
	}, reader.Close), nil
}

func (p *anthropicProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", defaultAnthropicVersion)
}

func (p *anthropicProvider) buildRequest(req *ChatRequest, stream bool) (anthropicRequest, string, error) {
	if len(req.Messages) == 0 {
		return anthropicRequest{}, "", fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}
	toolChoice, err := buildAnthropicToolChoice(req)
	if err != nil {
		return anthropicRequest{}, "", err
	}
	structuredTool, structuredChoice, err := anthropicStructuredTool(req.ResponseFormat)
	if err != nil {
		return anthropicRequest{}, "", err
	}

	model := firstString(req.Model, p.model)
	out := anthropicRequest{
		Model:      model,
		MaxTokens:  defaultNativeMaxTokens,
		Stream:     stream,
		Tools:      anthropicTools(req.Tools),
		ToolChoice: toolChoice,
	}
	if structuredTool != nil {
		out.Tools = append(out.Tools, *structuredTool)
		out.ToolChoice = structuredChoice
	}
	if req.MaxTokens > 0 {
		out.MaxTokens = req.MaxTokens
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

	content, finishReason, toolCalls, err := geminiResponseContent(resp)
	if err != nil {
		return nil, err
	}
	if metadata.Model == "" {
		metadata.Model = firstString(resp.ModelVersion, model)
	}
	return &ChatResponse{
		Content:      content,
		FinishReason: finishReason,
		Usage:        usageFromGemini(resp.UsageMetadata),
		Metadata:     metadata,
		ToolCalls:    toolCalls,
	}, nil
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

	reader, err := doNativeStream(ctx, p.httpClient, nativeHTTPRequest{
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
	var usageAcc geminiUsage
	return NewStreamReader(func() (*StreamChunk, error) {
		return recvGeminiStreamChunk(reader, &usageAcc)
	}, reader.Close), nil
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
	toolConfig, err := buildGeminiToolConfig(req)
	if err != nil {
		return geminiRequest{}, "", err
	}

	model := firstString(req.Model, p.model)
	out := geminiRequest{
		GenerationConfig: generation,
		Tools:            geminiTools(req.Tools),
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

func doNativeStream(ctx context.Context, client HTTPDoer, cfg nativeHTTPRequest) (*sseReader, error) {
	req, err := buildNativeHTTPRequest(ctx, cfg)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return nil, wrapNativeTransportError(cfg.Provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, decodeNativeHTTPError(cfg.Provider, resp, cfg.DecodeError)
	}

	return newSSEReader(resp.Body), nil
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

func recvAnthropicStreamChunk(reader *sseReader, usageAcc *anthropicUsage) (*StreamChunk, error) {
	for {
		data, err := reader.Next()
		if err != nil {
			if errors.Is(err, errSkipNativeStreamEvent) {
				continue
			}
			return nil, err
		}
		chunk, ok, err := anthropicStreamChunk(data, usageAcc)
		if err != nil {
			return nil, err
		}
		if ok {
			return chunk, nil
		}
	}
}

func recvGeminiStreamChunk(reader *sseReader, usageAcc *geminiUsage) (*StreamChunk, error) {
	for {
		data, err := reader.Next()
		if err != nil {
			return nil, err
		}
		chunk, ok, err := geminiStreamChunk(data, usageAcc)
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

func geminiStreamChunk(data []byte, usageAcc *geminiUsage) (*StreamChunk, bool, error) {
	var event geminiResponse
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, false, errSkipNativeStreamEvent
	}
	// usageMetadata 随流累计更新，最终值随 FinishReason 非空的 chunk 给出。
	if event.UsageMetadata != (geminiUsage{}) {
		*usageAcc = event.UsageMetadata
	}
	content, finishReason, toolCalls, err := geminiResponseContent(event)
	if err != nil {
		return nil, false, err
	}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if content == "" && finishReason == "" {
		return nil, false, errSkipNativeStreamEvent
	}
	chunk := &StreamChunk{
		Delta:        content,
		FinishReason: finishReason,
		ToolCalls:    toolCallDeltas(toolCalls),
	}
	if finishReason != "" {
		chunk.Usage = usageFromGemini(*usageAcc)
	}
	return chunk, true, nil
}

func anthropicStreamChunk(data []byte, usageAcc *anthropicUsage) (*StreamChunk, bool, error) {
	var event anthropicStreamEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, false, errSkipNativeStreamEvent
	}
	switch event.Type {
	case "message_start":
		mergeAnthropicStreamUsage(usageAcc, event.Message.Usage)
		return &StreamChunk{}, true, nil
	case "content_block_start":
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
		if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
			return &StreamChunk{Delta: event.Delta.Text}, true, nil
		}
		if event.Delta.Type == "input_json_delta" && event.Delta.PartialJSON != "" {
			return &StreamChunk{ToolCalls: []ToolCallDelta{{
				Index: event.Index,
				Function: FunctionCallDelta{
					Arguments: event.Delta.PartialJSON,
				},
			}}}, true, nil
		}
	case "message_delta":
		mergeAnthropicStreamUsage(usageAcc, event.Usage)
		if event.Delta.StopReason != "" {
			finishReason := event.Delta.StopReason
			if finishReason == "tool_use" {
				finishReason = "tool_calls"
			}
			return &StreamChunk{
				FinishReason: finishReason,
				Usage:        usageFromAnthropic(*usageAcc),
			}, true, nil
		}
	case "message_stop":
		return nil, false, io.EOF
	case "error":
		return nil, false, anthropicStreamProviderError(event)
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
