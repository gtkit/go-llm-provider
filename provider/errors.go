package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// ErrorCode classifies provider failures into stable buckets for callers.
type ErrorCode string

const (
	// ErrorCodeUnknown is used when the failure cannot be classified reliably.
	ErrorCodeUnknown ErrorCode = "unknown"
	// ErrorCodeAuth represents authentication or authorization failures.
	ErrorCodeAuth ErrorCode = "auth"
	// ErrorCodeRateLimit represents provider-side throttling.
	ErrorCodeRateLimit ErrorCode = "rate_limit"
	// ErrorCodeTimeout represents request timeout failures.
	ErrorCodeTimeout ErrorCode = "timeout"
	// ErrorCodeContextLength represents requests that exceed model context limits.
	ErrorCodeContextLength ErrorCode = "context_length"
	// ErrorCodeContentFilter represents provider-side safety or content filtering blocks.
	ErrorCodeContentFilter ErrorCode = "content_filter"
	// ErrorCodeInvalidRequest represents invalid caller input or unsupported parameters.
	ErrorCodeInvalidRequest ErrorCode = "invalid_request"
	// ErrorCodeServerError represents 5xx class provider failures.
	ErrorCodeServerError ErrorCode = "server_error"
	// ErrorCodeNetwork represents transport or network-layer failures before an HTTP response.
	ErrorCodeNetwork ErrorCode = "network"
)

// ProviderError is the structured error returned by provider calls.
//
// Callers use errors.As(err, &providerErr) to inspect Provider, Code,
// StatusCode, Retryable and Message. Common branches can use errors.Is with the
// sentinel errors declared in provider.go.
//
//revive:disable-next-line:exported
type ProviderError struct {
	Provider   ProviderName
	Code       ErrorCode
	StatusCode int
	Status     string
	RawCode    string
	RawType    string
	RawParam   string
	Retryable  bool
	Message    string
	Cause      error
}

// HTTPError is kept as a deprecated alias for compatibility.
//
// Deprecated: use ProviderError.
type HTTPError = ProviderError

// Error returns a readable summary for logs and manual debugging.
func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil>"
	}

	provider := string(e.Provider)
	if provider == "" {
		provider = "unknown"
	}

	code := e.effectiveCode()
	if code == "" {
		code = ErrorCodeUnknown
	}

	parts := []string{"[" + provider + "]", string(code)}
	if e.StatusCode > 0 {
		if e.Status != "" {
			parts = append(parts, "http "+e.Status)
		} else {
			parts = append(parts, "http "+strconv.Itoa(e.StatusCode))
		}
	}

	msg := strings.Join(parts, " ")
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

// Unwrap returns the underlying cause.
func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is allows errors.Is(err, ErrRateLimit) style checks against ProviderError.
func (e *ProviderError) Is(target error) bool {
	if e == nil {
		return false
	}

	switch target {
	case ErrAuth:
		return e.effectiveCode() == ErrorCodeAuth
	case ErrRateLimit:
		return e.effectiveCode() == ErrorCodeRateLimit
	case ErrTimeout:
		return e.effectiveCode() == ErrorCodeTimeout
	case ErrContextLength:
		return e.effectiveCode() == ErrorCodeContextLength
	case ErrContentFilter:
		return e.effectiveCode() == ErrorCodeContentFilter
	case ErrInvalidRequest:
		return e.effectiveCode() == ErrorCodeInvalidRequest
	case ErrServerError:
		return e.effectiveCode() == ErrorCodeServerError
	case ErrNetwork:
		return e.effectiveCode() == ErrorCodeNetwork
	default:
		return false
	}
}

func (e *ProviderError) effectiveCode() ErrorCode {
	if e == nil {
		return ErrorCodeUnknown
	}
	if e.Code != "" && e.Code != ErrorCodeUnknown {
		return e.Code
	}
	if e.StatusCode > 0 {
		return CodeFromHTTPStatus(e.StatusCode)
	}
	return ErrorCodeUnknown
}

// CodeFromHTTPStatus maps HTTP status codes to coarse provider error classes.
func CodeFromHTTPStatus(status int) ErrorCode {
	switch {
	case status == 401 || status == 403:
		return ErrorCodeAuth
	case status == 408 || status == 504:
		return ErrorCodeTimeout
	case status == 429:
		return ErrorCodeRateLimit
	case status >= 400 && status < 500:
		return ErrorCodeInvalidRequest
	case status >= 500 && status < 600:
		return ErrorCodeServerError
	default:
		return ErrorCodeUnknown
	}
}

// RetryableByCode reports whether a caller can usually retry this error class.
func RetryableByCode(code ErrorCode) bool {
	switch code {
	case ErrorCodeRateLimit, ErrorCodeTimeout, ErrorCodeServerError, ErrorCodeNetwork:
		return true
	default:
		return false
	}
}

// WrapProviderError normalizes provider/client errors into ProviderError.
func WrapProviderError(name ProviderName, err error) error {
	if err == nil {
		return nil
	}

	var existing *ProviderError
	if errors.As(err, &existing) {
		if existing.Provider == "" {
			existing.Provider = name
		}
		if existing.Code == "" {
			existing.Code = existing.effectiveCode()
		}
		existing.Retryable = RetryableByCode(existing.effectiveCode())
		return existing
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	if apiErr, ok := errors.AsType[*openai.APIError](err); ok {
		code := providerErrorCodeFromAPIError(apiErr)
		return &ProviderError{
			Provider:   name,
			Code:       code,
			StatusCode: apiErr.HTTPStatusCode,
			Status:     apiErr.HTTPStatus,
			RawCode:    providerRawCode(apiErr.Code),
			RawType:    apiErr.Type,
			RawParam:   providerRawParam(apiErr.Param),
			Retryable:  RetryableByCode(code),
			Message:    apiErr.Message,
			Cause:      err,
		}
	}

	if reqErr, ok := errors.AsType[*openai.RequestError](err); ok {
		code := CodeFromHTTPStatus(reqErr.HTTPStatusCode)
		if reqErr.HTTPStatusCode == 0 {
			code = ErrorCodeNetwork
		}
		msg := ""
		if reqErr.Err != nil {
			msg = reqErr.Err.Error()
		}
		return &ProviderError{
			Provider:   name,
			Code:       code,
			StatusCode: reqErr.HTTPStatusCode,
			Status:     reqErr.HTTPStatus,
			Retryable:  RetryableByCode(code),
			Message:    msg,
			Cause:      err,
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return &ProviderError{
			Provider:  name,
			Code:      ErrorCodeNetwork,
			Retryable: RetryableByCode(ErrorCodeNetwork),
			Message:   err.Error(),
			Cause:     err,
		}
	}

	return &ProviderError{
		Provider:  name,
		Code:      ErrorCodeUnknown,
		Retryable: RetryableByCode(ErrorCodeUnknown),
		Message:   err.Error(),
		Cause:     err,
	}
}

func providerErrorCodeFromAPIError(err *openai.APIError) ErrorCode {
	if err == nil {
		return ErrorCodeUnknown
	}

	if code, ok := apiErrorCodeString(err.Code); ok {
		switch code {
		case "context_length_exceeded":
			return ErrorCodeContextLength
		case "content_filter":
			return ErrorCodeContentFilter
		}
	}

	return CodeFromHTTPStatus(err.HTTPStatusCode)
}

func apiErrorCodeString(code any) (string, bool) {
	switch value := code.(type) {
	case string:
		normalized := strings.TrimSpace(strings.ToLower(value))
		if normalized == "" {
			return "", false
		}
		return normalized, true
	default:
		return "", false
	}
}

func providerRawCode(code any) string {
	if code == nil {
		return ""
	}
	switch value := code.(type) {
	case string:
		return strings.TrimSpace(value)
	default:
		return fmt.Sprint(value)
	}
}

func providerRawParam(param *string) string {
	if param == nil {
		return ""
	}
	return *param
}
