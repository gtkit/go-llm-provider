package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extraFieldsContext 把待注入字段直接写入 context，让 doer 与注入函数的测试
// 不依赖任何具体平台的 builder 语义。
func extraFieldsContext(t *testing.T, fields map[string]any) context.Context {
	t.Helper()

	return context.WithValue(t.Context(), extraFieldsCtxKey{}, fields)
}

// ============================================================
// 构造器登记表
// ============================================================

func TestNeedsExtraFieldsMatchesBuilderRegistry(t *testing.T) {
	t.Parallel()

	// needsExtraFields 决定构造时是否包装 HTTPDoer，withExtraFields 决定请求时
	// 是否写入字段。两者必须以同一张登记表为准，否则会出现"登记了却没包装
	// Doer"（字段被静默丢弃）或"包装了却查不到构造器"（白包一层）。
	require.NotEmpty(t, extraFieldsBuilders)

	for name, build := range extraFieldsBuilders {
		assert.Truef(t, needsExtraFields(name), "registered provider=%s", name)
		// 登记成 nil 能编译通过，但会让 withExtraFields 在请求路径上 panic
		require.NotNilf(t, build, "registered provider=%s has nil builder", name)
	}

	assert.False(t, needsExtraFields(ProviderOpenAI))
	assert.False(t, needsExtraFields(ProviderName("unregistered")))
}

// TestExtraFieldsBuildersConcurrentLookup 为"登记表初始化后只读，可并发查表"
// 这条声明提供反证：若有人给这张表加了运行时写入，-race 会在此处抓到。
func TestExtraFieldsBuildersConcurrentLookup(t *testing.T) {
	t.Parallel()

	req := &ChatRequest{Thinking: &Thinking{Enabled: boolPtr(true)}}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// 构造路径与请求路径查的是同一张表
			_ = needsExtraFields(ProviderArk)
			_ = withExtraFields(context.Background(), ProviderArk, req)
		}()
	}
	wg.Wait()
}

func TestWithExtraFieldsOnlyWhenBuilderProducesFields(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	enabled := true

	// 未登记的平台、nil 请求、构造器产出空字段：一律原样返回，不挂载 value。
	assert.Equal(t, ctx, withExtraFields(ctx, ProviderOpenAI, &ChatRequest{Thinking: &Thinking{Enabled: &enabled}}))
	assert.Equal(t, ctx, withExtraFields(ctx, ProviderArk, nil))
	assert.Equal(t, ctx, withExtraFields(ctx, ProviderArk, &ChatRequest{}))
	assert.Equal(t, ctx, withExtraFields(ctx, ProviderArk, &ChatRequest{Thinking: &Thinking{Effort: ThinkingEffortLow}}))

	got := withExtraFields(ctx, ProviderArk, &ChatRequest{Thinking: &Thinking{Enabled: &enabled}})
	require.NotEqual(t, ctx, got)
	fields, ok := got.Value(extraFieldsCtxKey{}).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"thinking": map[string]any{"type": "enabled"}}, fields)
}

// ============================================================
// extraFieldsDoer
// ============================================================

func TestExtraFieldsDoerRewritesBodyAndGetBody(t *testing.T) {
	t.Parallel()

	rec := &recordingHTTPClient{}
	doer := &extraFieldsDoer{next: rec}

	ctx := extraFieldsContext(t, map[string]any{"thinking": map[string]any{"type": "enabled"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://ark.test/api/v3/chat/completions", strings.NewReader(`{"model":"m"}`))
	require.NoError(t, err)

	resp, err := doer.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Len(t, rec.requests, 1)
	sent := rec.requests[0]

	body, err := io.ReadAll(sent.Body)
	require.NoError(t, err)
	// 断言字节精确相等而非 JSONEq：注入必须是原字节末尾追加，不得重排或重编码，
	// JSONEq 会把字段重排、空白压缩这类回归一并放过。
	//nolint:testifylint // encoded-compare: 此处要的正是字节级相等，不能退化成 JSONEq
	assert.Equal(t, `{"model":"m","thinking":{"type":"enabled"}}`, string(body))
	assert.Equal(t, int64(len(body)), sent.ContentLength)

	// GetBody 必须可重放同一注入后的请求体（重试场景）
	require.NotNil(t, sent.GetBody)
	replay, err := sent.GetBody()
	require.NoError(t, err)
	replayBody, err := io.ReadAll(replay)
	require.NoError(t, err)
	assert.Equal(t, body, replayBody)
	assert.NoError(t, replay.Close())
}

func TestExtraFieldsDoerPassthrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func(*testing.T) context.Context
	}{
		{
			name: "no context value",
			ctx:  func(t *testing.T) context.Context { t.Helper(); return t.Context() },
		},
		{
			name: "empty field map",
			ctx:  func(t *testing.T) context.Context { t.Helper(); return extraFieldsContext(t, map[string]any{}) },
		},
		{
			name: "nil field map",
			ctx:  func(t *testing.T) context.Context { t.Helper(); return extraFieldsContext(t, nil) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recordingHTTPClient{}
			doer := &extraFieldsDoer{next: rec}

			req, err := http.NewRequestWithContext(tc.ctx(t), http.MethodPost,
				"https://ark.test/api/v3/chat/completions", strings.NewReader(`{"model":"m"}`))
			require.NoError(t, err)

			resp, err := doer.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			require.Len(t, rec.requests, 1)
			body, err := io.ReadAll(rec.requests[0].Body)
			require.NoError(t, err)
			assert.JSONEq(t, `{"model":"m"}`, string(body))
		})
	}
}

func TestExtraFieldsDoerWrapsTransportError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("dial tcp: connection refused")

	tests := []struct {
		name   string
		fields map[string]any
	}{
		{name: "inject path", fields: map[string]any{"thinking": map[string]any{"type": "enabled"}}},
		{name: "passthrough path", fields: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doer := &extraFieldsDoer{next: &errHTTPClient{err: sentinel}}
			req, err := http.NewRequestWithContext(extraFieldsContext(t, tc.fields), http.MethodPost,
				"https://ark.test/api/v3/chat/completions", strings.NewReader(`{"model":"m"}`))
			require.NoError(t, err)

			resp, doErr := doer.Do(req) //nolint:bodyclose // 传输失败路径不返回响应体
			require.ErrorIs(t, doErr, sentinel)
			require.Nil(t, resp)
			assert.Contains(t, doErr.Error(), "http request")
		})
	}
}

func TestExtraFieldsDoerBodyFailuresReturnError(t *testing.T) {
	t.Parallel()

	readErr := errors.New("boom read")
	closeErr := errors.New("boom close")

	tests := []struct {
		name string
		body io.ReadCloser
		want error
	}{
		{name: "read fails", body: &errReadCloser{readErr: readErr}, want: readErr},
		{name: "close fails", body: &errReadCloser{closeErr: closeErr}, want: closeErr},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recordingHTTPClient{}
			doer := &extraFieldsDoer{next: rec}

			ctx := extraFieldsContext(t, map[string]any{"thinking": map[string]any{"type": "enabled"}})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				"https://ark.test/api/v3/chat/completions", http.NoBody)
			require.NoError(t, err)
			req.Body = tc.body

			resp, doErr := doer.Do(req) //nolint:bodyclose // 请求体失败路径不返回响应体
			require.ErrorIs(t, doErr, tc.want)
			require.Nil(t, resp)
			assert.Contains(t, doErr.Error(), "read request body")
			assert.Empty(t, rec.requests, "请求体不可用时不得发出请求")
		})
	}
}

func TestExtraFieldsDoerRejectsNonObjectBody(t *testing.T) {
	t.Parallel()

	rec := &recordingHTTPClient{}
	doer := &extraFieldsDoer{next: rec}

	ctx := extraFieldsContext(t, map[string]any{"thinking": map[string]any{"type": "enabled"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://ark.test/api/v3/chat/completions", strings.NewReader(`[{"model":"m"}]`))
	require.NoError(t, err)

	resp, doErr := doer.Do(req) //nolint:bodyclose // 注入失败路径不返回响应体
	require.Error(t, doErr)
	require.Nil(t, resp)
	assert.Contains(t, doErr.Error(), "decode request body")
	assert.Empty(t, rec.requests, "注入失败时不得发出请求")
}

// ============================================================
// injectExtraFields
// ============================================================

func TestInjectExtraFieldsPreservesOriginalBytes(t *testing.T) {
	t.Parallel()

	// 字段顺序、大整数精度、Unicode 转义都必须原样保留：
	// 走 map 重编码会重排字段并把 1e30 之类的数值改写成其他形式。
	body := `{"model":"ep-x","messages":[{"role":"user","content":"a&b \" \\ 中文"}],` +
		`"max_tokens":1000000000000000000000000000000,"temperature":0.10}`

	out, err := injectExtraFields([]byte(body), map[string]any{"thinking": map[string]any{"type": "enabled"}})
	require.NoError(t, err)

	want := body[:len(body)-1] + `,"thinking":{"type":"enabled"}}`
	assert.Equal(t, want, string(out))
	assert.True(t, json.Valid(out))
}

func TestInjectExtraFieldsBodyVariants(t *testing.T) {
	t.Parallel()

	fields := map[string]any{"thinking": map[string]any{"type": "disabled"}}

	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "object", body: `{"model":"m"}`, want: `{"model":"m","thinking":{"type":"disabled"}}`},
		{name: "empty object", body: `{}`, want: `{"thinking":{"type":"disabled"}}`},
		{name: "object with padding", body: "  {\"model\":\"m\"}\n", want: `{"model":"m","thinking":{"type":"disabled"}}`},
		// 对象内部空白属于原字节，只裁剪外层
		{name: "whitespace only object", body: `{   }`, want: `{   "thinking":{"type":"disabled"}}`},
		// 已有同名字段时追加项在后，按 JSON 重复键取后者的惯例覆盖
		{
			name: "existing field",
			body: `{"thinking":{"type":"enabled"},"model":"m"}`,
			want: `{"thinking":{"type":"enabled"},"model":"m","thinking":{"type":"disabled"}}`,
		},
		{name: "json null", body: `null`, wantErr: true},
		{name: "json array", body: `[{"model":"m"}]`, wantErr: true},
		{name: "json string", body: `"body"`, wantErr: true},
		{name: "json number", body: `42`, wantErr: true},
		{name: "empty body", body: ``, wantErr: true},
		{name: "whitespace body", body: "  \n\t ", wantErr: true},
		{name: "not json", body: `not-json`, wantErr: true},
		{name: "unclosed object", body: `{"model":"m"`, wantErr: true},
		{name: "single brace", body: `{`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := injectExtraFields([]byte(tc.body), fields)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "decode request body")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))
		})
	}
}

func TestInjectExtraFieldsWithoutFieldsReturnsBodyUnchanged(t *testing.T) {
	t.Parallel()

	// 无字段时连 body 合法性都不校验：这条路径本就不该改写请求。
	for _, body := range []string{`{"model":"m"}`, `[not json`, ``} {
		for _, fields := range []map[string]any{nil, {}} {
			out, err := injectExtraFields([]byte(body), fields)
			require.NoError(t, err)
			assert.Equal(t, body, string(out))
		}
	}
}

func TestInjectExtraFieldsEscapesMetaCharacters(t *testing.T) {
	t.Parallel()

	// 字段名与字段值都走 JSON 编码，不是字符串拼接：含引号、反斜杠、换行的内容
	// 不得破坏请求体结构，更不得注入出额外的顶层字段。
	fields := map[string]any{
		`quote"key`:  `auto","injected":"yes`,
		`back\slash`: "line\nbreak\ttab",
	}

	out, err := injectExtraFields([]byte(`{"model":"m"}`), fields)
	require.NoError(t, err)
	require.True(t, json.Valid(out), "注入后必须仍是合法 JSON: %s", out)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out, &decoded))

	assert.Equal(t, "m", decoded["model"])
	assert.Equal(t, `auto","injected":"yes`, decoded[`quote"key`])
	assert.Equal(t, "line\nbreak\ttab", decoded[`back\slash`])
	assert.NotContains(t, decoded, "injected", "元字符不得逃逸成额外的顶层字段")
	assert.Len(t, decoded, 3)
}

func TestInjectExtraFieldsSortsFieldNames(t *testing.T) {
	t.Parallel()

	fields := map[string]any{"zebra": 1, "alpha": true, "middle": "x"}

	first, err := injectExtraFields([]byte(`{"model":"m"}`), fields)
	require.NoError(t, err)
	// 断言字节相等而非 JSONEq：这条测试要的就是字段名有序，JSONEq 会把顺序漂移放过。
	//nolint:testifylint // encoded-compare: 校验的是排序后的字节，不能退化成 JSONEq
	assert.Equal(t, `{"model":"m","alpha":true,"middle":"x","zebra":1}`, string(first))

	// map 遍历顺序随机，重复注入必须逐字节一致，否则请求体会随机漂移。
	for range 64 {
		again, err := injectExtraFields([]byte(`{"model":"m"}`), fields)
		require.NoError(t, err)
		require.Equal(t, string(first), string(again))
	}
}

func TestInjectExtraFieldsRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields map[string]any
		errMsg string
	}{
		{
			name:   "empty field name",
			fields: map[string]any{"": "x"},
			errMsg: "empty field name",
		},
		{
			name:   "unencodable value",
			fields: map[string]any{"thinking": make(chan int)},
			errMsg: `encode field "thinking"`,
		},
		{
			// JSON 编码会把非法字节替换成 U+FFFD，发出去的字段名就不再是声明的那个
			name:   "field name is not valid UTF-8",
			fields: map[string]any{"think\x97ing": "x"},
			errMsg: "not valid UTF-8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := injectExtraFields([]byte(`{"model":"m"}`), tc.fields)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errMsg)
			assert.Nil(t, out, "注入失败不得返回半成品请求体")
		})
	}
}

// ============================================================
// 测试替身
// ============================================================

// errHTTPClient 固定返回传输错误，用于验证 extraFieldsDoer 的错误包装。
type errHTTPClient struct {
	err error
}

func (c *errHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, c.err
}

// errReadCloser 按需在读取或关闭阶段报错，用于验证请求体失败路径。
type errReadCloser struct {
	readErr  error
	closeErr error
}

func (r *errReadCloser) Read([]byte) (int, error) {
	if r.readErr != nil {
		return 0, r.readErr
	}

	return 0, io.EOF
}

func (r *errReadCloser) Close() error {
	return r.closeErr
}
