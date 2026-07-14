package provider

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPricingTableCost(t *testing.T) {
	t.Parallel()

	table := PricingTable{
		"full": {
			InputPer1M:      2_000_000, // 2 元 / 1M
			OutputPer1M:     8_000_000,
			CacheReadPer1M:  200_000,
			CacheWritePer1M: 2_500_000,
			ReasoningPer1M:  4_000_000,
			Currency:        "CNY",
		},
		"fallback": {
			InputPer1M:  1_000_000,
			OutputPer1M: 3_000_000,
			Currency:    "CNY",
		},
	}

	tests := []struct {
		name       string
		model      string
		usage      Usage
		wantMicros int64
		wantErr    error
	}{
		{
			name:  "全分档：缓存与推理按各自单价，基础项先减子集",
			model: "full",
			usage: Usage{
				PromptTokens:     1_000_000, // 其中 read 40 万、write 10 万，基础 50 万
				CacheReadTokens:  400_000,
				CacheWriteTokens: 100_000,
				CompletionTokens: 200_000, // 其中 reasoning 15 万，基础 5 万
				ReasoningTokens:  150_000,
			},
			// 0.5M*2 + 0.4M*0.2 + 0.1M*2.5 + 0.05M*8 + 0.15M*4 = 1+0.08+0.25+0.4+0.6 = 2.33 元
			wantMicros: 2_330_000,
		},
		{
			name:  "未配缓存/推理价：回落基础价，等价于不拆分",
			model: "fallback",
			usage: Usage{
				PromptTokens:     1_000_000,
				CacheReadTokens:  600_000,
				CompletionTokens: 100_000,
				ReasoningTokens:  40_000,
			},
			// 输入整体 1 元 + 输出整体 0.3 元
			wantMicros: 1_300_000,
		},
		{
			name:       "reasoning 等于 completion（全推理输出）不重复计费",
			model:      "full",
			usage:      Usage{CompletionTokens: 100_000, ReasoningTokens: 100_000},
			wantMicros: 400_000, // 全部按 reasoning 价 4 元/1M
		},
		{
			name:       "cache 等于 prompt（全命中）",
			model:      "full",
			usage:      Usage{PromptTokens: 1_000_000, CacheReadTokens: 1_000_000},
			wantMicros: 200_000,
		},
		{
			name:    "未配价模型返回错误",
			model:   "unknown",
			wantErr: ErrModelNotPriced,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			micros, currency, err := table.Cost(tt.model, tt.usage)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMicros, micros)
			assert.Equal(t, "CNY", currency)
		})
	}
}

func TestPricingTableCostWebSearch(t *testing.T) {
	t.Parallel()

	table := PricingTable{
		"per-request": {
			InputPer1M:     2_000_000,
			OutputPer1M:    8_000_000,
			WebSearchPer1K: 70_000_000, // 70 元 / 1000 次
			Currency:       "CNY",
		},
		"per-grounded-prompt": {
			InputPer1M:          2_000_000,
			OutputPer1M:         8_000_000,
			GroundedPromptPer1K: 245_000_000, // 245 元 / 1000 次 grounded prompt
			Currency:            "CNY",
		},
		"both-rates": {
			InputPer1M:          2_000_000,
			OutputPer1M:         8_000_000,
			WebSearchPer1K:      70_000_000,
			GroundedPromptPer1K: 245_000_000,
			Currency:            "CNY",
		},
		"tokens-only": {
			InputPer1M:  2_000_000,
			OutputPer1M: 8_000_000,
			Currency:    "CNY",
		},
	}

	t.Run("按次数计费并与 token 项累加", func(t *testing.T) {
		t.Parallel()
		micros, currency, err := table.Cost("per-request", Usage{
			PromptTokens:      1_000_000,
			CompletionTokens:  100_000,
			WebSearchRequests: 3,
		})
		require.NoError(t, err)
		// 2 元 + 0.8 元 + 3 次 * 0.07 元 = 3.01 元
		assert.Equal(t, int64(3_010_000), micros)
		assert.Equal(t, "CNY", currency)
	})

	t.Run("Gemini 双口径用量按 grounded prompt 费率计费", func(t *testing.T) {
		t.Parallel()
		// Gemini 上报双口径（2 个 query + 1 个 grounded prompt），
		// 配置 GroundedPromptPer1K 时按 grounded prompt 计，query 数不重复计费。
		micros, _, err := table.Cost("per-grounded-prompt", Usage{
			WebSearchRequests:        2,
			WebSearchGroundedPrompts: 1,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(245_000), micros)
	})

	t.Run("Gemini 双口径用量按次数费率计费", func(t *testing.T) {
		t.Parallel()
		micros, _, err := table.Cost("per-request", Usage{
			WebSearchRequests:        2,
			WebSearchGroundedPrompts: 1,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(140_000), micros)
	})

	t.Run("双费率同时配置返回 ErrInvalidPricing 防重复计费", func(t *testing.T) {
		t.Parallel()
		_, _, err := table.Cost("both-rates", Usage{WebSearchRequests: 1, WebSearchGroundedPrompts: 1})
		require.ErrorIs(t, err, ErrInvalidPricing)
	})

	t.Run("双费率同时配置但无搜索用量不报错", func(t *testing.T) {
		t.Parallel()
		micros, _, err := table.Cost("both-rates", Usage{PromptTokens: 1_000_000})
		require.NoError(t, err)
		assert.Equal(t, int64(2_000_000), micros)
	})

	t.Run("配置口径与用量口径不符返回 ErrModelNotPriced", func(t *testing.T) {
		t.Parallel()
		// Anthropic 只有次数口径，配了 grounded prompt 费率时无法计价——报错防漏账。
		_, _, err := table.Cost("per-grounded-prompt", Usage{WebSearchRequests: 2})
		require.ErrorIs(t, err, ErrModelNotPriced)
		_, _, err = table.Cost("per-request", Usage{WebSearchGroundedPrompts: 1})
		require.ErrorIs(t, err, ErrModelNotPriced)
	})

	t.Run("无搜索用量时不要求配置搜索价", func(t *testing.T) {
		t.Parallel()
		micros, _, err := table.Cost("tokens-only", Usage{PromptTokens: 1_000_000})
		require.NoError(t, err)
		assert.Equal(t, int64(2_000_000), micros)
	})

	t.Run("有搜索用量但未配搜索价返回 ErrModelNotPriced", func(t *testing.T) {
		t.Parallel()
		_, _, err := table.Cost("tokens-only", Usage{WebSearchRequests: 1})
		require.ErrorIs(t, err, ErrModelNotPriced)
	})

	t.Run("负搜索用量报错", func(t *testing.T) {
		t.Parallel()
		_, _, err := table.Cost("per-request", Usage{WebSearchRequests: -1})
		require.ErrorIs(t, err, ErrInvalidPricing)
		_, _, err = table.Cost("per-grounded-prompt", Usage{WebSearchGroundedPrompts: -1})
		require.ErrorIs(t, err, ErrInvalidPricing)
	})

	t.Run("搜索次数换算溢出报错而非回绕", func(t *testing.T) {
		t.Parallel()
		_, _, err := table.Cost("per-request", Usage{WebSearchRequests: math.MaxInt})
		require.ErrorIs(t, err, ErrInvalidPricing)
	})
}

func TestPricingTableCostRejectsInconsistentUsage(t *testing.T) {
	t.Parallel()

	table := PricingTable{"m": {InputPer1M: 1_000_000, OutputPer1M: 1_000_000, Currency: "CNY"}}
	_, _, err := table.Cost("m", Usage{PromptTokens: 100, CacheReadTokens: 200})
	require.ErrorIs(t, err, ErrInvalidPricing)
	_, _, err = table.Cost("m", Usage{CompletionTokens: 100, ReasoningTokens: 200})
	require.ErrorIs(t, err, ErrInvalidPricing)
}

func TestFormatMicros(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "0", FormatMicros(0))
	assert.Equal(t, "2.33", FormatMicros(2_330_000))
	assert.Equal(t, "1.234567", FormatMicros(1_234_567))
	assert.Equal(t, "5", FormatMicros(5_000_000))
	assert.Equal(t, "-0.5", FormatMicros(-500_000))
}

func TestPricingTableCostRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	t.Run("费率超出上限报错而非静默溢出", func(t *testing.T) {
		t.Parallel()
		table := PricingTable{"m": {InputPer1M: 1 << 62, Currency: "CNY"}}
		_, _, err := table.Cost("m", Usage{PromptTokens: 2})
		require.ErrorIs(t, err, ErrInvalidPricing)
	})

	t.Run("负费率报错", func(t *testing.T) {
		t.Parallel()
		table := PricingTable{"m": {InputPer1M: -1, Currency: "CNY"}}
		_, _, err := table.Cost("m", Usage{PromptTokens: 1})
		require.ErrorIs(t, err, ErrInvalidPricing)
	})

	t.Run("负 token 报错", func(t *testing.T) {
		t.Parallel()
		table := PricingTable{"m": {InputPer1M: 1_000_000, Currency: "CNY"}}
		_, _, err := table.Cost("m", Usage{PromptTokens: -5})
		require.ErrorIs(t, err, ErrInvalidPricing)
	})

	t.Run("合法费率配合极端 token 报错而非溢出", func(t *testing.T) {
		t.Parallel()
		table := PricingTable{"m": {InputPer1M: maxRatePer1M, Currency: "CNY"}}
		_, _, err := table.Cost("m", Usage{PromptTokens: math.MaxInt})
		require.ErrorIs(t, err, ErrInvalidPricing)
	})
}
