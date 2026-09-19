package provider

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildRequestThinkingDeepSeekEffort 锁定 DeepSeek 的档位口径：Effort 走类型化的
// reasoning_effort；Enabled 落在顶层 thinking 扩展字段，由注入通道而非请求结构体承载。
func TestBuildRequestThinkingDeepSeekEffort(t *testing.T) {
	t.Parallel()

	enabled := true
	p := &openaiProvider{name: ProviderDeepSeek, model: "deepseek-v4-flash"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: &enabled, Effort: ThinkingEffortMax},
	})
	require.NoError(t, err)
	assert.Equal(t, "max", req.ReasoningEffort)
	// 官方端点不识别 vLLM 约定的 chat_template_kwargs，不得再下发。
	assert.Nil(t, req.ChatTemplateKwargs)
}

// TestThinkingTypeExtraFields 覆盖顶层 thinking 字段的共享映射，
// 以及它在四个同形平台上都已登记。
func TestThinkingTypeExtraFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *ChatRequest
		want map[string]any
	}{
		{name: "nil request", req: nil},
		{name: "no thinking", req: &ChatRequest{}},
		// Effort 走 go-openai 的类型化 reasoning_effort 字段，不需要顶层注入
		{name: "effort only", req: &ChatRequest{Thinking: &Thinking{Effort: ThinkingEffortLow}}},
		{
			name: "enabled",
			req:  &ChatRequest{Thinking: &Thinking{Enabled: boolPtr(true)}},
			want: map[string]any{"thinking": map[string]any{"type": "enabled"}},
		},
		{
			name: "disabled",
			req:  &ChatRequest{Thinking: &Thinking{Enabled: boolPtr(false)}},
			want: map[string]any{"thinking": map[string]any{"type": "disabled"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, thinkingTypeExtraFields(tc.req))
		})
	}

	enabled := true
	for _, name := range []ProviderName{ProviderDeepSeek, ProviderZhipu, ProviderMoonshot, ProviderArk} {
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			require.True(t, needsExtraFields(name))
			ctx := withExtraFields(t.Context(), name, &ChatRequest{Thinking: &Thinking{Enabled: &enabled}})
			fields, ok := ctx.Value(extraFieldsCtxKey{}).(map[string]any)
			require.True(t, ok)
			assert.Equal(t, map[string]any{"thinking": map[string]any{"type": "enabled"}}, fields)
		})
	}
}

func TestBuildRequestThinkingOpenAIEffort(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{name: ProviderOpenAI, model: "gpt-5.6-terra"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Effort: ThinkingEffortHigh},
	})
	require.NoError(t, err)
	assert.Equal(t, "high", req.ReasoningEffort)
}

func TestBuildRequestThinkingUnmappedProviderRejected(t *testing.T) {
	t.Parallel()

	enabled := true
	p := &openaiProvider{name: ProviderQianfan, model: "ernie-5.1"}

	_, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{
			Enabled: &enabled,
			Effort:  ThinkingEffortLow,
		},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestBuildRequestThinkingUnmappedFieldRejected(t *testing.T) {
	t.Parallel()

	budget := 2048
	// DeepSeek 映射 Enabled 与 Effort；BudgetTokens 无映射，必须报错而非静默丢弃。
	p := &openaiProvider{name: ProviderDeepSeek, model: "deepseek-v4-flash"}

	_, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{BudgetTokens: &budget},
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestStreamChunkReasoningDeltaField(t *testing.T) {
	t.Parallel()

	chunk := StreamChunk{ReasoningDelta: "thinking..."}
	assert.Equal(t, "thinking...", chunk.ReasoningDelta)
}

func TestUsageReasoningTokensField(t *testing.T) {
	t.Parallel()

	usage := Usage{ReasoningTokens: 12}
	assert.Equal(t, 12, usage.ReasoningTokens)
}

func TestOpenAIReasoningContentFieldAvailableInSDK(t *testing.T) {
	t.Parallel()

	msg := openai.ChatCompletionMessage{ReasoningContent: "trace"}
	assert.Equal(t, "trace", msg.ReasoningContent)
}

// TestAnthropicThinkingParam 覆盖 Anthropic thinking 参数的映射与结构性拒绝。
//
// budget 非正数一项是 omitempty 的反证：budget_tokens 带 omitempty，
// 若放行 0 会序列化出只含 type 的 {"type":"enabled"}，
// 平台收到语义损坏的请求而调用方无从得知。
func TestAnthropicThinkingParam(t *testing.T) {
	t.Parallel()

	enabled, disabled := true, false
	budget, zero, negative := 2048, 0, -1

	t.Run("映射", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			thinking *Thinking
			want     *anthropicThinking
		}{
			{
				name:     "nil 不下发",
				thinking: nil,
				want:     nil,
			},
			{
				name:     "全未设置不下发",
				thinking: &Thinking{},
				want:     nil,
			},
			{
				name:     "仅 Effort 不下发 thinking（落在 output_config）",
				thinking: &Thinking{Effort: ThinkingEffortHigh},
				want:     nil,
			},
			{
				name:     "开启未给预算：自适应",
				thinking: &Thinking{Enabled: &enabled},
				want:     &anthropicThinking{Type: "adaptive"},
			},
			{
				name:     "开启并给出预算：旧式固定预算",
				thinking: &Thinking{Enabled: &enabled, BudgetTokens: &budget},
				want:     &anthropicThinking{Type: "enabled", BudgetTokens: 2048},
			},
			{
				name:     "只给预算视为开启",
				thinking: &Thinking{BudgetTokens: &budget},
				want:     &anthropicThinking{Type: "enabled", BudgetTokens: 2048},
			},
			{
				name:     "显式关闭",
				thinking: &Thinking{Enabled: &disabled},
				want:     &anthropicThinking{Type: "disabled"},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := anthropicThinkingParam(tc.thinking)
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("拒绝", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			thinking *Thinking
		}{
			{
				name:     "预算为 0（会被 omitempty 吞掉）",
				thinking: &Thinking{BudgetTokens: &zero},
			},
			{
				name:     "预算为负",
				thinking: &Thinking{BudgetTokens: &negative},
			},
			{
				name:     "关闭却给预算",
				thinking: &Thinking{Enabled: &disabled, BudgetTokens: &budget},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				_, err := anthropicThinkingParam(tc.thinking)
				require.ErrorIs(t, err, ErrInvalidRequest)
			})
		}
	})
}

// TestAnthropicBuildRequestThinking 确认 thinking 与 output_config.effort 真的进入请求体。
func TestAnthropicBuildRequestThinking(t *testing.T) {
	t.Parallel()

	p := &anthropicProvider{model: "claude-x"}
	budget := 2048
	enabled := true

	req, _, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hi")},
		Thinking: &Thinking{BudgetTokens: &budget},
	}, false)
	require.NoError(t, err)
	require.NotNil(t, req.Thinking)
	assert.Equal(t, "enabled", req.Thinking.Type)
	assert.Equal(t, 2048, req.Thinking.BudgetTokens)
	assert.Nil(t, req.OutputConfig)

	req, _, err = p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hi")},
		Thinking: &Thinking{Enabled: &enabled, Effort: ThinkingEffortHigh},
	}, false)
	require.NoError(t, err)
	require.NotNil(t, req.Thinking)
	assert.Equal(t, "adaptive", req.Thinking.Type)
	require.NotNil(t, req.OutputConfig)
	assert.Equal(t, "high", req.OutputConfig.Effort)
	assert.Nil(t, req.OutputConfig.Format)
}

// TestGeminiThinkingParam 覆盖 Gemini thinkingConfig 的映射：
// Effort → thinkingLevel，BudgetTokens → thinkingBudget；
// 未给预算时借用平台语义取值：开启用 -1（模型自决）、关闭用 0（禁用）。
func TestGeminiThinkingParam(t *testing.T) {
	t.Parallel()

	enabled, disabled := true, false
	budget, zero := 2048, 0

	tests := []struct {
		name     string
		thinking *Thinking
		// wantNil 为 true 时要求不下发 thinkingConfig；否则校验 wantBudget / wantLevel。
		wantBudget *int
		wantLevel  string
		wantNil    bool
	}{
		{name: "nil 不下发", thinking: nil, wantNil: true},
		{name: "全未设置不下发", thinking: &Thinking{}, wantNil: true},
		{name: "开启未给预算：动态", thinking: &Thinking{Enabled: &enabled}, wantBudget: intPtr(-1)},
		{name: "关闭：禁用", thinking: &Thinking{Enabled: &disabled}, wantBudget: intPtr(0)},
		{name: "显式预算", thinking: &Thinking{BudgetTokens: &budget}, wantBudget: intPtr(2048)},
		{name: "预算 0 即禁用", thinking: &Thinking{BudgetTokens: &zero}, wantBudget: intPtr(0)},
		{name: "档位", thinking: &Thinking{Effort: ThinkingEffortHigh}, wantLevel: "high"},
		{name: "开启并给档位", thinking: &Thinking{Enabled: &enabled, Effort: ThinkingEffortLow}, wantLevel: "low"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := geminiThinkingParam(tc.thinking)
			require.NoError(t, err)
			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.wantBudget, got.ThinkingBudget)
			assert.Equal(t, tc.wantLevel, got.ThinkingLevel)
		})
	}

	t.Run("拒绝", func(t *testing.T) {
		t.Parallel()
		for name, thinking := range map[string]*Thinking{
			"关闭却给预算":  {Enabled: &disabled, BudgetTokens: &budget},
			"关闭却给档位":  {Enabled: &disabled, Effort: ThinkingEffortLow},
			"档位与预算同设": {Effort: ThinkingEffortLow, BudgetTokens: &budget},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				_, err := geminiThinkingParam(thinking)
				require.ErrorIs(t, err, ErrInvalidRequest)
			})
		}
	})
}

// TestGeminiBuildRequestThinkingConfig 确认 thinkingConfig 进入 generationConfig，
// 且在其他生成参数全未设置时也会创建该配置块。
func TestGeminiBuildRequestThinkingConfig(t *testing.T) {
	t.Parallel()

	p := &geminiProvider{model: "gemini-x"}
	enabled := true

	req, _, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hi")},
		Thinking: &Thinking{Enabled: &enabled},
	})
	require.NoError(t, err)
	require.NotNil(t, req.GenerationConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, -1, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

// TestAnthropicResponseThinkingBlock 是"思考内容不混入正文"的反证。
// redacted_thinking 是加密块，其内容不得出现在任何文本字段里，但必须原样保留供回传。
func TestAnthropicResponseThinkingBlock(t *testing.T) {
	t.Parallel()

	parsed, err := anthropicResponseContent(anthropicResponse{
		StopReason: "end_turn",
		Content: []anthropicContentPart{
			{Type: "thinking", Thinking: "让我想想", Signature: "sig-1"},
			{Type: "redacted_thinking", Data: "ENCRYPTED-MUST-NOT-LEAK"},
			{Type: "text", Text: "答案是 42"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "答案是 42", parsed.Text)
	assert.Equal(t, "让我想想", parsed.Reasoning)
	assert.NotContains(t, parsed.Text, "ENCRYPTED-MUST-NOT-LEAK")
	assert.NotContains(t, parsed.Reasoning, "ENCRYPTED-MUST-NOT-LEAK")
	assert.Equal(t, []ThinkingBlock{
		{Thinking: "让我想想", Signature: "sig-1"},
		{Redacted: "ENCRYPTED-MUST-NOT-LEAK"},
	}, parsed.ThinkingBlocks)
}

// TestGeminiResponseThoughtPart 是"thought part 不混入正文、且不连带丢弃工具调用"的反证：
// 若用 continue 跳过整个 part，同一 part 上的 functionCall 会被静默丢弃。
func TestGeminiResponseThoughtPart(t *testing.T) {
	t.Parallel()

	parsed, err := geminiResponseContent(geminiResponse{
		Candidates: []geminiCandidate{{
			FinishReason: "STOP",
			Content: geminiContent{Parts: []geminiPart{
				{Text: "思考中", Thought: true},
				{Text: "正文"},
				{Thought: true, FunctionCall: &geminiFunctionCall{
					Name: "calc", Args: map[string]any{"x": 1},
				}, ThoughtSignature: "ts-1"},
			}},
		}},
	})
	require.NoError(t, err)

	assert.Equal(t, "正文", parsed.Text)
	assert.Equal(t, "思考中", parsed.Reasoning)
	assert.Equal(t, "tool_calls", parsed.FinishReason)
	require.Len(t, parsed.ToolCalls, 1, "thought part 上的 functionCall 不得被跳过")
	assert.Equal(t, "calc", parsed.ToolCalls[0].Function.Name)
	assert.Equal(t, "ts-1", parsed.ToolCalls[0].ThoughtSignature, "Gemini 3 的签名必须随工具调用保留")
}

// TestStreamThinkingDelta 覆盖两条原生路径的流式思考增量归位，
// 并确认抽出 delta 处理函数后工具入参增量仍然正常。
func TestStreamThinkingDelta(t *testing.T) {
	t.Parallel()

	t.Run("anthropic thinking_delta", func(t *testing.T) {
		t.Parallel()
		chunk, ok, err := anthropicStreamChunk(
			[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"逐步推理"}}`),
			&anthropicStreamState{})
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "逐步推理", chunk.ReasoningDelta)
		assert.Empty(t, chunk.Delta)
	})

	t.Run("anthropic text_delta 不受影响", func(t *testing.T) {
		t.Parallel()
		chunk, ok, err := anthropicStreamChunk(
			[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"正文"}}`),
			&anthropicStreamState{})
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "正文", chunk.Delta)
		assert.Empty(t, chunk.ReasoningDelta)
	})

	t.Run("anthropic input_json_delta 不受影响", func(t *testing.T) {
		t.Parallel()
		chunk, ok, err := anthropicStreamChunk(
			[]byte(`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"x\":"}}`),
			&anthropicStreamState{})
		require.NoError(t, err)
		require.True(t, ok)
		require.Len(t, chunk.ToolCalls, 1)
		assert.Equal(t, `{"x":`, chunk.ToolCalls[0].Function.Arguments)
		assert.Equal(t, 3, chunk.ToolCalls[0].Index)
	})

	t.Run("gemini 纯思考事件不被跳过", func(t *testing.T) {
		t.Parallel()
		chunk, ok, err := geminiStreamChunk(
			[]byte(`{"candidates":[{"content":{"parts":[{"text":"思考片段","thought":true}]}}]}`),
			&geminiStreamState{})
		require.NoError(t, err, "只带思考摘要的事件不得被当作空事件丢弃")
		require.True(t, ok)
		assert.Equal(t, "思考片段", chunk.ReasoningDelta)
		assert.Empty(t, chunk.Delta)
	})
}

// TestUnsupportedThinkingFieldErrorListsMappedFields 是"错误信息列出该平台已映射字段"
// 的反证：调用方要靠这条信息知道该换用哪个字段，
// 只说"不支持"而不给出路等于把人留在原地。
func TestUnsupportedThinkingFieldErrorListsMappedFields(t *testing.T) {
	t.Parallel()

	enabled := true
	budget := 2048

	tests := []struct {
		name         string
		provider     ProviderName
		thinking     *Thinking
		wantMentions []string
		wantAbsent   []string
	}{
		{
			name:     "OpenAI 不支持 Enabled，应指向 Effort",
			provider: ProviderOpenAI,
			thinking: &Thinking{Enabled: &enabled},
			// 提示里必须出现该平台唯一已映射的字段。
			wantMentions: []string{"Thinking.Enabled", "Thinking.Effort"},
			wantAbsent:   []string{"Thinking.BudgetTokens"},
		},
		{
			name:         "OpenAI 不支持 BudgetTokens，应指向 Effort",
			provider:     ProviderOpenAI,
			thinking:     &Thinking{BudgetTokens: &budget},
			wantMentions: []string{"Thinking.BudgetTokens", "Thinking.Effort"},
		},
		{
			name:         "DeepSeek 不支持 BudgetTokens，应指向 Enabled 与 Effort",
			provider:     ProviderDeepSeek,
			thinking:     &Thinking{BudgetTokens: &budget},
			wantMentions: []string{"Thinking.BudgetTokens", "Thinking.Enabled", "Thinking.Effort"},
		},
		{
			name:         "百炼不支持 Effort，应指向 Enabled 与 BudgetTokens",
			provider:     ProviderQwen,
			thinking:     &Thinking{Effort: ThinkingEffortHigh},
			wantMentions: []string{"Thinking.Effort", "Thinking.Enabled", "Thinking.BudgetTokens"},
		},
		{
			name:         "完全无推理映射的平台说明自身不支持",
			provider:     ProviderQianfan,
			thinking:     &Thinking{Enabled: &enabled},
			wantMentions: []string{"reasoning control", string(ProviderQianfan)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateThinking(tc.provider, tc.thinking)
			require.ErrorIs(t, err, ErrInvalidRequest)
			for _, want := range tc.wantMentions {
				assert.Contains(t, err.Error(), want)
			}
			for _, absent := range tc.wantAbsent {
				assert.NotContains(t, err.Error(), absent,
					"不得把该平台未映射的字段列为可用出路")
			}
		})
	}
}

// TestAssistantMessageCarriesReasoningOutsideContent 锁定推理往返的载体：
// 思考模型在工具循环中要求回传上一轮推理，Reasoning 与签名思考块随 assistant 消息携带，
// 但绝不混入正文——正文回传会把思考当普通文本污染上下文并重复计费。
func TestAssistantMessageCarriesReasoningOutsideContent(t *testing.T) {
	t.Parallel()

	resp := &ChatResponse{
		Content:        "答案是 42",
		Reasoning:      "先分解问题再验算",
		ThinkingBlocks: []ThinkingBlock{{Thinking: "先分解问题再验算", Signature: "sig"}},
		ToolCalls:      []ToolCall{{ID: "t1", Function: FunctionCall{Name: "calc", Arguments: "{}"}, ThoughtSignature: "ts"}},
	}

	msg := resp.AssistantMessage()
	assert.Equal(t, RoleAssistant, msg.Role)
	assert.Equal(t, resp.ToolCalls, msg.ToolCalls, "工具调用（含 Gemini 签名）仍须原样回传")
	assert.Equal(t, resp.Reasoning, msg.Reasoning)
	assert.Equal(t, resp.ThinkingBlocks, msg.ThinkingBlocks)

	text := contentText(msg.Content)
	assert.Contains(t, text, "答案是 42")
	assert.NotContains(t, text, "先分解问题再验算",
		"思考内容不得进入正文，只能走 Reasoning / ThinkingBlocks 字段")

	// 防御性拷贝：修改消息上的块不得影响响应。
	msg.ThinkingBlocks[0].Signature = "tampered"
	assert.Equal(t, "sig", resp.ThinkingBlocks[0].Signature)
}

// TestAnthropicThinkingBudgetAdaptsDefaultMaxTokens 覆盖"只设思考预算、不设 MaxTokens"
// 这条路径：思考预算需小于 max_tokens，而本库的 max_tokens 有默认值。
// 若沿用默认上限，调用方会收到一个针对自己从未设置过的参数的平台错误。
func TestAnthropicThinkingBudgetAdaptsDefaultMaxTokens(t *testing.T) {
	t.Parallel()

	p := &anthropicProvider{model: "claude-x"}

	t.Run("预算超过默认上限时自动抬高 max_tokens", func(t *testing.T) {
		t.Parallel()

		budget := defaultNativeMaxTokens * 2
		req, _, err := p.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{BudgetTokens: &budget},
		}, false)
		require.NoError(t, err)
		require.NotNil(t, req.Thinking)
		assert.Equal(t, budget, req.Thinking.BudgetTokens)
		assert.Greater(t, req.MaxTokens, budget,
			"max_tokens 必须大于思考预算，否则平台会拒绝这个调用方没设置过的参数")
	})

	t.Run("预算小于默认上限时保持默认值", func(t *testing.T) {
		t.Parallel()

		budget := 1024
		req, _, err := p.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{BudgetTokens: &budget},
		}, false)
		require.NoError(t, err)
		assert.Equal(t, defaultNativeMaxTokens, req.MaxTokens)
	})

	t.Run("显式设置的 MaxTokens 一律尊重，不被改写", func(t *testing.T) {
		t.Parallel()

		budget := 8000
		req, _, err := p.buildRequest(&ChatRequest{
			Messages:  []Message{UserText("hi")},
			MaxTokens: 5000, // 小于预算：调用方显式配置的冲突，交由平台裁决
			Thinking:  &Thinking{BudgetTokens: &budget},
		}, false)
		require.NoError(t, err)
		assert.Equal(t, 5000, req.MaxTokens, "不得覆盖调用方显式设置的 MaxTokens")
	})

	t.Run("关闭思考时不影响 max_tokens", func(t *testing.T) {
		t.Parallel()

		disabled := false
		req, _, err := p.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{Enabled: &disabled},
		}, false)
		require.NoError(t, err)
		assert.Equal(t, defaultNativeMaxTokens, req.MaxTokens)
	})
}

// TestSupportsReasoningEffortUnlocksCustomProvider 覆盖自定义接入平台的推理控制。
//
// 库对未收录的平台默认拒绝全部 Thinking 字段（不静默丢弃调用方意图），
// 但这会把用 NewProvider 接入的 OpenAI 兼容平台、自建推理服务一并堵死——
// 它们中不少直接接受 OpenAI 标准的 reasoning_effort。
// ProviderConfig.SupportsReasoningEffort 是知情调用方的解锁开关。
func TestSupportsReasoningEffortUnlocksCustomProvider(t *testing.T) {
	t.Parallel()

	const custom ProviderName = "my-vllm"

	t.Run("未声明时 Effort 被拒并指出声明入口", func(t *testing.T) {
		t.Parallel()

		p := &openaiProvider{name: custom, model: "Qwen3-235B"}
		_, err := p.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{Effort: ThinkingEffortHigh},
		})
		require.ErrorIs(t, err, ErrInvalidRequest)
		assert.Contains(t, err.Error(), "SupportsReasoningEffort",
			"错误信息需给出解除限制的路径，否则调用方无从下手")
	})

	t.Run("声明后 Effort 映射到 reasoning_effort", func(t *testing.T) {
		t.Parallel()

		p := &openaiProvider{name: custom, model: "Qwen3-235B", supportsReasoningEffort: true}
		req, err := p.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{Effort: ThinkingEffortHigh},
		})
		require.NoError(t, err)
		assert.Equal(t, "high", req.ReasoningEffort)
	})

	t.Run("声明只解锁 Effort，不解锁无通用映射的字段", func(t *testing.T) {
		t.Parallel()

		enabled := true
		budget := 2048
		p := &openaiProvider{name: custom, model: "Qwen3-235B", supportsReasoningEffort: true}

		for name, thinking := range map[string]*Thinking{
			"Enabled":      {Enabled: &enabled},
			"BudgetTokens": {BudgetTokens: &budget},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				_, err := p.buildRequest(&ChatRequest{
					Messages: []Message{UserText("hi")},
					Thinking: thinking,
				})
				require.ErrorIs(t, err, ErrInvalidRequest,
					"各平台把该字段落在互不相同的私有字段上，库无从代为映射")
			})
		}
	})

	t.Run("内置预设优先于声明", func(t *testing.T) {
		t.Parallel()

		// 百炼内置映射不支持 Effort；对它声明 SupportsReasoningEffort
		// 不应生效——内置平台的映射由库判定，不交给调用方覆盖。
		p := &openaiProvider{name: ProviderQwen, model: "qwen3.7-plus", supportsReasoningEffort: true}
		_, err := p.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{Effort: ThinkingEffortHigh},
		})
		require.ErrorIs(t, err, ErrInvalidRequest)

		// 反向：内置支持 Effort 的平台，不声明也照常可用。
		p2 := &openaiProvider{name: ProviderOpenAI, model: "gpt-5"}
		req, err := p2.buildRequest(&ChatRequest{
			Messages: []Message{UserText("hi")},
			Thinking: &Thinking{Effort: ThinkingEffortHigh},
		})
		require.NoError(t, err)
		assert.Equal(t, "high", req.ReasoningEffort)
	})
}

// TestNewProviderCarriesReasoningEffortDeclaration 确认声明经由 NewProvider
// 真的落到请求上——只测 buildRequest 会漏掉构造函数没传递字段这类错误。
func TestNewProviderCarriesReasoningEffortDeclaration(t *testing.T) {
	t.Parallel()

	p, err := NewProvider(ProviderConfig{
		Name:                    "my-vllm",
		BaseURL:                 "http://127.0.0.1:8080/v1",
		APIKey:                  "no-key-needed",
		Model:                   "Qwen3-235B",
		SupportsReasoningEffort: true,
	})
	require.NoError(t, err)

	op, ok := p.(*openaiProvider)
	require.True(t, ok)
	req, err := op.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hi")},
		Thinking: &Thinking{Effort: ThinkingEffortLow},
	})
	require.NoError(t, err)
	assert.Equal(t, "low", req.ReasoningEffort)
}
