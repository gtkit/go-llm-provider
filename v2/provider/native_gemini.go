package provider

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/gtkit/json"
)

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents,omitempty"`
	SystemInstruction geminiContent           `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MIMEType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     *float32 `json:"temperature,omitempty"`
	TopP            *float32 `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiResponse struct {
	Candidates     []geminiCandidate `json:"candidates"`
	UsageMetadata  geminiUsage       `json:"usageMetadata"`
	ModelVersion   string            `json:"modelVersion"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
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

type geminiErrorEnvelope struct {
	Error geminiErrorBody `json:"error"`
}

type geminiErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func geminiParts(parts []ContentPart) ([]geminiPart, error) {
	if len(parts) == 0 {
		return []geminiPart{{Text: ""}}, nil
	}
	out := make([]geminiPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentTypeText:
			out = append(out, geminiPart{Text: part.Text})
		case ContentTypeImageURL:
			image, err := geminiImagePart(part)
			if err != nil {
				return nil, err
			}
			out = append(out, image)
		default:
			return nil, fmt.Errorf("%w: unsupported gemini content type %q", ErrInvalidRequest, part.Type)
		}
	}
	return out, nil
}

func geminiTextParts(parts []ContentPart) []geminiPart {
	out := make([]geminiPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == ContentTypeText && part.Text != "" {
			out = append(out, geminiPart{Text: part.Text})
		}
	}
	return out
}

func geminiImagePart(part ContentPart) (geminiPart, error) {
	if len(part.ImageData) > 0 {
		if part.MIMEType == "" {
			return geminiPart{}, fmt.Errorf("%w: image MIME type is required", ErrInvalidRequest)
		}
		return geminiPart{
			InlineData: &geminiInlineData{
				MIMEType: part.MIMEType,
				Data:     base64.StdEncoding.EncodeToString(part.ImageData),
			},
		}, nil
	}
	if part.ImageURL == "" {
		return geminiPart{}, fmt.Errorf("%w: image source is required", ErrInvalidRequest)
	}
	if strings.HasPrefix(part.ImageURL, "data:") {
		mimeType, data, ok := parseDataURLImage(part.ImageURL)
		if !ok {
			return geminiPart{}, fmt.Errorf("%w: invalid data URL image", ErrInvalidRequest)
		}
		return geminiPart{
			InlineData: &geminiInlineData{
				MIMEType: mimeType,
				Data:     data,
			},
		}, nil
	}
	return geminiPart{}, fmt.Errorf("%w: gemini image URL parts require inline data", ErrInvalidRequest)
}

func geminiRole(role Role) string {
	switch role {
	case RoleAssistant:
		return "model"
	default:
		return "user"
	}
}

func validateGeminiRequest(req *ChatRequest) error {
	if len(req.Tools) > 0 || req.ToolChoice != nil || req.ParallelToolCalls != nil {
		return fmt.Errorf("%w: gemini native tool use is not implemented", ErrInvalidRequest)
	}
	if req.ResponseFormat != nil {
		return fmt.Errorf("%w: gemini native response format is not implemented", ErrInvalidRequest)
	}
	return nil
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
		parts := geminiTextParts(msg.Content)
		if len(parts) > 0 {
			out.SystemInstruction.Parts = append(out.SystemInstruction.Parts, parts...)
		}
	case RoleUser, RoleAssistant:
		parts, err := geminiParts(msg.Content)
		if err != nil {
			return err
		}
		out.Contents = append(out.Contents, geminiContent{
			Role:  geminiRole(msg.Role),
			Parts: parts,
		})
	default:
		return fmt.Errorf("%w: unsupported gemini role %q", ErrInvalidRequest, msg.Role)
	}
	return nil
}

func geminiResponseContent(resp geminiResponse) (content, finishReason string) {
	if len(resp.Candidates) == 0 {
		return "", ""
	}
	candidate := resp.Candidates[0]
	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		text.WriteString(part.Text)
	}
	return text.String(), candidate.FinishReason
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
