package provider

import (
	"context"
	"fmt"
	"sync"
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

// defaultEmbedBatchSize 是未知平台的单次请求文本条数上限：取各平台已知下限，
// 保证在任何预设平台上都不会因批量过大被拒。
const defaultEmbedBatchSize = 10

// embeddingBatchLimits 是各平台 embeddings 端点单次请求的文本条数上限（官方文档口径）。
// 未收录的平台用 defaultEmbedBatchSize。
var embeddingBatchLimits = map[ProviderName]int{
	ProviderOpenAI:      2048,
	ProviderAzureOpenAI: 2048,
	ProviderGemini:      100,
	ProviderCohere:      96,
	ProviderQwen:        10,
}

// EmbedBatchOptions 配置 EmbedBatchWithOptions 的分片与请求参数。零值可用。
type EmbedBatchOptions struct {
	// Model / Dimensions / User / TaskType 原样透传到每个分片请求，语义同 EmbeddingRequest。
	Model      string
	Dimensions *int
	User       string
	TaskType   EmbeddingTaskType

	// BatchSize 是单次请求最多携带的文本条数；<= 0 时按 Embedder 名取平台已知上限，
	// 未知平台用 defaultEmbedBatchSize。
	BatchSize int

	// Concurrency 是同时在途的分片请求数；<= 1 顺序执行。
	// 并发会放大瞬时 RPM / TPM，配合 WithEmbedderRateLimit 使用。
	Concurrency int
}

// EmbedBatch 批量向量化，返回与 texts 顺序一致的向量数组。
// 等价于以零值 EmbedBatchOptions 调用 EmbedBatchWithOptions：按平台上限自动分片、顺序请求。
func EmbedBatch(ctx context.Context, e Embedder, texts []string) ([][]float32, error) {
	return EmbedBatchWithOptions(ctx, e, texts, EmbedBatchOptions{})
}

// EmbedBatchWithOptions 批量向量化：把 texts 按 BatchSize 切成多个请求，
// 可选并发发出，结果按原始顺序拼回。任一分片失败即返回错误并取消其余在途请求，
// 不返回半成品——RAG 建索引宁可整批重跑，也不要悄悄漏掉一段文档。
//
// 即使底层 API 乱序返回，本函数也会按 Embedding.Index 重新排列；
// 若响应长度不匹配、索引越界或存在重复索引导致漏填，均返回错误。
func EmbedBatchWithOptions(ctx context.Context, e Embedder, texts []string, opts EmbedBatchOptions) ([][]float32, error) {
	if embedderIsNil(e) {
		return nil, ErrNilEmbedder
	}
	if len(texts) == 0 {
		return nil, ErrEmptyEmbeddingInput
	}

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = embeddingBatchLimits[e.Name()]
		if batchSize <= 0 {
			batchSize = defaultEmbedBatchSize
		}
	}

	out := make([][]float32, len(texts))
	embedChunk := func(ctx context.Context, start int) error {
		end := min(start+batchSize, len(texts))
		resp, err := e.Embed(ctx, &EmbeddingRequest{
			Model:      opts.Model,
			Input:      texts[start:end],
			Dimensions: opts.Dimensions,
			User:       opts.User,
			TaskType:   opts.TaskType,
		})
		if err != nil {
			return fmt.Errorf("embed batch [%d:%d]: %w", start, end, err)
		}
		return placeEmbeddings(out[start:end], resp.Data, start)
	}

	if opts.Concurrency <= 1 || len(texts) <= batchSize {
		for start := 0; start < len(texts); start += batchSize {
			if err := embedChunk(ctx, start); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	if err := runEmbedChunks(ctx, len(texts), batchSize, opts.Concurrency, embedChunk); err != nil {
		return nil, err
	}
	// 外部 ctx 在派发期间被取消时会有分片未派发：结果不完整，如实报错而非返回带空洞的切片。
	for i, vector := range out {
		if vector == nil {
			if cause := context.Cause(ctx); cause != nil {
				return nil, fmt.Errorf("embed batch: incomplete at index %d: %w", i, cause)
			}
			return nil, fmt.Errorf("embed batch: missing embedding for input index %d", i)
		}
	}
	return out, nil
}

// runEmbedChunks 以至多 concurrency 个在途请求并发执行各分片；任一分片失败即取消其余
// 在途请求并返回首个错误。分片失败后不再派发新分片，只等在途的收尾。
func runEmbedChunks(ctx context.Context, total, batchSize, concurrency int, embedChunk func(context.Context, int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
		sem      = make(chan struct{}, concurrency)
	)
	for start := 0; start < total && ctx.Err() == nil; start += batchSize {
		sem <- struct{}{}
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := embedChunk(ctx, start); err != nil {
				once.Do(func() {
					firstErr = err
					cancel()
				})
			}
		}(start)
	}
	wg.Wait()
	return firstErr
}

// placeEmbeddings 把一个分片的响应按 Index 填入 dst，offset 是分片在全量输入中的起点。
// 长度不匹配、索引越界、重复索引导致漏填均返回错误。
func placeEmbeddings(dst [][]float32, data []Embedding, offset int) error {
	if len(data) != len(dst) {
		return fmt.Errorf("embed batch: response length %d mismatches input length %d",
			len(data), len(dst))
	}
	for _, d := range data {
		if d.Index < 0 || d.Index >= len(dst) {
			return fmt.Errorf("embed batch: response index %d out of range [0, %d)",
				d.Index+offset, len(dst)+offset)
		}
		dst[d.Index] = d.Vector
	}
	// 防守性校验：重复索引会导致某个槽位始终为 nil。
	for i, v := range dst {
		if v == nil {
			return fmt.Errorf("embed batch: missing embedding for input index %d", i+offset)
		}
	}
	return nil
}
