package provider

import (
	"context"
	"fmt"

	"github.com/gtkit/json"
)

// StructuredValidator validates decoded structured output.
type StructuredValidator[T any] func(T) error

// GenerateJSON calls p.Chat and decodes the JSON response content into T.
func GenerateJSON[T any](ctx context.Context, p Provider, req *ChatRequest) (T, *ChatResponse, error) {
	return GenerateJSONWithValidator[T](ctx, p, req, nil)
}

// GenerateJSONWithValidator calls p.Chat, decodes JSON response content into T,
// and runs validator when it is not nil.
func GenerateJSONWithValidator[T any](
	ctx context.Context,
	p Provider,
	req *ChatRequest,
	validator StructuredValidator[T],
) (T, *ChatResponse, error) {
	var out T
	resp, err := GenerateJSONIntoWithValidator(ctx, p, req, &out, validator)
	if err != nil {
		return out, resp, err
	}
	return out, resp, nil
}

// GenerateJSONInto calls p.Chat and decodes the JSON response content into target.
func GenerateJSONInto[T any](ctx context.Context, p Provider, req *ChatRequest, target *T) (*ChatResponse, error) {
	return GenerateJSONIntoWithValidator(ctx, p, req, target, nil)
}

// GenerateJSONIntoWithValidator calls p.Chat, decodes JSON response content into
// target, and runs validator when it is not nil.
func GenerateJSONIntoWithValidator[T any](
	ctx context.Context,
	p Provider,
	req *ChatRequest,
	target *T,
	validator StructuredValidator[T],
) (*ChatResponse, error) {
	if providerIsNil(p) {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilChatRequest
	}
	if target == nil {
		return nil, ErrNilStructuredTarget
	}

	nextReq := *req
	if nextReq.ResponseFormat == nil {
		nextReq.ResponseFormat = JSONObjectFormat()
	}

	resp, err := p.Chat(ctx, &nextReq)
	if err != nil {
		return nil, fmt.Errorf("generate json: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("%w: empty chat response", ErrStructuredDecode)
	}

	if err := json.Unmarshal([]byte(resp.Content), target); err != nil {
		return resp, fmt.Errorf("%w: %w", ErrStructuredDecode, err)
	}
	if validator != nil {
		if err := validator(*target); err != nil {
			return resp, fmt.Errorf("%w: %w", ErrStructuredValidation, err)
		}
	}
	return resp, nil
}
