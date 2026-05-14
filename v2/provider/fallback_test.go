package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFallbackProviderUsesBackupAfterRetryablePrimaryFailure(t *testing.T) {
	t.Parallel()

	primary := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, &ProviderError{
				Provider:  ProviderOpenAI,
				Code:      ErrorCodeServerError,
				Retryable: true,
			}
		},
	}
	backup := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: "backup"}, nil
		},
	}

	p, err := NewFallbackProvider(primary, backup)
	require.NoError(t, err)

	resp, err := p.Chat(context.Background(), &ChatRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "backup", resp.Content)
	assert.Equal(t, ProviderOpenAI, p.Name())
}

func TestFallbackProviderDoesNotFallbackOnNonRetryableFailure(t *testing.T) {
	t.Parallel()

	var backupCalls int
	primaryErr := &ProviderError{
		Provider:  ProviderOpenAI,
		Code:      ErrorCodeInvalidRequest,
		Retryable: false,
	}
	primary := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, primaryErr
		},
	}
	backup := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			backupCalls++
			return &ChatResponse{Content: "backup"}, nil
		},
	}

	p, err := NewFallbackProvider(primary, backup)
	require.NoError(t, err)

	resp, err := p.Chat(context.Background(), &ChatRequest{})
	require.ErrorIs(t, err, primaryErr)
	assert.Nil(t, resp)
	assert.Equal(t, 0, backupCalls)
}

func TestFallbackProviderReturnsJoinedErrorWhenAllFail(t *testing.T) {
	t.Parallel()

	primaryErr := &ProviderError{Provider: ProviderOpenAI, Code: ErrorCodeServerError, Retryable: true}
	backupErr := &ProviderError{Provider: ProviderDeepSeek, Code: ErrorCodeServerError, Retryable: true}
	primary := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, primaryErr
		},
	}
	backup := &stubProvider{
		name: ProviderDeepSeek,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return nil, backupErr
		},
	}

	p, err := NewFallbackProvider(primary, backup)
	require.NoError(t, err)

	resp, err := p.Chat(context.Background(), &ChatRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	require.ErrorIs(t, err, primaryErr)
	require.ErrorIs(t, err, backupErr)
}

func TestFallbackProviderRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	p, err := NewFallbackProvider(nil)
	require.ErrorIs(t, err, ErrNilProvider)
	assert.Nil(t, p)
}
