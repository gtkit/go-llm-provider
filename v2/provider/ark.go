package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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
// Enabled 为 nil 时不注入该字段，由方舟按模型的默认行为决定是否深度思考。
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
	if err = errors.Join(err, req.Body.Close()); err != nil {
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

// injectArkThinking 在 OpenAI 兼容请求体的顶层对象末尾追加方舟的 thinking 字段。
// 直接在原始字节上追加而非解析后重新编码：其余字段的字节完全不变（数值精度、
// 字段顺序、字符串转义都不漂移），也避免了长上下文与图片请求体的解析开销。
// 请求体已含 thinking 字段时，追加项位置更靠后，按 JSON 重复键取后者的惯例生效。
func injectArkThinking(body []byte, thinkingType string) ([]byte, error) {
	switch thinkingType {
	case arkThinkingTypeEnabled, arkThinkingTypeDisabled:
	default:
		return nil, fmt.Errorf("ark thinking: unsupported thinking type %q", thinkingType)
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, errors.New("ark thinking: decode request body: not a JSON object")
	}

	field := `"thinking":{"type":"` + thinkingType + `"}`
	out := make([]byte, 0, len(trimmed)+len(field)+1)
	out = append(out, trimmed[:len(trimmed)-1]...)
	// 空对象后面不能有逗号，否则拼出非法 JSON。
	if len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) > 0 {
		out = append(out, ',')
	}
	out = append(out, field...)
	out = append(out, '}')

	return out, nil
}
