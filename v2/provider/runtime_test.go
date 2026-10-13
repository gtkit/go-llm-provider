package provider

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubProvider struct {
	name       ProviderName
	chat       func(context.Context, *ChatRequest) (*ChatResponse, error)
	chatStream func(context.Context, *ChatRequest) (*StreamReader, error)
}

func (p *stubProvider) Name() ProviderName {
	if p == nil {
		return ""
	}

	return p.name
}

func (p *stubProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if p.chat == nil {
		return nil, errors.New("chat not implemented")
	}

	return p.chat(ctx, req)
}

func (p *stubProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p.chatStream == nil {
		return nil, errors.New("chat stream not implemented")
	}

	return p.chatStream(ctx, req)
}

func TestSimpleChatRejectsNilProvider(t *testing.T) {
	t.Parallel()

	_, err := SimpleChat(t.Context(), nil, "hello")
	require.Error(t, err)
	assert.ErrorContains(t, err, "provider is nil")
}

func TestSimpleChatWithSystemRejectsTypedNilProvider(t *testing.T) {
	t.Parallel()

	var p *openaiProvider

	_, err := SimpleChatWithSystem(t.Context(), p, "system", "hello")
	require.Error(t, err)
	assert.ErrorContains(t, err, "provider is nil")
}

func TestCollectStreamRejectsNilProvider(t *testing.T) {
	t.Parallel()

	_, err := CollectStream(t.Context(), nil, &ChatRequest{
		Messages: []Message{UserText("hello")},
	}, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "provider is nil")
}

func TestStreamReaderZeroValueIsSafe(t *testing.T) {
	t.Parallel()

	var reader StreamReader

	require.NotPanics(t, func() {
		reader.Close()
	})

	_, err := reader.Recv()
	require.Error(t, err)
	assert.ErrorContains(t, err, "stream is not initialized")
}

// TestNewStreamReaderUsesInjectedCallbacks 验证自定义回调能够驱动统一的流读取器。
func TestNewStreamReaderUsesInjectedCallbacks(t *testing.T) {
	t.Parallel()

	chunks := []*StreamChunk{
		{Delta: "he"},
		{Delta: "llo"},
	}
	index := 0
	closed := false

	reader := NewStreamReader(func() (*StreamChunk, error) {
		if index >= len(chunks) {
			return nil, io.EOF
		}

		chunk := chunks[index]
		index++
		return chunk, nil
	}, func() error {
		closed = true
		return nil
	})

	first, err := reader.Recv()
	require.NoError(t, err)
	assert.Equal(t, "he", first.Delta)

	second, err := reader.Recv()
	require.NoError(t, err)
	assert.Equal(t, "llo", second.Delta)

	_, err = reader.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, reader.Close())
	assert.True(t, closed)
}

// TestNewStreamReaderAllowsNilClose 验证未提供关闭回调时 Close 仍然安全可用。
func TestNewStreamReaderAllowsNilClose(t *testing.T) {
	t.Parallel()

	reader := NewStreamReader(func() (*StreamChunk, error) {
		return nil, io.EOF
	}, nil)

	_, err := reader.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, reader.Close())
}
