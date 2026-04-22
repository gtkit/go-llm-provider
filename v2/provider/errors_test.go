package provider

import (
	"context"
	"errors"
	"net"
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderError_Error_Format(t *testing.T) {
	t.Parallel()

	cause := errors.New("underlying")

	tests := []struct {
		name string
		err  *ProviderError
		want string
	}{
		{
			name: "full fields",
			err: &ProviderError{
				Provider:   "openai",
				Code:       ErrorCodeRateLimit,
				StatusCode: 429,
				Status:     "429 Too Many Requests",
				Message:    "rate limit",
				Cause:      cause,
			},
			want: "[openai] rate_limit http 429 Too Many Requests: rate limit: underlying",
		},
		{
			name: "no status text",
			err: &ProviderError{
				Provider:   "openai",
				Code:       ErrorCodeServerError,
				StatusCode: 500,
				Message:    "server error",
				Cause:      cause,
			},
			want: "[openai] server_error http 500: server error: underlying",
		},
		{
			name: "no cause",
			err: &ProviderError{
				Provider:   "openai",
				Code:       ErrorCodeRateLimit,
				StatusCode: 429,
				Message:    "rate limit",
			},
			want: "[openai] rate_limit http 429: rate limit",
		},
		{
			name: "no message",
			err: &ProviderError{
				Provider:   "openai",
				Code:       ErrorCodeRateLimit,
				StatusCode: 429,
				Cause:      cause,
			},
			want: "[openai] rate_limit http 429: underlying",
		},
		{
			name: "status code zero",
			err: &ProviderError{
				Provider: "openai",
				Code:     ErrorCodeNetwork,
				Message:  "dial failed",
				Cause:    cause,
			},
			want: "[openai] network: dial failed: underlying",
		},
		{
			name: "empty provider uses unknown",
			err: &ProviderError{
				Code:       ErrorCodeRateLimit,
				StatusCode: 429,
				Message:    "rate limit",
			},
			want: "[unknown] rate_limit http 429: rate limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.err.Error())
		})
	}
}

func TestProviderError_Error_NilReceiver(t *testing.T) {
	t.Parallel()

	var err *ProviderError
	assert.Equal(t, "<nil>", err.Error())
}

func TestProviderError_Unwrap(t *testing.T) {
	t.Parallel()

	cause := errors.New("underlying")
	err := &ProviderError{Cause: cause}
	assert.Same(t, cause, err.Unwrap())

	empty := &ProviderError{}
	assert.NoError(t, empty.Unwrap())

	var nilErr *ProviderError
	assert.NoError(t, nilErr.Unwrap())
}

func TestProviderError_Is(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		code   ErrorCode
		target error
		want   bool
	}{
		{name: "auth", code: ErrorCodeAuth, target: ErrAuth, want: true},
		{name: "rate limit", code: ErrorCodeRateLimit, target: ErrRateLimit, want: true},
		{name: "timeout", code: ErrorCodeTimeout, target: ErrTimeout, want: true},
		{name: "context length", code: ErrorCodeContextLength, target: ErrContextLength, want: true},
		{name: "content filter", code: ErrorCodeContentFilter, target: ErrContentFilter, want: true},
		{name: "invalid request", code: ErrorCodeInvalidRequest, target: ErrInvalidRequest, want: true},
		{name: "server error", code: ErrorCodeServerError, target: ErrServerError, want: true},
		{name: "network", code: ErrorCodeNetwork, target: ErrNetwork, want: true},
		{name: "mismatch", code: ErrorCodeRateLimit, target: ErrAuth, want: false},
		{name: "other sentinel", code: ErrorCodeRateLimit, target: ErrNilProvider, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := &ProviderError{Code: tt.code}
			assert.Equal(t, tt.want, errors.Is(err, tt.target))
		})
	}
}

func TestProviderError_Is_NilReceiver(t *testing.T) {
	t.Parallel()

	var err *ProviderError
	assert.False(t, err.Is(ErrRateLimit))
}

func TestCodeFromHTTPStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   ErrorCode
	}{
		{0, ErrorCodeUnknown},
		{400, ErrorCodeInvalidRequest},
		{401, ErrorCodeAuth},
		{403, ErrorCodeAuth},
		{404, ErrorCodeInvalidRequest},
		{408, ErrorCodeTimeout},
		{422, ErrorCodeInvalidRequest},
		{429, ErrorCodeRateLimit},
		{500, ErrorCodeServerError},
		{502, ErrorCodeServerError},
		{503, ErrorCodeServerError},
		{504, ErrorCodeTimeout},
		{599, ErrorCodeServerError},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, CodeFromHTTPStatus(tt.status), "status=%d", tt.status)
	}
}

func TestRetryableByCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code ErrorCode
		want bool
	}{
		{ErrorCodeUnknown, false},
		{ErrorCodeAuth, false},
		{ErrorCodeRateLimit, true},
		{ErrorCodeTimeout, true},
		{ErrorCodeContextLength, false},
		{ErrorCodeContentFilter, false},
		{ErrorCodeInvalidRequest, false},
		{ErrorCodeServerError, true},
		{ErrorCodeNetwork, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, RetryableByCode(tt.code))
		})
	}
}

func TestWrapProviderError_Nil(t *testing.T) {
	t.Parallel()

	assert.NoError(t, WrapProviderError("openai", nil))
}

func TestWrapProviderError_AlreadyProviderError_EmptyProvider(t *testing.T) {
	t.Parallel()

	existing := &ProviderError{Code: ErrorCodeRateLimit, Message: "rate limit"}
	wrapped := WrapProviderError("openai", existing)

	var got *ProviderError
	require.ErrorAs(t, wrapped, &got)
	assert.Equal(t, ProviderName("openai"), got.Provider)
	assert.Same(t, existing, got)
}

func TestWrapProviderError_AlreadyProviderError_KeepProvider(t *testing.T) {
	t.Parallel()

	existing := &ProviderError{Provider: "qwen", Code: ErrorCodeServerError, Message: "boom"}
	wrapped := WrapProviderError("openai", existing)

	var got *ProviderError
	require.ErrorAs(t, wrapped, &got)
	assert.Equal(t, ProviderName("qwen"), got.Provider)
}

func TestWrapProviderError_ContextCanceled(t *testing.T) {
	t.Parallel()

	result := WrapProviderError("openai", context.Canceled)
	assert.Same(t, context.Canceled, result)

	joined := errors.Join(context.Canceled, errors.New("extra"))
	result = WrapProviderError("openai", joined)
	require.ErrorIs(t, result, context.Canceled)

	var got *ProviderError
	assert.NotErrorAs(t, result, &got)
}

func TestWrapProviderError_ContextDeadlineExceeded(t *testing.T) {
	t.Parallel()

	result := WrapProviderError("openai", context.DeadlineExceeded)
	require.ErrorIs(t, result, context.DeadlineExceeded)

	var got *ProviderError
	assert.NotErrorAs(t, result, &got)
}

func TestWrapProviderError_OpenAIAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		apiErr    *openai.APIError
		wantCode  ErrorCode
		wantRetry bool
		wantIs    error
	}{
		{
			name: "rate limit",
			apiErr: &openai.APIError{
				HTTPStatusCode: 429,
				HTTPStatus:     "429 Too Many Requests",
				Message:        "slow down",
			},
			wantCode:  ErrorCodeRateLimit,
			wantRetry: true,
			wantIs:    ErrRateLimit,
		},
		{
			name: "auth",
			apiErr: &openai.APIError{
				HTTPStatusCode: 401,
				HTTPStatus:     "401 Unauthorized",
				Message:        "bad key",
			},
			wantCode:  ErrorCodeAuth,
			wantRetry: false,
			wantIs:    ErrAuth,
		},
		{
			name: "context length by code",
			apiErr: &openai.APIError{
				HTTPStatusCode: 400,
				HTTPStatus:     "400 Bad Request",
				Message:        "too long",
				Code:           "context_length_exceeded",
			},
			wantCode:  ErrorCodeContextLength,
			wantRetry: false,
			wantIs:    ErrContextLength,
		},
		{
			name: "content filter by code",
			apiErr: &openai.APIError{
				HTTPStatusCode: 400,
				HTTPStatus:     "400 Bad Request",
				Message:        "filtered",
				Code:           "content_filter",
			},
			wantCode:  ErrorCodeContentFilter,
			wantRetry: false,
			wantIs:    ErrContentFilter,
		},
		{
			name: "invalid request",
			apiErr: &openai.APIError{
				HTTPStatusCode: 400,
				HTTPStatus:     "400 Bad Request",
				Message:        "invalid input",
			},
			wantCode:  ErrorCodeInvalidRequest,
			wantRetry: false,
			wantIs:    ErrInvalidRequest,
		},
		{
			name: "server error",
			apiErr: &openai.APIError{
				HTTPStatusCode: 500,
				HTTPStatus:     "500 Internal Server Error",
				Message:        "boom",
			},
			wantCode:  ErrorCodeServerError,
			wantRetry: true,
			wantIs:    ErrServerError,
		},
		{
			name: "timeout",
			apiErr: &openai.APIError{
				HTTPStatusCode: 504,
				HTTPStatus:     "504 Gateway Timeout",
				Message:        "timed out",
			},
			wantCode:  ErrorCodeTimeout,
			wantRetry: true,
			wantIs:    ErrTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wrapped := WrapProviderError("openai", tt.apiErr)

			var got *ProviderError
			require.ErrorAs(t, wrapped, &got)
			assert.Equal(t, ProviderName("openai"), got.Provider)
			assert.Equal(t, tt.wantCode, got.Code)
			assert.Equal(t, tt.apiErr.HTTPStatusCode, got.StatusCode)
			assert.Equal(t, tt.apiErr.HTTPStatus, got.Status)
			assert.Equal(t, tt.wantRetry, got.Retryable)
			assert.Equal(t, tt.apiErr.Message, got.Message)
			assert.Same(t, tt.apiErr, got.Cause)
			assert.ErrorIs(t, wrapped, tt.wantIs)
		})
	}
}

func TestWrapProviderError_PreservesRawAPIFields(t *testing.T) {
	t.Parallel()

	param := "messages"
	apiErr := &openai.APIError{
		Code:           "rate_limit_exceeded",
		Message:        "slow down",
		Param:          &param,
		Type:           "requests",
		HTTPStatus:     "429 Too Many Requests",
		HTTPStatusCode: 429,
	}

	wrapped := WrapProviderError("openai", apiErr)

	var got *ProviderError
	require.ErrorAs(t, wrapped, &got)
	assert.Equal(t, "rate_limit_exceeded", got.RawCode)
	assert.Equal(t, "requests", got.RawType)
	assert.Equal(t, "messages", got.RawParam)
	assert.ErrorIs(t, wrapped, ErrRateLimit)
}

func TestWrapProviderError_OpenAIRequestError(t *testing.T) {
	t.Parallel()

	inner := errors.New("connection refused")
	reqErr := &openai.RequestError{
		HTTPStatusCode: 503,
		HTTPStatus:     "503 Service Unavailable",
		Err:            inner,
	}

	wrapped := WrapProviderError("openai", reqErr)

	var got *ProviderError
	require.ErrorAs(t, wrapped, &got)
	assert.Equal(t, ErrorCodeServerError, got.Code)
	assert.Equal(t, 503, got.StatusCode)
	assert.Equal(t, "503 Service Unavailable", got.Status)
	assert.Equal(t, "connection refused", got.Message)
	assert.True(t, got.Retryable)
	assert.Same(t, reqErr, got.Cause)
	assert.ErrorIs(t, wrapped, ErrServerError)
}

func TestWrapProviderError_RequestErrorWithoutStatus_IsNetwork(t *testing.T) {
	t.Parallel()

	reqErr := &openai.RequestError{Err: errors.New("dial tcp 127.0.0.1: connect: connection refused")}
	wrapped := WrapProviderError("openai", reqErr)

	var got *ProviderError
	require.ErrorAs(t, wrapped, &got)
	assert.Equal(t, ErrorCodeNetwork, got.Code)
	assert.True(t, got.Retryable)
	assert.ErrorIs(t, wrapped, ErrNetwork)
}

func TestWrapProviderError_NetError_IsNetwork(t *testing.T) {
	t.Parallel()

	netErr := &net.DNSError{Err: "i/o timeout", Name: "api.openai.com", IsTimeout: true}
	wrapped := WrapProviderError("openai", netErr)

	var got *ProviderError
	require.ErrorAs(t, wrapped, &got)
	assert.Equal(t, ErrorCodeNetwork, got.Code)
	assert.True(t, got.Retryable)
	assert.ErrorIs(t, wrapped, ErrNetwork)
}

func TestWrapProviderError_GenericError_IsUnknown(t *testing.T) {
	t.Parallel()

	cause := errors.New("some random error")
	wrapped := WrapProviderError("openai", cause)

	var got *ProviderError
	require.ErrorAs(t, wrapped, &got)
	assert.Equal(t, ProviderName("openai"), got.Provider)
	assert.Equal(t, ErrorCodeUnknown, got.Code)
	assert.Zero(t, got.StatusCode)
	assert.False(t, got.Retryable)
	assert.Equal(t, "some random error", got.Message)
	assert.Same(t, cause, got.Cause)
}

func TestWrapProviderError_UnwrapChain(t *testing.T) {
	t.Parallel()

	apiErr := &openai.APIError{HTTPStatusCode: 429, Message: "too many"}
	wrapped := WrapProviderError("openai", apiErr)

	unwrapped := errors.Unwrap(wrapped)
	assert.Same(t, apiErr, unwrapped)
}
