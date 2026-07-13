package provider

import (
	"fmt"
	"math"
	"math/bits"
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
// 末尾一次整除向零截断，尾差对用户让利。乘加使用 128 位无符号中间值，
// 最终微元金额超出 int64 时返回 ErrInvalidPricing，不发生回绕错账。
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

	inputBase := usage.PromptTokens - usage.CacheReadTokens - usage.CacheWriteTokens
	outputBase := usage.CompletionTokens - usage.ReasoningTokens

	var totalHi, totalLo uint64
	for _, term := range []struct {
		tokens int
		rate   int64
	}{
		{inputBase, rate.InputPer1M},
		{usage.CacheReadTokens, cacheReadRate},
		{usage.CacheWriteTokens, cacheWriteRate},
		{outputBase, rate.OutputPer1M},
		{usage.ReasoningTokens, reasoningRate},
	} {
		var addErr error
		totalHi, totalLo, addErr = addCostTerm(totalHi, totalLo, term.tokens, term.rate)
		if addErr != nil {
			return 0, "", fmt.Errorf("%w: model %q cost overflow", ErrInvalidPricing, model)
		}
	}
	micros, err = divideCost(totalHi, totalLo)
	if err != nil {
		return 0, "", fmt.Errorf("%w: model %q cost exceeds int64 micros", ErrInvalidPricing, model)
	}
	return micros, rate.Currency, nil
}

// tokensPerRateUnit 是费率的计量基数：ModelRate 各单价均为"每 1M tokens"。
const tokensPerRateUnit = 1_000_000

// maxRatePer1M 是费率合法上限（1e12 微元 / 1M tokens，即每百万 token 一百万元）。
// Cost 对合法费率仍执行 128 位乘加与最终 int64 边界校验。
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
	if usage.CacheReadTokens > usage.PromptTokens-usage.CacheWriteTokens {
		return fmt.Errorf("%w: cache read/write tokens exceed prompt tokens", ErrInvalidPricing)
	}
	if usage.ReasoningTokens > usage.CompletionTokens {
		return fmt.Errorf("%w: reasoning tokens exceed completion tokens", ErrInvalidPricing)
	}
	return nil
}

func addCostTerm(totalHi, totalLo uint64, tokens int, rate int64) (resultHi, resultLo uint64, err error) {
	if tokens < 0 || rate < 0 {
		return 0, 0, ErrInvalidPricing
	}
	// tokens 与 rate 已在本函数入口验证为非负值，转换不会改变数值。
	termHi, termLo := bits.Mul64(uint64(tokens), uint64(rate))
	totalLo, carry := bits.Add64(totalLo, termLo, 0)
	totalHi, overflow := bits.Add64(totalHi, termHi, carry)
	if overflow != 0 {
		return 0, 0, ErrInvalidPricing
	}
	return totalHi, totalLo, nil
}

func divideCost(totalHi, totalLo uint64) (int64, error) {
	quotientHi, remainder := bits.Div64(0, totalHi, tokensPerRateUnit)
	quotientLo, _ := bits.Div64(remainder, totalLo, tokensPerRateUnit)
	if quotientHi != 0 || quotientLo > math.MaxInt64 {
		return 0, ErrInvalidPricing
	}
	return int64(quotientLo), nil
}

func mulDivFloor(a, b, divisor int64) (int64, error) {
	quotient, _, err := mulDiv(a, b, divisor)
	return quotient, err
}

func mulDiv(a, b, divisor int64) (quotient int64, remainder uint64, err error) {
	if a < 0 || b < 0 || divisor <= 0 {
		return 0, 0, ErrInvalidPricing
	}
	// a、b 与 divisor 已在本函数入口验证，以下转换不会改变数值。
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	quotientHi, remainderHi := bits.Div64(0, hi, uint64(divisor))
	quotientLo, remainder := bits.Div64(remainderHi, lo, uint64(divisor))
	if quotientHi != 0 || quotientLo > math.MaxInt64 {
		return 0, 0, ErrInvalidPricing
	}
	return int64(quotientLo), remainder, nil
}

func mulDivCeil(a, b, divisor int64) (int64, error) {
	quotient, remainder, err := mulDiv(a, b, divisor)
	if err != nil {
		return 0, err
	}
	if remainder == 0 {
		return quotient, nil
	}
	if quotient == math.MaxInt64 {
		return 0, ErrInvalidPricing
	}
	return quotient + 1, nil
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
