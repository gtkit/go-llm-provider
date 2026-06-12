package provider

import (
	"fmt"
	"strings"

	"github.com/gtkit/json/v2"
)

func contentText(parts []ContentPart) string {
	if len(parts) == 0 {
		return ""
	}
	var out strings.Builder
	for _, part := range parts {
		if part.Type != ContentTypeText {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(part.Text)
	}
	return out.String()
}

func parseDataURLImage(value string) (mimeType, data string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(value, prefix) {
		return "", "", false
	}
	meta, encoded, found := strings.Cut(strings.TrimPrefix(value, prefix), ",")
	if !found || encoded == "" {
		return "", "", false
	}
	parts := strings.Split(meta, ";")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}
	if !strings.Contains(meta, "base64") {
		return "", "", false
	}
	return parts[0], encoded, true
}

func rawJSONArgument(value string) (any, error) {
	if value == "" {
		return map[string]any{}, nil
	}
	var out any
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil, fmt.Errorf("parse JSON argument: %w", err)
	}
	return out, nil
}

func appendSystemText(system *string, text string) {
	if text == "" {
		return
	}
	if *system != "" {
		*system += "\n"
	}
	*system += text
}

func nativeStatusError(provider ProviderName, statusCode int, status, message string) error {
	code := CodeFromHTTPStatus(statusCode)
	return &ProviderError{
		Provider:   provider,
		Code:       code,
		StatusCode: statusCode,
		Status:     status,
		Retryable:  RetryableByCode(code),
		Message:    message,
	}
}
