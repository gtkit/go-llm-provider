package provider

import (
	"fmt"
	"strings"

	"github.com/gtkit/json"
)

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents,omitempty"`
	SystemInstruction geminiContent           `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     *float32 `json:"temperature,omitempty"`
	TopP            *float32 `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata geminiUsage       `json:"usageMetadata"`
	ModelVersion  string            `json:"modelVersion"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig geminiFunctionCallingConfig `json:"functionCallingConfig"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiFunctionCall struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type geminiErrorEnvelope struct {
	Error geminiErrorBody `json:"error"`
}

type geminiErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func geminiRole(role Role) string {
	switch role {
	case RoleAssistant:
		return "model"
	default:
		return "user"
	}
}

func geminiGeneration(req *ChatRequest) *geminiGenerationConfig {
	if req.MaxTokens <= 0 && req.Temperature == nil && req.TopP == nil && len(req.Stop) == 0 {
		return nil
	}
	cfg := &geminiGenerationConfig{}
	if req.MaxTokens > 0 {
		cfg.MaxOutputTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		cfg.Temperature = req.Temperature
	}
	if req.TopP != nil {
		cfg.TopP = req.TopP
	}
	if len(req.Stop) > 0 {
		cfg.StopSequences = append([]string(nil), req.Stop...)
	}
	return cfg
}

func geminiTools(tools []Tool) []geminiTool {
	if len(tools) == 0 {
		return nil
	}
	declarations := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		declarations = append(declarations, geminiFunctionDeclaration{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return []geminiTool{{FunctionDeclarations: declarations}}
}

func buildGeminiToolConfig(req *ChatRequest) (*geminiToolConfig, error) {
	if req.ToolChoice == nil {
		return nil, nil
	}

	cfg := geminiFunctionCallingConfig{}
	switch v := req.ToolChoice.(type) {
	case ToolChoiceMode:
		switch v {
		case ToolChoiceAuto:
			cfg.Mode = "AUTO"
		case ToolChoiceNone:
			cfg.Mode = "NONE"
		case ToolChoiceRequired:
			cfg.Mode = "ANY"
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidToolChoice, v)
		}
	case ToolChoiceFunction:
		if v.Name == "" {
			return nil, fmt.Errorf("%w: function name is required", ErrInvalidToolChoice)
		}
		cfg.Mode = "ANY"
		cfg.AllowedFunctionNames = []string{v.Name}
	default:
		return nil, fmt.Errorf("%w: unsupported gemini tool choice %T", ErrInvalidToolChoice, req.ToolChoice)
	}
	return &geminiToolConfig{FunctionCallingConfig: cfg}, nil
}

func fillGeminiContents(out *geminiRequest, messages []Message) error {
	for _, msg := range messages {
		if err := appendGeminiMessage(out, msg); err != nil {
			return err
		}
	}
	if len(out.SystemInstruction.Parts) == 0 {
		out.SystemInstruction = geminiContent{}
	}
	return nil
}

func appendGeminiMessage(out *geminiRequest, msg Message) error {
	switch msg.Role {
	case RoleSystem:
		if msg.Content != "" {
			out.SystemInstruction.Parts = append(out.SystemInstruction.Parts, geminiPart{Text: msg.Content})
		}
	case RoleUser:
		out.Contents = append(out.Contents, geminiContent{
			Role:  "user",
			Parts: []geminiPart{{Text: msg.Content}},
		})
	case RoleAssistant:
		parts, err := geminiAssistantParts(msg)
		if err != nil {
			return err
		}
		out.Contents = append(out.Contents, geminiContent{
			Role:  geminiRole(msg.Role),
			Parts: parts,
		})
	case RoleTool:
		part := geminiToolResponsePart(msg)
		out.Contents = append(out.Contents, geminiContent{
			Role:  "function",
			Parts: []geminiPart{part},
		})
	default:
		return fmt.Errorf("%w: unsupported gemini role %q", ErrInvalidRequest, msg.Role)
	}
	return nil
}

func geminiAssistantParts(msg Message) ([]geminiPart, error) {
	parts := []geminiPart{{Text: msg.Content}}
	if msg.Content == "" && len(msg.ToolCalls) > 0 {
		parts = parts[:0]
	}
	for _, call := range msg.ToolCalls {
		args, err := rawJSONArgument(call.Function.Arguments)
		if err != nil {
			return nil, err
		}
		parts = append(parts, geminiPart{
			FunctionCall: &geminiFunctionCall{
				ID:   call.ID,
				Name: call.Function.Name,
				Args: args,
			},
		})
	}
	return parts, nil
}

func geminiToolResponsePart(msg Message) geminiPart {
	response, err := rawJSONArgument(msg.Content)
	if err != nil {
		response = map[string]any{"content": msg.Content}
	}
	return geminiPart{
		FunctionResponse: &geminiFunctionResponse{
			ID:       msg.ToolCallID,
			Name:     msg.ToolCallID,
			Response: response,
		},
	}
}

func geminiResponseContent(resp geminiResponse) (content, finishReason string, toolCalls []ToolCall, err error) {
	if len(resp.Candidates) == 0 {
		return "", "", nil, nil
	}
	candidate := resp.Candidates[0]
	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		text.WriteString(part.Text)
		if part.FunctionCall != nil {
			arguments, marshalErr := json.Marshal(part.FunctionCall.Args)
			if marshalErr != nil {
				return "", "", nil, fmt.Errorf("marshal gemini function call args: %w", marshalErr)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID: firstString(part.FunctionCall.ID, "gemini_"+part.FunctionCall.Name),
				Function: FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(arguments),
				},
			})
		}
	}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	} else {
		finishReason = candidate.FinishReason
	}
	return text.String(), finishReason, toolCalls, nil
}

func decodeGeminiError(provider ProviderName, statusCode int, status string, body []byte) error {
	var envelope geminiErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nativeStatusError(provider, statusCode, status, "", "", string(body), nil)
	}
	code := geminiErrorCode(statusCode, envelope.Error.Status)
	return &ProviderError{
		Provider:   provider,
		Code:       code,
		StatusCode: statusCode,
		Status:     status,
		RawType:    envelope.Error.Status,
		Retryable:  RetryableByCode(code),
		Message:    envelope.Error.Message,
	}
}

func geminiErrorCode(statusCode int, rawStatus string) ErrorCode {
	switch strings.ToUpper(rawStatus) {
	case "UNAUTHENTICATED", "PERMISSION_DENIED":
		return ErrorCodeAuth
	case "RESOURCE_EXHAUSTED":
		return ErrorCodeRateLimit
	case "DEADLINE_EXCEEDED":
		return ErrorCodeTimeout
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION":
		return ErrorCodeInvalidRequest
	case "UNAVAILABLE", "INTERNAL":
		return ErrorCodeServerError
	default:
		return CodeFromHTTPStatus(statusCode)
	}
}
