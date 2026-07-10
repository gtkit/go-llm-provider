package provider

import (
	"context"
	"io"
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
		var acc anthropicUsage
		if _, ok, err := anthropicStreamChunk(frame, &acc); err != nil || !ok {
			b.Fatal("unexpected parse result")
		}
	}
}
