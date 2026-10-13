package einoadapter

import (
	"slices"

	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
)

// ToResponse reconstructs a response or concatenated stream, preserving the last
// complete Usage snapshot and all provider extensions. Opening is allowed only
// once, before content events. A lone opening is not a model response.
func ToResponse(msg *schema.Message) (*provider.ChatResponse, error) {
	if err := validateOutput(msg); err != nil {
		return nil, err
	}
	if len(msg.UserInputMultiContent) > 0 || len(msg.AssistantGenMultiContent) > 0 || len(msg.MultiContent) > 0 {
		return nil, invalid("response multimodal fields require provider extensions")
	}
	out := &provider.ChatResponse{Content: msg.Content, Reasoning: msg.ReasoningContent}
	var err error
	out.ToolCalls, err = callsFromEino(msg.ToolCalls)
	if err != nil {
		return nil, err
	}
	if msg.ResponseMeta != nil {
		out.FinishReason = msg.ResponseMeta.FinishReason
		out.Usage = usageFromEino(msg.ResponseMeta.Usage)
	}
	events, err := readEvents(msg.Extra)
	if err != nil {
		return nil, err
	}
	lastReasoning := 0
	for i, e := range events {
		if e.Kind == "input" || (e.Kind == "opening" && (i != 0 || (len(events) == 1 && msg.Content == "" && msg.ReasoningContent == "" && len(msg.ToolCalls) == 0 && msg.ResponseMeta == nil))) {
			return nil, invalid("unexpected input/opening in response")
		}
		if err := mergeResponseEvent(out, msg.ToolCalls, e, &lastReasoning); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func mergeResponseEvent(out *provider.ChatResponse, calls []schema.ToolCall, e event, lastReasoning *int) error {
	if e.Metadata != nil {
		out.Metadata = *e.Metadata
	}
	if e.Model != "" {
		out.Metadata.Model = e.Model
	}
	if e.Usage != nil {
		out.Usage = *e.Usage
	}
	out.Parts = append(out.Parts, e.Parts...)
	out.ThinkingBlocks = append(out.ThinkingBlocks, e.Blocks...)
	if e.Search != nil {
		out.Search = e.Search
	}
	if e.Signature != "" {
		if e.ReasoningEnd < *lastReasoning || e.ReasoningEnd > len(out.Reasoning) {
			return invalid("thinking signature offset")
		}
		out.ThinkingBlocks = append(out.ThinkingBlocks, provider.ThinkingBlock{Thinking: out.Reasoning[*lastReasoning:e.ReasoningEnd], Signature: e.Signature})
		*lastReasoning = e.ReasoningEnd
	}
	if e.Redacted != "" {
		out.ThinkingBlocks = append(out.ThinkingBlocks, provider.ThinkingBlock{Redacted: e.Redacted})
	}
	// ConcatMessages sorts tool calls by Index, which need not be contiguous.
	for _, sig := range e.Tools {
		index := slices.IndexFunc(calls, func(call schema.ToolCall) bool { return call.Index != nil && *call.Index == sig.Index })
		if err := applySignatures(out.ToolCalls, []toolSignature{{Index: index, Value: sig.Value}}); err != nil {
			return err
		}
	}
	return nil
}

// ToChunk converts a single content frame. It rejects opening and concatenated
// control frames; callers consume the opening through OpeningMetadata first.
func ToChunk(msg *schema.Message) (*provider.StreamChunk, error) {
	if err := validateOutput(msg); err != nil {
		return nil, err
	}
	events, err := readEvents(msg.Extra)
	if err != nil {
		return nil, err
	}
	if len(events) > 1 || (len(events) == 1 && events[0].Kind != "chunk") {
		return nil, invalid("expected a single chunk")
	}
	if len(msg.UserInputMultiContent) > 0 || len(msg.AssistantGenMultiContent) > 0 || len(msg.MultiContent) > 0 {
		return nil, invalid("chunk multimodal fields require extensions")
	}
	out := &provider.StreamChunk{Delta: msg.Content, ReasoningDelta: msg.ReasoningContent}
	if msg.ResponseMeta != nil {
		out.FinishReason = msg.ResponseMeta.FinishReason
		out.Usage = usageFromEino(msg.ResponseMeta.Usage)
	}
	for _, call := range msg.ToolCalls {
		if call.Index == nil || *call.Index < 0 || (call.Type != "" && call.Type != "function") || len(call.Extra) > 0 {
			return nil, invalid("chunk ToolCall index/type/extra")
		}
		out.ToolCalls = append(out.ToolCalls, provider.ToolCallDelta{Index: *call.Index, ID: call.ID, Function: provider.FunctionCallDelta{Name: call.Function.Name, Arguments: call.Function.Arguments}})
	}
	if len(events) == 1 {
		e := events[0]
		out.Model = e.Model
		out.Parts = e.Parts
		out.Search = e.Search
		out.ThinkingSignature = e.Signature
		out.RedactedThinking = e.Redacted
		if e.Usage != nil {
			out.Usage = *e.Usage
		}
		for _, s := range e.Tools {
			i := slices.IndexFunc(out.ToolCalls, func(call provider.ToolCallDelta) bool { return call.Index == s.Index })
			if i < 0 {
				return nil, invalid("chunk signature index")
			}
			out.ToolCalls[i].ThoughtSignature = s.Value
		}
	}
	return out, nil
}

// OpeningMetadata consumes the bridge's single opening control frame. It must
// never be forwarded to application content or first-token timeout accounting.
func OpeningMetadata(msg *schema.Message) (provider.ResponseMetadata, error) {
	if msg == nil {
		return provider.ResponseMetadata{}, invalid("missing opening")
	}
	events, err := readEvents(msg.Extra)
	if err != nil {
		return provider.ResponseMetadata{}, err
	}
	if len(events) != 1 || events[0].Kind != "opening" || events[0].Metadata == nil || msg.Content != "" || msg.ReasoningContent != "" || len(msg.ToolCalls) > 0 || msg.ResponseMeta != nil {
		return provider.ResponseMetadata{}, invalid("invalid opening")
	}
	return *events[0].Metadata, nil
}

func validateOutput(msg *schema.Message) error {
	if msg == nil {
		return invalid("nil output message")
	}
	if msg.Role != schema.Assistant || msg.Name != "" || msg.ToolCallID != "" || msg.ToolName != "" {
		return invalid("output Role/Name/ToolCallID/ToolName")
	}
	if msg.ResponseMeta != nil && msg.ResponseMeta.LogProbs != nil {
		return invalid("ResponseMeta.LogProbs unsupported")
	}
	return nil
}

func usageFromEino(u *schema.TokenUsage) provider.Usage {
	if u == nil {
		return provider.Usage{}
	}
	return provider.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens, CacheReadTokens: u.PromptTokenDetails.CachedTokens, CacheWriteTokens: u.PromptTokenDetails.CacheWriteTokens}
}
