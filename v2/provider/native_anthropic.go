package provider

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/gtkit/json"
)

type anthropicRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	Messages      []anthropicMessage `json:"messages"`
	System        string             `json:"system,omitempty"`
	Temperature   *float32           `json:"temperature,omitempty"`
	TopP          *float32           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string                 `json:"role"`
	Content []anthropicContentPart `json:"content"`
}

type anthropicContentPart struct {
	Type   string                `json:"type"`
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
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

func anthropicContentParts(parts []ContentPart) ([]anthropicContentPart, error) {
	if len(parts) == 0 {
		return []anthropicContentPart{{Type: "text", Text: ""}}, nil
	}
	out := make([]anthropicContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentTypeText:
			out = append(out, anthropicContentPart{Type: "text", Text: part.Text})
		case ContentTypeImageURL:
			image, err := anthropicImagePart(part)
			if err != nil {
				return nil, err
			}
			out = append(out, image)
		default:
			return nil, fmt.Errorf("%w: unsupported anthropic content type %q", ErrInvalidRequest, part.Type)
		}
	}
	return out, nil
}

func anthropicImagePart(part ContentPart) (anthropicContentPart, error) {
	if len(part.ImageData) > 0 {
		if part.MIMEType == "" {
			return anthropicContentPart{}, fmt.Errorf("%w: image MIME type is required", ErrInvalidRequest)
		}
		return anthropicContentPart{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: part.MIMEType,
				Data:      base64.StdEncoding.EncodeToString(part.ImageData),
			},
		}, nil
	}
	if part.ImageURL == "" {
		return anthropicContentPart{}, fmt.Errorf("%w: image source is required", ErrInvalidRequest)
	}
	if strings.HasPrefix(part.ImageURL, "data:") {
		mimeType, data, ok := parseDataURLImage(part.ImageURL)
		if !ok {
			return anthropicContentPart{}, fmt.Errorf("%w: invalid data URL image", ErrInvalidRequest)
		}
		return anthropicContentPart{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: mimeType,
				Data:      data,
			},
		}, nil
	}
	return anthropicContentPart{
		Type: "image",
		Source: &anthropicImageSource{
			Type: "url",
			URL:  part.ImageURL,
		},
	}, nil
}

func anthropicRole(role Role) string {
	switch role {
	case RoleAssistant:
		return "assistant"
	default:
		return "user"
	}
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
