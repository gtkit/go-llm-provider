package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"unicode/utf8"

	"github.com/gtkit/json/v2"
)

// OpenAI 兼容平台常有 go-openai 请求结构无法表达的顶层扩展字段（如方舟的
// thinking）。这里是统一的注入通道：provider 侧把本次请求要注入的字段写入
// context，HTTP 层在请求发出前追加到已序列化的 JSON 请求体。
// 新增平台的扩展字段只需在 extraFieldsBuilders 登记一个构造器。

// extraFieldsBuilder 由 ChatRequest 产出该平台要注入的顶层扩展字段。
// 返回空 map 表示本次请求无需注入。
// 返回的 map 会一路传到 HTTP 层，构造器交出后不得再修改它。
type extraFieldsBuilder func(*ChatRequest) map[string]any

// extraFieldsBuilders 按 provider 名登记顶层扩展字段构造器，是"哪些平台需要
// 注入"的唯一来源：构造时据此决定是否包装 HTTPDoer，请求时据此产出字段。
// 初始化后只读，因此可被并发的 NewProvider 与请求路径同时查表。
var extraFieldsBuilders = map[ProviderName]extraFieldsBuilder{
	ProviderArk: arkExtraFields,
}

// extraFieldsCtxKey 是 context 传递顶层扩展字段的私有 key 类型。
type extraFieldsCtxKey struct{}

// needsExtraFields 报告该 provider 是否登记了顶层扩展字段构造器。
func needsExtraFields(name ProviderName) bool {
	_, ok := extraFieldsBuilders[name]

	return ok
}

// withExtraFields 在该 provider 登记了构造器且本次请求确有扩展字段时，
// 把待注入字段写入 context；其余情况原样返回。
func withExtraFields(ctx context.Context, name ProviderName, req *ChatRequest) context.Context {
	build, ok := extraFieldsBuilders[name]
	if !ok || req == nil {
		return ctx
	}

	fields := build(req)
	if len(fields) == 0 {
		return ctx
	}

	return context.WithValue(ctx, extraFieldsCtxKey{}, fields)
}

// extraFieldsDoer 包装底层 HTTPDoer，在请求 context 携带扩展字段时向 JSON
// 请求体注入这些顶层字段；未携带字段的请求原样透传。
type extraFieldsDoer struct {
	next HTTPDoer
}

func (d *extraFieldsDoer) Do(req *http.Request) (*http.Response, error) {
	fields, ok := req.Context().Value(extraFieldsCtxKey{}).(map[string]any)
	if !ok || len(fields) == 0 || req.Body == nil {
		return d.send(req)
	}

	body, err := io.ReadAll(req.Body)
	if err = errors.Join(err, req.Body.Close()); err != nil {
		return nil, fmt.Errorf("extra fields: read request body: %w", err)
	}

	injected, err := injectExtraFields(body, fields)
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
func (d *extraFieldsDoer) send(req *http.Request) (*http.Response, error) {
	resp, err := d.next.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	return resp, nil
}

// injectExtraFields 在 OpenAI 兼容请求体的顶层对象末尾追加扩展字段。
// 只对新增字段做 JSON 编码，原请求体走字节级追加而不是解析后重新编码：
// 其余字段的字节完全不变（数值精度、字段顺序、字符串转义都不漂移），也避免了
// 长上下文与图片请求体的解析开销。
// 请求体已含同名字段时，追加项位置更靠后，按 JSON 重复键取后者的惯例生效。
//
// 前置条件：body 是合法的 JSON 对象——它来自 go-openai 对请求结构的序列化。
// 这里只做首尾花括号的形状检查挡住非对象输入，不做全量 json.Valid：全量校验要扫完
// 整个请求体，在长上下文与 base64 图片场景的开销与注入本身不成比例。因此 body 若本身
// 非法（如 `{0}`），输出同样非法，函数不负责替调用链兜底。
func injectExtraFields(body []byte, fields map[string]any) ([]byte, error) {
	if len(fields) == 0 {
		return body, nil
	}

	encoded, err := encodeExtraFields(fields)
	if err != nil {
		return nil, err
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, errors.New("extra fields: decode request body: not a JSON object")
	}

	out := make([]byte, 0, len(trimmed)+len(encoded)+1)
	out = append(out, trimmed[:len(trimmed)-1]...)
	// 空对象后面不能有逗号，否则拼出非法 JSON。
	if len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) > 0 {
		out = append(out, ',')
	}
	out = append(out, encoded...)
	out = append(out, '}')

	return out, nil
}

// encodeExtraFields 把扩展字段编码为可直接嵌入 JSON 对象的 `"k":v` 片段序列。
// 字段名与字段值都走 JSON 编码，含引号、反斜杠等元字符的内容不会破坏请求体结构。
// 字段按名字排序输出，保证同一组字段每次注入的字节一致。
//
// 字段名必须是合法 UTF-8：JSON 编码会把非法字节静默替换成 U+FFFD，那样发出的
// 字段名已不是调用方声明的那个，平台只会以未知字段报错，排查代价远高于就地拒绝。
// 字段值不做同等校验——值是数据而非协议契约，且逐层反射检查嵌套结构的成本不成比例。
func encodeExtraFields(fields map[string]any) ([]byte, error) {
	var out bytes.Buffer

	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if name == "" {
			return nil, errors.New("extra fields: empty field name")
		}
		if !utf8.ValidString(name) {
			return nil, fmt.Errorf("extra fields: field name is not valid UTF-8: %q", name)
		}

		key, err := json.Marshal(name)
		if err != nil {
			return nil, fmt.Errorf("extra fields: encode field name %q: %w", name, err)
		}

		value, err := json.Marshal(fields[name])
		if err != nil {
			return nil, fmt.Errorf("extra fields: encode field %q: %w", name, err)
		}

		if out.Len() > 0 {
			out.WriteByte(',')
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}

	return out.Bytes(), nil
}
