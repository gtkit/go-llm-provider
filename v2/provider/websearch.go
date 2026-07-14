package provider

// WebSearchOptions 配置厂商原生联网搜索工具的行为，零值可用。
//
// 原生搜索由平台服务端执行：模型自行决定何时搜索、搜索结果直接参与生成，
// 不产生客户端 ToolCall。搜索按次单独计费，用量见 Usage 的
// WebSearchRequests / WebSearchGroundedPrompts，费率见 ModelRate 的
// WebSearchPer1K / GroundedPromptPer1K。
//
// MaxUses 与域名过滤具有成本与访问范围约束语义，不支持的平台一律返回
// ErrInvalidRequest 而非静默忽略——静默降级会绕过调用方的安全与费用策略。
type WebSearchOptions struct {
	// MaxUses 限制单次请求中的最大搜索次数，<= 0 使用平台默认。
	// 仅 Anthropic 支持；Gemini 收到非零值返回 ErrInvalidRequest。
	MaxUses int

	// AllowedDomains 将搜索结果限定在这些域名内（白名单）。
	// 仅 Anthropic 支持，与 BlockedDomains 互斥，同时设置返回 ErrInvalidRequest；
	// Gemini 收到非空值返回 ErrInvalidRequest。
	AllowedDomains []string

	// BlockedDomains 从搜索结果中排除这些域名（黑名单）。
	// 仅 Anthropic 支持，与 AllowedDomains 互斥，同时设置返回 ErrInvalidRequest；
	// Gemini 收到非空值返回 ErrInvalidRequest。
	BlockedDomains []string
}

// WebSearchTool 构造一个厂商原生联网搜索工具。仅 Anthropic 与 Gemini
// 原生路径支持；其他 provider 收到该工具时返回 ErrInvalidRequest，不静默丢弃。
// 是否支持可用 ModelCapabilities.Supports(CapabilityWebSearch) 预检。
// 其余平台（含国内 OpenAI 兼容平台）接入搜索的推荐方式是普通函数工具 +
// RunToolLoop，由调用方 ToolHandler 调用搜索 API，见 README"国内平台的
// 联网搜索接入"一节。
//
// 当前限制：原生搜索不能与函数工具在同一请求中混用（Anthropic 的服务端
// 工具块尚未支持跨轮往返，Gemini 2.5 系平台不支持该组合），Anthropic 侧
// 也不能与结构化输出组合——这些组合一律返回 ErrInvalidRequest。
// 搜索结果的查询、来源与引用见 ChatResponse.Search / StreamChunk.Search。
func WebSearchTool() Tool {
	return Tool{WebSearch: &WebSearchOptions{}}
}

// WebSearchToolWithOptions 构造带配置的厂商原生联网搜索工具。
// 各字段的平台支持范围见 WebSearchOptions 说明。
func WebSearchToolWithOptions(opts WebSearchOptions) Tool {
	return Tool{WebSearch: &opts}
}

// SearchMetadata 是厂商原生联网搜索的结果元数据，跨 provider 归一，
// 见 ChatResponse.Search 与最终 StreamChunk.Search。
// 当前透出查询、来源与 Google Search 入口；回复文本级的引用区间暂不透出。
//
// 合规提示：Gemini 响应携带 SearchEntryPoint 时，Google 的使用条款要求
// 向终端用户展示 Search Suggestions 入口；面向用户的产品请遵守对应平台
// 的搜索结果展示与引用要求。
type SearchMetadata struct {
	// Queries 是本次调用实际执行的搜索查询。
	Queries []string

	// Sources 是搜索结果来源，顺序与平台返回一致。
	Sources []SearchSource

	// SearchEntryPoint 是 Google Search Suggestions 的渲染内容
	// （groundingMetadata.searchEntryPoint.renderedContent），仅 Gemini 返回。
	SearchEntryPoint string
}

// SearchSource 是一条搜索结果来源。
type SearchSource struct {
	URL   string
	Title string
}

// hasWebSearchTool 报告工具列表中是否声明了原生联网搜索。
func hasWebSearchTool(tools []Tool) bool {
	for _, tool := range tools {
		if tool.WebSearch != nil {
			return true
		}
	}
	return false
}
