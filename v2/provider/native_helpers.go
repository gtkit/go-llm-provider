package provider

import (
	"strings"
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

func nativeStatusError(provider ProviderName, statusCode int, status, rawType, rawCode, message string, cause error) error {
	code := CodeFromHTTPStatus(statusCode)
	return &ProviderError{
		Provider:   provider,
		Code:       code,
		StatusCode: statusCode,
		Status:     status,
		RawType:    rawType,
		RawCode:    rawCode,
		Retryable:  RetryableByCode(code),
		Message:    message,
		Cause:      cause,
	}
}
