package provider

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gtkit/json"
)

const (
	defaultOllamaBaseURL = "http://localhost:11434"
	maxOllamaScanToken   = 1 << 20
)

// OllamaProviderConfig configures a native local Ollama provider.
type OllamaProviderConfig struct {
	BaseURL    string
	Model      string
	HTTPClient HTTPDoer
}

func (cfg OllamaProviderConfig) validate() error {
	var errs []error
	if cfg.Model == "" {
		errs = append(errs, errors.New("model is required"))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalidProviderConfig, errors.Join(errs...))
}

// NewOllamaProvider creates a native Ollama Provider backed by /api/chat.
// The returned Provider is safe for concurrent use when cfg.HTTPClient is safe for concurrent use.
func NewOllamaProvider(cfg OllamaProviderConfig) (Provider, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultOllamaBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = DefaultHTTPClient()
	}
	return &ollamaProvider{
		baseURL:    cfg.BaseURL,
		model:      cfg.Model,
		httpClient: cfg.HTTPClient,
	}, nil
}

type ollamaProvider struct {
	baseURL    string
	model      string
	httpClient HTTPDoer
}

func (p *ollamaProvider) Name() ProviderName {
	if p == nil {
		return ""
	}
	return ProviderOllama
}

func (p *ollamaProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
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

	resp, metadata, err := doNativeJSON[ollamaChatResponse](ctx, p.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         p.baseURL + "/api/chat",
		Body:        nativeReq,
		Provider:    ProviderOllama,
		Model:       model,
		SetHeaders:  setOllamaHeaders,
		DecodeError: decodeOllamaError,
	})
	if err != nil {
		return nil, err
	}
	if metadata.Model == "" {
		metadata.Model = firstString(resp.Model, model)
	}
	return &ChatResponse{
		Content:      resp.Message.Content,
		FinishReason: resp.DoneReason,
		Usage:        ollamaUsage(resp),
		Metadata:     metadata,
	}, nil
}

func (p *ollamaProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
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

	httpReq, err := buildNativeHTTPRequest(ctx, nativeHTTPRequest{
		Method:     http.MethodPost,
		URL:        p.baseURL + "/api/chat",
		Body:       nativeReq,
		Provider:   ProviderOllama,
		Model:      model,
		SetHeaders: setOllamaHeaders,
	})
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, wrapNativeTransportError(ProviderOllama, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, decodeNativeHTTPError(ProviderOllama, resp, decodeOllamaError)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxOllamaScanToken)
	return NewStreamReader(func() (*StreamChunk, error) {
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var event ollamaChatResponse
			if err := json.Unmarshal(line, &event); err != nil {
				continue
			}
			return &StreamChunk{
				Delta:        event.Message.Content,
				FinishReason: event.DoneReason,
				Usage:        ollamaUsage(event),
			}, nil
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("[ollama] stream scan: %w", err)
		}
		return nil, io.EOF
	}, resp.Body.Close), nil
}

func (p *ollamaProvider) buildRequest(req *ChatRequest, stream bool) (ollamaChatRequest, string, error) {
	if len(req.Messages) == 0 {
		return ollamaChatRequest{}, "", fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil || req.ParallelToolCalls != nil {
		return ollamaChatRequest{}, "", fmt.Errorf("%w: ollama native tool use is not implemented", ErrInvalidRequest)
	}
	if req.ResponseFormat != nil {
		return ollamaChatRequest{}, "", fmt.Errorf("%w: ollama native response format is not implemented", ErrInvalidRequest)
	}

	model := firstString(req.Model, p.model)
	out := ollamaChatRequest{
		Model:    model,
		Messages: make([]ollamaMessage, 0, len(req.Messages)),
		Stream:   stream,
	}
	if req.MaxTokens > 0 || req.Temperature != nil || req.TopP != nil || len(req.Stop) > 0 {
		out.Options = &ollamaOptions{
			NumPredict:  req.MaxTokens,
			Temperature: req.Temperature,
			TopP:        req.TopP,
			Stop:        append([]string(nil), req.Stop...),
		}
	}
	for _, msg := range req.Messages {
		role, err := ollamaRole(msg.Role)
		if err != nil {
			return ollamaChatRequest{}, "", err
		}
		out.Messages = append(out.Messages, ollamaMessage{
			Role:    role,
			Content: contentText(msg.Content),
		})
	}
	return out, model, nil
}

func setOllamaHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
}

func ollamaRole(role Role) (string, error) {
	switch role {
	case RoleSystem, RoleUser, RoleAssistant:
		return string(role), nil
	default:
		return "", fmt.Errorf("%w: unsupported ollama role %q", ErrInvalidRequest, role)
	}
}

func ollamaUsage(resp ollamaChatResponse) Usage {
	return Usage{
		PromptTokens:     resp.PromptEvalCount,
		CompletionTokens: resp.EvalCount,
		TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
	}
}

func decodeOllamaError(provider ProviderName, statusCode int, status string, body []byte) error {
	var envelope ollamaErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nativeStatusError(provider, statusCode, status, string(body))
	}
	return nativeStatusError(provider, statusCode, status, envelope.Error)
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
}

type ollamaOptions struct {
	NumPredict  int      `json:"num_predict,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
	TopP        *float32 `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Model           string        `json:"model"`
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	DoneReason      string        `json:"done_reason"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}

type ollamaErrorEnvelope struct {
	Error string `json:"error"`
}
