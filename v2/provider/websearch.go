package provider

import "fmt"

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

	// Errors 是平台在 HTTP 200 响应内报告的搜索失败（如 Anthropic 的
	// max_uses_exceeded / too_many_requests / unavailable）。此时模型通常
	// 仍会生成解释性文本，Content 是否可用由调用方判断；本库如实透出，
	// 不静默丢弃——调用方可据此区分"未搜索"与"搜索失败"。
	Errors []SearchError
}

// SearchSource 是一条搜索结果来源。
type SearchSource struct {
	URL   string
	Title string
}

// SearchError 是平台报告的一次搜索失败。
type SearchError struct {
	// Code 是平台原始错误码，如 "max_uses_exceeded"、"too_many_requests"。
	Code string
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

// validateWebSearchRequest 校验携带原生搜索工具的请求的结构约束
// （平台无关，Anthropic 与 Gemini 的请求构造共用）：
//   - 同一 Tool 不得同时声明 Function 与 WebSearch（语义歧义，拒绝而非静默取舍）；
//   - 原生搜索工具至多声明一个（重复声明会向平台下发重名工具）；
//   - ToolChoiceRequired / ToolChoiceFunction 不得指向服务端搜索工具
//     （server tool 不受 tool_choice 约束，行为未定义）。
//
// 未声明搜索工具时恒返回 nil。
func validateWebSearchRequest(req *ChatRequest) error {
	searchTools, functionTools := 0, 0
	for _, tool := range req.Tools {
		if tool.WebSearch == nil {
			functionTools++
			continue
		}
		searchTools++
		if tool.Function.Name != "" || tool.Function.Description != "" || tool.Function.Parameters != nil {
			return fmt.Errorf("%w: a tool cannot declare both Function and WebSearch", ErrInvalidRequest)
		}
	}
	if searchTools == 0 {
		return nil
	}
	if searchTools > 1 {
		return fmt.Errorf("%w: at most one web search tool may be declared", ErrInvalidRequest)
	}
	// 混用（搜索 + 函数工具）由各 provider 的工具映射以更明确的错误拒绝，
	// tool_choice 约束只在纯搜索工具场景下校验。
	if functionTools > 0 {
		return nil
	}
	// 纯搜索场景下 tool_choice 只有 nil / Auto 有明确语义（交由模型决定是否搜索）：
	//   - Required / Function 会指向服务端搜索工具，而 server tool 不受 tool_choice 约束；
	//   - None 与"已声明搜索工具"直接矛盾（既声明又禁用）。
	// 这些组合行为未定义，一律拒绝而非静默生成可疑请求体。
	switch v := req.ToolChoice.(type) {
	case ToolChoiceMode:
		switch v {
		case ToolChoiceRequired:
			return fmt.Errorf("%w: tool choice %q cannot target a server-side web search tool", ErrInvalidRequest, v)
		case ToolChoiceNone:
			return fmt.Errorf("%w: tool choice %q contradicts a declared web search tool", ErrInvalidRequest, v)
		}
	case ToolChoiceFunction:
		return fmt.Errorf("%w: tool choice cannot force a function when only a server-side web search tool is declared", ErrInvalidRequest)
	}
	return nil
}
