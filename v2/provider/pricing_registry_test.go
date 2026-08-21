package provider

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registryPricing() PricingTable {
	return PricingTable{
		"glm-5.1":       {InputPer1M: 2_000_000, OutputPer1M: 8_000_000, Currency: "CNY"},
		"deepseek-chat": {InputPer1M: 1_000_000, OutputPer1M: 3_000_000, Currency: "CNY"},
	}
}

func TestNewPricingRegistryValidatesTable(t *testing.T) {
	t.Parallel()

	_, err := NewPricingRegistry(PricingTable{"bad": {InputPer1M: -1}}, "v1")
	require.ErrorIs(t, err, ErrInvalidPricing)
	require.ErrorContains(t, err, "bad")

	r, err := NewPricingRegistry(registryPricing(), "2026-08-21")
	require.NoError(t, err)
	assert.Equal(t, "2026-08-21", r.Version())
}

func TestPricingRegistryEmptyTableNeverBillsZero(t *testing.T) {
	t.Parallel()

	r, err := NewPricingRegistry(nil, "empty")
	require.NoError(t, err)

	_, _, err = r.Cost("glm-5.1", Usage{PromptTokens: 100, TotalTokens: 100})
	require.ErrorIs(t, err, ErrModelNotPriced)
	assert.Nil(t, r.Models())

	table, version := r.Snapshot()
	assert.Nil(t, table)
	assert.Equal(t, "empty", version)
}

func TestPricingRegistryCostMatchesTable(t *testing.T) {
	t.Parallel()

	table := registryPricing()
	r, err := NewPricingRegistry(table, "v1")
	require.NoError(t, err)

	usage := Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000, TotalTokens: 1_500_000}
	wantMicros, wantCurrency, wantErr := table.Cost("glm-5.1", usage)
	require.NoError(t, wantErr)

	gotMicros, gotCurrency, err := r.Cost("glm-5.1", usage)
	require.NoError(t, err)
	assert.Equal(t, wantMicros, gotMicros)
	assert.Equal(t, wantCurrency, gotCurrency)

	_, _, err = r.Cost("unknown-model", usage)
	require.ErrorIs(t, err, ErrModelNotPriced)
}

func TestPricingRegistryCopiesInputTable(t *testing.T) {
	t.Parallel()

	table := registryPricing()
	r, err := NewPricingRegistry(table, "v1")
	require.NoError(t, err)

	// 调用方事后修改自己那份表，不应影响已生效的价格。
	table["glm-5.1"] = ModelRate{InputPer1M: 999_000_000, OutputPer1M: 999_000_000, Currency: "CNY"}
	delete(table, "deepseek-chat")

	rate, ok := r.Rate("glm-5.1")
	require.True(t, ok)
	assert.Equal(t, int64(2_000_000), rate.InputPer1M)
	_, ok = r.Rate("deepseek-chat")
	assert.True(t, ok, "删除调用方的条目不应影响生效价格")
}

func TestPricingRegistrySnapshotIsDefensiveCopy(t *testing.T) {
	t.Parallel()

	r, err := NewPricingRegistry(registryPricing(), "v1")
	require.NoError(t, err)

	snapshot, version := r.Snapshot()
	require.Equal(t, "v1", version)
	snapshot["glm-5.1"] = ModelRate{InputPer1M: 1}
	delete(snapshot, "deepseek-chat")

	rate, ok := r.Rate("glm-5.1")
	require.True(t, ok)
	assert.Equal(t, int64(2_000_000), rate.InputPer1M)
	_, ok = r.Rate("deepseek-chat")
	assert.True(t, ok)
}

func TestPricingRegistryReplaceSwapsAtomically(t *testing.T) {
	t.Parallel()

	r, err := NewPricingRegistry(registryPricing(), "v1")
	require.NoError(t, err)

	require.NoError(t, r.Replace(PricingTable{
		"glm-5.1": {InputPer1M: 4_000_000, OutputPer1M: 16_000_000, Currency: "CNY"},
	}, "v2"))

	assert.Equal(t, "v2", r.Version())
	rate, ok := r.Rate("glm-5.1")
	require.True(t, ok)
	assert.Equal(t, int64(4_000_000), rate.InputPer1M)

	_, ok = r.Rate("deepseek-chat")
	assert.False(t, ok, "新一代价格表整体替换旧表")
	assert.Equal(t, []string{"glm-5.1"}, r.Models())
}

func TestPricingRegistryReplaceRejectsInvalidTableAndKeepsCurrent(t *testing.T) {
	t.Parallel()

	r, err := NewPricingRegistry(registryPricing(), "v1")
	require.NoError(t, err)

	err = r.Replace(PricingTable{
		"broken": {InputPer1M: 1, WebSearchPer1K: 1, GroundedPromptPer1K: 1, Currency: "CNY"},
	}, "v2")
	require.ErrorIs(t, err, ErrInvalidPricing)

	assert.Equal(t, "v1", r.Version(), "校验失败不应改变生效价格")
	rate, ok := r.Rate("glm-5.1")
	require.True(t, ok)
	assert.Equal(t, int64(2_000_000), rate.InputPer1M)
}

func TestPricingRegistryModelsAreSorted(t *testing.T) {
	t.Parallel()

	r, err := NewPricingRegistry(PricingTable{
		"z-model": {InputPer1M: 1, OutputPer1M: 1, Currency: "CNY"},
		"a-model": {InputPer1M: 1, OutputPer1M: 1, Currency: "CNY"},
		"m-model": {InputPer1M: 1, OutputPer1M: 1, Currency: "CNY"},
	}, "v1")
	require.NoError(t, err)

	assert.Equal(t, []string{"a-model", "m-model", "z-model"}, r.Models())
}

func TestPricingRegistryNilReceiver(t *testing.T) {
	t.Parallel()

	var r *PricingRegistry

	_, _, err := r.Cost("glm-5.1", Usage{PromptTokens: 1, TotalTokens: 1})
	require.ErrorIs(t, err, ErrModelNotPriced)

	_, ok := r.Rate("glm-5.1")
	assert.False(t, ok)
	assert.Empty(t, r.Version())
	assert.Nil(t, r.Models())

	table, version := r.Snapshot()
	assert.Nil(t, table)
	assert.Empty(t, version)

	require.ErrorIs(t, r.Replace(registryPricing(), "v1"), ErrNilPricingRegistry)
}

func TestPricingRegistryZeroValueIsEmpty(t *testing.T) {
	t.Parallel()

	var r PricingRegistry

	_, _, err := r.Cost("glm-5.1", Usage{PromptTokens: 1, TotalTokens: 1})
	require.ErrorIs(t, err, ErrModelNotPriced)
	assert.Empty(t, r.Version())

	require.NoError(t, r.Replace(registryPricing(), "v1"))
	assert.Equal(t, "v1", r.Version())
}

// TestPricingRegistryConcurrentReplaceAndCost 是"替换与计价并发安全"这条契约的
// 反证测试：把 atomic 指针换成裸 map 赋值就会触发 -race 报错。
func TestPricingRegistryConcurrentReplaceAndCost(t *testing.T) {
	t.Parallel()

	r, err := NewPricingRegistry(registryPricing(), "v1")
	require.NoError(t, err)

	usage := Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}
	var priced, replaced atomic.Int64

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			for range 200 {
				if i%2 == 0 {
					if _, _, costErr := r.Cost("glm-5.1", usage); costErr == nil {
						priced.Add(1)
					}
					continue
				}
				if replaceErr := r.Replace(registryPricing(), "v-concurrent"); replaceErr == nil {
					replaced.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()

	assert.Positive(t, priced.Load())
	assert.Positive(t, replaced.Load())
	assert.Equal(t, "v-concurrent", r.Version())
}

func BenchmarkPricingRegistryCost(b *testing.B) {
	r, err := NewPricingRegistry(registryPricing(), "v1")
	if err != nil {
		b.Fatal(err)
	}
	usage := Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, _, err := r.Cost("glm-5.1", usage); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPricingRegistryCostParallel(b *testing.B) {
	r, err := NewPricingRegistry(registryPricing(), "v1")
	if err != nil {
		b.Fatal(err)
	}
	usage := Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := r.Cost("glm-5.1", usage); err != nil {
				b.Fatal(err)
			}
		}
	})
}
