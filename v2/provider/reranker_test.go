package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestReranker(t *testing.T, handler http.Handler) Reranker {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	r, err := NewReranker(RerankerConfig{
		Name:    ProviderSiliconFlow,
		BaseURL: srv.URL,
		APIKey:  "sk-test",
		Model:   "BAAI/bge-reranker-v2-m3",
	})
	require.NoError(t, err)
	return r
}

func rerankTestDocuments() []string {
	return []string{"内华达州的首府是卡森城。", "美国的首都是华盛顿特区。", "塞班岛是北马里亚纳群岛的首府。"}
}

// TestRerankSiliconFlowShape 覆盖 tokens + document 对象的响应形态。
func TestRerankSiliconFlowShape(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotType   string
		gotBody   rerankAPIRequest
	)

	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		gotType = req.Header.Get("Content-Type")
		assert.NoError(t, json.NewDecoder(req.Body).Decode(&gotBody))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-rerank-1")
		_, _ = w.Write([]byte(`{
			"id": "0197f0",
			"results": [
				{"index": 1, "relevance_score": 0.98, "document": {"text": "美国的首都是华盛顿特区。"}},
				{"index": 0, "relevance_score": 0.12, "document": {"text": "内华达州的首府是卡森城。"}}
			],
			"tokens": {"input_tokens": 120, "output_tokens": 5}
		}`))
	}))

	topN := 2
	returnDocs := true
	resp, err := r.Rerank(t.Context(), &RerankRequest{
		Query:           "美国的首都是哪里？",
		Documents:       rerankTestDocuments(),
		TopN:            &topN,
		ReturnDocuments: &returnDocs,
	})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/rerank", gotPath)
	assert.Equal(t, "Bearer sk-test", gotAuth)
	assert.Equal(t, "application/json", gotType)
	assert.Equal(t, "BAAI/bge-reranker-v2-m3", gotBody.Model)
	assert.Equal(t, "美国的首都是哪里？", gotBody.Query)
	assert.Len(t, gotBody.Documents, 3)
	require.NotNil(t, gotBody.TopN)
	assert.Equal(t, 2, *gotBody.TopN)
	require.NotNil(t, gotBody.ReturnDocuments)
	assert.True(t, *gotBody.ReturnDocuments)

	require.Len(t, resp.Results, 2)
	assert.Equal(t, 1, resp.Results[0].Index)
	assert.InDelta(t, 0.98, resp.Results[0].RelevanceScore, 1e-9)
	assert.Equal(t, "美国的首都是华盛顿特区。", resp.Results[0].Document)
	assert.Equal(t, "BAAI/bge-reranker-v2-m3", resp.Model)
	assert.Equal(t, Usage{PromptTokens: 120, CompletionTokens: 5, TotalTokens: 125}, resp.Usage)
	assert.Equal(t, "req-rerank-1", resp.Metadata.RequestID)
	assert.Equal(t, ProviderSiliconFlow, resp.Metadata.Provider)
	assert.Equal(t, http.StatusOK, resp.Metadata.StatusCode)
}

// TestRerankJinaShape 覆盖 usage + 无 document 字段的响应形态。
func TestRerankJinaShape(t *testing.T) {
	t.Parallel()

	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"model": "jina-reranker-v3.5",
			"usage": {"total_tokens": 2813},
			"results": [
				{"index": 2, "relevance_score": 0.93},
				{"index": 0, "relevance_score": 0.41}
			]
		}`))
	}))

	docs := rerankTestDocuments()
	resp, err := r.Rerank(t.Context(), &RerankRequest{Query: "首都", Documents: docs})
	require.NoError(t, err)

	assert.Equal(t, "jina-reranker-v3.5", resp.Model)
	assert.Equal(t, Usage{TotalTokens: 2813}, resp.Usage)
	require.Len(t, resp.Results, 2)
	assert.Empty(t, resp.Results[0].Document, "平台未回传原文时留空，用 Index 回查")
	assert.Equal(t, []string{docs[2], docs[0]}, resp.SortedDocuments(docs))
}

func TestRerankUsageFallsBackToPromptPlusCompletion(t *testing.T) {
	t.Parallel()

	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"usage": {"prompt_tokens": 30, "completion_tokens": 2},
			"results": [{"index": 0, "relevance_score": 0.5}]
		}`))
	}))

	resp, err := r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: rerankTestDocuments()})
	require.NoError(t, err)
	assert.Equal(t, Usage{PromptTokens: 30, CompletionTokens: 2, TotalTokens: 32}, resp.Usage)
}

func TestRerankUsageAbsentStaysZero(t *testing.T) {
	t.Parallel()

	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results": [{"index": 0, "relevance_score": 0.5}]}`))
	}))

	resp, err := r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: rerankTestDocuments()})
	require.NoError(t, err)
	assert.Equal(t, Usage{}, resp.Usage, "用量缺失时不猜测")
}

func TestRerankDocumentForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "对象形态",
			body: `{"results":[{"index":0,"relevance_score":1,"document":{"text":"对象文本"}}]}`,
			want: "对象文本",
		},
		{
			name: "裸字符串形态",
			body: `{"results":[{"index":0,"relevance_score":1,"document":"字符串文本"}]}`,
			want: "字符串文本",
		},
		{
			name: "null",
			body: `{"results":[{"index":0,"relevance_score":1,"document":null}]}`,
			want: "",
		},
		{
			name: "字段缺失",
			body: `{"results":[{"index":0,"relevance_score":1}]}`,
			want: "",
		},
		{
			name: "对象缺 text 字段",
			body: `{"results":[{"index":0,"relevance_score":1,"document":{}}]}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))

			resp, err := r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: rerankTestDocuments()})
			require.NoError(t, err)
			require.Len(t, resp.Results, 1)
			assert.Equal(t, tt.want, resp.Results[0].Document)
		})
	}
}

func TestRerankRejectsUnsupportedDocumentForm(t *testing.T) {
	t.Parallel()

	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":1,"document":123}]}`))
	}))

	_, err := r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: rerankTestDocuments()})
	require.Error(t, err)
	require.ErrorContains(t, err, "unsupported document form")
}

// TestRerankRejectsOutOfRangeIndex 是"Index 始终可用于索引请求 Documents"这条
// 契约的反证测试：去掉下标校验后，调用方按 Index 取原文就会越界 panic。
func TestRerankRejectsOutOfRangeIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "上界越界", body: `{"results":[{"index":7,"relevance_score":1}]}`},
		{name: "负下标", body: `{"results":[{"index":-1,"relevance_score":1}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))

			_, err := r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: rerankTestDocuments()})
			require.Error(t, err)
			require.ErrorContains(t, err, "out of range")
		})
	}
}

func TestRerankOmitsNonPositiveTopN(t *testing.T) {
	t.Parallel()

	var raw map[string]any
	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.NoError(t, json.NewDecoder(req.Body).Decode(&raw))
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":1}]}`))
	}))

	zero := 0
	_, err := r.Rerank(t.Context(), &RerankRequest{
		Query:     "q",
		Documents: rerankTestDocuments(),
		TopN:      &zero,
	})
	require.NoError(t, err)

	assert.NotContains(t, raw, "top_n", "≤0 的 TopN 按未设置处理")
	assert.NotContains(t, raw, "return_documents")
}

func TestRerankUsesRequestModelOverride(t *testing.T) {
	t.Parallel()

	var gotModel string
	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body rerankAPIRequest
		assert.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		gotModel = body.Model
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":1}]}`))
	}))

	_, err := r.Rerank(t.Context(), &RerankRequest{
		Model:     "netease-youdao/bce-reranker-base_v1",
		Query:     "q",
		Documents: rerankTestDocuments(),
	})
	require.NoError(t, err)
	assert.Equal(t, "netease-youdao/bce-reranker-base_v1", gotModel)
}

func TestRerankValidatesRequest(t *testing.T) {
	t.Parallel()

	r := newTestReranker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("非法请求不应发往平台")
	}))

	_, err := r.Rerank(t.Context(), nil)
	require.ErrorIs(t, err, ErrNilRerankRequest)

	_, err = r.Rerank(t.Context(), &RerankRequest{Documents: rerankTestDocuments()})
	require.ErrorIs(t, err, ErrEmptyRerankQuery)

	_, err = r.Rerank(t.Context(), &RerankRequest{Query: "   ", Documents: rerankTestDocuments()})
	require.ErrorIs(t, err, ErrEmptyRerankQuery)

	_, err = r.Rerank(t.Context(), &RerankRequest{Query: "q"})
	require.ErrorIs(t, err, ErrEmptyRerankDocuments)

	_, err = r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: []string{}})
	require.ErrorIs(t, err, ErrEmptyRerankDocuments)
}

func TestRerankNilReceiver(t *testing.T) {
	t.Parallel()

	var r *httpReranker

	assert.Empty(t, r.Name())
	_, err := r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: []string{"d"}})
	require.ErrorIs(t, err, ErrNilReranker)
	assert.True(t, rerankerIsNil(nil))
	assert.False(t, rerankerIsNil(r))
}

func TestRerankMapsHTTPErrors(t *testing.T) {
	t.Parallel()

	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"rate limited"}`))
	}))

	_, err := r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: rerankTestDocuments()})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrRateLimit)
	assert.True(t, IsRetryableError(err))

	var providerErr *ProviderError
	require.ErrorAs(t, err, &providerErr)
	assert.Equal(t, ProviderSiliconFlow, providerErr.Provider)
	assert.Equal(t, http.StatusTooManyRequests, providerErr.StatusCode)
	assert.Equal(t, 3*1000*1000*1000, int(providerErr.RetryAfter))
}

func TestRerankRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	r := newTestReranker(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results": [`))
	}))

	_, err := r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: rerankTestDocuments()})
	require.Error(t, err)
	require.ErrorContains(t, err, "decode response")
}

func TestRerankerConfigValidate(t *testing.T) {
	t.Parallel()

	err := RerankerConfig{}.Validate()
	require.ErrorIs(t, err, ErrInvalidRerankerConfig)
	require.ErrorContains(t, err, "name is required")
	require.ErrorContains(t, err, "base url is required")
	require.ErrorContains(t, err, "api key is required")
	require.ErrorContains(t, err, "model is required")

	require.NoError(t, RerankerConfig{
		Name:    ProviderSiliconFlow,
		BaseURL: "https://api.siliconflow.cn/v1",
		APIKey:  "sk-test",
		Model:   "BAAI/bge-reranker-v2-m3",
	}.Validate())

	_, err = NewReranker(RerankerConfig{Name: ProviderSiliconFlow})
	require.ErrorIs(t, err, ErrInvalidRerankerConfig)
}

func TestNewRerankerTrimsBaseURL(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":1}]}`))
	}))
	t.Cleanup(srv.Close)

	r, err := NewReranker(RerankerConfig{
		Name:    ProviderSiliconFlow,
		BaseURL: srv.URL + "/v1///",
		APIKey:  "sk-test",
		Model:   "BAAI/bge-reranker-v2-m3",
	})
	require.NoError(t, err)

	_, err = r.Rerank(t.Context(), &RerankRequest{Query: "q", Documents: []string{"d"}})
	require.NoError(t, err)
	assert.Equal(t, "/v1/rerank", gotPath)
}

func TestRerankResponseSortedDocuments(t *testing.T) {
	t.Parallel()

	docs := rerankTestDocuments()
	resp := &RerankResponse{Results: []RerankResult{
		{Index: 2, RelevanceScore: 0.9},
		{Index: 0, RelevanceScore: 0.5},
	}}
	assert.Equal(t, []string{docs[2], docs[0]}, resp.SortedDocuments(docs))

	// 传入与请求不一致的切片时跳过越界项，不 panic。
	assert.Equal(t, []string{docs[0]}, resp.SortedDocuments(docs[:1]))
	assert.Nil(t, resp.SortedDocuments(nil))

	var nilResp *RerankResponse
	assert.Nil(t, nilResp.SortedDocuments(docs))
	assert.Nil(t, (&RerankResponse{}).SortedDocuments(docs))
}

func TestNewRerankerFromPreset(t *testing.T) {
	t.Parallel()

	r, err := NewRerankerFromPreset(ProviderSiliconFlow, "sk-test", "")
	require.NoError(t, err)
	assert.Equal(t, ProviderSiliconFlow, r.Name())

	r, err = NewRerankerFromPreset(ProviderSiliconFlow, "sk-test", "netease-youdao/bce-reranker-base_v1")
	require.NoError(t, err)
	assert.Equal(t, ProviderSiliconFlow, r.Name())

	_, err = NewRerankerFromPreset(ProviderDeepSeek, "sk-test", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "does not have a rerank preset")

	_, err = NewRerankerFromPreset("nope", "sk-test", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "no preset for provider")

	// 预设未覆盖时显式给出模型名即可接入。
	r, err = NewRerankerFromPreset(ProviderQwen, "sk-test", "gte-rerank-v2")
	require.NoError(t, err)
	assert.Equal(t, ProviderQwen, r.Name())
}

func TestRerankCapabilityIsDiscoverable(t *testing.T) {
	t.Parallel()

	caps, ok := ModelCapabilitiesFromPreset(ProviderSiliconFlow)
	require.True(t, ok)
	assert.True(t, caps.Supports(CapabilityRerank))
	assert.Equal(t, "BAAI/bge-reranker-v2-m3", caps.RerankModel)

	byCapability := ModelCapabilitiesByCapability(CapabilityRerank)
	assert.Contains(t, byCapability, ProviderSiliconFlow)

	preset, ok := AllPresets()[ProviderSiliconFlow]
	require.True(t, ok)
	assert.Equal(t, "BAAI/bge-reranker-v2-m3", preset.RerankModel)
}

func BenchmarkRerankDecodeResults(b *testing.B) {
	items := make([]rerankAPIResult, 0, 64)
	for i := range 64 {
		items = append(items, rerankAPIResult{
			Index:          i,
			RelevanceScore: 0.5,
			Document:       json.RawMessage(`{"text":"候选文档"}`),
		})
	}
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, err := rerankResults(ProviderSiliconFlow, items, len(items)); err != nil {
			b.Fatal(err)
		}
	}
}
