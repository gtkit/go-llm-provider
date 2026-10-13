package einoadapter

import (
	"fmt"
	"slices"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
	"github.com/gtkit/json/v2"
)

type callOptions struct {
	request *provider.ChatRequest
	control *StreamControl
}

// FromRequest snapshots request options and converts its messages. Messages are
// not retained in the option. An explicit request replaces bound tools, including
// an empty list. Later common Eino options override the snapshot.
func FromRequest(req *provider.ChatRequest) ([]*schema.Message, []model.Option, error) {
	if req == nil {
		return nil, nil, provider.ErrNilChatRequest
	}
	snapshot, err := cloneRequest(req)
	if err != nil {
		return nil, nil, err
	}
	messages := make([]*schema.Message, len(req.Messages))
	for i, message := range req.Messages {
		messages[i], err = inputMessage(message)
		if err != nil {
			return nil, nil, fmt.Errorf("message %d: %w", i, err)
		}
	}
	return messages, []model.Option{model.WrapImplSpecificOptFn(func(o *callOptions) { o.request = snapshot })}, nil
}

// WithStreamControl binds the required single-use control to one Stream call.
func WithStreamControl(control *StreamControl) model.Option {
	return model.WrapImplSpecificOptFn(func(o *callOptions) { o.control = control })
}

func (m *ChatModel) request(messages []*schema.Message, opts []model.Option) (*provider.ChatRequest, *StreamControl, error) {
	specific := model.GetImplSpecificOptions[callOptions](nil, opts...)
	base := specific.request
	if base == nil {
		base = &provider.ChatRequest{Tools: m.tools}
	}
	req, err := cloneRequest(base)
	if err != nil {
		return nil, nil, err
	}
	common := model.GetCommonOptions(nil, opts...)
	if common.DeferredTools != nil || common.ToolSearchTool != nil || common.AgenticToolChoice != nil {
		return nil, nil, invalid("DeferredTools/ToolSearchTool/AgenticToolChoice unsupported")
	}
	if common.Model != nil {
		req.Model = *common.Model
	}
	if common.MaxTokens != nil {
		req.MaxTokens = *common.MaxTokens
	}
	if common.Temperature != nil {
		req.Temperature = new(*common.Temperature)
	}
	if common.TopP != nil {
		req.TopP = new(*common.TopP)
	}
	if common.Stop != nil {
		req.Stop = slices.Clone(common.Stop)
	}
	if common.Tools != nil {
		req.Tools, err = toolsFromEino(common.Tools)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := applyToolChoice(req, common); err != nil {
		return nil, nil, err
	}
	req.Messages = make([]provider.Message, len(messages))
	for i, message := range messages {
		req.Messages[i], err = messageToProvider(message)
		if err != nil {
			return nil, nil, fmt.Errorf("message %d: %w", i, err)
		}
	}
	return req, specific.control, nil
}

func applyToolChoice(req *provider.ChatRequest, common *model.Options) error {
	if common.ToolChoice != nil {
		switch *common.ToolChoice {
		case schema.ToolChoiceAllowed:
			req.ToolChoice = provider.ToolChoiceAuto
		case schema.ToolChoiceForbidden:
			req.ToolChoice = provider.ToolChoiceNone
		case schema.ToolChoiceForced:
			req.ToolChoice = provider.ToolChoiceRequired
		default:
			return invalid("ToolChoice")
		}
	}
	if common.AllowedToolNames == nil {
		return nil
	}
	if len(common.AllowedToolNames) == 0 {
		return invalid("AllowedToolNames empty")
	}
	selected := make([]provider.Tool, 0, len(common.AllowedToolNames))
	seen := make(map[string]bool)
	for _, name := range common.AllowedToolNames {
		if seen[name] {
			return invalid("AllowedToolNames duplicate")
		}
		seen[name] = true
		i := slices.IndexFunc(req.Tools, func(tool provider.Tool) bool { return tool.WebSearch == nil && tool.Function.Name == name })
		if i < 0 {
			return invalid("AllowedToolNames unknown name")
		}
		selected = append(selected, req.Tools[i])
	}
	req.Tools = selected
	// A forced singleton means a specific function, while allowed/forbidden
	// keep their original selection semantics after filtering.
	if len(selected) == 1 && req.ToolChoice == provider.ToolChoiceRequired {
		req.ToolChoice = provider.ToolChoiceFunction{Name: selected[0].Function.Name}
	}
	return nil
}

func cloneRequest(req *provider.ChatRequest) (*provider.ChatRequest, error) {
	if req.CandidateCount < 0 || req.CandidateCount > 1 {
		return nil, invalid("CandidateCount supports zero or one")
	}
	out := *req
	out.Messages = nil
	out.Stop = slices.Clone(req.Stop)
	out.OutputModalities = slices.Clone(req.OutputModalities)
	out.Temperature = clonePointer(req.Temperature)
	out.TopP = clonePointer(req.TopP)
	out.Seed = clonePointer(req.Seed)
	out.StreamUsage = clonePointer(req.StreamUsage)
	out.ParallelToolCalls = clonePointer(req.ParallelToolCalls)
	if req.Thinking != nil {
		out.Thinking = new(*req.Thinking)
		out.Thinking.Enabled = clonePointer(req.Thinking.Enabled)
		out.Thinking.BudgetTokens = clonePointer(req.Thinking.BudgetTokens)
	}
	var err error
	if req.ResponseFormat != nil {
		out.ResponseFormat = new(*req.ResponseFormat)
		out.ResponseFormat.Strict = clonePointer(req.ResponseFormat.Strict)
		out.ResponseFormat.Schema, err = copySchema(req.ResponseFormat.Schema)
		if err != nil {
			return nil, err
		}
	}
	out.Tools, err = cloneTools(req.Tools)
	if err != nil {
		return nil, err
	}
	switch choice := req.ToolChoice.(type) {
	case nil, provider.ToolChoiceMode, provider.ToolChoiceFunction:
	case *provider.ToolChoiceMode:
		if choice == nil {
			return nil, invalid("ToolChoice nil")
		}
		out.ToolChoice = *choice
	case *provider.ToolChoiceFunction:
		if choice == nil {
			return nil, invalid("ToolChoice nil")
		}
		out.ToolChoice = *choice
	default:
		return nil, invalid("ToolChoice unsupported")
	}
	return &out, nil
}

func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	return new(*p)
}

func copySchema(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("einoadapter: schema: %w: %w", provider.ErrInvalidRequest, err)
	}
	return json.RawMessage(raw), nil
}

func cloneTools(tools []provider.Tool) ([]provider.Tool, error) {
	out := slices.Clone(tools)
	for i := range out {
		out[i].Function.Strict = clonePointer(tools[i].Function.Strict)
		var err error
		out[i].Function.Parameters, err = copySchema(tools[i].Function.Parameters)
		if err != nil {
			return nil, err
		}
		if search := tools[i].WebSearch; search != nil {
			out[i].WebSearch = new(*search)
			out[i].WebSearch.AllowedDomains = slices.Clone(search.AllowedDomains)
			out[i].WebSearch.BlockedDomains = slices.Clone(search.BlockedDomains)
		}
	}
	return out, nil
}

func toolsFromEino(tools []*schema.ToolInfo) ([]provider.Tool, error) {
	out := make([]provider.Tool, len(tools))
	names := make(map[string]bool, len(tools))
	for i, tool := range tools {
		if tool == nil || tool.Name == "" || names[tool.Name] {
			return nil, invalid("Tools nil/empty/duplicate name")
		}
		if len(tool.Extra) > 0 {
			return nil, invalid("Tools.Extra unsupported")
		}
		names[tool.Name] = true
		// Validate the public JSON representation before ToJSONSchema, whose
		// ParameterInfo implementation dereferences map entries without a nil check.
		raw, err := json.Marshal(tool)
		if err != nil {
			return nil, fmt.Errorf("einoadapter: tool: %w: %w", provider.ErrInvalidRequest, err)
		}
		var view struct {
			Params map[string]*schema.ParameterInfo `json:"params"`
		}
		if err := json.Unmarshal(raw, &view); err != nil {
			return nil, fmt.Errorf("einoadapter: tool parameters: %w", err)
		}
		if err := validateParams(view.Params); err != nil {
			return nil, err
		}
		parameters, err := tool.ToJSONSchema()
		if err != nil {
			return nil, fmt.Errorf("einoadapter: tool schema: %w", err)
		}
		var copied any
		if parameters != nil {
			copied, err = copySchema(parameters)
			if err != nil {
				return nil, err
			}
		}
		out[i] = provider.Tool{Function: provider.FunctionDef{Name: tool.Name, Description: tool.Desc, Parameters: copied}}
	}
	return out, nil
}

func validateParams(params map[string]*schema.ParameterInfo) error {
	for _, p := range params {
		if p == nil {
			return invalid("Tools.Parameters nil parameter")
		}
		if err := validateParams(p.SubParams); err != nil {
			return err
		}
		if p.ElemInfo != nil {
			if err := validateParams(map[string]*schema.ParameterInfo{"element": p.ElemInfo}); err != nil {
				return err
			}
		}
	}
	return nil
}
