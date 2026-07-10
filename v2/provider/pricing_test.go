package provider

import (
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

func TestPricingTableCostDefendsInconsistentUsage(t *testing.T) {
	t.Parallel()

	table := PricingTable{"m": {InputPer1M: 1_000_000, OutputPer1M: 1_000_000, Currency: "CNY"}}
	// 子集字段超过总量的异常数据：基础项按 0 计，不产生负费用。
	micros, _, err := table.Cost("m", Usage{PromptTokens: 100, CacheReadTokens: 200})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, micros, int64(0))
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
}
