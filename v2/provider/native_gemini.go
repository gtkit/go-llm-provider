package provider

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/gtkit/json/v2"
)

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents,omitempty"`
	SystemInstruction geminiContent           `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inline_data,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MIMEType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens    int      `json:"maxOutputTokens,omitempty"`
	CandidateCount     int      `json:"candidateCount,omitempty"`
	Seed               *int     `json:"seed,omitempty"`
	Temperature        *float32 `json:"temperature,omitempty"`
	TopP               *float32 `json:"topP,omitempty"`
	StopSequences      []string `json:"stopSequences,omitempty"`
	ResponseMIMEType   string   `json:"responseMimeType,omitempty"`
	ResponseSchema     any      `json:"responseSchema,omitempty"`
	ResponseModalities []string `json:"responseModalities,omitempty"`
}

// geminiResponseModalities 将统一 Modality 映射为 Gemini 的 responseModalities 枚举。
func geminiResponseModalities(modalities []Modality) ([]string, error) {
	if len(modalities) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(modalities))
	for _, modality := range modalities {
		switch modality {
		case ModalityText:
			out = append(out, "TEXT")
		case ModalityImage:
			out = append(out, "IMAGE")
		default:
			return nil, fmt.Errorf("%w: gemini does not support %q output modality", ErrInvalidRequest, modality)
		}
	}
	return out, nil
}

type geminiResponse struct {
	Candidates     []geminiCandidate `json:"candidates"`
	UsageMetadata  geminiUsage       `json:"usageMetadata"`
	ModelVersion   string            `json:"modelVersion"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

type geminiCandidate struct {
	Content           geminiContent            `json:"content"`
	FinishReason      string                   `json:"finishReason"`
	GroundingMetadata *geminiGroundingMetadata `json:"groundingMetadata"`
}

type geminiGroundingMetadata struct {
	WebSearchQueries []string                `json:"webSearchQueries"`
	SearchEntryPoint *geminiSearchEntryPoint `json:"searchEntryPoint"`
	GroundingChunks  []geminiGroundingChunk  `json:"groundingChunks"`
}

type geminiSearchEntryPoint struct {
	RenderedContent string `json:"renderedContent"`
}

type geminiGroundingChunk struct {
	Web *geminiGroundingWeb `json:"web"`
}

type geminiGroundingWeb struct {
	URI   string `json:"uri"`
	Title string `json:"title"`
}

// geminiGrounding 返回响应中第一个携带搜索查询的 groundingMetadata，
// 未触发 grounding 时返回 nil。
func geminiGrounding(resp geminiResponse) *geminiGroundingMetadata {
	for _, candidate := range resp.Candidates {
		if candidate.GroundingMetadata != nil && len(candidate.GroundingMetadata.WebSearchQueries) > 0 {
			return candidate.GroundingMetadata
		}
	}
	return nil
}

// applyGeminiGrounding 把 grounding 结果写入 usage（双口径：query 数与
// grounded prompt 0/1，计价时二选一）并归一化为 SearchMetadata。
func applyGeminiGrounding(gm *geminiGroundingMetadata, usage *Usage) *SearchMetadata {
	if gm == nil {
		return nil
	}
	usage.WebSearchRequests = len(gm.WebSearchQueries)
	usage.WebSearchGroundedPrompts = 1

	search := &SearchMetadata{Queries: slices.Clone(gm.WebSearchQueries)}
	if gm.SearchEntryPoint != nil {
		search.SearchEntryPoint = gm.SearchEntryPoint.RenderedContent
	}
	for _, chunk := range gm.GroundingChunks {
		if chunk.Web == nil {
			continue
		}
		search.Sources = append(search.Sources, SearchSource{
			URL:   chunk.Web.URI,
			Title: chunk.Web.Title,
		})
	}
	return search
}

type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

// usageFromGemini 将 geminiUsage 归一化为统一 Usage。
// Gemini 的 promptTokenCount 已含 cachedContentTokenCount，而 candidatesTokenCount
// 不含 thoughtsTokenCount，这里归一化为 CompletionTokens 包含推理部分，
// 与其他 provider 语义对齐。
func usageFromGemini(usage geminiUsage) Usage {
	completion := usage.CandidatesTokenCount + usage.ThoughtsTokenCount
	total := usage.TotalTokenCount
	if total == 0 {
		total = usage.PromptTokenCount + completion
	}
	return Usage{
		PromptTokens:     usage.PromptTokenCount,
		CompletionTokens: completion,
		ReasoningTokens:  usage.ThoughtsTokenCount,
		CacheReadTokens:  usage.CachedContentTokenCount,
		TotalTokens:      total,
	}
}

type geminiEmbeddingRequest struct {
	Model                string        `json:"model,omitempty"`
	Content              geminiContent `json:"content"`
	OutputDimensionality *int          `json:"outputDimensionality,omitempty"`
}

type geminiBatchEmbeddingRequest struct {
	Requests []geminiEmbeddingRequest `json:"requests"`
}

type geminiEmbeddingResponse struct {
	Embedding geminiEmbedding `json:"embedding"`
}

type geminiBatchEmbeddingResponse struct {
	Embeddings []geminiEmbedding `json:"embeddings"`
}

type geminiEmbedding struct {
	Values []float32 `json:"values"`
}

type geminiCountTokensRequest struct {
	Contents          []geminiContent `json:"contents,omitempty"`
	SystemInstruction geminiContent   `json:"systemInstruction,omitempty"`
}

type geminiCountTokensResponse struct {
	TotalTokens int `json:"totalTokens"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
	GoogleSearch         *geminiGoogleSearch         `json:"google_search,omitempty"`
}

// geminiGoogleSearch 是 Gemini 原生联网搜索（Grounding with Google Search）工具，
// 无配置项。
type geminiGoogleSearch struct{}

type geminiFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig geminiFunctionCallingConfig `json:"functionCallingConfig"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiFunctionCall struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type geminiErrorEnvelope struct {
	Error geminiErrorBody `json:"error"`
}

type geminiErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func geminiParts(parts []ContentPart) ([]geminiPart, error) {
	if len(parts) == 0 {
		return []geminiPart{{Text: ""}}, nil
	}
	out := make([]geminiPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentTypeText:
			out = append(out, geminiPart{Text: part.Text})
		case ContentTypeImageURL:
			image, err := geminiImagePart(part)
			if err != nil {
				return nil, err
			}
			out = append(out, image)
		case ContentTypeFile:
			file, err := geminiFilePart(part)
			if err != nil {
				return nil, err
			}
			out = append(out, file)
		default:
			return nil, fmt.Errorf("%w: unsupported gemini content type %q", ErrInvalidRequest, part.Type)
		}
	}
	return out, nil
}

func geminiAssistantParts(msg Message) ([]geminiPart, error) {
	parts, err := geminiParts(msg.Content)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 && parts[0].Text == "" && len(msg.ToolCalls) > 0 {
		parts = parts[:0]
	}
	for _, call := range msg.ToolCalls {
		args, err := rawJSONArgument(call.Function.Arguments)
		if err != nil {
			return nil, err
		}
		parts = append(parts, geminiPart{
			FunctionCall: &geminiFunctionCall{
				ID:   call.ID,
				Name: call.Function.Name,
				Args: args,
			},
		})
	}
	return parts, nil
}

func geminiToolResponsePart(msg Message) geminiPart {
	text := contentText(msg.Content)
	response, err := rawJSONArgument(text)
	if err != nil {
		response = map[string]any{"content": text}
	}
	return geminiPart{
		FunctionResponse: &geminiFunctionResponse{
			ID:       msg.ToolCallID,
			Name:     msg.ToolCallID,
			Response: response,
		},
	}
}

func geminiTextParts(parts []ContentPart) []geminiPart {
	out := make([]geminiPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == ContentTypeText && part.Text != "" {
			out = append(out, geminiPart{Text: part.Text})
		}
	}
	return out
}

func geminiImagePart(part ContentPart) (geminiPart, error) {
	if len(part.ImageData) > 0 {
		if part.MIMEType == "" {
			return geminiPart{}, fmt.Errorf("%w: image MIME type is required", ErrInvalidRequest)
		}
		return geminiPart{
			InlineData: &geminiInlineData{
				MIMEType: part.MIMEType,
				Data:     base64.StdEncoding.EncodeToString(part.ImageData),
			},
		}, nil
	}
	if part.ImageURL == "" {
		return geminiPart{}, fmt.Errorf("%w: image source is required", ErrInvalidRequest)
	}
	if strings.HasPrefix(part.ImageURL, "data:") {
		mimeType, data, ok := parseDataURLImage(part.ImageURL)
		if !ok {
			return geminiPart{}, fmt.Errorf("%w: invalid data URL image", ErrInvalidRequest)
		}
		return geminiPart{
			InlineData: &geminiInlineData{
				MIMEType: mimeType,
				Data:     data,
			},
		}, nil
	}
	return geminiPart{}, fmt.Errorf("%w: gemini image URL parts require inline data", ErrInvalidRequest)
}

func geminiFilePart(part ContentPart) (geminiPart, error) {
	if len(part.FileData) == 0 {
		return geminiPart{}, fmt.Errorf("%w: gemini file parts require inline data", ErrInvalidRequest)
	}
	if part.MIMEType == "" {
		return geminiPart{}, fmt.Errorf("%w: file MIME type is required", ErrInvalidRequest)
	}
	return geminiPart{
		InlineData: &geminiInlineData{
			MIMEType: part.MIMEType,
			Data:     base64.StdEncoding.EncodeToString(part.FileData),
		},
	}, nil
}

func geminiRole(role Role) string {
	switch role {
	case RoleAssistant:
		return "model"
	default:
		return "user"
	}
}

func geminiGeneration(req *ChatRequest) *geminiGenerationConfig {
	if req.MaxTokens <= 0 &&
		req.CandidateCount <= 0 &&
		req.Seed == nil &&
		req.Temperature == nil &&
		req.TopP == nil &&
		len(req.Stop) == 0 &&
		len(req.OutputModalities) == 0 {
		return nil
	}
	cfg := &geminiGenerationConfig{}
	if req.MaxTokens > 0 {
		cfg.MaxOutputTokens = req.MaxTokens
	}
	if req.CandidateCount > 0 {
		cfg.CandidateCount = req.CandidateCount
	}
	if req.Seed != nil {
		cfg.Seed = req.Seed
	}
	if req.Temperature != nil {
		cfg.Temperature = req.Temperature
	}
	if req.TopP != nil {
		cfg.TopP = req.TopP
	}
	if len(req.Stop) > 0 {
		cfg.StopSequences = append([]string(nil), req.Stop...)
	}
	return cfg
}

func applyGeminiResponseFormat(cfg *geminiGenerationConfig, format *ResponseFormat) (*geminiGenerationConfig, error) {
	if format == nil || format.Type == ResponseFormatText {
		return cfg, nil
	}
	if cfg == nil {
		cfg = &geminiGenerationConfig{}
	}
	switch format.Type {
	case ResponseFormatJSONObject:
		cfg.ResponseMIMEType = "application/json"
	case ResponseFormatJSONSchema:
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseSchema = format.Schema
	default:
		return nil, fmt.Errorf("%w: unsupported gemini response format %q", ErrInvalidRequest, format.Type)
	}
	return cfg, nil
}

func geminiTools(tools []Tool) ([]geminiTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	declarations := make([]geminiFunctionDeclaration, 0, len(tools))
	webSearch := false
	for _, tool := range tools {
		if tool.WebSearch != nil {
			// MaxUses 与域名过滤具有成本/访问范围约束语义，Gemini 的
			// google_search 不支持这些配置——拒绝而非静默忽略，
			// 否则会绕过调用方的安全与费用策略。
			if tool.WebSearch.MaxUses > 0 ||
				len(tool.WebSearch.AllowedDomains) > 0 ||
				len(tool.WebSearch.BlockedDomains) > 0 {
				return nil, fmt.Errorf(
					"%w: gemini google_search does not support MaxUses or domain filters", ErrInvalidRequest)
			}
			webSearch = true
			continue
		}
		declarations = append(declarations, geminiFunctionDeclaration{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	// Gemini 2.5 系（含默认模型）不支持 google_search 与函数声明同用，
	// 平台会直接拒绝请求；在客户端给出明确错误。
	if webSearch && len(declarations) > 0 {
		return nil, fmt.Errorf(
			"%w: gemini does not support combining google_search with function tools", ErrInvalidRequest)
	}
	if webSearch {
		return []geminiTool{{GoogleSearch: &geminiGoogleSearch{}}}, nil
	}
	return []geminiTool{{FunctionDeclarations: declarations}}, nil
}

func buildGeminiToolConfig(req *ChatRequest) (*geminiToolConfig, error) {
	if req.ToolChoice == nil {
		return nil, nil
	}

	cfg := geminiFunctionCallingConfig{}
	switch v := req.ToolChoice.(type) {
	case ToolChoiceMode:
		switch v {
		case ToolChoiceAuto:
			cfg.Mode = "AUTO"
		case ToolChoiceNone:
			cfg.Mode = "NONE"
		case ToolChoiceRequired:
			cfg.Mode = "ANY"
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidToolChoice, v)
		}
	case ToolChoiceFunction:
		if v.Name == "" {
			return nil, fmt.Errorf("%w: function name is required", ErrInvalidToolChoice)
		}
		cfg.Mode = "ANY"
		cfg.AllowedFunctionNames = []string{v.Name}
	default:
		return nil, fmt.Errorf("%w: unsupported gemini tool choice %T", ErrInvalidToolChoice, req.ToolChoice)
	}
	return &geminiToolConfig{FunctionCallingConfig: cfg}, nil
}

func fillGeminiContents(out *geminiRequest, messages []Message) error {
	for _, msg := range messages {
		if err := appendGeminiMessage(out, msg); err != nil {
			return err
		}
	}
	if len(out.SystemInstruction.Parts) == 0 {
		out.SystemInstruction = geminiContent{}
	}
	return nil
}

func appendGeminiMessage(out *geminiRequest, msg Message) error {
	switch msg.Role {
	case RoleSystem:
		parts := geminiTextParts(msg.Content)
		if len(parts) > 0 {
			out.SystemInstruction.Parts = append(out.SystemInstruction.Parts, parts...)
		}
	case RoleUser, RoleAssistant:
		parts, err := geminiParts(msg.Content)
		if msg.Role == RoleAssistant {
			parts, err = geminiAssistantParts(msg)
		}
		if err != nil {
			return err
		}
		out.Contents = append(out.Contents, geminiContent{
			Role:  geminiRole(msg.Role),
			Parts: parts,
		})
	case RoleTool:
		part := geminiToolResponsePart(msg)
		out.Contents = append(out.Contents, geminiContent{
			Role:  "function",
			Parts: []geminiPart{part},
		})
	default:
		return fmt.Errorf("%w: unsupported gemini role %q", ErrInvalidRequest, msg.Role)
	}
	return nil
}

func geminiResponseContent(resp geminiResponse) (content, finishReason string, toolCalls []ToolCall, parts []ContentPart, err error) {
	if len(resp.Candidates) == 0 {
		return "", "", nil, nil, nil
	}
	candidate := resp.Candidates[0]
	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		text.WriteString(part.Text)
		if part.FunctionCall != nil {
			arguments, marshalErr := json.Marshal(part.FunctionCall.Args)
			if marshalErr != nil {
				return "", "", nil, nil, fmt.Errorf("marshal gemini function call args: %w", marshalErr)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID: firstString(part.FunctionCall.ID, "gemini_"+part.FunctionCall.Name),
				Function: FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(arguments),
				},
			})
		}
		if part.InlineData != nil {
			output, convertErr := geminiInlineOutputPart(part.InlineData)
			if convertErr != nil {
				return "", "", nil, nil, convertErr
			}
			parts = append(parts, output)
		}
	}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	} else {
		finishReason = candidate.FinishReason
	}
	return text.String(), finishReason, toolCalls, parts, nil
}

// geminiInlineOutputPart 将响应中的 inlineData（base64）转换为输出 ContentPart：
// image/* 映射为图像，其余 MIME 类型映射为文件（类型层已就绪，等平台支持）。
func geminiInlineOutputPart(inline *geminiInlineData) (ContentPart, error) {
	data, err := base64.StdEncoding.DecodeString(inline.Data)
	if err != nil {
		return ContentPart{}, fmt.Errorf("%w: decode gemini inline output: %w", ErrInvalidRequest, err)
	}
	if strings.HasPrefix(inline.MIMEType, "image/") {
		return ImageDataPart(data, inline.MIMEType), nil
	}
	return ContentPart{
		Type:     ContentTypeFile,
		FileData: data,
		MIMEType: inline.MIMEType,
	}, nil
}

func decodeGeminiError(provider ProviderName, statusCode int, status string, body []byte) error {
	var envelope geminiErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nativeStatusError(provider, statusCode, status, string(body))
	}
	code := geminiErrorCode(statusCode, envelope.Error.Status)
	return &ProviderError{
		Provider:   provider,
		Code:       code,
		StatusCode: statusCode,
		Status:     status,
		RawType:    envelope.Error.Status,
		Retryable:  RetryableByCode(code),
		Message:    envelope.Error.Message,
	}
}

func geminiErrorCode(statusCode int, rawStatus string) ErrorCode {
	switch strings.ToUpper(rawStatus) {
	case "UNAUTHENTICATED", "PERMISSION_DENIED":
		return ErrorCodeAuth
	case "RESOURCE_EXHAUSTED":
		return ErrorCodeRateLimit
	case "DEADLINE_EXCEEDED":
		return ErrorCodeTimeout
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION":
		return ErrorCodeInvalidRequest
	case "UNAVAILABLE", "INTERNAL":
		return ErrorCodeServerError
	default:
		return CodeFromHTTPStatus(statusCode)
	}
}
