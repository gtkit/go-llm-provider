package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gtkit/json/v2"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAIResponsesConfig 配置走 OpenAI Responses API（POST {BaseURL}/responses）的 Provider。
//
// Responses API 是 OpenAI 官方推荐的推理 / 工具调用 / 多轮入口，DeepSeek、火山方舟、智谱、
// Kimi 等平台也已提供同形端点。与 Chat Completions 路径相比，它把推理项作为一等输出
// （encrypted_content 可在零数据保留模式下往返）、原生联网搜索作为内建工具。
type OpenAIResponsesConfig struct {
	// Name 留空时为 ProviderOpenAI；接入其他提供 Responses 端点的平台时填各自的 ProviderName，
	// 观测与计费按该名字归账。
	Name       ProviderName
	BaseURL    string
	APIKey     string
	Model      string
	OrgID      string
	HTTPClient HTTPDoer

	// Store 控制平台是否保存本次响应。默认 false：零数据保留友好，推理项以
	// encrypted_content 随 ThinkingBlocks 往返；为 true 时平台按 id 保存状态。
	Store *bool

	// SupportsReasoningEffort 语义同 ProviderConfig.SupportsReasoningEffort，
	// 仅对库未收录的平台生效。
	SupportsReasoningEffort bool
}

// Validate 检查 OpenAIResponsesConfig 的必填字段。
func (cfg OpenAIResponsesConfig) Validate() error {
	var errs []error
	if cfg.APIKey == "" {
		errs = append(errs, errors.New("api key is required"))
	}
	if cfg.Model == "" {
		errs = append(errs, errors.New("model is required"))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalidProviderConfig, errors.Join(errs...))
}

// NewOpenAIResponsesProvider 创建走 Responses API 的 Provider。
// 返回的 Provider 在 cfg.HTTPClient 并发安全时并发安全。
func NewOpenAIResponsesProvider(cfg OpenAIResponsesConfig) (Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ocfg := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		ocfg.BaseURL = cfg.BaseURL
	}
	if cfg.OrgID != "" {
		ocfg.OrgID = cfg.OrgID
	}
	ocfg.HTTPClient = cfg.HTTPClient
	if ocfg.HTTPClient == nil {
		ocfg.HTTPClient = DefaultHTTPClient()
	}
	name := cfg.Name
	if name == "" {
		name = ProviderOpenAI
	}
	store := cfg.Store != nil && *cfg.Store
	return &responsesProvider{
		name:                    name,
		model:                   cfg.Model,
		store:                   store,
		supportsReasoningEffort: cfg.SupportsReasoningEffort,
		client:                  openai.NewClientWithConfig(ocfg),
	}, nil
}

type responsesProvider struct {
	name                    ProviderName
	model                   string
	store                   bool
	supportsReasoningEffort bool
	client                  *openai.Client
}

func (p *responsesProvider) Name() ProviderName {
	if p == nil {
		return ""
	}
	return p.name
}

// DefaultModel 返回构造时配置的默认模型名（实现观测层的可选探测接口）。
func (p *responsesProvider) DefaultModel() string {
	if p == nil {
		return ""
	}
	return p.model
}

func (p *responsesProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}
	nativeReq, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.CreateResponse(ctx, nativeReq)
	if err != nil {
		return nil, WrapProviderError(p.name, err)
	}
	if resp.Status == openai.ResponseStatusFailed {
		return nil, responsesFailure(p.name, resp.Error)
	}

	parsed, err := parseResponsesOutput(resp.Output)
	if err != nil {
		return nil, err
	}
	usage := usageFromResponses(resp.Usage, parsed.searchCalls)
	return &ChatResponse{
		Content:        parsed.text,
		Reasoning:      parsed.reasoning,
		ThinkingBlocks: parsed.thinkingBlocks,
		FinishReason:   responsesFinishReason(resp.Status, resp.IncompleteDetails, len(parsed.toolCalls) > 0),
		Usage:          usage,
		Metadata:       metadataFromHeader(p.name, firstString(resp.Model, nativeReq.Model), resp.Header()),
		Search:         parsed.search,
		ToolCalls:      parsed.toolCalls,
	}, nil
}

func (p *responsesProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}
	nativeReq, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}

	stream, err := p.client.CreateResponseStream(ctx, nativeReq)
	if err != nil {
		return nil, WrapProviderError(p.name, err)
	}
	state := &responsesStreamState{toolIndex: make(map[string]int)}
	return NewStreamReaderWithMetadata(func() (*StreamChunk, error) {
		for {
			event, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			if err != nil {
				return nil, WrapProviderError(p.name, err)
			}
			chunk, ok, err := responsesStreamChunk(p.name, event, state)
			if err != nil {
				return nil, err
			}
			if ok {
				return chunk, nil
			}
		}
	}, stream.Close, metadataFromHeader(p.name, nativeReq.Model, stream.Header())), nil
}

// ============================================================
// 请求映射
// ============================================================

func (p *responsesProvider) buildRequest(req *ChatRequest) (openai.CreateResponseRequest, error) {
	if len(req.Messages) == 0 {
		return openai.CreateResponseRequest{}, fmt.Errorf("%w: messages are required", ErrInvalidRequest)
	}
	if err := requireTextOnlyOutput(p.name, req.OutputModalities); err != nil {
		return openai.CreateResponseRequest{}, err
	}
	// Responses API 没有 stop / seed / n 这三个采样参数，拒绝而非静默丢弃。
	if len(req.Stop) > 0 {
		return openai.CreateResponseRequest{}, fmt.Errorf("%w: responses api does not support stop sequences", ErrInvalidRequest)
	}
	if req.Seed != nil {
		return openai.CreateResponseRequest{}, fmt.Errorf("%w: responses api does not support seed", ErrInvalidRequest)
	}
	if req.CandidateCount > 0 {
		return openai.CreateResponseRequest{}, fmt.Errorf("%w: responses api does not support candidate count", ErrInvalidRequest)
	}
	if err := validateWebSearchRequest(req); err != nil {
		return openai.CreateResponseRequest{}, err
	}
	support := resolveThinkingSupport(p.name, thinkingSupport{effort: p.supportsReasoningEffort})
	if err := validateThinkingWith(p.name, support, req.Thinking); err != nil {
		return openai.CreateResponseRequest{}, err
	}

	store := p.store
	out := openai.CreateResponseRequest{
		Model:             firstString(req.Model, p.model),
		MaxOutputTokens:   req.MaxTokens,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		ParallelToolCalls: req.ParallelToolCalls,
		Store:             &store,
	}
	// 不落库时推理项只能靠 encrypted_content 往返，必须显式请求平台返回。
	if !p.store {
		out.Include = []openai.ResponseInclude{openai.ResponseIncludeReasoningEncryptedContent}
	}
	if req.Thinking != nil && req.Thinking.Effort != "" {
		// summary 让平台返回可读的推理摘要，归位到 Reasoning；不影响计费口径。
		out.Reasoning = &openai.ResponseReasoning{Effort: req.Thinking.Effort, Summary: "auto"}
	}

	tools, err := responsesTools(req.Tools)
	if err != nil {
		return openai.CreateResponseRequest{}, err
	}
	out.Tools = tools
	toolChoice, err := responsesToolChoice(req.ToolChoice)
	if err != nil {
		return openai.CreateResponseRequest{}, err
	}
	out.ToolChoice = toolChoice
	text, err := responsesTextConfig(req.ResponseFormat)
	if err != nil {
		return openai.CreateResponseRequest{}, err
	}
	out.Text = text

	instructions, input, err := responsesInput(req.Messages)
	if err != nil {
		return openai.CreateResponseRequest{}, err
	}
	out.Instructions = instructions
	out.Input = input
	return out, nil
}

func responsesTools(tools []Tool) ([]openai.ResponseTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]openai.ResponseTool, 0, len(tools))
	for _, tool := range tools {
		if tool.WebSearch != nil {
			search, err := responsesWebSearchTool(tool.WebSearch)
			if err != nil {
				return nil, err
			}
			out = append(out, search)
			continue
		}
		def := openai.FunctionDefinition{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		}
		if tool.Function.Strict != nil {
			def.Strict = *tool.Function.Strict
		}
		out = append(out, openai.NewResponseFunctionTool(def))
	}
	return out, nil
}

// responsesWebSearchTool 映射内建联网搜索：支持域名白名单，不支持次数上限与黑名单——
// 这两项有成本与访问范围语义，拒绝而非静默忽略。
func responsesWebSearchTool(opts *WebSearchOptions) (openai.ResponseTool, error) {
	if opts.MaxUses > 0 || len(opts.BlockedDomains) > 0 {
		return openai.ResponseTool{}, fmt.Errorf(
			"%w: responses api web_search does not support MaxUses or BlockedDomains", ErrInvalidRequest)
	}
	tool := openai.ResponseTool{Type: "web_search"}
	if len(opts.AllowedDomains) > 0 {
		tool.Parameters = map[string]any{"filters": map[string]any{"allowed_domains": opts.AllowedDomains}}
	}
	return tool, nil
}

// responsesToolChoice 映射 tool_choice：Responses API 的指定函数形态是扁平的
// {"type":"function","name":...}，与 Chat Completions 的嵌套形态不同。
func responsesToolChoice(choice ToolChoiceOption) (any, error) {
	switch v := choice.(type) {
	case nil:
		return nil, nil
	case ToolChoiceMode:
		switch v {
		case ToolChoiceAuto, ToolChoiceNone, ToolChoiceRequired:
			return string(v), nil
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidToolChoice, v)
		}
	case ToolChoiceFunction:
		if v.Name == "" {
			return nil, fmt.Errorf("%w: function name is required", ErrInvalidToolChoice)
		}
		return map[string]any{"type": "function", "name": v.Name}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported responses tool choice %T", ErrInvalidToolChoice, choice)
	}
}

func responsesTextConfig(format *ResponseFormat) (*openai.ResponseTextConfig, error) {
	if format == nil || format.Type == ResponseFormatText {
		return nil, nil
	}
	switch format.Type {
	case ResponseFormatJSONObject:
		return &openai.ResponseTextConfig{Format: &openai.ResponseTextFormat{Type: "json_object"}}, nil
	case ResponseFormatJSONSchema:
		strict := format.Strict != nil && *format.Strict
		return &openai.ResponseTextConfig{Format: &openai.ResponseTextFormat{
			Type:   "json_schema",
			Name:   firstString(format.Name, "response"),
			Schema: format.Schema,
			Strict: strict,
		}}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported responses response format %q", ErrInvalidRequest, format.Type)
	}
}

// responsesInput 把统一消息映射为 Responses API 的 instructions 与输入项序列：
// system 文本合并进 instructions；assistant 的推理项（带 encrypted_content）、正文、
// 函数调用各成一项；工具结果映射为 function_call_output。
func responsesInput(messages []Message) (instructionsText string, input []any, err error) {
	var instructions strings.Builder
	input = make([]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			if text := contentText(msg.Content); text != "" {
				if instructions.Len() > 0 {
					instructions.WriteByte('\n')
				}
				instructions.WriteString(text)
			}
		case RoleUser:
			content, err := responsesInputContent(msg.Content)
			if err != nil {
				return "", nil, err
			}
			input = append(input, openai.ResponseInputMessage{Type: "message", Role: "user", Content: content})
		case RoleAssistant:
			input = append(input, responsesAssistantItems(msg)...)
		case RoleTool:
			input = append(input, openai.ResponseFunctionCallOutput{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: contentText(msg.Content),
			})
		default:
			return "", nil, fmt.Errorf("%w: unsupported responses role %q", ErrInvalidRequest, msg.Role)
		}
	}
	return instructions.String(), input, nil
}

func responsesInputContent(parts []ContentPart) ([]any, error) {
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentTypeText:
			out = append(out, openai.ResponseInputText{Type: "input_text", Text: part.Text})
		case ContentTypeImageURL:
			source, ok := part.preferredImageSource()
			if !ok {
				return nil, fmt.Errorf("%w: image source is required", ErrInvalidRequest)
			}
			out = append(out, openai.ResponseInputImage{Type: "input_image", ImageURL: source, Detail: string(part.ImageDetail)})
		case ContentTypeFile:
			file, err := responsesInputFile(part)
			if err != nil {
				return nil, err
			}
			out = append(out, file)
		default:
			return nil, fmt.Errorf("%w: unsupported responses content type %q", ErrInvalidRequest, part.Type)
		}
	}
	if len(out) == 0 {
		out = append(out, openai.ResponseInputText{Type: "input_text", Text: ""})
	}
	return out, nil
}

func responsesInputFile(part ContentPart) (openai.ResponseInputFile, error) {
	switch {
	case part.FileID != "":
		return openai.ResponseInputFile{Type: "input_file", FileID: part.FileID}, nil
	case part.FileURL != "":
		return openai.ResponseInputFile{Type: "input_file", FileURL: part.FileURL}, nil
	case len(part.FileData) > 0:
		if part.MIMEType == "" {
			return openai.ResponseInputFile{}, fmt.Errorf("%w: file MIME type is required", ErrInvalidRequest)
		}
		return openai.ResponseInputFile{
			Type:     "input_file",
			Filename: part.Filename,
			FileData: dataURL(part.MIMEType, part.FileData),
		}, nil
	default:
		return openai.ResponseInputFile{}, fmt.Errorf("%w: file source is required", ErrInvalidRequest)
	}
}

// responsesReasoningItem / responsesMessageItem / responsesFunctionCallItem 是回放历史用的输入项。
type responsesReasoningItem struct {
	Type             string                       `json:"type"`
	ID               string                       `json:"id,omitempty"`
	EncryptedContent string                       `json:"encrypted_content,omitempty"`
	Summary          []openai.ResponseSummaryPart `json:"summary"`
}

type responsesFunctionCallItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// responsesAssistantItems 把 assistant 消息拆成 Responses API 的输入项：
// 推理项在前（只回放带 encrypted_content 的块，平台按它恢复推理状态；没有加密内容的块
// 无法往返），然后是正文消息，最后是函数调用。
func responsesAssistantItems(msg Message) []any {
	items := make([]any, 0, 2+len(msg.ToolCalls))
	for _, block := range msg.ThinkingBlocks {
		if block.Redacted == "" {
			continue
		}
		item := responsesReasoningItem{Type: "reasoning", ID: block.Signature, EncryptedContent: block.Redacted, Summary: []openai.ResponseSummaryPart{}}
		if block.Thinking != "" {
			item.Summary = append(item.Summary, openai.ResponseSummaryPart{Type: "summary_text", Text: block.Thinking})
		}
		items = append(items, item)
	}
	if text := contentText(msg.Content); text != "" {
		items = append(items, openai.ResponseInputMessage{
			Type:    "message",
			Role:    "assistant",
			Content: []any{openai.ResponseOutputContent{Type: "output_text", Text: text}},
		})
	}
	for _, call := range msg.ToolCalls {
		items = append(items, responsesFunctionCallItem{
			Type:      "function_call",
			CallID:    call.ID,
			Name:      call.Function.Name,
			Arguments: firstString(call.Function.Arguments, "{}"),
		})
	}
	return items
}

// ============================================================
// 响应映射
// ============================================================

// responsesOutputItem 是本库需要的输出项字段集合；go-openai 的 Output 是 []any，
// 逐项解码以拿到 encrypted_content 等其未建模的字段。
type responsesOutputItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Status           string `json:"status"`
	CallID           string `json:"call_id"`
	Name             string `json:"name"`
	Arguments        string `json:"arguments"`
	EncryptedContent string `json:"encrypted_content"`
	Content          []struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Refusal     string `json:"refusal"`
		Annotations []struct {
			Type  string `json:"type"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"annotations"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
	Action struct {
		Query string `json:"query"`
	} `json:"action"`
}

type responsesParsedOutput struct {
	text           string
	reasoning      string
	thinkingBlocks []ThinkingBlock
	toolCalls      []ToolCall
	search         *SearchMetadata
	searchCalls    int
}

func decodeResponsesItem(raw any) (responsesOutputItem, error) {
	var item responsesOutputItem
	data, err := json.Marshal(raw)
	if err != nil {
		return item, fmt.Errorf("marshal responses output item: %w", err)
	}
	if err := json.Unmarshal(data, &item); err != nil {
		return item, fmt.Errorf("decode responses output item: %w", err)
	}
	return item, nil
}

func parseResponsesOutput(output []any) (responsesParsedOutput, error) {
	var out responsesParsedOutput
	var text, reasoning strings.Builder
	for _, raw := range output {
		item, err := decodeResponsesItem(raw)
		if err != nil {
			return out, err
		}
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				// 拒答文本同样是模型的可见回复，归入正文而非丢弃。
				text.WriteString(firstString(content.Text, content.Refusal))
				for _, annotation := range content.Annotations {
					if annotation.Type == "url_citation" {
						out.search = appendSearchSources(out.search, []SearchSource{{URL: annotation.URL, Title: annotation.Title}})
					}
				}
			}
		case "function_call":
			out.toolCalls = append(out.toolCalls, ToolCall{
				ID:       item.CallID,
				Function: FunctionCall{Name: item.Name, Arguments: firstString(item.Arguments, "{}")},
			})
		case "reasoning":
			var summary strings.Builder
			for _, part := range item.Summary {
				summary.WriteString(part.Text)
			}
			reasoning.WriteString(summary.String())
			out.thinkingBlocks = append(out.thinkingBlocks, ThinkingBlock{
				Thinking:  summary.String(),
				Signature: item.ID,
				Redacted:  item.EncryptedContent,
			})
		case "web_search_call":
			out.searchCalls++
			if item.Action.Query != "" {
				out.search = appendSearchQuery(out.search, item.Action.Query)
			}
		}
	}
	out.text = text.String()
	out.reasoning = reasoning.String()
	return out, nil
}

// usageFromResponses 归一化用量：input_tokens 已含缓存部分，output_tokens 已含推理部分，
// 与统一 Usage 的包含关系一致。
func usageFromResponses(usage *openai.ResponseUsage, searchCalls int) Usage {
	if usage == nil {
		return Usage{WebSearchRequests: searchCalls}
	}
	out := Usage{
		PromptTokens:      usage.InputTokens,
		CompletionTokens:  usage.OutputTokens,
		TotalTokens:       usage.TotalTokens,
		WebSearchRequests: searchCalls,
	}
	if usage.InputTokensDetails != nil {
		out.CacheReadTokens = usage.InputTokensDetails.CachedTokens
		out.CacheWriteTokens = usage.InputTokensDetails.CacheWriteTokens
	}
	if usage.OutputTokensDetails != nil {
		out.ReasoningTokens = usage.OutputTokensDetails.ReasoningTokens
	}
	return out
}

// responsesFinishReason 对齐结束原因：有函数调用为 tool_calls，输出截断为 length，其余为 stop。
func responsesFinishReason(status openai.ResponseStatus, incomplete *openai.ResponseIncompleteDetails, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	if status == openai.ResponseStatusIncomplete {
		if incomplete != nil && incomplete.Reason == "max_output_tokens" {
			return "length"
		}
		if incomplete != nil && incomplete.Reason != "" {
			return incomplete.Reason
		}
		return "incomplete"
	}
	return "stop"
}

// responsesFailure 把 HTTP 200 内的失败状态映射为 ProviderError。
func responsesFailure(name ProviderName, failure *openai.ResponseError) error {
	code, message := "", "response failed"
	if failure != nil {
		code, message = failure.Code, firstString(failure.Message, message)
	}
	errCode := ErrorCodeServerError
	switch code {
	case "rate_limit_exceeded":
		errCode = ErrorCodeRateLimit
	case "invalid_prompt", "invalid_request_error":
		errCode = ErrorCodeInvalidRequest
	}
	return &ProviderError{
		Provider:  name,
		Code:      errCode,
		RawCode:   code,
		Retryable: RetryableByCode(errCode),
		Message:   message,
	}
}

// ============================================================
// 流式映射
// ============================================================

// responsesStreamState 保存流式解析的跨事件状态：函数调用项 id → 统一 ToolCallDelta 序号，
// 以及是否出现过函数调用（决定 FinishReason）。
type responsesStreamState struct {
	toolIndex map[string]int
	nextTool  int
}

func responsesStreamChunk(name ProviderName, event openai.ResponseStreamEvent, state *responsesStreamState) (*StreamChunk, bool, error) {
	switch event.Type {
	case openai.ResponseStreamEventOutputTextDelta, openai.ResponseStreamEventRefusalDelta:
		return &StreamChunk{Delta: event.Delta}, true, nil

	case openai.ResponseStreamEventReasoningSummaryTextDelta, openai.ResponseStreamEventReasoningTextDelta:
		return &StreamChunk{ReasoningDelta: event.Delta}, true, nil

	case openai.ResponseStreamEventOutputItemAdded:
		if event.Item != nil && event.Item.Type == "function_call" {
			index := state.nextTool
			state.nextTool++
			state.toolIndex[event.Item.ID] = index
			return &StreamChunk{ToolCalls: []ToolCallDelta{{
				Index:    index,
				ID:       event.Item.CallID,
				Function: FunctionCallDelta{Name: event.Item.Name, Arguments: event.Item.Arguments},
			}}}, true, nil
		}
		return nil, false, nil

	case openai.ResponseStreamEventFunctionArgumentsDelta:
		index, ok := state.toolIndex[event.ItemID]
		if !ok {
			return nil, false, nil
		}
		return &StreamChunk{ToolCalls: []ToolCallDelta{{Index: index, Function: FunctionCallDelta{Arguments: event.Delta}}}}, true, nil

	case openai.ResponseStreamEventOutputItemDone:
		// 推理项整体完成时给出 id 与 encrypted_content：签名闭合此前累积的推理增量，
		// 加密内容随同一块保留，供回放时恢复推理状态。
		if event.Item != nil && event.Item.Type == "reasoning" {
			item, err := responsesRawItem(event.Raw)
			if err != nil {
				return nil, false, err
			}
			return &StreamChunk{ThinkingSignature: firstString(item.ID, event.Item.ID), RedactedThinking: item.EncryptedContent}, true, nil
		}
		return nil, false, nil

	case openai.ResponseStreamEventCompleted, openai.ResponseStreamEventIncomplete:
		if event.Response == nil {
			return nil, false, nil
		}
		parsed, err := parseResponsesOutput(event.Response.Output)
		if err != nil {
			return nil, false, err
		}
		return &StreamChunk{
			FinishReason: responsesFinishReason(event.Response.Status, event.Response.IncompleteDetails, state.nextTool > 0),
			Model:        event.Response.Model,
			Usage:        usageFromResponses(event.Response.Usage, parsed.searchCalls),
			Search:       parsed.search,
		}, true, nil

	case openai.ResponseStreamEventFailed:
		var failure *openai.ResponseError
		if event.Response != nil {
			failure = event.Response.Error
		}
		return nil, false, responsesFailure(name, failure)

	case openai.ResponseStreamEventError:
		return nil, false, responsesFailure(name, &openai.ResponseError{Code: event.Code, Message: event.Message})
	}
	return nil, false, nil
}

// responsesRawItem 从原始事件里解出 item（go-openai 的 Item 未建模 encrypted_content）。
func responsesRawItem(raw []byte) (responsesOutputItem, error) {
	var envelope struct {
		Item responsesOutputItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return responsesOutputItem{}, fmt.Errorf("decode responses stream item: %w", err)
	}
	return envelope.Item, nil
}
