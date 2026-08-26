package provider

import (
	"context"
	"io"
	"strings"
	"testing"
)

func BenchmarkCosineSimilarity(b *testing.B) {
	a := make([]float32, 1536)
	c := make([]float32, 1536)
	for i := range a {
		a[i] = float32(i%7) + 0.1
		c[i] = float32(i%5) + 0.2
	}

	b.ReportAllocs()
	for b.Loop() {
		_, err := CosineSimilarity(a, c)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRankBySimilarity(b *testing.B) {
	query := make([]float32, 768)
	candidates := make([][]float32, 128)
	for i := range query {
		query[i] = float32(i%11) + 0.1
	}
	for i := range candidates {
		candidates[i] = make([]float32, len(query))
		for j := range candidates[i] {
			candidates[i][j] = float32((i+j)%13) + 0.2
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		_, err := RankBySimilarity(query, candidates)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateJSON(b *testing.B) {
	type payload struct {
		Value string `json:"value"`
	}

	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Content: `{"value":"ok"}`}, nil
		},
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			return NewStreamReader(func() (*StreamChunk, error) { return nil, io.EOF }, nil), nil
		},
	}
	req := &ChatRequest{Messages: []Message{UserText("value")}}

	b.ReportAllocs()
	for b.Loop() {
		_, _, err := GenerateJSON[payload](context.Background(), p, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPricingTableCost(b *testing.B) {
	table := PricingTable{
		"deepseek-chat": {
			InputPer1M:      2_000_000,
			OutputPer1M:     8_000_000,
			CacheReadPer1M:  200_000,
			CacheWritePer1M: 2_000_000,
			ReasoningPer1M:  8_000_000,
			Currency:        "CNY",
		},
	}
	usage := Usage{
		PromptTokens:     12_000,
		CompletionTokens: 3_000,
		CacheReadTokens:  8_000,
		ReasoningTokens:  1_000,
		TotalTokens:      15_000,
	}

	b.ReportAllocs()
	for b.Loop() {
		micros, _, err := table.Cost("deepseek-chat", usage)
		if err != nil || micros <= 0 {
			b.Fatal("unexpected pricing result")
		}
	}
}

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
				Message{Role: RoleAssistant, Content: []ContentPart{TextPart("")},
					ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "query_metrics", Arguments: `{"quarter":"Q3","fields":["revenue","margin"]}`}}}},
				ToolResultMessage("c1", `{"revenue":1234567,"margin":0.38}`),
			)
		}
	}
	return msgs
}

// BenchmarkEstimateTokens 基准化额度预检热路径：
// TokenBudget/CostBudget middleware 每请求对全部历史执行一次估算。
func BenchmarkEstimateTokens(b *testing.B) {
	msgs := benchmarkHistory()
	b.ReportAllocs()
	for b.Loop() {
		if EstimateTokens(msgs) <= 0 {
			b.Fatal("unexpected estimate")
		}
	}
}

// BenchmarkTrimMessagesToTokenBudget 基准化组感知历史裁剪。
func BenchmarkTrimMessagesToTokenBudget(b *testing.B) {
	msgs := benchmarkHistory()
	budget := EstimateTokens(msgs) / 2
	b.ReportAllocs()
	for b.Loop() {
		if out := TrimMessagesToTokenBudget(msgs, budget); len(out) == 0 {
			b.Fatal("unexpected empty result")
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

	fields := arkExtraFields(&ChatRequest{Thinking: &Thinking{Enabled: boolPtr(true)}})
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
