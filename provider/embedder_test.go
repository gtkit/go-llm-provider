package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// EmbedderConfig / NewEmbedder 测试
// ============================================================

func TestEmbedderConfigValidate(t *testing.T) {
	t.Parallel()

	t.Run("accepts complete config", func(t *testing.T) {
		t.Parallel()
		err := (EmbedderConfig{
			Name:   ProviderOpenAI,
			APIKey: "k",
			Model:  "text-embedding-3-small",
		}).Validate()
		require.NoError(t, err)
	})

	t.Run("rejects missing name", func(t *testing.T) {
		t.Parallel()
		err := (EmbedderConfig{
			APIKey: "k",
			Model:  "m",
		}).Validate()
		require.ErrorIs(t, err, ErrInvalidEmbedderConfig)
		require.ErrorContains(t, err, "name is required")
	})

	t.Run("rejects missing api key", func(t *testing.T) {
		t.Parallel()
		err := (EmbedderConfig{
			Name:  ProviderOpenAI,
			Model: "m",
		}).Validate()
		require.ErrorIs(t, err, ErrInvalidEmbedderConfig)
		require.ErrorContains(t, err, "api key is required")
	})

	t.Run("rejects missing model", func(t *testing.T) {
		t.Parallel()
		err := (EmbedderConfig{
			Name:   ProviderOpenAI,
			APIKey: "k",
		}).Validate()
		require.ErrorIs(t, err, ErrInvalidEmbedderConfig)
		require.ErrorContains(t, err, "model is required")
	})

	t.Run("rejects empty config", func(t *testing.T) {
		t.Parallel()
		err := EmbedderConfig{}.Validate()
		require.ErrorIs(t, err, ErrInvalidEmbedderConfig)
		require.ErrorContains(t, err, "name is required")
		require.ErrorContains(t, err, "api key is required")
		require.ErrorContains(t, err, "model is required")
	})
}

func TestNewEmbedder(t *testing.T) {
	t.Parallel()

	t.Run("creates embedder with valid config", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-xxx",
			Model:   "text-embedding-3-small",
		})
		require.NoError(t, err)
		require.NotNil(t, e)
		assert.Equal(t, ProviderOpenAI, e.Name())
	})

	t.Run("rejects invalid config", func(t *testing.T) {
		t.Parallel()
		_, err := NewEmbedder(EmbedderConfig{})
		require.ErrorIs(t, err, ErrInvalidEmbedderConfig)
	})

	t.Run("allows empty base url", func(t *testing.T) {
		t.Parallel()
		// BaseURL 为空时，go-openai 会使用默认地址（官方 OpenAI）
		e, err := NewEmbedder(EmbedderConfig{
			Name:   ProviderOpenAI,
			APIKey: "sk-xxx",
			Model:  "text-embedding-3-small",
		})
		require.NoError(t, err)
		require.NotNil(t, e)
	})
}

// ============================================================
// openaiEmbedder.Embed 测试（mock HTTP）
// ============================================================

// newEmbeddingMockServer 构造一个替代的 /v1/embeddings 服务器。
func newEmbeddingMockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func writeEmbeddingJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(w).Encode(payload))
}

func TestOpenaiEmbedderEmbed(t *testing.T) {
	t.Parallel()

	t.Run("returns vectors for batch input", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/embeddings", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)

			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "text-embedding-3-small", body["model"])
			inputs, _ := body["input"].([]any)
			assert.Len(t, inputs, 2)

			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "text-embedding-3-small",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1, 0.2, 0.3}},
					{"object": "embedding", "index": 1, "embedding": []float32{0.4, 0.5, 0.6}},
				},
				"usage": map[string]any{"prompt_tokens": 10, "total_tokens": 10},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: srv.URL,
			APIKey:  "test-key",
			Model:   "text-embedding-3-small",
		})
		require.NoError(t, err)

		resp, err := e.Embed(context.Background(), &EmbeddingRequest{
			Input: []string{"hello", "world"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.Data, 2)
		assert.Equal(t, 0, resp.Data[0].Index)
		assert.Equal(t, []float32{0.1, 0.2, 0.3}, resp.Data[0].Vector)
		assert.Equal(t, 1, resp.Data[1].Index)
		assert.Equal(t, []float32{0.4, 0.5, 0.6}, resp.Data[1].Vector)
		assert.Equal(t, "text-embedding-3-small", resp.Model)
		assert.Equal(t, 10, resp.Usage.PromptTokens)
		assert.Equal(t, 10, resp.Usage.TotalTokens)
	})

	t.Run("passes dimensions when provided", func(t *testing.T) {
		t.Parallel()

		var seenDims float64
		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			if d, ok := body["dimensions"].(float64); ok {
				seenDims = d
			}

			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "text-embedding-3-small",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: srv.URL,
			APIKey:  "test-key",
			Model:   "text-embedding-3-small",
		})
		require.NoError(t, err)

		dims := 256
		_, err = e.Embed(context.Background(), &EmbeddingRequest{
			Input:      []string{"hi"},
			Dimensions: &dims,
		})
		require.NoError(t, err)
		assert.InDelta(t, 256.0, seenDims, 0.0001)
	})

	t.Run("omits dimensions when nil", func(t *testing.T) {
		t.Parallel()

		var hasDims bool
		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			_, hasDims = body["dimensions"]

			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "text-embedding-3-small",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: srv.URL,
			APIKey:  "test-key",
			Model:   "text-embedding-3-small",
		})
		require.NoError(t, err)

		_, err = e.Embed(context.Background(), &EmbeddingRequest{
			Input: []string{"hi"},
		})
		require.NoError(t, err)
		assert.False(t, hasDims, "dimensions should not be present when nil")
	})

	t.Run("uses model override from request", func(t *testing.T) {
		t.Parallel()

		var seenModel string
		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			seenModel, _ = body["model"].(string)

			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  seenModel,
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: srv.URL,
			APIKey:  "test-key",
			Model:   "text-embedding-3-small",
		})
		require.NoError(t, err)

		_, err = e.Embed(context.Background(), &EmbeddingRequest{
			Model: "text-embedding-3-large",
			Input: []string{"hi"},
		})
		require.NoError(t, err)
		assert.Equal(t, "text-embedding-3-large", seenModel)
	})

	t.Run("uses default model when request model empty", func(t *testing.T) {
		t.Parallel()

		var seenModel string
		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			seenModel, _ = body["model"].(string)

			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  seenModel,
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: srv.URL,
			APIKey:  "test-key",
			Model:   "text-embedding-3-small",
		})
		require.NoError(t, err)

		_, err = e.Embed(context.Background(), &EmbeddingRequest{
			Input: []string{"hi"},
		})
		require.NoError(t, err)
		assert.Equal(t, "text-embedding-3-small", seenModel)
	})

	t.Run("propagates user field", func(t *testing.T) {
		t.Parallel()

		var seenUser string
		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			seenUser, _ = body["user"].(string)

			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "m",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: srv.URL,
			APIKey:  "test-key",
			Model:   "m",
		})
		require.NoError(t, err)

		_, err = e.Embed(context.Background(), &EmbeddingRequest{
			Input: []string{"hi"},
			User:  "user-123",
		})
		require.NoError(t, err)
		assert.Equal(t, "user-123", seenUser)
	})

	t.Run("rejects nil request", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: "http://127.0.0.1:0",
			APIKey:  "k",
			Model:   "m",
		})
		require.NoError(t, err)

		_, err = e.Embed(context.Background(), nil)
		require.ErrorIs(t, err, ErrNilEmbeddingRequest)
	})

	t.Run("rejects empty input", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: "http://127.0.0.1:0",
			APIKey:  "k",
			Model:   "m",
		})
		require.NoError(t, err)

		_, err = e.Embed(context.Background(), &EmbeddingRequest{Input: []string{}})
		require.ErrorIs(t, err, ErrEmptyEmbeddingInput)

		_, err = e.Embed(context.Background(), &EmbeddingRequest{Input: nil})
		require.ErrorIs(t, err, ErrEmptyEmbeddingInput)
	})

	t.Run("propagates http error with provider tag", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: srv.URL,
			APIKey:  "bad-key",
			Model:   "m",
		})
		require.NoError(t, err)

		_, err = e.Embed(context.Background(), &EmbeddingRequest{Input: []string{"hi"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "[openai]")
		assert.Contains(t, err.Error(), "create embeddings")
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(_ http.ResponseWriter, _ *http.Request) {
			// 永远不返回，让 ctx.Done 触发
			select {}
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: srv.URL,
			APIKey:  "k",
			Model:   "m",
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // 预先取消

		_, err = e.Embed(ctx, &EmbeddingRequest{Input: []string{"hi"}})
		require.Error(t, err)
	})
}

func TestOpenaiEmbedderNilReceiver(t *testing.T) {
	t.Parallel()

	t.Run("Name returns empty", func(t *testing.T) {
		t.Parallel()
		var e *openaiEmbedder
		assert.Equal(t, ProviderName(""), e.Name())
	})

	t.Run("Embed returns ErrNilEmbedder", func(t *testing.T) {
		t.Parallel()
		var e *openaiEmbedder
		_, err := e.Embed(context.Background(), &EmbeddingRequest{Input: []string{"hi"}})
		require.ErrorIs(t, err, ErrNilEmbedder)
	})
}

func TestEmbedderIsNil(t *testing.T) {
	t.Parallel()

	assert.True(t, embedderIsNil(nil))

	e, err := NewEmbedder(EmbedderConfig{
		Name:   ProviderOpenAI,
		APIKey: "k",
		Model:  "m",
	})
	require.NoError(t, err)
	assert.False(t, embedderIsNil(e))
}

// ============================================================
// NewEmbedderFromPreset 测试
// ============================================================

func TestNewEmbedderFromPreset(t *testing.T) {
	t.Parallel()

	t.Run("known preset uses default embedding model", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedderFromPreset(ProviderOpenAI, "test-key", "")
		require.NoError(t, err)
		require.NotNil(t, e)
		assert.Equal(t, ProviderOpenAI, e.Name())

		oe, ok := e.(*openaiEmbedder)
		require.True(t, ok)
		assert.Equal(t, "text-embedding-3-small", oe.model)
	})

	t.Run("custom model override", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedderFromPreset(ProviderQwen, "test-key", "text-embedding-v4")
		require.NoError(t, err)

		oe, ok := e.(*openaiEmbedder)
		require.True(t, ok)
		assert.Equal(t, "text-embedding-v4", oe.model)
	})

	t.Run("qwen preset model", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedderFromPreset(ProviderQwen, "test-key", "")
		require.NoError(t, err)
		oe := e.(*openaiEmbedder)
		assert.Equal(t, "text-embedding-v3", oe.model)
	})

	t.Run("zhipu preset model", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedderFromPreset(ProviderZhipu, "test-key", "")
		require.NoError(t, err)
		oe := e.(*openaiEmbedder)
		assert.Equal(t, "embedding-3", oe.model)
	})

	t.Run("qianfan preset model", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedderFromPreset(ProviderQianfan, "test-key", "")
		require.NoError(t, err)
		oe := e.(*openaiEmbedder)
		assert.Equal(t, "embedding-v1", oe.model)
	})

	t.Run("siliconflow preset model", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedderFromPreset(ProviderSiliconFlow, "test-key", "")
		require.NoError(t, err)
		oe := e.(*openaiEmbedder)
		assert.Equal(t, "BAAI/bge-m3", oe.model)
	})

	t.Run("deepseek returns unsupported error", func(t *testing.T) {
		t.Parallel()
		_, err := NewEmbedderFromPreset(ProviderDeepSeek, "test-key", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not support embedding")
	})

	t.Run("moonshot returns unsupported error", func(t *testing.T) {
		t.Parallel()
		_, err := NewEmbedderFromPreset(ProviderMoonshot, "test-key", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not support embedding")
	})

	t.Run("unknown preset returns error", func(t *testing.T) {
		t.Parallel()
		_, err := NewEmbedderFromPreset("unknown-provider", "key", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no preset for provider")
	})

	t.Run("custom model bypasses unsupported check", func(t *testing.T) {
		t.Parallel()
		// 即使平台默认无 embedding 模型，显式传入 model 也应能构造
		// （方便用户对接自部署或未预设的 embedding 服务）
		e, err := NewEmbedderFromPreset(ProviderDeepSeek, "test-key", "my-custom-model")
		require.NoError(t, err)
		require.NotNil(t, e)
	})
}

// ============================================================
// Registry embedder 管理测试
// ============================================================

func TestRegistryEmbedder(t *testing.T) {
	t.Parallel()

	t.Run("register and get", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		e, err := NewEmbedder(EmbedderConfig{
			Name:    ProviderOpenAI,
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "test",
			Model:   "text-embedding-3-small",
		})
		require.NoError(t, err)
		reg.RegisterEmbedder(e)

		got, err := reg.GetEmbedder(ProviderOpenAI)
		require.NoError(t, err)
		assert.Equal(t, ProviderOpenAI, got.Name())
	})

	t.Run("first registered becomes default", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		e1, err := NewEmbedder(EmbedderConfig{Name: ProviderOpenAI, APIKey: "k1", Model: "m1"})
		require.NoError(t, err)
		e2, err := NewEmbedder(EmbedderConfig{Name: ProviderQwen, APIKey: "k2", Model: "m2"})
		require.NoError(t, err)

		reg.RegisterEmbedder(e1)
		reg.RegisterEmbedder(e2)

		def, err := reg.DefaultEmbedder()
		require.NoError(t, err)
		assert.Equal(t, ProviderOpenAI, def.Name())
	})

	t.Run("set default", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		e1, err := NewEmbedder(EmbedderConfig{Name: ProviderOpenAI, APIKey: "k1", Model: "m1"})
		require.NoError(t, err)
		e2, err := NewEmbedder(EmbedderConfig{Name: ProviderQwen, APIKey: "k2", Model: "m2"})
		require.NoError(t, err)
		reg.RegisterEmbedder(e1)
		reg.RegisterEmbedder(e2)

		err = reg.SetDefaultEmbedder(ProviderQwen)
		require.NoError(t, err)

		def, err := reg.DefaultEmbedder()
		require.NoError(t, err)
		assert.Equal(t, ProviderQwen, def.Name())
	})

	t.Run("set default on unregistered returns error", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		err := reg.SetDefaultEmbedder(ProviderZhipu)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not registered")
	})

	t.Run("get unregistered returns error", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		_, err := reg.GetEmbedder(ProviderZhipu)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not registered")
	})

	t.Run("default on empty registry returns error", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		_, err := reg.DefaultEmbedder()
		assert.Error(t, err)
	})

	t.Run("embedder names returns all registered", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		e1, err := NewEmbedder(EmbedderConfig{Name: ProviderOpenAI, APIKey: "k1", Model: "m1"})
		require.NoError(t, err)
		e2, err := NewEmbedder(EmbedderConfig{Name: ProviderQwen, APIKey: "k2", Model: "m2"})
		require.NoError(t, err)
		reg.RegisterEmbedder(e1)
		reg.RegisterEmbedder(e2)

		names := reg.EmbedderNames()
		assert.Equal(t, []ProviderName{ProviderOpenAI, ProviderQwen}, names)
	})

	t.Run("register nil is ignored", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.RegisterEmbedder(nil)
		assert.Empty(t, reg.EmbedderNames())
	})

	t.Run("providers and embedders are independent", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()

		p, err := NewProvider(ProviderConfig{Name: ProviderOpenAI, APIKey: "k", Model: "chat"})
		require.NoError(t, err)
		reg.Register(p)

		e, err := NewEmbedder(EmbedderConfig{Name: ProviderQwen, APIKey: "k", Model: "emb"})
		require.NoError(t, err)
		reg.RegisterEmbedder(e)

		// Provider 侧只有 openai
		assert.Equal(t, []ProviderName{ProviderOpenAI}, reg.Names())
		// Embedder 侧只有 qwen
		assert.Equal(t, []ProviderName{ProviderQwen}, reg.EmbedderNames())

		// 两边的默认也互不干扰
		pDef, err := reg.Default()
		require.NoError(t, err)
		assert.Equal(t, ProviderOpenAI, pDef.Name())

		eDef, err := reg.DefaultEmbedder()
		require.NoError(t, err)
		assert.Equal(t, ProviderQwen, eDef.Name())
	})
}

// ============================================================
// QuickRegistry / QuickRegistryStrict 的 embedder 集成测试
// ============================================================

func TestQuickRegistryEmbedder(t *testing.T) {
	t.Parallel()

	t.Run("registers both provider and embedder when preset supports embedding", func(t *testing.T) {
		t.Parallel()
		reg := QuickRegistry(map[ProviderName]string{
			ProviderOpenAI: "sk-test",
			ProviderQwen:   "qw-test",
		})

		// 两边都有
		providerNames := reg.Names()
		embedderNames := reg.EmbedderNames()
		assert.ElementsMatch(t, []ProviderName{ProviderOpenAI, ProviderQwen}, providerNames)
		assert.ElementsMatch(t, []ProviderName{ProviderOpenAI, ProviderQwen}, embedderNames)
	})

	t.Run("skips embedder for platforms without embedding support", func(t *testing.T) {
		t.Parallel()
		reg := QuickRegistry(map[ProviderName]string{
			ProviderDeepSeek: "dk-test",
			ProviderMoonshot: "mk-test",
			ProviderZhipu:    "zp-test",
		})

		// Provider 三个都注册
		assert.ElementsMatch(t,
			[]ProviderName{ProviderDeepSeek, ProviderMoonshot, ProviderZhipu},
			reg.Names(),
		)

		// Embedder 只有 Zhipu（DeepSeek 和 Moonshot 无 embedding 模型）
		assert.Equal(t, []ProviderName{ProviderZhipu}, reg.EmbedderNames())
	})

	t.Run("empty key skips both provider and embedder", func(t *testing.T) {
		t.Parallel()
		reg := QuickRegistry(map[ProviderName]string{
			ProviderOpenAI: "sk-test",
			ProviderQwen:   "", // 空 key，两边都不注册
		})

		assert.Equal(t, []ProviderName{ProviderOpenAI}, reg.Names())
		assert.Equal(t, []ProviderName{ProviderOpenAI}, reg.EmbedderNames())
	})
}

func TestQuickRegistryStrictEmbedder(t *testing.T) {
	t.Parallel()

	t.Run("no error when all valid", func(t *testing.T) {
		t.Parallel()
		reg, err := QuickRegistryStrict(map[ProviderName]string{
			ProviderOpenAI: "sk-test",
			ProviderQwen:   "qw-test",
		})
		require.NoError(t, err)
		assert.ElementsMatch(t, []ProviderName{ProviderOpenAI, ProviderQwen}, reg.EmbedderNames())
	})

	t.Run("platforms without embedding support do not produce errors", func(t *testing.T) {
		t.Parallel()
		// DeepSeek、Moonshot 无 embedding 模型，不应触发 error
		reg, err := QuickRegistryStrict(map[ProviderName]string{
			ProviderDeepSeek: "dk-test",
			ProviderMoonshot: "mk-test",
		})
		require.NoError(t, err)
		assert.ElementsMatch(t,
			[]ProviderName{ProviderDeepSeek, ProviderMoonshot},
			reg.Names(),
		)
		assert.Empty(t, reg.EmbedderNames())
	})

	t.Run("unknown provider produces joined errors", func(t *testing.T) {
		t.Parallel()
		reg, err := QuickRegistryStrict(map[ProviderName]string{
			ProviderOpenAI: "sk-test",
			"unknown":      "bad-key",
		})
		require.Error(t, err)
		require.NotNil(t, reg)
		require.ErrorContains(t, err, `register provider "unknown"`)

		// 合法的 openai 仍然注册成功
		assert.Contains(t, reg.Names(), ProviderOpenAI)
		assert.Contains(t, reg.EmbedderNames(), ProviderOpenAI)
	})
}

// ============================================================
// SimpleEmbed / EmbedBatch 便捷函数测试
// ============================================================

func TestSimpleEmbed(t *testing.T) {
	t.Parallel()

	t.Run("returns vector", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			inputs, _ := body["input"].([]any)
			assert.Len(t, inputs, 1)

			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "m",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{1.0, 2.0, 3.0}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		vec, err := SimpleEmbed(context.Background(), e, "hello")
		require.NoError(t, err)
		assert.Equal(t, []float32{1.0, 2.0, 3.0}, vec)
	})

	t.Run("nil embedder returns ErrNilEmbedder", func(t *testing.T) {
		t.Parallel()
		_, err := SimpleEmbed(context.Background(), nil, "hi")
		require.ErrorIs(t, err, ErrNilEmbedder)
	})

	t.Run("propagates underlying error", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		_, err = SimpleEmbed(context.Background(), e, "hi")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "simple embed")
	})
}

func TestEmbedBatch(t *testing.T) {
	t.Parallel()

	t.Run("returns vectors in request order", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			inputs, _ := body["input"].([]any)
			assert.Len(t, inputs, 3)

			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "m",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
					{"object": "embedding", "index": 1, "embedding": []float32{0.2}},
					{"object": "embedding", "index": 2, "embedding": []float32{0.3}},
				},
				"usage": map[string]any{"prompt_tokens": 3, "total_tokens": 3},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		vecs, err := EmbedBatch(context.Background(), e, []string{"a", "b", "c"})
		require.NoError(t, err)
		require.Len(t, vecs, 3)
		assert.Equal(t, []float32{0.1}, vecs[0])
		assert.Equal(t, []float32{0.2}, vecs[1])
		assert.Equal(t, []float32{0.3}, vecs[2])
	})

	t.Run("reorders scrambled response by index", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, _ *http.Request) {
			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "m",
				"data": []map[string]any{
					{"object": "embedding", "index": 2, "embedding": []float32{0.3}},
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
					{"object": "embedding", "index": 1, "embedding": []float32{0.2}},
				},
				"usage": map[string]any{"prompt_tokens": 3, "total_tokens": 3},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		vecs, err := EmbedBatch(context.Background(), e, []string{"a", "b", "c"})
		require.NoError(t, err)
		require.Len(t, vecs, 3)
		assert.Equal(t, []float32{0.1}, vecs[0])
		assert.Equal(t, []float32{0.2}, vecs[1])
		assert.Equal(t, []float32{0.3}, vecs[2])
	})

	t.Run("single-item batch works", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, _ *http.Request) {
			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "m",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.5}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		vecs, err := EmbedBatch(context.Background(), e, []string{"only"})
		require.NoError(t, err)
		require.Len(t, vecs, 1)
		assert.Equal(t, []float32{0.5}, vecs[0])
	})

	t.Run("nil embedder returns ErrNilEmbedder", func(t *testing.T) {
		t.Parallel()
		_, err := EmbedBatch(context.Background(), nil, []string{"a"})
		require.ErrorIs(t, err, ErrNilEmbedder)
	})

	t.Run("empty input returns ErrEmptyEmbeddingInput", func(t *testing.T) {
		t.Parallel()
		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: "http://127.0.0.1:0", APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		_, err = EmbedBatch(context.Background(), e, []string{})
		require.ErrorIs(t, err, ErrEmptyEmbeddingInput)

		_, err = EmbedBatch(context.Background(), e, nil)
		require.ErrorIs(t, err, ErrEmptyEmbeddingInput)
	})

	t.Run("propagates underlying error", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		_, err = EmbedBatch(context.Background(), e, []string{"a"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "embed batch")
	})

	t.Run("detects response length mismatch", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, _ *http.Request) {
			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "m",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		_, err = EmbedBatch(context.Background(), e, []string{"a", "b"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "response length")
	})

	t.Run("detects out-of-range index", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, _ *http.Request) {
			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "m",
				"data": []map[string]any{
					{"object": "embedding", "index": 5, "embedding": []float32{0.1}},
					{"object": "embedding", "index": 1, "embedding": []float32{0.2}},
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		_, err = EmbedBatch(context.Background(), e, []string{"a", "b"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "out of range")
	})

	t.Run("detects duplicate index (missing slot)", func(t *testing.T) {
		t.Parallel()

		srv := newEmbeddingMockServer(t, func(w http.ResponseWriter, _ *http.Request) {
			writeEmbeddingJSON(t, w, map[string]any{
				"object": "list",
				"model":  "m",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float32{0.1}},
					{"object": "embedding", "index": 0, "embedding": []float32{0.9}}, // 重复
				},
				"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			})
		})

		e, err := NewEmbedder(EmbedderConfig{
			Name: ProviderOpenAI, BaseURL: srv.URL, APIKey: "k", Model: "m",
		})
		require.NoError(t, err)

		_, err = EmbedBatch(context.Background(), e, []string{"a", "b"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing embedding")
	})
}

// ============================================================
// Preset 字段完整性测试
// ============================================================

func TestPresetsEmbeddingModelDefaults(t *testing.T) {
	t.Parallel()

	wantEmbeddings := map[ProviderName]string{
		ProviderOpenAI:      "text-embedding-3-small",
		ProviderQwen:        "text-embedding-v3",
		ProviderZhipu:       "embedding-3",
		ProviderQianfan:     "embedding-v1",
		ProviderSiliconFlow: "BAAI/bge-m3",
		ProviderDeepSeek:    "", // 明确无 embedding 模型
		ProviderMoonshot:    "", // 明确无 embedding 模型
	}

	catalog := AllPresets()
	for name, want := range wantEmbeddings {
		preset, ok := catalog[name]
		require.Truef(t, ok, "preset for %q missing", name)
		assert.Equalf(t, want, preset.EmbeddingModel,
			"preset %q EmbeddingModel mismatch", name)
	}
}
