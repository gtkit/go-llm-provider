package provider

import (
	"fmt"
	"slices"

	"github.com/sashabaranov/go-openai"
)

// OpenAI 兼容平台的服务端内建联网搜索映射。
//
// 两家平台的声明位置不同，各走一条既有通道：
//   - 智谱的搜索是 tools 数组里的一项，形如 {"type":"web_search","web_search":{...}}。
//     openai.Tool 的 Parameters 字段会被其 MarshalJSON 序列化到 type 旁边，
//     恰好能表达这种带同名兄弟对象的工具项，因此走常规的工具映射。
//   - 百炼的搜索是请求体顶层参数（enable_search / search_options），go-openai
//     的请求结构表达不了，走 extrafields.go 的顶层字段注入通道。

// 百炼的联网搜索顶层参数。
const (
	qwenEnableSearchField     = "enable_search"
	qwenSearchOptionsField    = "search_options"
	qwenAssignedSiteListField = "assigned_site_list"
)

// 智谱的搜索工具字段取值。zhipuWebSearchToolType 既是 tools 项的 type，
// 也是同名兄弟对象的键；enable 按平台文档是字符串而非布尔。
const (
	zhipuWebSearchToolType = "web_search"
	zhipuWebSearchEnabled  = "True"
)

// compatWebSearchSupport 描述一个 OpenAI 兼容平台对内建联网搜索的支持形态。
type compatWebSearchSupport struct {
	// allowedDomainsLimit 是平台白名单字段能承载的域名个数上限，
	// <= 0 表示不限个数。智谱的 search_domain_filter 是单值字符串，取 1。
	allowedDomainsLimit int

	// sourcesUnavailable 表示该平台的 OpenAI 兼容协议不回传搜索来源。
	sourcesUnavailable bool
}

// compatWebSearchSupportByProvider 是"哪些 OpenAI 兼容平台支持内建搜索"的唯一来源：
// 请求校验、工具映射、响应捕获与能力位都查这张表。初始化后只读。
var compatWebSearchSupportByProvider = map[ProviderName]compatWebSearchSupport{
	ProviderZhipu: {allowedDomainsLimit: 1},
	ProviderQwen:  {sourcesUnavailable: true},
}

// compatWebSearchOptions 取出请求中声明的搜索工具选项，未声明时返回 nil。
// validateWebSearchRequest 已保证至多声明一个。
func compatWebSearchOptions(tools []Tool) *WebSearchOptions {
	for _, tool := range tools {
		if tool.WebSearch != nil {
			return tool.WebSearch
		}
	}

	return nil
}

// validateCompatWebSearch 校验兼容平台上的搜索选项，无对应物的字段一律拒绝。
// 该平台未登记支持，或本次未声明搜索工具时恒返回 nil——前者由
// buildOpenAITools 以"该 provider 不支持原生搜索"拒绝，错误更明确。
func validateCompatWebSearch(name ProviderName, tools []Tool) error {
	support, ok := compatWebSearchSupportByProvider[name]
	if !ok {
		return nil
	}
	opts := compatWebSearchOptions(tools)
	if opts == nil {
		return nil
	}
	if opts.MaxUses > 0 {
		return fmt.Errorf("%w: %s does not support WebSearchOptions.MaxUses", ErrInvalidRequest, name)
	}
	if len(opts.BlockedDomains) > 0 {
		return fmt.Errorf("%w: %s does not support WebSearchOptions.BlockedDomains", ErrInvalidRequest, name)
	}
	if limit := support.allowedDomainsLimit; limit > 0 && len(opts.AllowedDomains) > limit {
		// 静默只取第一个会让调用方以为白名单生效，实际放行了其余全部域名。
		// 这是安全边界，只能拒绝。
		return fmt.Errorf(
			"%w: %s supports at most %d entry in WebSearchOptions.AllowedDomains, got %d",
			ErrInvalidRequest, name, limit, len(opts.AllowedDomains),
		)
	}
	// 空串会被平台字段的 omitempty 吃掉或被平台忽略，结果是白名单形同虚设而
	// 调用方毫不知情——同样是安全边界，拒绝而非放行。
	if slices.Contains(opts.AllowedDomains, "") {
		return fmt.Errorf("%w: %s received an empty domain in WebSearchOptions.AllowedDomains", ErrInvalidRequest, name)
	}

	return nil
}

// zhipuWebSearchSpec 只声明本库实际映射的字段；平台其余选项未映射，
// 不下发（下发默认值会覆盖平台侧的账号级配置）。
// SearchResult 置 true 才会在响应中回传来源，是 Search.Sources 的前提。
type zhipuWebSearchSpec struct {
	Enable             string `json:"enable"`
	SearchResult       bool   `json:"search_result"`
	SearchDomainFilter string `json:"search_domain_filter,omitempty"`
}

// zhipuWebSearchTool 把搜索选项映射为智谱的工具项。
// Parameters 的键会被 openai.Tool.MarshalJSON 平铺到 type 旁边，
// 因此产出的 JSON 是 {"type":"web_search","web_search":{...}}。
func zhipuWebSearchTool(opts *WebSearchOptions) openai.Tool {
	spec := zhipuWebSearchSpec{Enable: zhipuWebSearchEnabled, SearchResult: true}
	if len(opts.AllowedDomains) == 1 {
		spec.SearchDomainFilter = opts.AllowedDomains[0]
	}

	return openai.Tool{
		Type:       zhipuWebSearchToolType,
		Parameters: map[string]any{zhipuWebSearchToolType: spec},
	}
}

// compatWebSearchTool 把搜索选项映射为该平台在 tools 数组中的工具项。
// 平台的搜索声明不在 tools 里（如百炼走顶层参数）时返回 (zero, false)。
func compatWebSearchTool(name ProviderName, opts *WebSearchOptions) (openai.Tool, bool) {
	if name != ProviderZhipu {
		return openai.Tool{}, false
	}

	return zhipuWebSearchTool(opts), true
}

// qwenWebSearchFields 在声明了搜索工具时返回百炼的顶层搜索参数，否则返回 nil。
func qwenWebSearchFields(req *ChatRequest) map[string]any {
	if req == nil {
		return nil
	}
	opts := compatWebSearchOptions(req.Tools)
	if opts == nil {
		return nil
	}

	fields := map[string]any{qwenEnableSearchField: true}
	if len(opts.AllowedDomains) > 0 {
		// 注入的字段会一路传到 HTTP 层，不能把调用方仍持有的切片直接交出去。
		fields[qwenSearchOptionsField] = map[string]any{
			qwenAssignedSiteListField: slices.Clone(opts.AllowedDomains),
		}
	}

	return fields
}

// compatSearchMetadata 组装兼容平台的搜索结果元数据。
// 本次未声明搜索工具，或该平台未登记支持时返回 nil——Search 非 nil 的语义是
// "本次请求走了搜索路径"，不能因为平台不回传来源就变成 nil。
func compatSearchMetadata(name ProviderName, req *ChatRequest, capture *searchCapture) *SearchMetadata {
	support, ok := compatWebSearchSupportByProvider[name]
	if !ok || req == nil || !hasWebSearchTool(req.Tools) {
		return nil
	}
	meta := &SearchMetadata{SourcesUnavailable: support.sourcesUnavailable}
	if !support.sourcesUnavailable {
		meta.Sources = capture.sources()
	}

	return meta
}

// providerCapturesSearchSources 报告该平台的响应体里可能带回搜索来源，
// 决定是否要为它包装 searchCaptureDoer。协议不回传来源的平台不包装——
// 白读一遍响应体没有任何收益。
func providerCapturesSearchSources(name ProviderName) bool {
	support, ok := compatWebSearchSupportByProvider[name]

	return ok && !support.sourcesUnavailable
}

// needsSearchCapture 报告本次请求是否要捕获响应体中的搜索来源。
func needsSearchCapture(name ProviderName, req *ChatRequest) bool {
	return providerCapturesSearchSources(name) && req != nil && hasWebSearchTool(req.Tools)
}

// supportsCompatWebSearch 报告该 OpenAI 兼容平台是否登记了内建搜索支持。
func supportsCompatWebSearch(name ProviderName) bool {
	_, ok := compatWebSearchSupportByProvider[name]

	return ok
}
