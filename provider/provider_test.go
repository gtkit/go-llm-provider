package provider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Provider / Preset 测试
// ============================================================

func TestNewProviderFromPreset(t *testing.T) {
	t.Run("known preset", func(t *testing.T) {
		p, err := NewProviderFromPreset(ProviderDeepSeek, "test-key", "")
		require.NoError(t, err)
		assert.Equal(t, ProviderDeepSeek, p.Name())
	})

	t.Run("custom model override", func(t *testing.T) {
		p, err := NewProviderFromPreset(ProviderQwen, "test-key", "qwen-turbo")
		require.NoError(t, err)

		op := p.(*openaiProvider)
		assert.Equal(t, "qwen-turbo", op.model)
	})

	t.Run("unknown preset returns error", func(t *testing.T) {
		_, err := NewProviderFromPreset("unknown-provider", "key", "")
		assert.Error(t, err)
	})
}

// ============================================================
// Registry 测试
// ============================================================

func TestRegistry(t *testing.T) {
	t.Run("register and get", func(t *testing.T) {
		reg := NewRegistry()
		p := NewProvider(ProviderConfig{
			Name:    ProviderDeepSeek,
			BaseURL: "https://api.deepseek.com/v1",
			APIKey:  "test",
			Model:   "deepseek-chat",
		})
		reg.Register(p)

		got, err := reg.Get(ProviderDeepSeek)
		require.NoError(t, err)
		assert.Equal(t, ProviderDeepSeek, got.Name())
	})

	t.Run("first registered becomes default", func(t *testing.T) {
		reg := NewRegistry()
		p1 := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "k1", Model: "m1"})
		p2 := NewProvider(ProviderConfig{Name: ProviderQwen, APIKey: "k2", Model: "m2"})

		reg.Register(p1)
		reg.Register(p2)

		def, err := reg.Default()
		require.NoError(t, err)
		assert.Equal(t, ProviderDeepSeek, def.Name())
	})

	t.Run("set default", func(t *testing.T) {
		reg := NewRegistry()
		reg.Register(NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "k1", Model: "m1"}))
		reg.Register(NewProvider(ProviderConfig{Name: ProviderQwen, APIKey: "k2", Model: "m2"}))

		err := reg.SetDefault(ProviderQwen)
		require.NoError(t, err)

		def, err := reg.Default()
		require.NoError(t, err)
		assert.Equal(t, ProviderQwen, def.Name())
	})

	t.Run("get unregistered returns error", func(t *testing.T) {
		reg := NewRegistry()
		_, err := reg.Get(ProviderZhipu)
		assert.Error(t, err)
	})

	t.Run("default on empty registry returns error", func(t *testing.T) {
		reg := NewRegistry()
		_, err := reg.Default()
		assert.Error(t, err)
	})

	t.Run("names returns all registered", func(t *testing.T) {
		reg := NewRegistry()
		reg.Register(NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "k1", Model: "m1"}))
		reg.Register(NewProvider(ProviderConfig{Name: ProviderZhipu, APIKey: "k2", Model: "m2"}))

		names := reg.Names()
		assert.Len(t, names, 2)
		assert.Contains(t, names, ProviderDeepSeek)
		assert.Contains(t, names, ProviderZhipu)
	})
}

func TestQuickRegistry(t *testing.T) {
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

// ============================================================
// buildRequest 测试
// ============================================================

func TestBuildRequest(t *testing.T) {
	p := &openaiProvider{
		name:  ProviderDeepSeek,
		model: "deepseek-chat",
	}

	t.Run("uses default model when empty", func(t *testing.T) {
		req := p.buildRequest(&ChatRequest{
			Messages: []Message{{Role: RoleUser, Content: "hi"}},
		})
		assert.Equal(t, "deepseek-chat", req.Model)
	})

	t.Run("uses custom model when specified", func(t *testing.T) {
		req := p.buildRequest(&ChatRequest{
			Model:    "deepseek-reasoner",
			Messages: []Message{{Role: RoleUser, Content: "hi"}},
		})
		assert.Equal(t, "deepseek-reasoner", req.Model)
	})

	t.Run("maps messages correctly", func(t *testing.T) {
		temp := float32(0.7)
		req := p.buildRequest(&ChatRequest{
			Messages: []Message{
				{Role: RoleSystem, Content: "you are helpful"},
				{Role: RoleUser, Content: "hello"},
			},
			MaxTokens:   1024,
			Temperature: &temp,
			Stop:        []string{"\n"},
		})

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
	p := &openaiProvider{
		name:  ProviderDeepSeek,
		model: "deepseek-chat",
	}

	t.Run("maps tools correctly", func(t *testing.T) {
		req := p.buildRequest(&ChatRequest{
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

		require.Len(t, req.Tools, 1)
		assert.Equal(t, "get_weather", req.Tools[0].Function.Name)
		assert.Equal(t, "获取天气", req.Tools[0].Function.Description)
	})

	t.Run("maps tool_choice string", func(t *testing.T) {
		req := p.buildRequest(&ChatRequest{
			Messages:   []Message{{Role: RoleUser, Content: "hi"}},
			Tools:      []Tool{{Function: FunctionDef{Name: "f1"}}},
			ToolChoice: "required",
		})
		assert.Equal(t, "required", req.ToolChoice)
	})

	t.Run("maps tool_choice function", func(t *testing.T) {
		req := p.buildRequest(&ChatRequest{
			Messages:   []Message{{Role: RoleUser, Content: "hi"}},
			Tools:      []Tool{{Function: FunctionDef{Name: "get_weather"}}},
			ToolChoice: ToolChoiceFunction{Name: "get_weather"},
		})
		// ToolChoice 应该是 openai.ToolChoice 结构体
		assert.NotNil(t, req.ToolChoice)
	})

	t.Run("maps parallel tool calls", func(t *testing.T) {
		parallel := true
		req := p.buildRequest(&ChatRequest{
			Messages:          []Message{{Role: RoleUser, Content: "hi"}},
			ParallelToolCalls: &parallel,
		})
		require.NotNil(t, req.ParallelToolCalls)
		got, ok := req.ParallelToolCalls.(*bool)
		require.True(t, ok)
		assert.True(t, *got)
	})

	t.Run("maps enable thinking", func(t *testing.T) {
		req := p.buildRequest(&ChatRequest{
			Messages:       []Message{{Role: RoleUser, Content: "hi"}},
			EnableThinking: true,
		})
		require.NotNil(t, req.ChatTemplateKwargs)
		assert.Equal(t, true, req.ChatTemplateKwargs["enable_thinking"])
	})
}

func TestBuildRequestWithToolMessages(t *testing.T) {
	p := &openaiProvider{
		name:  ProviderDeepSeek,
		model: "deepseek-chat",
	}

	t.Run("maps assistant message with tool calls", func(t *testing.T) {
		req := p.buildRequest(&ChatRequest{
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
	t.Run("parse valid JSON", func(t *testing.T) {
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
		fc := FunctionCall{Name: "f1", Arguments: ""}
		var args map[string]any
		err := fc.ParseArguments(&args)
		assert.Error(t, err)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		fc := FunctionCall{Name: "f1", Arguments: "{invalid"}
		var args map[string]any
		err := fc.ParseArguments(&args)
		assert.Error(t, err)
	})
}

func TestChatResponseHelpers(t *testing.T) {
	t.Run("HasToolCalls true", func(t *testing.T) {
		resp := &ChatResponse{
			ToolCalls: []ToolCall{{ID: "1", Function: FunctionCall{Name: "f1"}}},
		}
		assert.True(t, resp.HasToolCalls())
	})

	t.Run("HasToolCalls false", func(t *testing.T) {
		resp := &ChatResponse{Content: "hello"}
		assert.False(t, resp.HasToolCalls())
	})

	t.Run("AssistantMessage preserves tool calls", func(t *testing.T) {
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
	msg := ToolResultMessage("call_abc", `{"result": 42}`)
	assert.Equal(t, RoleTool, msg.Role)
	assert.Equal(t, "call_abc", msg.ToolCallID)
	assert.Equal(t, `{"result": 42}`, msg.Content)
}

func TestToolResultMessageJSON(t *testing.T) {
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
	assert.Equal(t, 20, maxIterations(0))
	assert.Equal(t, 20, maxIterations(-1))
	assert.Equal(t, 5, maxIterations(5))
	assert.Equal(t, 1, maxIterations(1))
}
