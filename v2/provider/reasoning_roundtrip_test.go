package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// OpenAI 兼容路径：reasoning_content 回传、max_completion_tokens、strict 工具
// ============================================================

// TestOpenAICompatEchoesReasoningContentOnlySupportedProviders 锁定 reasoning_content 的下发边界：
// DeepSeek 带 tools 时缺失直接 400，必须回传；OpenAI 官方端点拒绝该非标准字段，不得下发；
// 自定义平台由调用方声明。
func TestOpenAICompatEchoesReasoningContentOnlySupportedProviders(t *testing.T) {
	t.Parallel()

	history := []Message{
		UserText("查天气"),
		{
			Role:      RoleAssistant,
			Reasoning: "用户要天气，先调用工具",
			ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "get_weather", Arguments: `{}`}}},
		},
		ToolResultMessage("c1", `{"temp":20}`),
	}

	tests := []struct {
		name     string
		provider *openaiProvider
		wantEcho bool
	}{
		{name: "DeepSeek 回传", provider: &openaiProvider{name: ProviderDeepSeek, model: "m"}, wantEcho: true},
		{name: "Kimi 回传", provider: &openaiProvider{name: ProviderMoonshot, model: "m"}, wantEcho: true},
		{name: "百炼回传", provider: &openaiProvider{name: ProviderQwen, model: "m"}, wantEcho: true},
		{name: "OpenAI 不下发", provider: &openaiProvider{name: ProviderOpenAI, model: "m"}},
		{name: "未声明的自定义平台不下发", provider: &openaiProvider{name: "my-vllm", model: "m"}},
		{
			name:     "声明后的自定义平台回传",
			provider: &openaiProvider{name: "my-vllm", model: "m", supportsReasoningContent: true},
			wantEcho: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, err := tc.provider.buildRequest(&ChatRequest{Messages: history})
			require.NoError(t, err)
			require.Len(t, req.Messages, 3)
			if tc.wantEcho {
				assert.Equal(t, "用户要天气，先调用工具", req.Messages[1].ReasoningContent)
			} else {
				assert.Empty(t, req.Messages[1].ReasoningContent)
			}
			// 非 assistant 消息永远不带该字段。
			assert.Empty(t, req.Messages[0].ReasoningContent)
			assert.Empty(t, req.Messages[2].ReasoningContent)
		})
	}
}

// TestOpenAICompatMaxTokensField 锁定输出上限字段的平台分发：
// OpenAI / Azure 的推理模型拒绝 max_tokens，其余兼容平台只认 max_tokens。
func TestOpenAICompatMaxTokensField(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name               ProviderName
		wantCompletionOnly bool
	}{
		{name: ProviderOpenAI, wantCompletionOnly: true},
		{name: ProviderAzureOpenAI, wantCompletionOnly: true},
		{name: ProviderDeepSeek},
		{name: ProviderQwen},
		{name: "my-vllm"},
	} {
		t.Run(string(tc.name), func(t *testing.T) {
			t.Parallel()
			p := &openaiProvider{name: tc.name, model: "m"}
			req, err := p.buildRequest(&ChatRequest{Messages: []Message{UserText("hi")}, MaxTokens: 256})
			require.NoError(t, err)
			// 兼容平台只认已被 OpenAI 标记废弃的 max_tokens，这里必须读它。
			legacy := req.MaxTokens //nolint:staticcheck // 断言目标正是该废弃字段
			if tc.wantCompletionOnly {
				assert.Equal(t, 256, req.MaxCompletionTokens)
				assert.Zero(t, legacy)
			} else {
				assert.Equal(t, 256, legacy)
				assert.Zero(t, req.MaxCompletionTokens)
			}
		})
	}
}

func TestOpenAICompatMapsStrictTools(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{name: ProviderOpenAI, model: "m"}
	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools: []Tool{
			{Function: FunctionDef{Name: "strict_tool", Parameters: ParamSchema{Type: "object"}, Strict: new(true)}},
			{Function: FunctionDef{Name: "loose_tool", Parameters: ParamSchema{Type: "object"}}},
		},
	})
	require.NoError(t, err)
	require.Len(t, req.Tools, 2)
	assert.True(t, req.Tools[0].Function.Strict)
	assert.False(t, req.Tools[1].Function.Strict)
}

// ============================================================
// Anthropic：思考块回放、tool_result 形态、system 缓存断点、TTL、自定义头
// ============================================================

// TestAnthropicAssistantPartsReplayThinkingBlocks 锁定思考块回传：平台要求开启思考并使用
// 工具时把带签名的思考块原样放在 assistant 内容最前面；没有签名的块无法校验，不得回传。
func TestAnthropicAssistantPartsReplayThinkingBlocks(t *testing.T) {
	t.Parallel()

	parts, err := anthropicAssistantParts(Message{
		Role:    RoleAssistant,
		Content: []ContentPart{TextPart("我来查一下")},
		ThinkingBlocks: []ThinkingBlock{
			{Thinking: "需要调用天气工具", Signature: "sig-1"},
			{Redacted: "ENC"},
			{Thinking: "无签名的文本不得回传"},
		},
		ToolCalls: []ToolCall{{ID: "toolu_1", Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`}}},
	})
	require.NoError(t, err)
	require.Len(t, parts, 4)

	assert.Equal(t, "thinking", parts[0].Type)
	assert.Equal(t, "需要调用天气工具", parts[0].Thinking)
	assert.Equal(t, "sig-1", parts[0].Signature)
	assert.Equal(t, "redacted_thinking", parts[1].Type)
	assert.Equal(t, "ENC", parts[1].Data)
	assert.Equal(t, "text", parts[2].Type)
	assert.Equal(t, "tool_use", parts[3].Type)
	assert.Equal(t, "toolu_1", parts[3].ID)

	// 正文为空、只有思考块与工具调用时不发空文本块（平台拒绝空文本块）。
	parts, err = anthropicAssistantParts(Message{
		Role:           RoleAssistant,
		Content:        []ContentPart{TextPart("")},
		ThinkingBlocks: []ThinkingBlock{{Thinking: "t", Signature: "s"}},
		ToolCalls:      []ToolCall{{ID: "toolu_2", Function: FunctionCall{Name: "f", Arguments: `{}`}}},
	})
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.Equal(t, "thinking", parts[0].Type)
	assert.Equal(t, "tool_use", parts[1].Type)
}

// TestAnthropicToolResultPartContentShape 是"tool_result 内容不得直接序列化库内类型"的反证：
// ContentPart 没有 json tag，原样序列化会发出 {"Type":...,"Text":...} 这类平台不认识的字段。
func TestAnthropicToolResultPartContentShape(t *testing.T) {
	t.Parallel()

	text, err := anthropicToolResultPart(ToolResultMessage("toolu_1", `{"temp":20}`))
	require.NoError(t, err)
	assert.Equal(t, "tool_result", text.Type)
	assert.Equal(t, "toolu_1", text.ToolUseID)
	assert.Equal(t, `{"temp":20}`, text.Content)

	mixed, err := anthropicToolResultPart(Message{
		Role:       RoleTool,
		ToolCallID: "toolu_2",
		Content:    []ContentPart{TextPart("截图如下"), ImageDataPart([]byte{1, 2}, "image/png")},
	})
	require.NoError(t, err)
	blocks, ok := mixed.Content.([]anthropicContentPart)
	require.True(t, ok)
	require.Len(t, blocks, 2)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "image", blocks[1].Type)

	raw, err := json.Marshal(mixed)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"Type"`, "不得泄漏库内字段名")
}

// TestAnthropicSystemCacheControlWithTTL 锁定两件事：system 提示词以内容块数组下发以承载
// 片段级缓存断点（system 是缓存最常见的命中对象）；TTL 档位随 cache_control 下发。
func TestAnthropicSystemCacheControlWithTTL(t *testing.T) {
	t.Parallel()

	p := &anthropicProvider{model: "claude-x"}
	req, _, err := p.buildRequest(&ChatRequest{
		Messages: []Message{
			SystemMessage(
				TextPart("你是客服助手。"),
				WithCacheControl(TextPart("<很长的知识库>"), CacheControlEphemeralTTL(CacheControlTTL1h)),
			),
			UserMessage(WithCacheControl(TextPart("hi"), CacheControlEphemeral())),
		},
	}, false)
	require.NoError(t, err)

	require.Len(t, req.System, 2)
	assert.Nil(t, req.System[0].CacheControl)
	require.NotNil(t, req.System[1].CacheControl)
	assert.Equal(t, "ephemeral", req.System[1].CacheControl.Type)
	assert.Equal(t, "1h", req.System[1].CacheControl.TTL)

	require.Len(t, req.Messages, 1)
	require.NotNil(t, req.Messages[0].Content[0].CacheControl)
	assert.Empty(t, req.Messages[0].Content[0].CacheControl.TTL, "未指定 TTL 时不下发，沿用平台默认")
}

func TestAnthropicMapsStrictToolsAndKeepsSearchWithStructuredOutput(t *testing.T) {
	t.Parallel()

	p := &anthropicProvider{model: "claude-x"}
	req, _, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hi")},
		Tools: []Tool{{Function: FunctionDef{
			Name: "f", Parameters: ParamSchema{Type: "object"}, Strict: new(true),
		}}},
		ResponseFormat: JSONObjectFormat(),
	}, false)
	require.NoError(t, err)
	require.Len(t, req.Tools, 1, "原生结构化输出不再占用 tools")
	require.NotNil(t, req.Tools[0].Strict)
	assert.True(t, *req.Tools[0].Strict)
	assert.Nil(t, req.ToolChoice, "原生结构化输出不再强制 tool_choice")
	require.NotNil(t, req.OutputConfig)
	require.NotNil(t, req.OutputConfig.Format)
	assert.Equal(t, "json_schema", req.OutputConfig.Format.Type)

	// 原生搜索与结构化输出不再互斥。
	_, _, err = p.buildRequest(&ChatRequest{
		Messages:       []Message{UserText("hi")},
		Tools:          []Tool{WebSearchTool()},
		ResponseFormat: JSONObjectFormat(),
	}, false)
	require.NoError(t, err)
}

// TestNativeProviderConfigHeaders 锁定自定义请求头：在库默认头之后写入、同名覆盖，
// 是 anthropic-beta 与企业网关鉴权头的唯一入口。
func TestNativeProviderConfigHeaders(t *testing.T) {
	t.Parallel()

	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude-x",` +
			`"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	headers := map[string]string{
		"anthropic-beta":    "compact-2026-01-12",
		"X-Gateway-Token":   "gw-secret",
		"anthropic-version": "2030-01-01", // 同名覆盖库默认值
	}
	p, err := NewAnthropicProvider(NativeProviderConfig{
		APIKey: "k", BaseURL: srv.URL, Model: "claude-x", Headers: headers,
	})
	require.NoError(t, err)
	// 构造后修改调用方的 map 不得影响 provider。
	headers["X-Gateway-Token"] = "tampered"

	_, err = p.Chat(t.Context(), &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)
	assert.Equal(t, "compact-2026-01-12", got.Get("anthropic-beta"))
	assert.Equal(t, "gw-secret", got.Get("X-Gateway-Token"))
	assert.Equal(t, "2030-01-01", got.Get("anthropic-version"))
	assert.Equal(t, "k", got.Get("x-api-key"), "库默认头仍在")
}

// ============================================================
// Gemini：thoughtSignature 回放
// ============================================================

func TestGeminiAssistantPartsReplayThoughtSignature(t *testing.T) {
	t.Parallel()

	parts, err := geminiAssistantParts(Message{
		Role:    RoleAssistant,
		Content: []ContentPart{TextPart("")},
		ToolCalls: []ToolCall{{
			ID:               "call_1",
			Function:         FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`},
			ThoughtSignature: "ts-abc",
		}},
	})
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.NotNil(t, parts[0].FunctionCall)
	assert.Equal(t, "ts-abc", parts[0].ThoughtSignature, "Gemini 3 校验回传的签名，缺失即 400")

	raw, err := json.Marshal(parts[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"thoughtSignature":"ts-abc"`)
}

// ============================================================
// 工具循环：推理往返端到端
// ============================================================

func TestToolResultMessageFor(t *testing.T) {
	t.Parallel()

	call := ToolCall{ID: "call_1", Function: FunctionCall{Name: "get_weather"}}

	msg := ToolResultMessageFor(call, "ok")
	assert.Equal(t, RoleTool, msg.Role)
	assert.Equal(t, "call_1", msg.ToolCallID)
	assert.Equal(t, "get_weather", msg.ToolName)
	assert.Equal(t, "ok", contentText(msg.Content))

	jsonMsg, err := ToolResultMessageJSONFor(call, map[string]int{"temp": 20})
	require.NoError(t, err)
	assert.Equal(t, "get_weather", jsonMsg.ToolName)
	assert.JSONEq(t, `{"temp":20}`, contentText(jsonMsg.Content))

	_, err = ToolResultMessageJSONFor(call, make(chan int))
	require.Error(t, err)
}
