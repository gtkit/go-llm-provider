package provider

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gtkit/json/v2"
)

type anthropicRequest struct {
	Model         string               `json:"model"`
	MaxTokens     int                  `json:"max_tokens"`
	Messages      []anthropicMessage   `json:"messages"`
	System        string               `json:"system,omitempty"`
	Temperature   *float32             `json:"temperature,omitempty"`
	TopP          *float32             `json:"top_p,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	Tools         []anthropicTool      `json:"tools,omitempty"`
	ToolChoice    *anthropicToolChoice `json:"tool_choice,omitempty"`
}

type anthropicMessage struct {
	Role    string                 `json:"role"`
	Content []anthropicContentPart `json:"content"`
}

type anthropicContentPart struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
}

type anthropicResponse struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Role       string                 `json:"role"`
	Model      string                 `json:"model"`
	Content    []anthropicContentPart `json:"content"`
	StopReason string                 `json:"stop_reason"`
	Usage      anthropicUsage         `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

type anthropicToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use,omitempty"`
}

type anthropicStreamEvent struct {
	Type    string                 `json:"type"`
	Delta   anthropicStreamDelta   `json:"delta"`
	Message anthropicStreamMessage `json:"message"`
	Error   anthropicErrorBody     `json:"error"`
}

type anthropicStreamDelta struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	StopReason string `json:"stop_reason"`
}

type anthropicStreamMessage struct {
	ID    string `json:"id"`
	Model string `json:"model"`
}

type anthropicErrorEnvelope struct {
	Type  string             `json:"type"`
	Error anthropicErrorBody `json:"error"`
}

type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func anthropicContentParts(content string) []anthropicContentPart {
	return []anthropicContentPart{{Type: "text", Text: content}}
}

func anthropicAssistantParts(msg Message) ([]anthropicContentPart, error) {
	parts := anthropicContentParts(msg.Content)
	if msg.Content == "" && len(msg.ToolCalls) > 0 {
		parts = parts[:0]
	}
	for _, call := range msg.ToolCalls {
		input, err := rawJSONArgument(call.Function.Arguments)
		if err != nil {
			return nil, err
		}
		parts = append(parts, anthropicContentPart{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Function.Name,
			Input: input,
		})
	}
	return parts, nil
}

func anthropicToolResultPart(msg Message) anthropicContentPart {
	return anthropicContentPart{
		Type:      "tool_result",
		ToolUseID: msg.ToolCallID,
		Content:   msg.Content,
	}
}

func anthropicTools(tools []Tool) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropicTool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, anthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	return out
}

func buildAnthropicToolChoice(req *ChatRequest) (*anthropicToolChoice, error) {
	if req.ToolChoice == nil && req.ParallelToolCalls == nil {
		return nil, nil
	}

	choice := &anthropicToolChoice{}
	switch v := req.ToolChoice.(type) {
	case nil:
		choice.Type = "auto"
	case ToolChoiceMode:
		switch v {
		case ToolChoiceAuto:
			choice.Type = "auto"
		case ToolChoiceNone:
			choice.Type = "none"
		case ToolChoiceRequired:
			choice.Type = "any"
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidToolChoice, v)
		}
	case ToolChoiceFunction:
		if v.Name == "" {
			return nil, fmt.Errorf("%w: function name is required", ErrInvalidToolChoice)
		}
		choice.Type = "tool"
		choice.Name = v.Name
	default:
		return nil, fmt.Errorf("%w: unsupported anthropic tool choice %T", ErrInvalidToolChoice, req.ToolChoice)
	}

	if req.ParallelToolCalls != nil {
		disable := !*req.ParallelToolCalls
		choice.DisableParallelToolUse = &disable
	}
	return choice, nil
}

func anthropicRole(role Role) string {
	switch role {
	case RoleAssistant:
		return "assistant"
	default:
		return "user"
	}
}

func anthropicResponseContent(resp anthropicResponse) (content, finishReason string, toolCalls []ToolCall, err error) {
	var text strings.Builder
	for _, part := range resp.Content {
		switch part.Type {
		case "text":
			text.WriteString(part.Text)
		case "tool_use":
			arguments, marshalErr := json.Marshal(part.Input)
			if marshalErr != nil {
				return "", "", nil, fmt.Errorf("marshal anthropic tool input: %w", marshalErr)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID: part.ID,
				Function: FunctionCall{
					Name:      part.Name,
					Arguments: string(arguments),
				},
			})
		}
	}
	if len(toolCalls) > 0 || resp.StopReason == "tool_use" {
		finishReason = "tool_calls"
	} else {
		finishReason = resp.StopReason
	}
	return text.String(), finishReason, toolCalls, nil
}

func decodeAnthropicError(provider ProviderName, statusCode int, status string, body []byte) error {
	var envelope anthropicErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nativeStatusError(provider, statusCode, status, "", "", string(body), nil)
	}
	code := anthropicErrorCode(statusCode, envelope.Error.Type)
	return &ProviderError{
		Provider:   provider,
		Code:       code,
		StatusCode: statusCode,
		Status:     status,
		RawType:    envelope.Error.Type,
		Retryable:  RetryableByCode(code),
		Message:    envelope.Error.Message,
	}
}

func anthropicStreamProviderError(event anthropicStreamEvent) error {
	code := anthropicErrorCode(http.StatusOK, event.Error.Type)
	return &ProviderError{
		Provider:  ProviderAnthropic,
		Code:      code,
		RawType:   event.Error.Type,
		Retryable: RetryableByCode(code),
		Message:   event.Error.Message,
	}
}

func anthropicErrorCode(statusCode int, rawType string) ErrorCode {
	switch strings.ToLower(rawType) {
	case "authentication_error", "permission_error":
		return ErrorCodeAuth
	case "rate_limit_error":
		return ErrorCodeRateLimit
	case "overloaded_error", "api_error":
		return ErrorCodeServerError
	case "invalid_request_error":
		return ErrorCodeInvalidRequest
	default:
		return CodeFromHTTPStatus(statusCode)
	}
}
