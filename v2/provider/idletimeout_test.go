package provider

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipeDoer 以管道读端作为响应体，测试经写端精确控制数据到达时机。
type pipeDoer struct{ body io.ReadCloser }

func (d pipeDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: d.body}, nil
}

func idleTimeoutBodyFor(t *testing.T, r io.ReadCloser, timeout time.Duration) io.ReadCloser {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.invalid", http.NoBody)
	require.NoError(t, err)
	resp, err := NewIdleTimeoutDoer(pipeDoer{body: r}, timeout).Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp.Body
}

func TestIdleTimeoutDoerInterruptsStalledRead(t *testing.T) {
	t.Parallel()

	r, w := io.Pipe()
	go func() { _, _ = w.Write([]byte("data: 1\n")) }()
	body := idleTimeoutBodyFor(t, r, 50*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(body)
		done <- err
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrStreamIdleTimeout)
		require.ErrorIs(t, err, ErrTimeout)
		pe, ok := errors.AsType[*ProviderError](err)
		require.True(t, ok)
		assert.Equal(t, ErrorCodeTimeout, pe.Code)
	case <-time.After(5 * time.Second):
		t.Fatal("stalled read was not interrupted")
	}
}

func TestIdleTimeoutDoerReadsSteadyStream(t *testing.T) {
	t.Parallel()

	r, w := io.Pipe()
	go func() {
		for range 5 {
			time.Sleep(20 * time.Millisecond)
			_, _ = w.Write([]byte("x"))
		}
		_ = w.Close()
	}()
	// 总时长约 100ms 超过阈值，但每段间隔都小于阈值：计时必须随数据到达重置。
	data, err := io.ReadAll(idleTimeoutBodyFor(t, r, 60*time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, "xxxxx", string(data))
}

func TestNewIdleTimeoutDoerNonPositiveReturnsNext(t *testing.T) {
	t.Parallel()

	next := pipeDoer{body: io.NopCloser(strings.NewReader(""))}
	for _, timeout := range []time.Duration{0, -time.Second} {
		assert.Equal(t, HTTPDoer(next), NewIdleTimeoutDoer(next, timeout))
	}
}
