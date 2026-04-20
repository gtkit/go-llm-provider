package provider

import (
	"context"
	"fmt"
)

// SimpleEmbed 是最简便的向量化调用方式：单条文本 → 向量。
// 内部以 len=1 的批量请求调用 Embedder。
func SimpleEmbed(ctx context.Context, e Embedder, text string) ([]float32, error) {
	if embedderIsNil(e) {
		return nil, ErrNilEmbedder
	}

	resp, err := e.Embed(ctx, &EmbeddingRequest{
		Input: []string{text},
	})
	if err != nil {
		return nil, fmt.Errorf("simple embed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("simple embed: empty response data")
	}

	return resp.Data[0].Vector, nil
}

// EmbedBatch 批量向量化，返回与 texts 顺序一致的向量数组。
//
// 即使底层 API 乱序返回，本函数也会按 Embedding.Index 重新排列；
// 若响应长度不匹配、索引越界或存在重复索引导致漏填，均返回错误。
func EmbedBatch(ctx context.Context, e Embedder, texts []string) ([][]float32, error) {
	if embedderIsNil(e) {
		return nil, ErrNilEmbedder
	}
	if len(texts) == 0 {
		return nil, ErrEmptyEmbeddingInput
	}

	resp, err := e.Embed(ctx, &EmbeddingRequest{
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("embed batch: %w", err)
	}

	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embed batch: response length %d mismatches input length %d",
			len(resp.Data), len(texts))
	}

	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("embed batch: response index %d out of range [0, %d)",
				d.Index, len(texts))
		}
		out[d.Index] = d.Vector
	}

	// 防守性校验：重复索引会导致某个槽位始终为 nil。
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("embed batch: missing embedding for input index %d", i)
		}
	}

	return out, nil
}
