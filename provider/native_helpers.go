package provider

import (
	"fmt"
	"strings"

	"github.com/gtkit/json/v2"
)

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

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
		Message:    strings.TrimSpace(message),
		Cause:      cause,
	}
}
