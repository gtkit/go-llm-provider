package provider

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"unicode/utf8"

	"github.com/gtkit/json/v2"
)

// FuzzAnthropicStreamChunk 验证任意字节输入下流事件解析不 panic、不产生非法状态。
func FuzzAnthropicStreamChunk(f *testing.F) {
	f.Add([]byte(`{"type":"message_start","message":{"model":"m","usage":{"input_tokens":3}}}`))
	f.Add([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`))
	f.Add([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`))
	f.Add([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"x"}}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, data []byte) {
		state := &anthropicStreamState{}
		chunk, ok, err := anthropicStreamChunk(data, state)
		if ok && chunk == nil {
			t.Fatal("ok=true 时 chunk 不得为 nil")
		}
		if state.usage.InputTokens < 0 || state.usage.OutputTokens < 0 {
			// 累积值仅在事件携带正值时覆盖，不应变负。
			t.Fatalf("usage 累积出现负值: %+v", state.usage)
		}
		_ = err
	})
}

// FuzzGeminiStreamChunk 验证任意字节输入下 Gemini 流事件解析不 panic。
func FuzzGeminiStreamChunk(f *testing.F) {
	f.Add([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`))
	f.Add([]byte(`{"candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/png","data":"aGk="}}]},"finishReason":"STOP"}]}`))
	f.Add([]byte(`{"usageMetadata":{"promptTokenCount":1}}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, data []byte) {
		chunk, ok, _ := geminiStreamChunk(data, &geminiStreamState{})
		if ok && chunk == nil {
			t.Fatal("ok=true 时 chunk 不得为 nil")
		}
	})
}

// FuzzSSEReader 验证任意字节流下 SSE 解析不 panic、必然终止。
func FuzzSSEReader(f *testing.F) {
	f.Add([]byte("data: {\"a\":1}\n\ndata: [DONE]\n\n"))
	f.Add([]byte("event: message_start\ndata: {}\n\n"))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte("data:"))
	f.Fuzz(func(t *testing.T, data []byte) {
		reader := newSSEReader(io.NopCloser(bytes.NewReader(data)))
		defer reader.Close()
		for range 10_000 { // 输入有限，必然到 EOF；上限防御意外死循环
			_, err := reader.Next()
			if err == nil || errors.Is(err, errSkipNativeStreamEvent) {
				continue
			}
			return
		}
		t.Fatal("SSE 读取未在输入耗尽后终止")
	})
}

// FuzzInjectExtraFields 验证顶层扩展字段注入的两条不变式：注入后的请求体仍是
// 合法 JSON，且字段值能原样读回。字段名与字段值来自各平台的扩展协议，含引号、
// 反斜杠、控制字符时都不得破坏请求体结构或逃逸出额外的顶层字段。
func FuzzInjectExtraFields(f *testing.F) {
	f.Add(`{"model":"m"}`, "thinking", "enabled")
	f.Add(`{}`, `quote"key`, `auto","injected":"yes`)
	f.Add(`   {"a":1,"b":[1,2,{"c":"d"}]}  `, "x", "\n\t\\")
	f.Add(`{"thinking":"old"}`, "thinking", "new")
	f.Add(`[1,2]`, "k", "v")
	f.Add(``, "k", "v")

	f.Fuzz(func(t *testing.T, body, name, value string) {
		out, err := injectExtraFields([]byte(body), map[string]any{name: value})
		if err != nil {
			// 非 JSON 对象的请求体与非法字段名都必须被拒绝，拒绝即正确
			return
		}
		// 注入只做首尾花括号的形状检查，不全量校验 body（见 injectExtraFields 的前置
		// 条件）。body 本身非法时输出跟着非法，属于契约之外，不在此断言。
		if !json.Valid([]byte(body)) {
			return
		}

		if !json.Valid(out) {
			t.Fatalf("注入后不是合法 JSON: body=%q name=%q value=%q out=%q", body, name, value, out)
		}

		var decoded map[string]any
		if err := json.Unmarshal(out, &decoded); err != nil {
			// 原请求体可能含无法解码到 any 的数值（如超大浮点），此时无从比较字段值
			var base map[string]any
			if json.Unmarshal([]byte(body), &base) != nil {
				return
			}
			t.Fatalf("注入后无法解码: body=%q name=%q value=%q out=%q err=%v", body, name, value, out, err)
		}

		// 重复键取后者，注入值必须胜出
		got, ok := decoded[name]
		if !ok {
			t.Fatalf("注入的字段缺失: name=%q out=%q", name, out)
		}
		// 字段值含非法 UTF-8 时，JSON 编码按语义替换成 U+FFFD，读回的不再是原字节。
		// 字段名不受此影响：encodeExtraFields 直接拒绝非法 UTF-8 的字段名。
		if utf8.ValidString(value) && got != value {
			t.Fatalf("字段值不一致: name=%q want=%q got=%v", name, value, got)
		}
	})
}
