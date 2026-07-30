package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/gtkit/json/v2"
)

// 火山方舟（Ark）的深度思考开关是 Chat Completions 请求体顶层的 thinking
// 扩展字段（{"thinking": {"type": "enabled"|"disabled"}}），go-openai 的请求
// 结构无法表达该字段。这里通过 context 把开关传递到 HTTP 层，在请求发出前
// 对序列化后的 JSON 请求体做一次字段注入，其余字段保持原始字节不变。

const (
	arkThinkingTypeEnabled  = "enabled"
	arkThinkingTypeDisabled = "disabled"
)

// arkThinkingCtxKey 是 context 传递方舟 thinking 开关的私有 key 类型。
type arkThinkingCtxKey struct{}

// arkThinkingContext 在 provider 为方舟且显式设置了 Thinking.Enabled 时，
// 把映射后的 thinking type 写入 context；其余情况原样返回。
// Enabled 为 nil 表示跟随平台默认行为（方舟侧为 auto），不注入字段。
func arkThinkingContext(ctx context.Context, name ProviderName, thinking *Thinking) context.Context {
	if name != ProviderArk || thinking == nil || thinking.Enabled == nil {
		return ctx
	}

	thinkingType := arkThinkingTypeDisabled
	if *thinking.Enabled {
		thinkingType = arkThinkingTypeEnabled
	}

	return context.WithValue(ctx, arkThinkingCtxKey{}, thinkingType)
}

// arkThinkingDoer 包装底层 HTTPDoer，在请求 context 携带 thinking 开关时
// 向 JSON 请求体注入顶层 thinking 字段；未携带开关的请求原样透传。
type arkThinkingDoer struct {
	next HTTPDoer
}

func (d *arkThinkingDoer) Do(req *http.Request) (*http.Response, error) {
	thinkingType, ok := req.Context().Value(arkThinkingCtxKey{}).(string)
	if !ok || req.Body == nil {
		return d.send(req)
	}

	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("ark thinking: read request body: %w", err)
	}

	injected, err := injectArkThinking(body, thinkingType)
	if err != nil {
		return nil, err
	}

	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(injected))
	clone.ContentLength = int64(len(injected))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(injected)), nil
	}

	return d.send(clone)
}

// send 转发请求到底层 HTTPDoer，统一包装传输层错误。
func (d *arkThinkingDoer) send(req *http.Request) (*http.Response, error) {
	resp, err := d.next.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ark http request: %w", err)
	}

	return resp, nil
}

// injectArkThinking 向 OpenAI 兼容请求体追加方舟的顶层 thinking 字段。
// 用 RawMessage 保留其余字段的原始字节，避免数值精度与字段格式漂移。
func injectArkThinking(body []byte, thinkingType string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("ark thinking: decode request body: %w", err)
	}

	raw, err := json.Marshal(map[string]string{"type": thinkingType})
	if err != nil {
		return nil, fmt.Errorf("ark thinking: encode thinking field: %w", err)
	}
	fields["thinking"] = raw

	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("ark thinking: encode request body: %w", err)
	}

	return out, nil
}
