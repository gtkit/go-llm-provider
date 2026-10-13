package einoadapter

import (
	"context"
	"fmt"
	"reflect"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
)

// ChatModel performs a single model invocation. WithTools returns an independent
// model; concurrent use requires a concurrency-safe underlying Provider.
type ChatModel struct {
	provider provider.Provider
	tools    []provider.Tool
}

// New validates p without making requests or starting goroutines.
func New(p provider.Provider) (*ChatModel, error) {
	if p == nil || (reflect.ValueOf(p).Kind() == reflect.Pointer && reflect.ValueOf(p).IsNil()) {
		return nil, fmt.Errorf("einoadapter: %w", provider.ErrNilProvider)
	}
	return &ChatModel{provider: p}, nil
}

// WithTools returns a model with a private copy of the supplied function tools.
func (m *ChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	converted, err := toolsFromEino(tools)
	if err != nil {
		return nil, err
	}
	return &ChatModel{provider: m.provider, tools: converted}, nil
}

// Generate sends one request. Common Eino options override FromRequest options.
func (m *ChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	req, _, err := m.request(messages, opts)
	if err != nil {
		return nil, err
	}
	resp, err := m.provider.Chat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("einoadapter: generate: %w", err)
	}
	if resp == nil {
		return nil, invalid("nil response")
	}
	return responseMessage(resp)
}

var _ model.ToolCallingChatModel = (*ChatModel)(nil)

func invalid(field string) error {
	return fmt.Errorf("einoadapter: %s: %w", field, provider.ErrInvalidRequest)
}
