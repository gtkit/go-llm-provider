package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gtkit/json/v2"
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
	toolCalls, err := ollamaToolCalls(resp.Message.ToolCalls, 0)
	if err != nil {
		return nil, err
	}
	return &ChatResponse{
		Content:      resp.Message.Content,
		Reasoning:    resp.Message.Thinking,
		FinishReason: ollamaFinishReason(resp.DoneReason, len(toolCalls) > 0),
		Usage:        ollamaUsage(resp),
		Metadata:     metadata,
		ToolCalls:    toolCalls,
	}, nil
}

// DefaultModel 返回构造时配置的默认模型名（实现观测层的可选探测接口）。
func (p *ollamaProvider) DefaultModel() string {
	if p == nil {
		return ""
	}
	return p.model
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
	// Ollama 不给工具调用编号，流上按到达顺序编号；done 事件把是否发生过工具调用
	// 折算进 FinishReason（Ollama 的 done_reason 对工具调用也报 stop）。
	nextToolIndex := 0
	return NewStreamReaderWithMetadata(func() (*StreamChunk, error) {
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var event ollamaChatResponse
			if err := json.Unmarshal(line, &event); err != nil {
				continue
			}
			toolCalls, err := ollamaToolCalls(event.Message.ToolCalls, nextToolIndex)
			if err != nil {
				return nil, err
			}
			chunk := &StreamChunk{
				Delta:          event.Message.Content,
				ReasoningDelta: event.Message.Thinking,
				Model:          event.Model,
				ToolCalls:      ollamaToolCallDeltas(toolCalls, nextToolIndex),
			}
			nextToolIndex += len(toolCalls)
			if event.Done {
				chunk.FinishReason = ollamaFinishReason(event.DoneReason, nextToolIndex > 0)
				chunk.Usage = ollamaUsage(event)
			}
			return chunk, nil
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("[ollama] stream scan: %w", err)
		}
		return nil, io.EOF
	}, resp.Body.Close, metadataFromHeader(ProviderOllama, model, resp.Header)), nil
}

func (p *ollamaProvider) buildRequest(req *ChatRequest, stream bool) (ollamaChatRequest, string, error) {
	if len(req.Messages) == 0 {
		return ollamaChatRequest{}, "", fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}
	if err := requireTextOnlyOutput(ProviderOllama, req.OutputModalities); err != nil {
		return ollamaChatRequest{}, "", err
	}
	if err := validateThinking(ProviderOllama, req.Thinking); err != nil {
		return ollamaChatRequest{}, "", err
	}
	think, err := ollamaThink(req.Thinking)
	if err != nil {
		return ollamaChatRequest{}, "", err
	}
	// Ollama 没有 tool_choice 与 parallel_tool_calls：非默认取值无法表达，拒绝而非静默丢弃。
	if req.ToolChoice != nil && req.ToolChoice != ToolChoiceAuto {
		return ollamaChatRequest{}, "", fmt.Errorf("%w: ollama only supports automatic tool choice", ErrInvalidRequest)
	}
	if req.ParallelToolCalls != nil {
		return ollamaChatRequest{}, "", fmt.Errorf("%w: ollama does not support parallel_tool_calls control", ErrInvalidRequest)
	}
	if req.CandidateCount > 0 {
		return ollamaChatRequest{}, "", fmt.Errorf("%w: ollama does not support candidate count", ErrInvalidRequest)
	}
	tools, err := ollamaTools(req.Tools)
	if err != nil {
		return ollamaChatRequest{}, "", err
	}
	format, err := ollamaFormat(req.ResponseFormat)
	if err != nil {
		return ollamaChatRequest{}, "", err
	}

	model := firstString(req.Model, p.model)
	out := ollamaChatRequest{
		Model:    model,
		Messages: make([]ollamaMessage, 0, len(req.Messages)),
		Stream:   stream,
		Tools:    tools,
		Format:   format,
		Think:    think,
	}
	if req.MaxTokens > 0 || req.Temperature != nil || req.TopP != nil || len(req.Stop) > 0 || req.Seed != nil {
		out.Options = &ollamaOptions{
			NumPredict:  req.MaxTokens,
			Temperature: req.Temperature,
			TopP:        req.TopP,
			Stop:        append([]string(nil), req.Stop...),
			Seed:        req.Seed,
		}
	}
	// ToolCallID → 函数名，供工具结果消息补 tool_name（库合成的 ID 里虽含函数名，但不依赖该格式）。
	toolNames := make(map[string]string)
	for _, msg := range req.Messages {
		converted, err := ollamaMessageFrom(msg, toolNames)
		if err != nil {
			return ollamaChatRequest{}, "", err
		}
		out.Messages = append(out.Messages, converted)
	}
	return out, model, nil
}

// ollamaThink 把统一 Thinking 映射为 Ollama 的 think 参数：bool 开关，或 gpt-oss 一类
// 模型接受的档位字符串（low / medium / high）。
func ollamaThink(thinking *Thinking) (any, error) {
	if thinking == nil {
		return nil, nil
	}
	if thinking.Enabled != nil && !*thinking.Enabled {
		if thinking.Effort != "" {
			return nil, fmt.Errorf("%w: ollama thinking cannot set Effort while Enabled is false", ErrInvalidRequest)
		}
		return false, nil
	}
	if thinking.Effort != "" {
		return thinking.Effort, nil
	}
	if thinking.Enabled != nil {
		return true, nil
	}
	return nil, nil
}

func ollamaTools(tools []Tool) ([]ollamaTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]ollamaTool, 0, len(tools))
	for _, tool := range tools {
		if tool.WebSearch != nil {
			return nil, fmt.Errorf("%w: ollama does not support native web search tools", ErrInvalidRequest)
		}
		out = append(out, ollamaTool{
			Type: "function",
			Function: ollamaToolFunction{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			},
		})
	}
	return out, nil
}

// ollamaFormat 映射结构化输出：json_object → "json"，json_schema → schema 对象本身。
func ollamaFormat(format *ResponseFormat) (any, error) {
	if format == nil || format.Type == ResponseFormatText {
		return nil, nil
	}
	switch format.Type {
	case ResponseFormatJSONObject:
		return "json", nil
	case ResponseFormatJSONSchema:
		if format.Schema == nil {
			return "json", nil
		}
		return format.Schema, nil
	default:
		return nil, fmt.Errorf("%w: unsupported ollama response format %q", ErrInvalidRequest, format.Type)
	}
}

// ollamaMessageFrom 把统一 Message 映射为 Ollama 消息：文本进 content，图片进 images
// （仅接受内联字节或 data URL，Ollama 不拉取远程 URL），推理文本进 thinking，
// 工具调用与工具结果按 Ollama 的无 ID 形态映射。
func ollamaMessageFrom(msg Message, toolNames map[string]string) (ollamaMessage, error) {
	role, err := ollamaRole(msg.Role)
	if err != nil {
		return ollamaMessage{}, err
	}
	out := ollamaMessage{Role: role}
	for _, part := range msg.Content {
		switch part.Type {
		case ContentTypeText:
			if out.Content != "" && part.Text != "" {
				out.Content += "\n"
			}
			out.Content += part.Text
		case ContentTypeImageURL:
			image, err := ollamaImage(part)
			if err != nil {
				return ollamaMessage{}, err
			}
			out.Images = append(out.Images, image)
		default:
			return ollamaMessage{}, fmt.Errorf("%w: ollama does not support %q content parts", ErrInvalidRequest, part.Type)
		}
	}
	switch msg.Role {
	case RoleAssistant:
		out.Thinking = msg.Reasoning
		for _, call := range msg.ToolCalls {
			args, err := rawJSONArgument(call.Function.Arguments)
			if err != nil {
				return ollamaMessage{}, err
			}
			toolNames[call.ID] = call.Function.Name
			out.ToolCalls = append(out.ToolCalls, ollamaToolCall{
				Function: ollamaToolCallFunction{Name: call.Function.Name, Arguments: args},
			})
		}
	case RoleTool:
		out.ToolName = firstString(msg.ToolName, toolNames[msg.ToolCallID])
	}
	return out, nil
}

func ollamaImage(part ContentPart) (string, error) {
	if len(part.ImageData) > 0 {
		return base64.StdEncoding.EncodeToString(part.ImageData), nil
	}
	if strings.HasPrefix(part.ImageURL, "data:") {
		if _, data, ok := parseDataURLImage(part.ImageURL); ok {
			return data, nil
		}
		return "", fmt.Errorf("%w: invalid data URL image", ErrInvalidRequest)
	}
	return "", fmt.Errorf("%w: ollama image parts require inline data", ErrInvalidRequest)
}

// ollamaToolCalls 把响应中的工具调用映射为统一 ToolCall。Ollama 不签发调用 ID，
// 这里按"ollama_<函数名>_<序号>"合成，保证同一响应内唯一、可与工具结果对应。
func ollamaToolCalls(calls []ollamaToolCall, startIndex int) ([]ToolCall, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	out := make([]ToolCall, 0, len(calls))
	for i, call := range calls {
		arguments, err := json.Marshal(call.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("marshal ollama tool arguments: %w", err)
		}
		if call.Function.Arguments == nil {
			arguments = []byte("{}")
		}
		out = append(out, ToolCall{
			ID: fmt.Sprintf("ollama_%s_%d", call.Function.Name, startIndex+i),
			Function: FunctionCall{
				Name:      call.Function.Name,
				Arguments: string(arguments),
			},
		})
	}
	return out, nil
}

// ollamaToolCallDeltas 把整块到达的工具调用包装为流式增量（Ollama 不分片下发入参）。
func ollamaToolCallDeltas(calls []ToolCall, startIndex int) []ToolCallDelta {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCallDelta, 0, len(calls))
	for i, call := range calls {
		out = append(out, ToolCallDelta{
			Index: startIndex + i,
			ID:    call.ID,
			Function: FunctionCallDelta{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

// ollamaFinishReason 统一结束原因：Ollama 对工具调用也报 stop，这里对齐其他平台的 tool_calls。
func ollamaFinishReason(doneReason string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	return doneReason
}

func setOllamaHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
}

func ollamaRole(role Role) (string, error) {
	switch role {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
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
	Tools    []ollamaTool    `json:"tools,omitempty"`
	// Format 是 "json" 或 JSON Schema 对象。
	Format any `json:"format,omitempty"`
	// Think 是 bool 开关或档位字符串。
	Think   any            `json:"think,omitempty"`
	Options *ollamaOptions `json:"options,omitempty"`
}

type ollamaOptions struct {
	NumPredict  int      `json:"num_predict,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
	TopP        *float32 `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
}

type ollamaTool struct {
	Type     string             `json:"type"`
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Thinking  string           `json:"thinking,omitempty"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFunction `json:"function"`
}

// ollamaToolCallFunction 的 arguments 是 JSON 对象而非字符串。
type ollamaToolCallFunction struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
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
