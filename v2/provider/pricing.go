package provider

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"math/bits"
	"slices"
	"strconv"
	"strings"
)

// ModelRate 是单个模型的计费费率，单位为微元 / 1M tokens（1 微元 = 1e-6 货币单位）。
// 金额一律使用 int64 微元表示，避免 float64 累计精度损失。
type ModelRate struct {
	InputPer1M      int64 // 未命中缓存的输入单价
	OutputPer1M     int64 // 非推理输出单价
	CacheReadPer1M  int64 // 缓存命中输入单价；0 表示未配置，回落到 InputPer1M
	CacheWritePer1M int64 // 缓存写入输入单价；0 表示未配置，回落到 InputPer1M
	ReasoningPer1M  int64 // 推理 token 单价；0 表示未配置，回落到 OutputPer1M（主流平台按输出价计）

	// CacheWrite5mPer1M 与 CacheWrite1hPer1M 是按缓存 TTL 分档的写入单价，
	// 对应 Usage.CacheWrite5mTokens 与 CacheWrite1hTokens。
	// 长 TTL 的写入单价通常更高，两档配置不同费率才能算准；
	// 0 表示未配置，该档回落到 CacheWritePer1M（再回落到 InputPer1M）。
	// 平台未上报分档明细时这两项不参与计算，写入总量整体按 CacheWritePer1M 计价。
	CacheWrite5mPer1M int64
	CacheWrite1hPer1M int64

	// WebSearchPer1K 是"按搜索次数"口径的原生联网搜索单价（微元 / 1000 次），
	// 对应 Usage.WebSearchRequests（Anthropic 全系、Gemini 3 系按 query 计费）。
	// 与 GroundedPromptPer1K 无条件互斥：一个模型只可能按一种口径计费，
	// 双配即配置错误（ErrInvalidPricing，Validate 与 Cost 均拒绝）；
	// 存在搜索用量但全缺时 Cost 返回 ErrModelNotPriced。
	WebSearchPer1K int64

	// GroundedPromptPer1K 是"按触发 grounding 的请求"口径的搜索单价（微元 / 1000 次），
	// 对应 Usage.WebSearchGroundedPrompts（Gemini 2.5 系按 grounded prompt 计费）。
	// 互斥规则见 WebSearchPer1K。
	GroundedPromptPer1K int64

	Currency string // 币种标识，如 "CNY"、"USD"
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
// CacheRead/WriteTokens ⊆ PromptTokens、CacheWrite5m/1hTokens ⊆ CacheWriteTokens）
// 保证各计费项互斥，不会重复计费：
//
//	inputBase      = PromptTokens - CacheReadTokens - CacheWriteTokens
//	outputBase     = CompletionTokens - ReasoningTokens
//	cacheWriteBase = CacheWriteTokens - CacheWrite5mTokens - CacheWrite1hTokens
//	cost = inputBase*Input + CacheRead*CacheRead
//	     + cacheWriteBase*CacheWrite
//	     + CacheWrite5m*CacheWrite5m + CacheWrite1h*CacheWrite1h
//	     + outputBase*Output + Reasoning*Reasoning
//	     + searchUnits*1000*searchRate  （各项 / 1M，末尾一次整除）
//
// 缓存写入按 TTL 分档计价：平台上报明细时各档按自己的费率计，未被分档覆盖的
// 部分按 CacheWritePer1M 计；平台未上报明细时写入总量整体按 CacheWritePer1M 计，
// 与未配置分档费率时的结果一致。
//
// 原生搜索按次计价（微元/1000 次），换算到统一的 1M 基数后与 token 项一并累加。
// 两种搜索口径（WebSearchRequests / WebSearchGroundedPrompts）按费率配置二选一：
// 配置 WebSearchPer1K 按次数计、配置 GroundedPromptPer1K 按 grounded prompt 计；
// 双配无条件返回 ErrInvalidPricing（配置错误，启动期可用 Validate 提前发现），
// 存在搜索用量但费率全缺、或配置口径与实际用量口径不符时返回
// ErrModelNotPriced，不静默漏账。
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
	searchUnits, searchRate, err := webSearchCostTerm(model, usage, rate)
	if err != nil {
		return 0, "", err
	}
	if searchUnits > math.MaxInt/searchUnitScale {
		return 0, "", fmt.Errorf("%w: model %q cost overflow", ErrInvalidPricing, model)
	}

	cacheReadRate := fallbackRate(rate.CacheReadPer1M, rate.InputPer1M)
	cacheWriteRate := fallbackRate(rate.CacheWritePer1M, rate.InputPer1M)
	cacheWrite5mRate := fallbackRate(rate.CacheWrite5mPer1M, cacheWriteRate)
	cacheWrite1hRate := fallbackRate(rate.CacheWrite1hPer1M, cacheWriteRate)
	reasoningRate := fallbackRate(rate.ReasoningPer1M, rate.OutputPer1M)

	inputBase := usage.PromptTokens - usage.CacheReadTokens - usage.CacheWriteTokens
	outputBase := usage.CompletionTokens - usage.ReasoningTokens
	// 写入总量中未被 TTL 分档覆盖的部分按总档单价计；validateUsage 已保证不为负。
	cacheWriteBase := usage.CacheWriteTokens - usage.CacheWrite5mTokens - usage.CacheWrite1hTokens

	var totalHi, totalLo uint64
	for _, term := range []struct {
		tokens int
		rate   int64
	}{
		{inputBase, rate.InputPer1M},
		{usage.CacheReadTokens, cacheReadRate},
		{cacheWriteBase, cacheWriteRate},
		{usage.CacheWrite5mTokens, cacheWrite5mRate},
		{usage.CacheWrite1hTokens, cacheWrite1hRate},
		{outputBase, rate.OutputPer1M},
		{usage.ReasoningTokens, reasoningRate},
		{searchUnits * searchUnitScale, searchRate},
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

// searchUnitScale 将按次搜索费率（微元/1K 次）换算到 1M 计量基数：
// requests * WebSearchPer1K / 1000 == requests * searchUnitScale * WebSearchPer1K / 1M。
const searchUnitScale = 1000

// maxRatePer1M 是费率合法上限（1e12 微元 / 1M tokens，即每百万 token 一百万元）。
// Cost 对合法费率仍执行 128 位乘加与最终 int64 边界校验。
const maxRatePer1M = 1_000_000_000_000

// webSearchCostTerm 按费率配置在两种搜索口径中选定计费项：
// 返回 (用量, 单价)。无搜索用量时恒返回 (0, 0)；有用量时执行漏账校验，
// 双费率互斥已在 validateRate 中无条件拒绝。规则见 Cost 与 ModelRate 的字段文档。
func webSearchCostTerm(model string, usage Usage, rate ModelRate) (units int, per1K int64, err error) {
	hasUsage := usage.WebSearchRequests > 0 || usage.WebSearchGroundedPrompts > 0
	if !hasUsage {
		return 0, 0, nil
	}
	switch {
	case rate.WebSearchPer1K > 0:
		if usage.WebSearchRequests == 0 {
			return 0, 0, fmt.Errorf(
				"%w: model %q priced per search request but usage only reports grounded prompts", ErrModelNotPriced, model)
		}
		return usage.WebSearchRequests, rate.WebSearchPer1K, nil
	case rate.GroundedPromptPer1K > 0:
		if usage.WebSearchGroundedPrompts == 0 {
			return 0, 0, fmt.Errorf(
				"%w: model %q priced per grounded prompt but usage only reports search requests", ErrModelNotPriced, model)
		}
		return usage.WebSearchGroundedPrompts, rate.GroundedPromptPer1K, nil
	default:
		return 0, 0, fmt.Errorf("%w: model %q web search rate not configured", ErrModelNotPriced, model)
	}
}

func validateRate(model string, rate ModelRate) error {
	for _, per1M := range []int64{
		rate.InputPer1M, rate.OutputPer1M, rate.CacheReadPer1M, rate.CacheWritePer1M, rate.ReasoningPer1M,
		rate.CacheWrite5mPer1M, rate.CacheWrite1hPer1M,
		rate.WebSearchPer1K, rate.GroundedPromptPer1K,
	} {
		if per1M < 0 || per1M > maxRatePer1M {
			return fmt.Errorf("%w: model %q rate %d out of [0, %d]", ErrInvalidPricing, model, per1M, int64(maxRatePer1M))
		}
	}
	// 双搜索费率无条件互斥：一个模型只可能按一种口径计费，双配是配置错误，
	// 应在首次计价（或启动期 Validate）即暴露，而不是等出现搜索用量才发现。
	if rate.WebSearchPer1K > 0 && rate.GroundedPromptPer1K > 0 {
		return fmt.Errorf(
			"%w: model %q configures both WebSearchPer1K and GroundedPromptPer1K", ErrInvalidPricing, model)
	}
	return nil
}

// Validate 在启动期整表校验费率合法性（范围与搜索双费率互斥），
// 返回按模型名排序聚合的全部问题；表为空或全部合法返回 nil。
// 建议在服务启动、价格表热更新时调用，把配置错误挡在计价之前。
func (t PricingTable) Validate() error {
	var errs []error
	for _, model := range slices.Sorted(maps.Keys(t)) {
		if err := validateRate(model, t[model]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validateUsage(usage Usage) error {
	for _, n := range []int{
		usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens,
		usage.CacheReadTokens, usage.CacheWriteTokens, usage.TotalTokens,
		usage.CacheWrite5mTokens, usage.CacheWrite1hTokens,
		usage.WebSearchRequests, usage.WebSearchGroundedPrompts,
	} {
		if n < 0 {
			return fmt.Errorf("%w: negative token count %d", ErrInvalidPricing, n)
		}
	}
	if usage.CacheReadTokens > usage.PromptTokens-usage.CacheWriteTokens {
		return fmt.Errorf("%w: cache read/write tokens exceed prompt tokens", ErrInvalidPricing)
	}
	// 分档必须是写入总量的子集，否则 Cost 的未分档部分会变成负数、算出偏低的金额。
	if usage.CacheWrite5mTokens+usage.CacheWrite1hTokens > usage.CacheWriteTokens {
		return fmt.Errorf("%w: cache write tier tokens (5m %d + 1h %d) exceed cache write tokens %d",
			ErrInvalidPricing, usage.CacheWrite5mTokens, usage.CacheWrite1hTokens, usage.CacheWriteTokens)
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
