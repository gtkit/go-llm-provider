package provider

import (
	"fmt"
	"strconv"
	"strings"
)

// ModelRate 是单个模型的计费费率，单位为微元 / 1M tokens（1 微元 = 1e-6 货币单位）。
// 金额一律使用 int64 微元表示，避免 float64 累计精度损失。
type ModelRate struct {
	InputPer1M      int64  // 未命中缓存的输入单价
	OutputPer1M     int64  // 非推理输出单价
	CacheReadPer1M  int64  // 缓存命中输入单价；0 表示未配置，回落到 InputPer1M
	CacheWritePer1M int64  // 缓存写入输入单价；0 表示未配置，回落到 InputPer1M
	ReasoningPer1M  int64  // 推理 token 单价；0 表示未配置，回落到 OutputPer1M（主流平台按输出价计）
	Currency        string // 币种标识，如 "CNY"、"USD"
}

// PricingTable 是模型名到费率的映射，由调用方注入并维护——
// 本库不硬编码任何厂商价格（价格会过期）。
// 底层成本价与对用户售价是两套业务口径，各自实例化一份表即可。
//
// 表在注入后应视为只读：价格调整时构造新表整体替换（并更新
// billingstore 的 PricingVersion），不要在运行中修改条目——
// map 并发读写会触发 data race。
type PricingTable map[string]ModelRate

// Cost 计算一次调用的费用（微元）。
//
// Usage 的统一包含关系（ReasoningTokens ⊆ CompletionTokens、
// CacheRead/WriteTokens ⊆ PromptTokens）保证各计费项互斥，不会重复计费：
//
//	inputBase  = PromptTokens - CacheReadTokens - CacheWriteTokens
//	outputBase = CompletionTokens - ReasoningTokens
//	cost = inputBase*Input + CacheRead*CacheRead + CacheWrite*CacheWrite
//	     + outputBase*Output + Reasoning*Reasoning   （各项 / 1M，末尾一次整除）
//
// 末尾一次整除向零截断，尾差对用户让利。
// 溢出余量：token 计数 ~1e7、高价模型费率 ~1e9 微元/1M 时，
// 五项累加 ~1e17，仍低于 int64 上限（~9.2e18）一个数量级以上。
// 模型不在表中时返回 ErrModelNotPriced，不静默按零计费。
func (t PricingTable) Cost(model string, usage Usage) (micros int64, currency string, err error) {
	rate, ok := t[model]
	if !ok {
		return 0, "", fmt.Errorf("%w: %q", ErrModelNotPriced, model)
	}
	if err := validateRate(model, rate); err != nil {
		return 0, "", err
	}
	if err := validateUsage(usage); err != nil {
		return 0, "", err
	}

	cacheReadRate := fallbackRate(rate.CacheReadPer1M, rate.InputPer1M)
	cacheWriteRate := fallbackRate(rate.CacheWritePer1M, rate.InputPer1M)
	reasoningRate := fallbackRate(rate.ReasoningPer1M, rate.OutputPer1M)

	// 异常数据防御：子集字段超过总量时按 0 计基础项，缓存/推理项照常计。
	inputBase := max(int64(usage.PromptTokens-usage.CacheReadTokens-usage.CacheWriteTokens), 0)
	outputBase := max(int64(usage.CompletionTokens-usage.ReasoningTokens), 0)

	total := inputBase*rate.InputPer1M +
		int64(usage.CacheReadTokens)*cacheReadRate +
		int64(usage.CacheWriteTokens)*cacheWriteRate +
		outputBase*rate.OutputPer1M +
		int64(usage.ReasoningTokens)*reasoningRate

	return total / tokensPerRateUnit, rate.Currency, nil
}

// tokensPerRateUnit 是费率的计量基数：ModelRate 各单价均为"每 1M tokens"。
const tokensPerRateUnit = 1_000_000

// maxRatePer1M 是费率合法上限（1e12 微元 / 1M tokens，即每百万 token 一百万元），
// 远超任何真实模型定价三个数量级。上限校验把 Cost 注释中的溢出余量分析
// 从假设变成硬保证：合法费率与 token 量下，乘加全程不会越过 int64。
const maxRatePer1M = 1_000_000_000_000

func validateRate(model string, rate ModelRate) error {
	for _, per1M := range []int64{
		rate.InputPer1M, rate.OutputPer1M, rate.CacheReadPer1M, rate.CacheWritePer1M, rate.ReasoningPer1M,
	} {
		if per1M < 0 || per1M > maxRatePer1M {
			return fmt.Errorf("%w: model %q rate %d out of [0, %d]", ErrInvalidPricing, model, per1M, int64(maxRatePer1M))
		}
	}
	return nil
}

func validateUsage(usage Usage) error {
	for _, n := range []int{
		usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens,
		usage.CacheReadTokens, usage.CacheWriteTokens, usage.TotalTokens,
	} {
		if n < 0 {
			return fmt.Errorf("%w: negative token count %d", ErrInvalidPricing, n)
		}
	}
	return nil
}

func fallbackRate(rate, fallback int64) int64 {
	if rate > 0 {
		return rate
	}
	return fallback
}

// FormatMicros 将微元金额格式化为十进制货币字符串（如 1234567 -> "1.234567"），
// 仅用于展示；账务比较与累加请始终使用 int64 微元。
// 基于十进制字符串移位实现，全域无溢出（含 math.MinInt64）。
func FormatMicros(micros int64) string {
	const fracDigits = 6 // 微元 = 1e-6 货币单位，小数部分固定 6 位
	s := strconv.FormatInt(micros, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign = "-"
		s = s[1:]
	}
	if len(s) < fracDigits+1 {
		s = strings.Repeat("0", fracDigits+1-len(s)) + s
	}
	whole := s[:len(s)-fracDigits]
	frac := strings.TrimRight(s[len(s)-fracDigits:], "0")
	if frac == "" {
		return sign + whole
	}
	return sign + whole + "." + frac
}
