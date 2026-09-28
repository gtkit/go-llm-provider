package provider

import (
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// NewIdleTimeoutDoer 包装 next，为响应体读取加空闲超时：自拿到响应起、以及每次读到数据后，
// 超过 timeout 未读到任何字节即关闭响应体，读取返回 Code 为 ErrorCodeTimeout、
// Cause 为 ErrStreamIdleTimeout 的 *ProviderError。
//
// 计时按字节进行，SSE 保活注释同样会重置计时；等待响应头的时长由传输层
// （DefaultHTTPClient 的 ResponseHeaderTimeout）负责。next 为 nil 时使用 DefaultHTTPClient()，
// timeout <= 0 时原样返回 next。返回值可直接用作 ProviderConfig.HTTPClient，并发安全性与 next 一致。
func NewIdleTimeoutDoer(next HTTPDoer, timeout time.Duration) HTTPDoer {
	if next == nil {
		next = DefaultHTTPClient()
	}
	if timeout <= 0 {
		return next
	}
	return idleTimeoutDoer{next: next, timeout: timeout}
}

type idleTimeoutDoer struct {
	next    HTTPDoer
	timeout time.Duration
}

// Do 透传请求并包装响应体；传输层错误原样返回，这一层不产生新的失败语义。
//
//nolint:wrapcheck // 透明装饰器：传输层错误保持原值语义
func (d idleTimeoutDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := d.next.Do(req)
	if err != nil {
		return resp, err
	}
	body := &idleTimeoutBody{ReadCloser: resp.Body, timeout: d.timeout}
	body.timer = time.AfterFunc(d.timeout, body.expire)
	resp.Body = body
	return resp, nil
}

type idleTimeoutBody struct {
	io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
	expired atomic.Bool
}

// expire 关闭底层响应体以打断阻塞中的 Read，不依赖传输层对 ctx 的响应。
func (b *idleTimeoutBody) expire() {
	b.expired.Store(true)
	_ = b.ReadCloser.Close()
}

// Read 透传底层读取结果；io.EOF 等错误必须保持原值，只有超时触发时替换为超时错误。
//
//nolint:wrapcheck // 透明装饰器：错误必须保持原值语义
func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if b.expired.Load() {
		return n, &ProviderError{
			Code:      ErrorCodeTimeout,
			Retryable: RetryableByCode(ErrorCodeTimeout),
			Message:   fmt.Sprintf("no data received for %s", b.timeout),
			Cause:     ErrStreamIdleTimeout,
		}
	}
	if n > 0 {
		b.timer.Reset(b.timeout)
	}
	return n, err
}

//nolint:wrapcheck // 同 Read：关闭错误原样交还给流关闭路径
func (b *idleTimeoutBody) Close() error {
	b.timer.Stop()
	return b.ReadCloser.Close()
}
