package provider

import (
	"context"
	"errors"
	"fmt"
)

// FallbackProvider tries providers in order when the previous provider returns
// a retryable error. It is safe for concurrent use when the wrapped providers
// are safe for concurrent use.
type FallbackProvider struct {
	providers []Provider
}

// NewFallbackProvider returns a Provider that falls back across providers in order.
func NewFallbackProvider(providers ...Provider) (*FallbackProvider, error) {
	if len(providers) == 0 {
		return nil, ErrNilProvider
	}
	out := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if providerIsNil(p) {
			return nil, ErrNilProvider
		}
		out = append(out, p)
	}
	return &FallbackProvider{providers: out}, nil
}

// Name returns the first provider name.
func (p *FallbackProvider) Name() ProviderName {
	if p == nil || len(p.providers) == 0 || providerIsNil(p.providers[0]) {
		return ""
	}
	return p.providers[0].Name()
}

// Chat tries providers in order until one succeeds or a non-retryable error occurs.
func (p *FallbackProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if p == nil || len(p.providers) == 0 {
		return nil, ErrNilProvider
	}

	var errs []error
	for i, provider := range p.providers {
		resp, err := provider.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", provider.Name(), err))
		if i == len(p.providers)-1 || !IsRetryableError(err) {
			return nil, errors.Join(errs...)
		}
	}

	return nil, errors.Join(errs...)
}

// ChatStream tries providers in order until stream creation succeeds or a non-retryable error occurs.
func (p *FallbackProvider) ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error) {
	if p == nil || len(p.providers) == 0 {
		return nil, ErrNilProvider
	}

	var errs []error
	for i, provider := range p.providers {
		stream, err := provider.ChatStream(ctx, req)
		if err == nil {
			return stream, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", provider.Name(), err))
		if i == len(p.providers)-1 || !IsRetryableError(err) {
			return nil, errors.Join(errs...)
		}
	}

	return nil, errors.Join(errs...)
}
