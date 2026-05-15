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
	"strings"

	"github.com/gtkit/json"
)

var errSkipNativeStreamEvent = errors.New("skip native stream event")

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicVersion = "2023-06-01"
	defaultAnthropicModel   = "claude-sonnet-4-5"
	defaultGeminiBaseURL    = "https://generativelanguage.googleapis.com/v1beta"
	defaultGeminiModel      = "gemini-2.5-flash"
	defaultNativeMaxTokens  = 4096
	maxNativeErrorBody      = 1 << 20
	maxSSETokenSize         = 1 << 20
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

	nativeReq, err := p.buildRequest(req, false)
	if err != nil {
		return nil, err
	}

	resp, err := doNativeJSON[anthropicResponse](ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         p.baseURL + "/v1/messages",
		Body:        nativeReq,
		Provider:    ProviderAnthropic,
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
	return &ChatResponse{
		Content:      content,
		FinishReason: finishReason,
		Usage: Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
		ToolCalls: toolCalls,
	}, nil
}

func (p *anthropicProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}

	nativeReq, err := p.buildRequest(req, true)
	if err != nil {
		return nil, err
	}

	httpReq, err := buildNativeHTTPRequest(ctx, nativeHTTPRequest{
		Method:     http.MethodPost,
		URL:        p.baseURL + "/v1/messages",
		Body:       nativeReq,
		Provider:   ProviderAnthropic,
		SetHeaders: p.setHeaders,
	})
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, wrapNativeTransportError(ProviderAnthropic, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, decodeNativeHTTPError(ProviderAnthropic, resp, decodeAnthropicError)
	}

	reader := newSSEReader(resp.Body)
	return NewStreamReader(func() (*StreamChunk, error) {
		return recvAnthropicStreamChunk(reader)
	}, reader.Close), nil
}

func (p *anthropicProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", defaultAnthropicVersion)
}

func (p *anthropicProvider) buildRequest(req *ChatRequest, stream bool) (anthropicRequest, error) {
	if len(req.Messages) == 0 {
		return anthropicRequest{}, fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}
	if stream && (len(req.Tools) > 0 || req.ToolChoice != nil || req.ParallelToolCalls != nil) {
		return anthropicRequest{}, fmt.Errorf("%w: anthropic native streaming tool use is not implemented", ErrInvalidRequest)
	}
	toolChoice, err := buildAnthropicToolChoice(req)
	if err != nil {
		return anthropicRequest{}, err
	}

	model := firstString(req.Model, p.model)
	out := anthropicRequest{
		Model:      model,
		MaxTokens:  defaultNativeMaxTokens,
		Stream:     stream,
		Tools:      anthropicTools(req.Tools),
		ToolChoice: toolChoice,
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

	for _, msg := range req.Messages {
		switch msg.Role {
		case RoleSystem:
			appendSystemText(&out.System, msg.Content)
		case RoleUser, RoleAssistant:
			parts := anthropicContentParts(msg.Content)
			if msg.Role == RoleAssistant {
				parts, err = anthropicAssistantParts(msg)
			}
			if err != nil {
				return anthropicRequest{}, err
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
			return anthropicRequest{}, fmt.Errorf("%w: unsupported anthropic role %q", ErrInvalidRequest, msg.Role)
		}
	}
	return out, nil
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

	nativeReq, model, err := p.buildRequest(req, false)
	if err != nil {
		return nil, err
	}

	resp, err := doNativeJSON[geminiResponse](ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         p.modelURL(model, "generateContent"),
		Body:        nativeReq,
		Provider:    ProviderGemini,
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
	return &ChatResponse{
		Content:      content,
		FinishReason: finishReason,
		Usage: Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		},
		ToolCalls: toolCalls,
	}, nil
}

func (p *geminiProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
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

	streamURL, err := url.Parse(p.modelURL(model, "streamGenerateContent"))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidProviderConfig, err)
	}
	query := streamURL.Query()
	query.Set("alt", "sse")
	streamURL.RawQuery = query.Encode()

	httpReq, err := buildNativeHTTPRequest(ctx, nativeHTTPRequest{
		Method:     http.MethodPost,
		URL:        streamURL.String(),
		Body:       nativeReq,
		Provider:   ProviderGemini,
		SetHeaders: p.setHeaders,
	})
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, wrapNativeTransportError(ProviderGemini, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, decodeNativeHTTPError(ProviderGemini, resp, decodeGeminiError)
	}

	reader := newSSEReader(resp.Body)
	return NewStreamReader(func() (*StreamChunk, error) {
		return recvGeminiStreamChunk(reader)
	}, reader.Close), nil
}

func (p *geminiProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.apiKey)
}

func (p *geminiProvider) modelURL(model, action string) string {
	return p.baseURL + "/models/" + url.PathEscape(model) + ":" + action
}

func (p *geminiProvider) buildRequest(req *ChatRequest, stream bool) (geminiRequest, string, error) {
	if len(req.Messages) == 0 {
		return geminiRequest{}, "", fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}
	if stream && (len(req.Tools) > 0 || req.ToolChoice != nil || req.ParallelToolCalls != nil) {
		return geminiRequest{}, "", fmt.Errorf("%w: gemini native streaming tool use is not implemented", ErrInvalidRequest)
	}
	toolConfig, err := buildGeminiToolConfig(req)
	if err != nil {
		return geminiRequest{}, "", err
	}

	model := firstString(req.Model, p.model)
	out := geminiRequest{
		GenerationConfig: geminiGeneration(req),
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
	SetHeaders  func(*http.Request)
	DecodeError func(ProviderName, int, string, []byte) error
}

func doNativeJSON[T any](ctx context.Context, client HTTPDoer, cfg nativeHTTPRequest) (T, error) {
	var zero T
	req, err := buildNativeHTTPRequest(ctx, cfg)
	if err != nil {
		return zero, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return zero, wrapNativeTransportError(cfg.Provider, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, decodeNativeHTTPError(cfg.Provider, resp, cfg.DecodeError)
	}

	if err := json.NewDecoder(resp.Body).Decode(&zero); err != nil {
		return zero, fmt.Errorf("[%s] decode response: %w", cfg.Provider, err)
	}
	return zero, nil
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNativeErrorBody))
	if err != nil {
		return &ProviderError{
			Provider:   provider,
			Code:       CodeFromHTTPStatus(resp.StatusCode),
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Retryable:  RetryableByCode(CodeFromHTTPStatus(resp.StatusCode)),
			Message:    "read error response failed",
			Cause:      err,
		}
	}
	if decode != nil {
		return decode(provider, resp.StatusCode, resp.Status, body)
	}
	code := CodeFromHTTPStatus(resp.StatusCode)
	return &ProviderError{
		Provider:   provider,
		Code:       code,
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Retryable:  RetryableByCode(code),
		Message:    string(body),
	}
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
			var valid json.RawMessage
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

func recvAnthropicStreamChunk(reader *sseReader) (*StreamChunk, error) {
	for {
		data, err := reader.Next()
		if err != nil {
			if errors.Is(err, errSkipNativeStreamEvent) {
				continue
			}
			return nil, err
		}
		chunk, ok, err := anthropicStreamChunk(data)
		if err != nil {
			return nil, err
		}
		if ok {
			return chunk, nil
		}
	}
}

func recvGeminiStreamChunk(reader *sseReader) (*StreamChunk, error) {
	for {
		data, err := reader.Next()
		if err != nil {
			return nil, err
		}
		chunk, ok, err := geminiStreamChunk(data)
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

func geminiStreamChunk(data []byte) (*StreamChunk, bool, error) {
	var event geminiResponse
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, false, errSkipNativeStreamEvent
	}
	content, finishReason, toolCalls, err := geminiResponseContent(event)
	if err != nil {
		return nil, false, err
	}
	if len(toolCalls) > 0 {
		return nil, false, fmt.Errorf("%w: gemini native streaming tool use is not implemented", ErrInvalidRequest)
	}
	if content == "" && finishReason == "" {
		return nil, false, nil
	}
	return &StreamChunk{Delta: content, FinishReason: finishReason}, true, nil
}

func anthropicStreamChunk(data []byte) (*StreamChunk, bool, error) {
	var event anthropicStreamEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, false, errSkipNativeStreamEvent
	}
	switch event.Type {
	case "message_start":
		return &StreamChunk{}, true, nil
	case "content_block_delta":
		if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
			return &StreamChunk{Delta: event.Delta.Text}, true, nil
		}
	case "message_delta":
		if event.Delta.StopReason != "" {
			return &StreamChunk{FinishReason: event.Delta.StopReason}, true, nil
		}
	case "error":
		return nil, false, anthropicStreamProviderError(event)
	case "message_stop":
		return nil, false, io.EOF
	}
	return nil, false, errSkipNativeStreamEvent
}
