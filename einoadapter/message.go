package einoadapter

import (
	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
)

func nativeCall(index int, call provider.ToolCall) schema.ToolCall {
	return schema.ToolCall{Index: new(index), ID: call.ID, Type: "function", Function: schema.FunctionCall{Name: call.Function.Name, Arguments: call.Function.Arguments}}
}

func callSignatures(calls []provider.ToolCall) []toolSignature {
	var signatures []toolSignature
	for i, call := range calls {
		if call.ThoughtSignature != "" {
			signatures = append(signatures, toolSignature{Index: i, Value: call.ThoughtSignature})
		}
	}
	return signatures
}

func callsFromEino(calls []schema.ToolCall) ([]provider.ToolCall, error) {
	var out []provider.ToolCall
	for _, call := range calls {
		if (call.Type != "" && call.Type != "function") || len(call.Extra) > 0 {
			return nil, invalid("ToolCall.Type/Extra unsupported")
		}
		out = append(out, provider.ToolCall{ID: call.ID, Function: provider.FunctionCall{Name: call.Function.Name, Arguments: call.Function.Arguments}})
	}
	return out, nil
}

func applySignatures(calls []provider.ToolCall, signatures []toolSignature) error {
	for _, s := range signatures {
		if s.Index < 0 || s.Index >= len(calls) {
			return invalid("tool signature index")
		}
		if calls[s.Index].ThoughtSignature != "" && calls[s.Index].ThoughtSignature != s.Value {
			return invalid("conflicting tool signature")
		}
		calls[s.Index].ThoughtSignature = s.Value
	}
	return nil
}

func nativeUsage(u provider.Usage) *schema.TokenUsage {
	if u == (provider.Usage{}) {
		return nil
	}
	return &schema.TokenUsage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens,
		PromptTokenDetails:      schema.PromptTokenDetails{CachedTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens},
		CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: u.ReasoningTokens}}
}

func responseMessage(resp *provider.ChatResponse) (*schema.Message, error) {
	out := &schema.Message{Role: schema.Assistant, Content: resp.Content, ReasoningContent: resp.Reasoning,
		ResponseMeta: &schema.ResponseMeta{FinishReason: resp.FinishReason, Usage: nativeUsage(resp.Usage)}}
	for i, call := range resp.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, nativeCall(i, call))
	}
	var err error
	out.Extra, err = eventExtra(event{Kind: "response", Metadata: &resp.Metadata, Usage: &resp.Usage, Blocks: resp.ThinkingBlocks, Parts: resp.Parts, Search: resp.Search, Tools: callSignatures(resp.ToolCalls)})
	return out, err
}
