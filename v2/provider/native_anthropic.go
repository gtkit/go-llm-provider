package provider

import (
	"encoding/base64"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/gtkit/json/v2"
)

type anthropicRequest struct {
	Model         string               `json:"model"`
	MaxTokens     int                  `json:"max_tokens"`
	Messages      []anthropicMessage   `json:"messages"`
	System        string               `json:"system,omitempty"`
	Temperature   *float32             `json:"temperature,omitempty"`
	TopP          *float32             `json:"top_p,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	Tools         []anthropicTool      `json:"tools,omitempty"`
	ToolChoice    *anthropicToolChoice `json:"tool_choice,omitempty"`
	Thinking      *anthropicThinking   `json:"thinking,omitempty"`
}

// anthropicThinking 是 Messages API 的 thinking 参数。
type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

const (
	anthropicThinkingEnabled  = "enabled"
	anthropicThinkingDisabled = "disabled"
)

// anthropicThinkingParam 把统一 Thinking 映射为 Anthropic 的 thinking 参数。
// 只处理 Enabled 与 BudgetTokens——Effort 无对应参数，已由 validateThinking 拦下。
//
// 开启思考时 budget_tokens 必填且须为正数，缺失或非正数返回 ErrInvalidRequest
// 而不代为推导：推理 token 按输出价计费，预算大小必须由调用方决定。
// "预算须小于 max_tokens"这一上限由平台校验。
func anthropicThinkingParam(thinking *Thinking) (*anthropicThinking, error) {
	if thinking == nil {
		return nil, nil
	}
	if thinking.Enabled != nil && !*thinking.Enabled {
		if thinking.BudgetTokens != nil {
			return nil, fmt.Errorf(
				"%w: anthropic thinking cannot set BudgetTokens while Enabled is false", ErrInvalidRequest)
		}
		return &anthropicThinking{Type: anthropicThinkingDisabled}, nil
	}
	// Enabled 未显式设置时，给出预算即视为开启。
	if thinking.Enabled == nil && thinking.BudgetTokens == nil {
		return nil, nil
	}
	if thinking.BudgetTokens == nil {
		return nil, fmt.Errorf(
			"%w: anthropic thinking requires Thinking.BudgetTokens when enabled", ErrInvalidRequest)
	}
	// 非正数预算无法表达"开启思考"，且 0 会被 budget_tokens 的 omitempty 吞掉，
	// 发出只含 type 的不完整参数——在本地拒绝，不让平台收到语义损坏的请求。
	if *thinking.BudgetTokens <= 0 {
		return nil, fmt.Errorf(
			"%w: anthropic thinking budget must be positive, got %d",
			ErrInvalidRequest, *thinking.BudgetTokens)
	}
	return &anthropicThinking{
		Type:         anthropicThinkingEnabled,
		BudgetTokens: *thinking.BudgetTokens,
	}, nil
}

type anthropicMessage struct {
	Role    string                 `json:"role"`
	Content []anthropicContentPart `json:"content"`
}

type anthropicContentPart struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	Thinking     string                 `json:"thinking,omitempty"`
	Title        string                 `json:"title,omitempty"`
	Source       *anthropicImageSource  `json:"source,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        any                    `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      any                    `json:"content,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
	FileID    string `json:"file_id,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

// anthropicCountTokensRequest 是 /v1/messages/count_tokens 的请求体。
// 该端点只接受消息语义字段，携带 max_tokens / stream 等生成参数会被平台拒绝，
// 因此不复用 anthropicRequest。
type anthropicCountTokensRequest struct {
	Model      string               `json:"model"`
	Messages   []anthropicMessage   `json:"messages"`
	System     string               `json:"system,omitempty"`
	Tools      []anthropicTool      `json:"tools,omitempty"`
	ToolChoice *anthropicToolChoice `json:"tool_choice,omitempty"`
}

type anthropicCountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

type anthropicResponse struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Role       string                 `json:"role"`
	Model      string                 `json:"model"`
	Content    []anthropicContentPart `json:"content"`
	StopReason string                 `json:"stop_reason"`
	Usage      anthropicUsage         `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int                    `json:"input_tokens"`
	OutputTokens             int                    `json:"output_tokens"`
	CacheCreationInputTokens int                    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                    `json:"cache_read_input_tokens"`
	ServerToolUse            anthropicServerToolUse `json:"server_tool_use"`
}

type anthropicServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests"`
}

// usageFromAnthropic 将 anthropicUsage 归一化为统一 Usage。
// Anthropic 的 input_tokens 不含缓存读写部分，这里归一化为
// PromptTokens = input + cache_read + cache_write，与其他 provider 语义对齐。
func usageFromAnthropic(usage anthropicUsage) Usage {
	prompt := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	return Usage{
		PromptTokens:      prompt,
		CompletionTokens:  usage.OutputTokens,
		CacheReadTokens:   usage.CacheReadInputTokens,
		CacheWriteTokens:  usage.CacheCreationInputTokens,
		TotalTokens:       prompt + usage.OutputTokens,
		WebSearchRequests: usage.ServerToolUse.WebSearchRequests,
	}
}

// mergeAnthropicStreamUsage 将流事件携带的 usage 合并进累积值。
// message_start 提供输入侧统计，message_delta 提供累计的 output_tokens，
// 各字段仅在事件携带非零值时覆盖。
func mergeAnthropicStreamUsage(acc *anthropicUsage, event anthropicUsage) {
	if event.InputTokens > 0 {
		acc.InputTokens = event.InputTokens
	}
	if event.OutputTokens > 0 {
		acc.OutputTokens = event.OutputTokens
	}
	if event.CacheCreationInputTokens > 0 {
		acc.CacheCreationInputTokens = event.CacheCreationInputTokens
	}
	if event.CacheReadInputTokens > 0 {
		acc.CacheReadInputTokens = event.CacheReadInputTokens
	}
	if event.ServerToolUse.WebSearchRequests > 0 {
		acc.ServerToolUse.WebSearchRequests = event.ServerToolUse.WebSearchRequests
	}
}

type anthropicTool struct {
	Type           string   `json:"type,omitempty"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	InputSchema    any      `json:"input_schema,omitempty"`
	MaxUses        int      `json:"max_uses,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

type anthropicToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use,omitempty"`
}

// anthropicStreamState 保存流式解析的跨事件状态：
// usage 在 message_start / message_delta 间累积；serverToolBlocks 记录
// 服务端工具内容块的索引，用于抑制其增量事件外露；serverToolInputs
// 按块索引累积 server_tool_use 的 input JSON（流式下 input 走
// input_json_delta 增量），流结束时解析出搜索查询；search 聚合来源。
type anthropicStreamState struct {
	usage            anthropicUsage
	serverToolBlocks map[int]struct{}
	serverToolInputs map[int]*strings.Builder
	search           *SearchMetadata
}

func (s *anthropicStreamState) markServerToolBlock(index int) {
	if s.serverToolBlocks == nil {
		s.serverToolBlocks = make(map[int]struct{})
	}
	s.serverToolBlocks[index] = struct{}{}
}

func (s *anthropicStreamState) isServerToolBlock(index int) bool {
	_, ok := s.serverToolBlocks[index]
	return ok
}

func (s *anthropicStreamState) appendServerToolInput(index int, partial string) {
	if s.serverToolInputs == nil {
		s.serverToolInputs = make(map[int]*strings.Builder)
	}
	if s.serverToolInputs[index] == nil {
		s.serverToolInputs[index] = &strings.Builder{}
	}
	_, _ = s.serverToolInputs[index].WriteString(partial) // strings.Builder 不返回错误
}

// finalizeSearch 组装流上聚合的搜索元数据：按块索引升序解析累积的
// server_tool_use 输入提取查询，合并已收集的来源。未触发搜索时返回 nil。
func (s *anthropicStreamState) finalizeSearch() *SearchMetadata {
	for _, index := range slices.Sorted(maps.Keys(s.serverToolInputs)) {
		raw := s.serverToolInputs[index].String()
		if raw == "" {
			continue
		}
		var input any
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			continue
		}
		if query := anthropicServerToolQuery(input); query != "" {
			s.search = appendSearchQuery(s.search, query)
		}
	}
	s.serverToolInputs = nil
	return s.search
}

// anthropicServerToolBlock 判定内容块是否为平台服务端执行的工具块
// （原生 web search 的调用与结果），这类块不映射为客户端工具调用。
func anthropicServerToolBlock(blockType string) bool {
	return blockType == "server_tool_use" || blockType == "web_search_tool_result"
}

type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index"`
	ContentBlock anthropicContentPart   `json:"content_block"`
	Delta        anthropicStreamDelta   `json:"delta"`
	Message      anthropicStreamMessage `json:"message"`
	Usage        anthropicUsage         `json:"usage"`
	Error        anthropicErrorBody     `json:"error"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

type anthropicStreamMessage struct {
	ID    string         `json:"id"`
	Model string         `json:"model"`
	Usage anthropicUsage `json:"usage"`
}

type anthropicErrorEnvelope struct {
	Type  string             `json:"type"`
	Error anthropicErrorBody `json:"error"`
}

type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func anthropicContentParts(parts []ContentPart) ([]anthropicContentPart, error) {
	if len(parts) == 0 {
		return []anthropicContentPart{{Type: "text", Text: ""}}, nil
	}
	out := make([]anthropicContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentTypeText:
			out = append(out, withAnthropicCacheControl(anthropicContentPart{Type: "text", Text: part.Text}, part.CacheControl))
		case ContentTypeImageURL:
			image, err := anthropicImagePart(part)
			if err != nil {
				return nil, err
			}
			out = append(out, withAnthropicCacheControl(image, part.CacheControl))
		case ContentTypeFile:
			file, err := anthropicFilePart(part)
			if err != nil {
				return nil, err
			}
			out = append(out, withAnthropicCacheControl(file, part.CacheControl))
		default:
			return nil, fmt.Errorf("%w: unsupported anthropic content type %q", ErrInvalidRequest, part.Type)
		}
	}
	return out, nil
}

func anthropicAssistantParts(msg Message) ([]anthropicContentPart, error) {
	parts, err := anthropicContentParts(msg.Content)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 && parts[0].Type == "text" && parts[0].Text == "" && len(msg.ToolCalls) > 0 {
		parts = parts[:0]
	}
	for _, call := range msg.ToolCalls {
		input, err := rawJSONArgument(call.Function.Arguments)
		if err != nil {
			return nil, err
		}
		parts = append(parts, anthropicContentPart{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Function.Name,
			Input: input,
		})
	}
	return parts, nil
}

func anthropicToolResultPart(msg Message) anthropicContentPart {
	return anthropicContentPart{
		Type:      "tool_result",
		ToolUseID: msg.ToolCallID,
		Content:   msg.Content,
	}
}

func anthropicImagePart(part ContentPart) (anthropicContentPart, error) {
	if len(part.ImageData) > 0 {
		if part.MIMEType == "" {
			return anthropicContentPart{}, fmt.Errorf("%w: image MIME type is required", ErrInvalidRequest)
		}
		return anthropicContentPart{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: part.MIMEType,
				Data:      base64.StdEncoding.EncodeToString(part.ImageData),
			},
		}, nil
	}
	if part.ImageURL == "" {
		return anthropicContentPart{}, fmt.Errorf("%w: image source is required", ErrInvalidRequest)
	}
	if strings.HasPrefix(part.ImageURL, "data:") {
		mimeType, data, ok := parseDataURLImage(part.ImageURL)
		if !ok {
			return anthropicContentPart{}, fmt.Errorf("%w: invalid data URL image", ErrInvalidRequest)
		}
		return anthropicContentPart{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: mimeType,
				Data:      data,
			},
		}, nil
	}
	return anthropicContentPart{
		Type: "image",
		Source: &anthropicImageSource{
			Type: "url",
			URL:  part.ImageURL,
		},
	}, nil
}

func anthropicFilePart(part ContentPart) (anthropicContentPart, error) {
	switch {
	case len(part.FileData) > 0:
		if part.MIMEType == "" {
			return anthropicContentPart{}, fmt.Errorf("%w: file MIME type is required", ErrInvalidRequest)
		}
		return anthropicContentPart{
			Type:  "document",
			Title: part.Filename,
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: part.MIMEType,
				Data:      base64.StdEncoding.EncodeToString(part.FileData),
			},
		}, nil
	case part.FileURL != "":
		return anthropicContentPart{
			Type:  "document",
			Title: part.Filename,
			Source: &anthropicImageSource{
				Type:      "url",
				URL:       part.FileURL,
				MediaType: part.MIMEType,
			},
		}, nil
	case part.FileID != "":
		return anthropicContentPart{
			Type:  "document",
			Title: part.Filename,
			Source: &anthropicImageSource{
				Type:   "file",
				FileID: part.FileID,
			},
		}, nil
	default:
		return anthropicContentPart{}, fmt.Errorf("%w: file source is required", ErrInvalidRequest)
	}
}

func withAnthropicCacheControl(part anthropicContentPart, control *CacheControl) anthropicContentPart {
	if control == nil {
		return part
	}
	part.CacheControl = &anthropicCacheControl{Type: string(control.Type)}
	return part
}

func anthropicRole(role Role) string {
	switch role {
	case RoleAssistant:
		return "assistant"
	default:
		return "user"
	}
}

// anthropicWebSearchToolType 是 Anthropic 服务端联网搜索工具的版本化类型标识。
const anthropicWebSearchToolType = "web_search_20250305"

// PauseTurnError 表示 Anthropic 在服务端工具（如原生 web search）运行中
// 暂停了回合（stop_reason "pause_turn"）。官方要求把暂停响应的内容块原样
// 回传续跑；本库尚未支持服务端工具块跨轮往返，因此以错误形式返回。
//
// Usage 与 Search 携带暂停前已真实产生的用量与搜索元数据——服务端已执行
// 的搜索会被平台计费，观测/计费层会从本错误提取用量，调用方不得按零消耗
// 处理。注意：不要原样重发请求"重试"——那会重新执行搜索、产生二次费用；
// 应降低搜索用量（如 MaxUses）或改用函数工具模式。
//
// errors.Is(err, ErrUnsupportedCapability) 为 true。
type PauseTurnError struct {
	Usage  Usage
	Search *SearchMetadata
}

func (e *PauseTurnError) Error() string {
	return `anthropic paused the turn while running a server tool (stop_reason "pause_turn"); resuming paused turns is not supported yet — do not resend the same request, it would re-run the search and incur charges again`
}

// Is 使 errors.Is(err, ErrUnsupportedCapability) 成立，兼容既有错误判定。
func (e *PauseTurnError) Is(target error) bool {
	return target == ErrUnsupportedCapability
}

func anthropicTools(tools []Tool) ([]anthropicTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	hasSearch, hasFunction := false, false
	out := make([]anthropicTool, 0, len(tools))
	for _, tool := range tools {
		if tool.WebSearch != nil {
			hasSearch = true
			search, err := anthropicWebSearchTool(tool.WebSearch)
			if err != nil {
				return nil, err
			}
			out = append(out, search)
			continue
		}
		hasFunction = true
		out = append(out, anthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	// 混用时响应会同时携带服务端与客户端工具块，续跑要求把服务端工具块
	// 原样回传；AssistantMessage 尚无法承载这些块，禁止组合而非静默丢上下文。
	if hasSearch && hasFunction {
		return nil, fmt.Errorf(
			"%w: anthropic web search cannot be combined with function tools yet (server tool blocks are not preserved across turns)",
			ErrInvalidRequest)
	}
	return out, nil
}

func anthropicWebSearchTool(opts *WebSearchOptions) (anthropicTool, error) {
	if len(opts.AllowedDomains) > 0 && len(opts.BlockedDomains) > 0 {
		return anthropicTool{}, fmt.Errorf(
			"%w: web search allowed and blocked domains are mutually exclusive", ErrInvalidRequest)
	}
	return anthropicTool{
		Type:           anthropicWebSearchToolType,
		Name:           "web_search",
		MaxUses:        max(opts.MaxUses, 0),
		AllowedDomains: opts.AllowedDomains,
		BlockedDomains: opts.BlockedDomains,
	}, nil
}

func anthropicStructuredTool(format *ResponseFormat) (*anthropicTool, *anthropicToolChoice, error) {
	if format == nil || format.Type == ResponseFormatText {
		return nil, nil, nil
	}
	switch format.Type {
	case ResponseFormatJSONObject, ResponseFormatJSONSchema:
	default:
		return nil, nil, fmt.Errorf("%w: unsupported anthropic response format %q", ErrInvalidRequest, format.Type)
	}

	name := format.Name
	if name == "" {
		name = "json_response"
	}
	schema := format.Schema
	if schema == nil {
		schema = ParamSchema{
			Type:                 "object",
			AdditionalProperties: boolPtr(true),
		}
	}
	return &anthropicTool{
		Name:        name,
		Description: "Return the response as structured JSON.",
		InputSchema: schema,
	}, &anthropicToolChoice{
		Type: "tool",
		Name: name,
	}, nil
}

func boolPtr(value bool) *bool {
	return &value
}

func buildAnthropicToolChoice(req *ChatRequest) (*anthropicToolChoice, error) {
	if req.ToolChoice == nil && req.ParallelToolCalls == nil {
		return nil, nil
	}

	choice := &anthropicToolChoice{}
	switch v := req.ToolChoice.(type) {
	case nil:
		choice.Type = "auto"
	case ToolChoiceMode:
		switch v {
		case ToolChoiceAuto:
			choice.Type = "auto"
		case ToolChoiceNone:
			choice.Type = "none"
		case ToolChoiceRequired:
			choice.Type = "any"
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidToolChoice, v)
		}
	case ToolChoiceFunction:
		if v.Name == "" {
			return nil, fmt.Errorf("%w: function name is required", ErrInvalidToolChoice)
		}
		choice.Type = "tool"
		choice.Name = v.Name
	default:
		return nil, fmt.Errorf("%w: unsupported anthropic tool choice %T", ErrInvalidToolChoice, req.ToolChoice)
	}

	if req.ParallelToolCalls != nil {
		disable := !*req.ParallelToolCalls
		choice.DisableParallelToolUse = &disable
	}
	return choice, nil
}

// anthropicParsedContent 是 Anthropic 响应内容的解析结果。
type anthropicParsedContent struct {
	Text         string
	Reasoning    string
	FinishReason string
	ToolCalls    []ToolCall
	Search       *SearchMetadata
}

func anthropicResponseContent(resp anthropicResponse) (anthropicParsedContent, error) {
	var out anthropicParsedContent
	var text strings.Builder
	var thinking strings.Builder
	for _, part := range resp.Content {
		switch part.Type {
		case "text":
			text.WriteString(part.Text)
		case "thinking":
			// 思考内容单独归位到 ChatResponse.Reasoning，不混入正文。
			thinking.WriteString(part.Thinking)
		case "tool_use":
			arguments, marshalErr := json.Marshal(part.Input)
			if marshalErr != nil {
				return anthropicParsedContent{}, fmt.Errorf("marshal anthropic tool input: %w", marshalErr)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID: part.ID,
				Function: FunctionCall{
					Name:      part.Name,
					Arguments: string(arguments),
				},
			})
		case "server_tool_use":
			if query := anthropicServerToolQuery(part.Input); query != "" {
				out.Search = appendSearchQuery(out.Search, query)
			}
		case "web_search_tool_result":
			sources, searchErr := anthropicSearchResultContent(part.Content)
			out.Search = appendSearchSources(out.Search, sources)
			out.Search = appendSearchError(out.Search, searchErr)
		}
	}
	if len(out.ToolCalls) > 0 || resp.StopReason == "tool_use" {
		out.FinishReason = "tool_calls"
	} else {
		out.FinishReason = resp.StopReason
	}
	out.Text = text.String()
	out.Reasoning = thinking.String()
	return out, nil
}

// anthropicServerToolQuery 提取 server_tool_use（web_search）输入中的搜索查询。
func anthropicServerToolQuery(input any) string {
	raw, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	var parsed struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	return parsed.Query
}

type anthropicWebSearchResult struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// anthropicSearchResultContent 解析 web_search_tool_result 块的 content：
// 成功时为结果数组（[{type:"web_search_result",url,title,...}]），
// 失败时为错误对象（{type:"web_search_tool_result_error",error_code:...}），
// 后者经 HTTP 200 到达，必须透出而非静默丢弃。两种形态都无法解析时返回双 nil。
func anthropicSearchResultContent(content any) ([]SearchSource, *SearchError) {
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, nil
	}
	var results []anthropicWebSearchResult
	if err := json.Unmarshal(raw, &results); err == nil {
		sources := make([]SearchSource, 0, len(results))
		for _, result := range results {
			if result.Type != "" && result.Type != "web_search_result" {
				continue
			}
			sources = append(sources, SearchSource{URL: result.URL, Title: result.Title})
		}
		if len(sources) == 0 {
			return nil, nil
		}
		return sources, nil
	}
	var errBody struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(raw, &errBody); err == nil && errBody.ErrorCode != "" {
		return nil, &SearchError{Code: errBody.ErrorCode}
	}
	return nil, nil
}

// ensureSearch 惰性初始化 SearchMetadata，供各 append helper 复用。
func ensureSearch(search *SearchMetadata) *SearchMetadata {
	if search == nil {
		return &SearchMetadata{}
	}
	return search
}

func appendSearchQuery(search *SearchMetadata, query string) *SearchMetadata {
	search = ensureSearch(search)
	search.Queries = append(search.Queries, query)
	return search
}

func appendSearchSources(search *SearchMetadata, sources []SearchSource) *SearchMetadata {
	if len(sources) == 0 {
		return search
	}
	search = ensureSearch(search)
	search.Sources = append(search.Sources, sources...)
	return search
}

func appendSearchError(search *SearchMetadata, searchErr *SearchError) *SearchMetadata {
	if searchErr == nil {
		return search
	}
	search = ensureSearch(search)
	search.Errors = append(search.Errors, *searchErr)
	return search
}

func anthropicStructuredContent(resp anthropicResponse) (content string, ok bool, err error) {
	for _, part := range resp.Content {
		if part.Type != "tool_use" {
			continue
		}
		data, err := json.Marshal(part.Input)
		if err != nil {
			return "", false, fmt.Errorf("marshal anthropic structured input: %w", err)
		}
		return string(data), true, nil
	}
	return "", false, nil
}

func decodeAnthropicError(provider ProviderName, statusCode int, status string, body []byte) error {
	var envelope anthropicErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nativeStatusError(provider, statusCode, status, string(body))
	}
	code := anthropicErrorCode(statusCode, envelope.Error.Type)
	return &ProviderError{
		Provider:   provider,
		Code:       code,
		StatusCode: statusCode,
		Status:     status,
		RawType:    envelope.Error.Type,
		Retryable:  RetryableByCode(code),
		Message:    envelope.Error.Message,
	}
}

func anthropicStreamProviderError(event anthropicStreamEvent) error {
	code := anthropicErrorCode(http.StatusOK, event.Error.Type)
	return &ProviderError{
		Provider:  ProviderAnthropic,
		Code:      code,
		RawType:   event.Error.Type,
		Retryable: RetryableByCode(code),
		Message:   event.Error.Message,
	}
}

func anthropicErrorCode(statusCode int, rawType string) ErrorCode {
	switch strings.ToLower(rawType) {
	case "authentication_error", "permission_error":
		return ErrorCodeAuth
	case "rate_limit_error":
		return ErrorCodeRateLimit
	case "overloaded_error", "api_error":
		return ErrorCodeServerError
	case "invalid_request_error":
		return ErrorCodeInvalidRequest
	default:
		return CodeFromHTTPStatus(statusCode)
	}
}
