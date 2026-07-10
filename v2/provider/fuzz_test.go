package provider

import (
	"bytes"
	"errors"
	"io"
	"math"
	"regexp"
	"testing"
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
		var acc anthropicUsage
		chunk, ok, err := anthropicStreamChunk(data, &acc)
		if ok && chunk == nil {
			t.Fatal("ok=true 时 chunk 不得为 nil")
		}
		if acc.InputTokens < 0 || acc.OutputTokens < 0 {
			// 累积值仅在事件携带正值时覆盖，不应变负。
			t.Fatalf("usage 累积出现负值: %+v", acc)
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
		var acc geminiUsage
		chunk, ok, _ := geminiStreamChunk(data, &acc)
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

// FuzzEstimateTokensAndTrim 验证估算恒非负、裁剪不变式（保 system、保底最新组）。
func FuzzEstimateTokensAndTrim(f *testing.F) {
	f.Add("hello 你好，世界", 100)
	f.Add("", 0)
	f.Add("a", -5)
	f.Fuzz(func(t *testing.T, text string, budget int) {
		msgs := []Message{
			SystemText(text),
			UserText(text),
			{Role: RoleAssistant, Content: []ContentPart{TextPart(text)}},
			UserText("latest"),
		}
		if EstimateTokens(msgs) < 0 {
			t.Fatal("EstimateTokens 不得为负")
		}
		out := TrimMessagesToTokenBudget(msgs, budget)
		if len(out) < 2 {
			t.Fatalf("裁剪必须至少保留 system 与最新一组，got %d", len(out))
		}
		if out[0].Role != RoleSystem {
			t.Fatal("system 消息必须保留在首位")
		}
		if contentText(out[len(out)-1].Content) != "latest" {
			t.Fatal("最新消息必须保留")
		}
	})
}

var microsPattern = regexp.MustCompile(`^-?\d+(\.\d{1,6})?$`)

// FuzzFormatMicros 验证任意 int64 金额的格式化输出合法。
func FuzzFormatMicros(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(-500_000))
	f.Add(int64(1_234_567))
	f.Add(int64(math.MinInt64)) // 回归：-MinInt64 取负溢出曾输出 "--…"
	f.Add(int64(math.MaxInt64))
	f.Fuzz(func(t *testing.T, micros int64) {
		out := FormatMicros(micros)
		if !microsPattern.MatchString(out) {
			t.Fatalf("非法金额格式: %q (输入 %d)", out, micros)
		}
	})
}
