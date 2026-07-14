package provider

import (
	"bytes"
	"errors"
	"io"
	"math"
	"math/big"
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

// oraclePricingCost 用 math/big 精确复算 PricingTable.Cost 的完整语义，作为差分预言机：
// 返回 (micros, wantErr)，wantErr 非 nil 表示 Cost 应返回同类错误
// （ErrInvalidPricing 或 ErrModelNotPriced）。
// 计费实现走 128 位定点乘加，此处用任意精度整数复核，二者必须逐位一致。
func oraclePricingCost(usage Usage, rate ModelRate) (int64, error) {
	for _, r := range []int64{
		rate.InputPer1M, rate.OutputPer1M, rate.CacheReadPer1M, rate.CacheWritePer1M, rate.ReasoningPer1M,
		rate.WebSearchPer1K, rate.GroundedPromptPer1K,
	} {
		if r < 0 || r > maxRatePer1M {
			return 0, ErrInvalidPricing
		}
	}
	for _, n := range []int{
		usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens,
		usage.CacheReadTokens, usage.CacheWriteTokens, usage.TotalTokens,
		usage.WebSearchRequests, usage.WebSearchGroundedPrompts,
	} {
		if n < 0 {
			return 0, ErrInvalidPricing
		}
	}
	if usage.CacheReadTokens > usage.PromptTokens-usage.CacheWriteTokens {
		return 0, ErrInvalidPricing
	}
	if usage.ReasoningTokens > usage.CompletionTokens {
		return 0, ErrInvalidPricing
	}
	// 复算 webSearchCostTerm 的口径二选一规则。
	searchUnits, searchRate := 0, int64(0)
	if usage.WebSearchRequests > 0 || usage.WebSearchGroundedPrompts > 0 {
		switch {
		case rate.WebSearchPer1K > 0 && rate.GroundedPromptPer1K > 0:
			return 0, ErrInvalidPricing
		case rate.WebSearchPer1K > 0:
			if usage.WebSearchRequests == 0 {
				return 0, ErrModelNotPriced
			}
			searchUnits, searchRate = usage.WebSearchRequests, rate.WebSearchPer1K
		case rate.GroundedPromptPer1K > 0:
			if usage.WebSearchGroundedPrompts == 0 {
				return 0, ErrModelNotPriced
			}
			searchUnits, searchRate = usage.WebSearchGroundedPrompts, rate.GroundedPromptPer1K
		default:
			return 0, ErrModelNotPriced
		}
	}
	if searchUnits > math.MaxInt/searchUnitScale {
		return 0, ErrInvalidPricing
	}

	fallback := func(r, fb int64) int64 {
		if r > 0 {
			return r
		}
		return fb
	}
	total := new(big.Int)
	addTerm := func(tokens int, r int64) {
		total.Add(total, new(big.Int).Mul(big.NewInt(int64(tokens)), big.NewInt(r)))
	}
	addTerm(usage.PromptTokens-usage.CacheReadTokens-usage.CacheWriteTokens, rate.InputPer1M)
	addTerm(usage.CacheReadTokens, fallback(rate.CacheReadPer1M, rate.InputPer1M))
	addTerm(usage.CacheWriteTokens, fallback(rate.CacheWritePer1M, rate.InputPer1M))
	addTerm(usage.CompletionTokens-usage.ReasoningTokens, rate.OutputPer1M)
	addTerm(usage.ReasoningTokens, fallback(rate.ReasoningPer1M, rate.OutputPer1M))
	addTerm(searchUnits*searchUnitScale, searchRate)

	// 非负值除法向零截断即向下取整，与 divideCost 的无符号整除一致。
	micros := new(big.Int).Quo(total, big.NewInt(tokensPerRateUnit))
	if !micros.IsInt64() {
		return 0, ErrInvalidPricing // 最终金额超出 int64，Cost 应报溢出
	}
	return micros.Int64(), nil
}

// FuzzPricingTableCost 以 math/big 预言机差分校验 128 位定点计费实现：
// 任意费率与用量下，Cost 的成功/报错分类与金额都必须与精确计算完全一致。
func FuzzPricingTableCost(f *testing.F) {
	// 种子覆盖：常规、缓存/推理子集、零费率回落、上界费率×极端 token（溢出）、
	// 负 token（拒绝）、缓存子集越界（拒绝）、搜索双口径
	// （按次/按 grounded prompt/未配价/双配冲突/口径不匹配/换算溢出）。
	f.Add(1000, 500, 0, 0, 0, 0, 0, int64(2_000_000), int64(8_000_000), int64(0), int64(0), int64(0), int64(0), int64(0))
	f.Add(1000, 500, 200, 300, 100, 0, 0, int64(2_000_000), int64(8_000_000), int64(500_000), int64(1_000_000), int64(4_000_000), int64(0), int64(0))
	f.Add(math.MaxInt, 0, 0, 0, 0, 0, 0, int64(maxRatePer1M), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0))
	f.Add(0, 0, 0, 0, 0, 0, 0, int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0))
	f.Add(-5, 0, 0, 0, 0, 0, 0, int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), int64(0))
	f.Add(100, 100, 0, 50, 60, 0, 0, int64(1), int64(1), int64(1), int64(1), int64(1), int64(0), int64(0))
	f.Add(1000, 500, 0, 0, 0, 3, 0, int64(2_000_000), int64(8_000_000), int64(0), int64(0), int64(0), int64(70_000_000), int64(0))
	f.Add(0, 0, 0, 0, 0, 2, 1, int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), int64(245_000_000))
	f.Add(0, 0, 0, 0, 0, 1, 0, int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), int64(0))
	f.Add(0, 0, 0, 0, 0, 1, 1, int64(1), int64(1), int64(0), int64(0), int64(0), int64(1), int64(1))
	f.Add(0, 0, 0, 0, 0, 2, 0, int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), int64(1))
	f.Add(0, 0, 0, 0, 0, math.MaxInt, 0, int64(1), int64(1), int64(0), int64(0), int64(0), int64(1), int64(0))

	f.Fuzz(func(t *testing.T,
		prompt, completion, reasoning, cacheRead, cacheWrite, webSearches, groundedPrompts int,
		inputRate, outputRate, cacheReadRate, cacheWriteRate, reasoningRate, webSearchRate, groundedRate int64,
	) {
		usage := Usage{
			PromptTokens:             prompt,
			CompletionTokens:         completion,
			ReasoningTokens:          reasoning,
			CacheReadTokens:          cacheRead,
			CacheWriteTokens:         cacheWrite,
			WebSearchRequests:        webSearches,
			WebSearchGroundedPrompts: groundedPrompts,
		}
		rate := ModelRate{
			InputPer1M:          inputRate,
			OutputPer1M:         outputRate,
			CacheReadPer1M:      cacheReadRate,
			CacheWritePer1M:     cacheWriteRate,
			ReasoningPer1M:      reasoningRate,
			WebSearchPer1K:      webSearchRate,
			GroundedPromptPer1K: groundedRate,
			Currency:            "CNY",
		}

		gotMicros, _, err := PricingTable{"m": rate}.Cost("m", usage)
		wantMicros, wantErr := oraclePricingCost(usage, rate)

		if wantErr == nil {
			if err != nil {
				t.Fatalf("预言机判定合法但 Cost 报错: usage=%+v rate=%+v err=%v", usage, rate, err)
			}
			if gotMicros != wantMicros {
				t.Fatalf("金额不一致: got=%d want=%d usage=%+v rate=%+v", gotMicros, wantMicros, usage, rate)
			}
			return
		}
		if err == nil {
			t.Fatalf("预言机判定非法但 Cost 未报错: got=%d usage=%+v rate=%+v", gotMicros, usage, rate)
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("错误类别不一致: want %v, got %v (usage=%+v rate=%+v)", wantErr, err, usage, rate)
		}
	})
}
