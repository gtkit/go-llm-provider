package provider

import (
	"strings"
	"testing"
	"time"
)

// benchmarkHistory 构造一份贴近真实对话的长历史：40 轮混合中英文 + 工具调用组。
func benchmarkHistory() []Message {
	msgs := []Message{SystemText("你是一个严谨的办公助手，回答保持简短、准确、专业。")}
	for i := range 40 {
		msgs = append(msgs,
			UserText("请帮我分析这份季度报表中的营收数据，注意 revenue growth 与 margin 的变化趋势。"),
			Message{Role: RoleAssistant, Content: []ContentPart{TextPart("根据数据，Q3 营收环比增长 12%，毛利率维持在 38% 左右，主要增长来自 enterprise 订阅。")}},
		)
		if i%5 == 0 {
			msgs = append(msgs,
				Message{
					Role: RoleAssistant, Content: []ContentPart{TextPart("")},
					ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "query_metrics", Arguments: `{"quarter":"Q3","fields":["revenue","margin"]}`}}},
				},
				ToolResultMessage("c1", `{"revenue":1234567,"margin":0.38}`),
			)
		}
	}
	return msgs
}

// BenchmarkRateLimitEstimateTokens 基准化额度预检热路径：
// 客户端限流每次请求对全部历史执行一次估算。
func BenchmarkRateLimitEstimateTokens(b *testing.B) {
	msgs := benchmarkHistory()
	b.ReportAllocs()
	for b.Loop() {
		if estimateTokens(msgs) <= 0 {
			b.Fatal("unexpected estimate")
		}
	}
}

// BenchmarkAnthropicStreamChunk 基准化流式帧解析热路径：流式响应每帧执行一次。
func BenchmarkAnthropicStreamChunk(b *testing.B) {
	frame := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"根据季度数据，营收环比增长约 12%，enterprise 订阅贡献显著。"}}`)
	b.ReportAllocs()
	for b.Loop() {
		state := &anthropicStreamState{}
		if _, ok, err := anthropicStreamChunk(frame, state); err != nil || !ok {
			b.Fatal("unexpected parse result")
		}
	}
}

// BenchmarkInjectExtraFields 基准化顶层扩展字段注入热路径：需要注入的每次请求
// 都会执行一次，请求体越大（长上下文、base64 图片）越要避免全量解析重编码。
func BenchmarkInjectExtraFields(b *testing.B) {
	const prefix = `{"model":"doubao-seed-2-0-pro-260215","messages":[{"role":"user","content":`

	fields := thinkingTypeExtraFields(&ChatRequest{Thinking: &Thinking{Enabled: new(true)}})
	if len(fields) == 0 {
		// 字段为空时 injectExtraFields 直接返回原字节，压测的就不是注入路径了。
		b.Fatal("extra fields must not be empty")
	}

	benchmarks := []struct {
		name string
		body []byte
	}{
		{name: "small", body: []byte(prefix + `"你好"}]}`)},
		{name: "large_context", body: []byte(prefix + `"` + strings.Repeat("这是一段用于压测长上下文的中文文本。", 2000) + `"}]}`)},
		{name: "image_part", body: []byte(prefix + `[{"type":"image_url","image_url":{"url":"data:image/png;base64,` +
			strings.Repeat("iVBORw0KGgoAAAANSUhEUg", 20000) + `"}}]}]}`)},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(bm.body)))
			for b.Loop() {
				if _, err := injectExtraFields(bm.body, fields); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkBreakerReport 对比熔断器两种判定模式在热路径上的开销：
// Report 每次调用都要走一次判定，失败率模式需要汇总分桶计数。
func BenchmarkBreakerReport(b *testing.B) {
	b.Run("failure_threshold", func(b *testing.B) {
		br := NewBreaker(BreakerOptions{Window: time.Minute})
		b.ReportAllocs()
		for b.Loop() {
			br.Report(nil)
		}
	})

	b.Run("failure_rate", func(b *testing.B) {
		// 阈值设得足够高，保证全程闭合、每次都要走完整判定。
		br := NewBreaker(BreakerOptions{
			Window:      time.Minute,
			ReadyToTrip: FailureRateTrip(1_000_000, 0.5),
		})
		b.ReportAllocs()
		for b.Loop() {
			br.Report(nil)
		}
	})
}
