package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type geminiEmbedder struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient HTTPDoer
}

// NewGeminiEmbedder creates a native Google Gemini Embedder.
// The returned Embedder is safe for concurrent use when cfg.HTTPClient is safe for concurrent use.
func NewGeminiEmbedder(cfg NativeProviderConfig) (Embedder, error) {
	normalized, err := normalizeNativeConfig(cfg, defaultGeminiBaseURL, defaultGeminiEmbeddingModel)
	if err != nil {
		return nil, err
	}
	return &geminiEmbedder{
		apiKey:     normalized.APIKey,
		baseURL:    normalized.BaseURL,
		model:      normalized.Model,
		httpClient: normalized.HTTPClient,
	}, nil
}

func (e *geminiEmbedder) Name() ProviderName {
	if e == nil {
		return ""
	}
	return ProviderGemini
}

func (e *geminiEmbedder) Embed(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	if e == nil {
		return nil, ErrNilEmbedder
	}
	if req == nil {
		return nil, ErrNilEmbeddingRequest
	}
	if len(req.Input) == 0 {
		return nil, ErrEmptyEmbeddingInput
	}

	model := firstString(req.Model, e.model)
	if len(req.Input) == 1 {
		return e.embedContent(ctx, model, req)
	}
	return e.batchEmbedContents(ctx, model, req)
}

func (e *geminiEmbedder) embedContent(ctx context.Context, model string, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	nativeReq := geminiEmbeddingRequest{
		Model:                geminiModelResource(model),
		Content:              geminiEmbeddingContent(req.Input[0]),
		OutputDimensionality: req.Dimensions,
	}

	resp, metadata, err := doNativeJSON[geminiEmbeddingResponse](ctx, e.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         e.modelURL(model, "embedContent"),
		Body:        nativeReq,
		Provider:    ProviderGemini,
		Model:       model,
		SetHeaders:  e.setHeaders,
		DecodeError: decodeGeminiError,
	})
	if err != nil {
		return nil, err
	}

	if metadata.Model == "" {
		metadata.Model = model
	}
	return &EmbeddingResponse{
		Model:    model,
		Metadata: metadata,
		Data: []Embedding{{
			Index:  0,
			Vector: append([]float32(nil), resp.Embedding.Values...),
		}},
	}, nil
}

func (e *geminiEmbedder) batchEmbedContents(ctx context.Context, model string, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	requests := make([]geminiEmbeddingRequest, 0, len(req.Input))
	for _, input := range req.Input {
		requests = append(requests, geminiEmbeddingRequest{
			Model:                geminiModelResource(model),
			Content:              geminiEmbeddingContent(input),
			OutputDimensionality: req.Dimensions,
		})
	}

	resp, metadata, err := doNativeJSON[geminiBatchEmbeddingResponse](ctx, e.httpClient, nativeHTTPRequest{
		Method:      http.MethodPost,
		URL:         e.modelURL(model, "batchEmbedContents"),
		Body:        geminiBatchEmbeddingRequest{Requests: requests},
		Provider:    ProviderGemini,
		Model:       model,
		SetHeaders:  e.setHeaders,
		DecodeError: decodeGeminiError,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Embeddings) != len(req.Input) {
		return nil, fmt.Errorf("[%s] invalid embedding response: got %d embeddings for %d inputs",
			ProviderGemini, len(resp.Embeddings), len(req.Input))
	}

	if metadata.Model == "" {
		metadata.Model = model
	}
	out := &EmbeddingResponse{
		Model:    model,
		Metadata: metadata,
		Data:     make([]Embedding, 0, len(resp.Embeddings)),
	}
	for i, embedding := range resp.Embeddings {
		out.Data = append(out.Data, Embedding{
			Index:  i,
			Vector: append([]float32(nil), embedding.Values...),
		})
	}
	return out, nil
}

func (e *geminiEmbedder) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", e.apiKey)
}

func (e *geminiEmbedder) modelURL(model, action string) string {
	return e.baseURL + "/models/" + url.PathEscape(geminiModelID(model)) + ":" + action
}

func geminiEmbeddingContent(input string) geminiContent {
	return geminiContent{Parts: []geminiPart{{Text: input}}}
}

func geminiModelID(model string) string {
	return strings.TrimPrefix(model, "models/")
}

func geminiModelResource(model string) string {
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}
