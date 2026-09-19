package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 并发测试的等待上限与轮询间隔。
const (
	testWait = 5 * time.Second
	testTick = 10 * time.Millisecond
)

// recordingEmbedder 记录每次请求的输入切片，按输入长度生成可辨识的向量。
type recordingEmbedder struct {
	name     ProviderName
	mu       sync.Mutex
	requests [][]string
	inflight atomic.Int32
	peak     atomic.Int32
	failOn   string // 输入中含该文本的请求返回错误
	block    chan struct{}
}

func (e *recordingEmbedder) Name() ProviderName { return e.name }

func (e *recordingEmbedder) Embed(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	cur := e.inflight.Add(1)
	defer e.inflight.Add(-1)
	for {
		peak := e.peak.Load()
		if cur <= peak || e.peak.CompareAndSwap(peak, cur) {
			break
		}
	}
	if e.block != nil {
		select {
		case <-e.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	e.mu.Lock()
	e.requests = append(e.requests, append([]string(nil), req.Input...))
	e.mu.Unlock()

	resp := &EmbeddingResponse{}
	for i, text := range req.Input {
		if e.failOn != "" && text == e.failOn {
			return nil, errors.New("boom")
		}
		resp.Data = append(resp.Data, Embedding{Index: i, Vector: []float32{float32(len(text))}})
	}
	return resp, nil
}

func batchTexts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("t%d", i)
	}
	return out
}

// TestEmbedBatchWithOptionsChunksByProviderLimit 锁定分片：百炼单次上限 10 条，
// 25 条文本必须拆成 3 个请求且结果顺序与输入一致。
func TestEmbedBatchWithOptionsChunksByProviderLimit(t *testing.T) {
	t.Parallel()

	e := &recordingEmbedder{name: ProviderQwen}
	texts := batchTexts(25)

	vecs, err := EmbedBatch(t.Context(), e, texts)
	require.NoError(t, err)
	require.Len(t, vecs, 25)
	for i, text := range texts {
		assert.Equal(t, []float32{float32(len(text))}, vecs[i], "index %d", i)
	}

	require.Len(t, e.requests, 3)
	assert.Len(t, e.requests[0], 10)
	assert.Len(t, e.requests[1], 10)
	assert.Len(t, e.requests[2], 5)
	assert.Equal(t, "t0", e.requests[0][0])
	assert.Equal(t, "t20", e.requests[2][0])
}

func TestEmbedBatchWithOptionsExplicitBatchSizeAndPassthrough(t *testing.T) {
	t.Parallel()

	e := &recordingEmbedder{name: ProviderOpenAI}
	dims := 256
	_, err := EmbedBatchWithOptions(t.Context(), e, batchTexts(5), EmbedBatchOptions{
		BatchSize:  2,
		Model:      "text-embedding-3-large",
		Dimensions: &dims,
		TaskType:   EmbeddingTaskDocument,
	})
	require.NoError(t, err)
	require.Len(t, e.requests, 3)
	assert.Equal(t, []string{"t4"}, e.requests[2])
}

// TestEmbedBatchWithOptionsConcurrency 锁定并发上限与顺序保真：在途请求数不超过 Concurrency，
// 结果仍按输入顺序拼回。
func TestEmbedBatchWithOptionsConcurrency(t *testing.T) {
	t.Parallel()

	e := &recordingEmbedder{name: "custom", block: make(chan struct{})}
	texts := batchTexts(40) // 未知平台默认 10 条一片 → 4 片

	done := make(chan struct{})
	var vecs [][]float32
	var err error
	go func() {
		defer close(done)
		vecs, err = EmbedBatchWithOptions(t.Context(), e, texts, EmbedBatchOptions{Concurrency: 2})
	}()

	// 等到两个分片在途后再放行，确认并发上限生效。
	require.Eventually(t, func() bool { return e.inflight.Load() == 2 }, testWait, testTick)
	close(e.block)
	<-done

	require.NoError(t, err)
	require.Len(t, vecs, 40)
	for i, text := range texts {
		assert.Equal(t, []float32{float32(len(text))}, vecs[i], "index %d", i)
	}
	assert.LessOrEqual(t, e.peak.Load(), int32(2), "在途请求不得超过 Concurrency")
	assert.Len(t, e.requests, 4)
}

// TestEmbedBatchWithOptionsFailureAbortsWithoutPartialResult 是"不返回半成品"的反证：
// RAG 建索引宁可整批重跑，也不能悄悄漏掉一段文档。
func TestEmbedBatchWithOptionsFailureAbortsWithoutPartialResult(t *testing.T) {
	t.Parallel()

	for _, concurrency := range []int{1, 3} {
		t.Run(fmt.Sprintf("concurrency=%d", concurrency), func(t *testing.T) {
			t.Parallel()
			e := &recordingEmbedder{name: ProviderQwen, failOn: "t12"}
			vecs, err := EmbedBatchWithOptions(t.Context(), e, batchTexts(30), EmbedBatchOptions{Concurrency: concurrency})
			require.Error(t, err)
			assert.Nil(t, vecs)
			assert.Contains(t, err.Error(), "embed batch")
		})
	}
}

func TestEmbedBatchWithOptionsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e := &recordingEmbedder{name: ProviderQwen, block: make(chan struct{})}
	vecs, err := EmbedBatchWithOptions(ctx, e, batchTexts(30), EmbedBatchOptions{Concurrency: 2})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, vecs)
}

// TestGeminiEmbedderMapsTaskType 锁定 RAG 非对称编码的 task_type 映射，
// 以及未知任务类型 fail-closed。
func TestGeminiEmbedderMapsTaskType(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embedding":{"values":[0.1,0.2]}}`))
	}))
	t.Cleanup(srv.Close)

	e, err := NewGeminiEmbedder(NativeProviderConfig{APIKey: "k", BaseURL: srv.URL + "/v1beta", Model: "gemini-embedding-001"})
	require.NoError(t, err)

	_, err = e.Embed(t.Context(), &EmbeddingRequest{Input: []string{"q"}, TaskType: EmbeddingTaskQuery})
	require.NoError(t, err)
	assert.Equal(t, "RETRIEVAL_QUERY", captured["taskType"])

	_, err = e.Embed(t.Context(), &EmbeddingRequest{Input: []string{"q"}, TaskType: "weird"})
	require.ErrorIs(t, err, ErrInvalidRequest)
}
