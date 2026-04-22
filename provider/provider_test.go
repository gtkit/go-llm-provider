package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Provider / Preset 测试
// ============================================================

func TestNewProviderFromPreset(t *testing.T) {
	t.Parallel()

	t.Run("openai preset", func(t *testing.T) {
		t.Parallel()

		p, err := NewProviderFromPreset(ProviderOpenAI, "test-key", "")
		require.NoError(t, err)
		assert.Equal(t, ProviderOpenAI, p.Name())
	})

	t.Run("known preset", func(t *testing.T) {
		t.Parallel()
		p, err := NewProviderFromPreset(ProviderDeepSeek, "test-key", "")
		require.NoError(t, err)
		assert.Equal(t, ProviderDeepSeek, p.Name())
	})

	t.Run("custom model override", func(t *testing.T) {
		t.Parallel()
		p, err := NewProviderFromPreset(ProviderQwen, "test-key", "qwen-turbo")
		require.NoError(t, err)

		op := p.(*openaiProvider)
		assert.Equal(t, "qwen-turbo", op.model)
	})

	t.Run("unknown preset returns error", func(t *testing.T) {
		t.Parallel()
		_, err := NewProviderFromPreset("unknown-provider", "key", "")
		assert.Error(t, err)
	})
}

func TestNewProviderFromPresetUsesApprovedDefaultModels(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		providerName  ProviderName
		expectedModel string
	}{
		{name: "deepseek", providerName: ProviderDeepSeek, expectedModel: "deepseek-chat"},
		{name: "qwen", providerName: ProviderQwen, expectedModel: "qwen3.6-plus"},
		{name: "zhipu", providerName: ProviderZhipu, expectedModel: "glm-5.1"},
		{name: "qianfan", providerName: ProviderQianfan, expectedModel: "ernie-4.5-turbo-32k"},
		{name: "siliconflow", providerName: ProviderSiliconFlow, expectedModel: "deepseek-ai/DeepSeek-V3"},
		{name: "moonshot", providerName: ProviderMoonshot, expectedModel: "kimi-k2-turbo-preview"},
		{name: "openai", providerName: ProviderOpenAI, expectedModel: "gpt-5.4-mini"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := NewProviderFromPreset(tc.providerName, "test-key", "")
			require.NoError(t, err)

			op, ok := p.(*openaiProvider)
			require.True(t, ok)
			assert.Equal(t, tc.expectedModel, op.model)
		})
	}
}

// ============================================================
// Registry 测试
// ============================================================

func TestRegistry(t *testing.T) {
	t.Parallel()

	t.Run("register and get", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		p, err := NewProvider(ProviderConfig{
			Name:    ProviderDeepSeek,
			BaseURL: "https://api.deepseek.com/v1",
			APIKey:  "test",
			Model:   "deepseek-chat",
		})
		require.NoError(t, err)
		reg.Register(p)

		got, err := reg.Get(ProviderDeepSeek)
		require.NoError(t, err)
		assert.Equal(t, ProviderDeepSeek, got.Name())
	})

	t.Run("first registered becomes default", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		p1, err := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "k1", Model: "m1"})
		require.NoError(t, err)
		p2, err := NewProvider(ProviderConfig{Name: ProviderQwen, APIKey: "k2", Model: "m2"})
		require.NoError(t, err)

		reg.Register(p1)
		reg.Register(p2)

		def, err := reg.Default()
		require.NoError(t, err)
		assert.Equal(t, ProviderDeepSeek, def.Name())
	})

	t.Run("set default", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		p1, err := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "k1", Model: "m1"})
		require.NoError(t, err)
		p2, err := NewProvider(ProviderConfig{Name: ProviderQwen, APIKey: "k2", Model: "m2"})
		require.NoError(t, err)
		reg.Register(p1)
		reg.Register(p2)

		err = reg.SetDefault(ProviderQwen)
		require.NoError(t, err)

		def, err := reg.Default()
		require.NoError(t, err)
		assert.Equal(t, ProviderQwen, def.Name())
	})

	t.Run("get unregistered returns error", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		_, err := reg.Get(ProviderZhipu)
		assert.Error(t, err)
	})

	t.Run("default on empty registry returns error", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		_, err := reg.Default()
		assert.Error(t, err)
	})

	t.Run("names returns all registered", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		p1, err := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "k1", Model: "m1"})
		require.NoError(t, err)
		p2, err := NewProvider(ProviderConfig{Name: ProviderZhipu, APIKey: "k2", Model: "m2"})
		require.NoError(t, err)
		reg.Register(p1)
		reg.Register(p2)

		names := reg.Names()
		assert.Len(t, names, 2)
		assert.Contains(t, names, ProviderDeepSeek)
		assert.Contains(t, names, ProviderZhipu)
	})
}

func TestQuickRegistry(t *testing.T) {
	t.Parallel()

	reg := QuickRegistry(map[ProviderName]string{
		ProviderDeepSeek: "dk-test",
		ProviderQwen:     "qw-test",
		ProviderZhipu:    "", // 空 key，不应注册
	})

	names := reg.Names()
	assert.Len(t, names, 2)
	assert.Contains(t, names, ProviderDeepSeek)
	assert.Contains(t, names, ProviderQwen)
	assert.NotContains(t, names, ProviderZhipu)
}

func TestQuickRegistryRegistersOpenAI(t *testing.T) {
	t.Parallel()

	reg := QuickRegistry(map[ProviderName]string{
		ProviderOpenAI: "openai-key",
	})

	assert.Equal(t, []ProviderName{ProviderOpenAI}, reg.Names())
}

func TestQuickRegistryStrict(t *testing.T) {
	t.Parallel()

	t.Run("returns joined errors for invalid providers", func(t *testing.T) {
		t.Parallel()

		reg, err := QuickRegistryStrict(map[ProviderName]string{
			ProviderDeepSeek: "dk-test",
			"deepsek":        "bad-key",
		})
		require.Error(t, err)
		require.NotNil(t, reg)
		require.ErrorContains(t, err, `register provider "deepsek"`)

		names := reg.Names()
		assert.Equal(t, []ProviderName{ProviderDeepSeek}, names)
	})

	t.Run("returns nil error when all providers are valid", func(t *testing.T) {
		t.Parallel()

		reg, err := QuickRegistryStrict(map[ProviderName]string{
			ProviderDeepSeek: "dk-test",
			ProviderQwen:     "qw-test",
		})
		require.NoError(t, err)
		assert.Equal(t, []ProviderName{ProviderDeepSeek, ProviderQwen}, reg.Names())
	})
}

// ============================================================
// buildRequest 测试
// ============================================================

func TestBuildRequest(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{
		name:  ProviderDeepSeek,
		model: "deepseek-chat",
	}

	t.Run("uses default model when empty", func(t *testing.T) {
		t.Parallel()
		req, err := p.buildRequest(&ChatRequest{
			Messages: []Message{{Role: RoleUser, Content: "hi"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "deepseek-chat", req.Model)
	})

	t.Run("uses custom model when specified", func(t *testing.T) {
		t.Parallel()
		req, err := p.buildRequest(&ChatRequest{
			Model:    "deepseek-reasoner",
			Messages: []Message{{Role: RoleUser, Content: "hi"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "deepseek-reasoner", req.Model)
	})

	t.Run("maps messages correctly", func(t *testing.T) {
		t.Parallel()
		temp := float32(0.7)
		req, err := p.buildRequest(&ChatRequest{
			Messages: []Message{
				{Role: RoleSystem, Content: "you are helpful"},
				{Role: RoleUser, Content: "hello"},
			},
			MaxTokens:   1024,
			Temperature: &temp,
			Stop:        []string{"\n"},
		})
		require.NoError(t, err)

		assert.Len(t, req.Messages, 2)
		assert.Equal(t, "system", req.Messages[0].Role)
		assert.Equal(t, "user", req.Messages[1].Role)
		assert.Equal(t, 1024, req.MaxTokens)
		assert.InDelta(t, 0.7, req.Temperature, 0.0001)
		assert.Equal(t, []string{"\n"}, req.Stop)
	})
}

// ============================================================
// Tool Use 相关测试
// ============================================================

func TestBuildRequestWithTools(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{
		name:  ProviderDeepSeek,
		model: "deepseek-chat",
	}

	t.Run("maps tools correctly", func(t *testing.T) {
		t.Parallel()
		req, err := p.buildRequest(&ChatRequest{
			Messages: []Message{{Role: RoleUser, Content: "天气"}},
			Tools: []Tool{
				{
					Function: FunctionDef{
						Name:        "get_weather",
						Description: "获取天气",
						Parameters: ParamSchema{
							Type: "object",
							Properties: map[string]ParamSchema{
								"city": {Type: "string", Description: "城市"},
							},
							Required: []string{"city"},
						},
					},
				},
			},
		})
		require.NoError(t, err)

		require.Len(t, req.Tools, 1)
		assert.Equal(t, "get_weather", req.Tools[0].Function.Name)
		assert.Equal(t, "获取天气", req.Tools[0].Function.Description)
	})

	t.Run("maps tool_choice string", func(t *testing.T) {
		t.Parallel()
		req, err := p.buildRequest(&ChatRequest{
			Messages:   []Message{{Role: RoleUser, Content: "hi"}},
			Tools:      []Tool{{Function: FunctionDef{Name: "f1"}}},
			ToolChoice: ToolChoiceRequired,
		})
		require.NoError(t, err)
		assert.Equal(t, "required", req.ToolChoice)
	})

	t.Run("maps tool_choice function", func(t *testing.T) {
		t.Parallel()
		req, err := p.buildRequest(&ChatRequest{
			Messages:   []Message{{Role: RoleUser, Content: "hi"}},
			Tools:      []Tool{{Function: FunctionDef{Name: "get_weather"}}},
			ToolChoice: ToolChoiceFunction{Name: "get_weather"},
		})
		require.NoError(t, err)
		// ToolChoice 应该是 openai.ToolChoice 结构体
		assert.NotNil(t, req.ToolChoice)
	})

	t.Run("maps parallel tool calls", func(t *testing.T) {
		t.Parallel()
		parallel := true
		req, err := p.buildRequest(&ChatRequest{
			Messages:          []Message{{Role: RoleUser, Content: "hi"}},
			ParallelToolCalls: &parallel,
		})
		require.NoError(t, err)
		require.NotNil(t, req.ParallelToolCalls)
		got, ok := req.ParallelToolCalls.(*bool)
		require.True(t, ok)
		assert.True(t, *got)
	})

	t.Run("maps enable thinking", func(t *testing.T) {
		t.Parallel()
		req, err := p.buildRequest(&ChatRequest{
			Messages:       []Message{{Role: RoleUser, Content: "hi"}},
			EnableThinking: true,
		})
		require.NoError(t, err)
		require.NotNil(t, req.ChatTemplateKwargs)
		assert.Equal(t, true, req.ChatTemplateKwargs["enable_thinking"])
	})

	t.Run("rejects invalid tool choice", func(t *testing.T) {
		t.Parallel()
		_, err := p.buildRequest(&ChatRequest{
			Messages:   []Message{{Role: RoleUser, Content: "hi"}},
			ToolChoice: ToolChoiceMode("sometimes"),
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidToolChoice)
	})

	t.Run("rejects empty function tool choice", func(t *testing.T) {
		t.Parallel()
		_, err := p.buildRequest(&ChatRequest{
			Messages:   []Message{{Role: RoleUser, Content: "hi"}},
			ToolChoice: ToolChoiceFunction{},
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidToolChoice)
	})

	t.Run("rejects thinking on unsupported provider", func(t *testing.T) {
		t.Parallel()
		other := &openaiProvider{
			name:  ProviderQwen,
			model: "qwen-plus",
		}

		_, err := other.buildRequest(&ChatRequest{
			Messages:       []Message{{Role: RoleUser, Content: "hi"}},
			EnableThinking: true,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnsupportedThinking)
	})
}

func TestBuildRequestWithToolMessages(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{
		name:  ProviderDeepSeek,
		model: "deepseek-chat",
	}

	t.Run("maps assistant message with tool calls", func(t *testing.T) {
		t.Parallel()
		req, err := p.buildRequest(&ChatRequest{
			Messages: []Message{
				{Role: RoleUser, Content: "天气怎么样"},
				{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{
						{
							ID: "call_abc123",
							Function: FunctionCall{
								Name:      "get_weather",
								Arguments: `{"city":"北京"}`,
							},
						},
					},
				},
				{
					Role:       RoleTool,
					Content:    `{"temperature":28}`,
					ToolCallID: "call_abc123",
				},
			},
		})
		require.NoError(t, err)

		require.Len(t, req.Messages, 3)

		// assistant 消息应包含 tool calls
		assert.Len(t, req.Messages[1].ToolCalls, 1)
		assert.Equal(t, "call_abc123", req.Messages[1].ToolCalls[0].ID)
		assert.Equal(t, "get_weather", req.Messages[1].ToolCalls[0].Function.Name)

		// tool 消息应包含 ToolCallID
		assert.Equal(t, "tool", req.Messages[2].Role)
		assert.Equal(t, "call_abc123", req.Messages[2].ToolCallID)
		assert.Equal(t, `{"temperature":28}`, req.Messages[2].Content)
	})
}

// ============================================================
// Tool Use 类型测试
// ============================================================

func TestFunctionCallParseArguments(t *testing.T) {
	t.Parallel()

	t.Run("parse valid JSON", func(t *testing.T) {
		t.Parallel()
		fc := FunctionCall{
			Name:      "get_weather",
			Arguments: `{"city":"北京","unit":"celsius"}`,
		}

		var args struct {
			City string `json:"city"`
			Unit string `json:"unit"`
		}
		err := fc.ParseArguments(&args)
		require.NoError(t, err)
		assert.Equal(t, "北京", args.City)
		assert.Equal(t, "celsius", args.Unit)
	})

	t.Run("empty arguments returns error", func(t *testing.T) {
		t.Parallel()
		fc := FunctionCall{Name: "f1", Arguments: ""}
		var args map[string]any
		err := fc.ParseArguments(&args)
		assert.Error(t, err)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		t.Parallel()
		fc := FunctionCall{Name: "f1", Arguments: "{invalid"}
		var args map[string]any
		err := fc.ParseArguments(&args)
		assert.Error(t, err)
	})
}

func TestChatResponseHelpers(t *testing.T) {
	t.Parallel()

	t.Run("HasToolCalls true", func(t *testing.T) {
		t.Parallel()
		resp := &ChatResponse{
			ToolCalls: []ToolCall{{ID: "1", Function: FunctionCall{Name: "f1"}}},
		}
		assert.True(t, resp.HasToolCalls())
	})

	t.Run("HasToolCalls false", func(t *testing.T) {
		t.Parallel()
		resp := &ChatResponse{Content: "hello"}
		assert.False(t, resp.HasToolCalls())
	})

	t.Run("AssistantMessage preserves tool calls", func(t *testing.T) {
		t.Parallel()
		tcs := []ToolCall{
			{ID: "call_1", Function: FunctionCall{Name: "f1", Arguments: `{"a":1}`}},
			{ID: "call_2", Function: FunctionCall{Name: "f2", Arguments: `{"b":2}`}},
		}
		resp := &ChatResponse{
			Content:   "",
			ToolCalls: tcs,
		}

		msg := resp.AssistantMessage()
		assert.Equal(t, RoleAssistant, msg.Role)
		assert.Len(t, msg.ToolCalls, 2)
		assert.Equal(t, "call_1", msg.ToolCalls[0].ID)
	})
}

func TestToolResultMessage(t *testing.T) {
	t.Parallel()

	msg := ToolResultMessage("call_abc", `{"result": 42}`)
	assert.Equal(t, RoleTool, msg.Role)
	assert.Equal(t, "call_abc", msg.ToolCallID)
	assert.Equal(t, `{"result": 42}`, msg.Content)
}

func TestToolResultMessageJSON(t *testing.T) {
	t.Parallel()

	result := map[string]any{"temperature": 28, "city": "北京"}
	msg, err := ToolResultMessageJSON("call_xyz", result)
	require.NoError(t, err)
	assert.Equal(t, RoleTool, msg.Role)
	assert.Equal(t, "call_xyz", msg.ToolCallID)

	// 验证 Content 是合法 JSON
	var parsed map[string]any
	err = json.Unmarshal([]byte(msg.Content), &parsed)
	require.NoError(t, err)
	assert.InDelta(t, 28.0, parsed["temperature"], 0.0001)
}

func TestParamSchema(t *testing.T) {
	t.Parallel()

	schema := ParamSchema{
		Type: "object",
		Properties: map[string]ParamSchema{
			"city": {Type: "string", Description: "城市名称"},
			"unit": {Type: "string", Enum: []string{"celsius", "fahrenheit"}},
			"tags": {
				Type:  "array",
				Items: &ParamSchema{Type: "string"},
			},
		},
		Required: []string{"city"},
	}

	// 验证能正确序列化为 JSON
	data, err := json.Marshal(schema)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "object", parsed["type"])
	props := parsed["properties"].(map[string]any)
	assert.Contains(t, props, "city")
	assert.Contains(t, props, "unit")
	assert.Contains(t, props, "tags")

	unitSchema := props["unit"].(map[string]any)
	assert.Contains(t, unitSchema, "enum")
}

// ============================================================
// RunToolLoop 辅助函数测试
// ============================================================

func TestMaxIterations(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 20, maxIterations(0))
	assert.Equal(t, 20, maxIterations(-1))
	assert.Equal(t, 5, maxIterations(5))
	assert.Equal(t, 1, maxIterations(1))
}

func TestProviderConfigValidate(t *testing.T) {
	t.Parallel()

	t.Run("accepts complete config", func(t *testing.T) {
		t.Parallel()
		err := (ProviderConfig{
			Name:   ProviderDeepSeek,
			APIKey: "k",
			Model:  "m",
		}).Validate()
		require.NoError(t, err)
	})

	t.Run("rejects missing required fields", func(t *testing.T) {
		t.Parallel()
		_, err := NewProvider(ProviderConfig{})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidProviderConfig)
		require.ErrorContains(t, err, "name is required")
		require.ErrorContains(t, err, "api key is required")
		require.ErrorContains(t, err, "model is required")
	})
}

// ============================================================
// openaiProvider.Chat 错误包装（WrapProviderError 集成）
// ============================================================

func TestOpenAIProviderChat_WrapsProviderError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		wantCode  ErrorCode
		wantIs    error
		wantRetry bool
	}{
		{"rate limit", http.StatusTooManyRequests, ErrorCodeRateLimit, ErrRateLimit, true},
		{"unauthorized", http.StatusUnauthorized, ErrorCodeAuth, ErrAuth, false},
		{"bad request", http.StatusBadRequest, ErrorCodeInvalidRequest, ErrInvalidRequest, false},
		{"server error", http.StatusInternalServerError, ErrorCodeServerError, ErrServerError, true},
		{"gateway timeout", http.StatusGatewayTimeout, ErrorCodeTimeout, ErrTimeout, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"error":{"message":"mock error","type":"test"}}`))
			}))
			t.Cleanup(srv.Close)

			p, err := NewProvider(ProviderConfig{
				Name:    ProviderOpenAI,
				BaseURL: srv.URL,
				APIKey:  "sk-test",
				Model:   "gpt-4",
			})
			require.NoError(t, err)

			_, err = p.Chat(context.Background(), &ChatRequest{
				Messages: []Message{{Role: RoleUser, Content: "hi"}},
			})
			require.Error(t, err)

			var providerErr *ProviderError
			require.ErrorAs(t, err, &providerErr, "expected *ProviderError from openaiProvider.Chat")
			assert.Equal(t, ProviderName("openai"), providerErr.Provider)
			assert.Equal(t, tt.status, providerErr.StatusCode)
			assert.Equal(t, tt.wantCode, providerErr.Code)
			assert.Equal(t, tt.wantRetry, providerErr.Retryable)
			assert.ErrorIs(t, err, tt.wantIs)
		})
	}
}
