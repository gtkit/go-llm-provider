package provider

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestThinkingDeepSeekEnabled(t *testing.T) {
	t.Parallel()

	enabled := true
	p := &openaiProvider{name: ProviderDeepSeek, model: "deepseek-chat"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: &enabled},
	})
	require.NoError(t, err)
	require.NotNil(t, req.ChatTemplateKwargs)
	assert.Equal(t, true, req.ChatTemplateKwargs["enable_thinking"])
}

func TestBuildRequestThinkingDeepSeekDisabled(t *testing.T) {
	t.Parallel()

	enabled := false
	p := &openaiProvider{name: ProviderDeepSeek, model: "deepseek-chat"}

	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		Thinking: &Thinking{Enabled: &enabled},
	})
	require.NoError(t, err)
	require.NotNil(t, req.ChatTemplateKwargs)
	assert.Equal(t, false, req.ChatTemplateKwargs["enable_thinking"])
}

func TestBuildRequestThinkingOpenAIEffort(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{name: ProviderOpenAI, model: "o4-mini"}

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
	p := &openaiProvider{name: ProviderQwen, model: "qwen-plus"}

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
	// DeepSeek 只映射 Enabled；BudgetTokens 无映射，必须报错而非静默丢弃。
	p := &openaiProvider{name: ProviderDeepSeek, model: "deepseek-chat"}

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
				name:     "仅 Enabled=true 之外都未设置时不下发",
				thinking: &Thinking{},
				want:     nil,
			},
			{
				name:     "开启并给出预算",
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
				name:     "开启但缺预算",
				thinking: &Thinking{Enabled: &enabled},
			},
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

// TestAnthropicBuildRequestThinking 确认 thinking 参数真的进入请求体，
// 且 Effort 在 Anthropic 上被拒（该平台无对应参数）。
func TestAnthropicBuildRequestThinking(t *testing.T) {
	t.Parallel()

	p := &anthropicProvider{model: "claude-x"}
	budget := 2048

	req, _, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hi")},
		Thinking: &Thinking{BudgetTokens: &budget},
	}, false)
	require.NoError(t, err)
	require.NotNil(t, req.Thinking)
	assert.Equal(t, "enabled", req.Thinking.Type)
	assert.Equal(t, 2048, req.Thinking.BudgetTokens)

	_, _, err = p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hi")},
		Thinking: &Thinking{Effort: ThinkingEffortHigh},
	}, false)
	require.ErrorIs(t, err, ErrInvalidRequest)
}

// TestGeminiThinkingParam 覆盖 Gemini thinkingConfig 的映射。
// 未给预算时借用平台语义取值：开启用 -1（模型自决）、关闭用 0（禁用）。
func TestGeminiThinkingParam(t *testing.T) {
	t.Parallel()

	enabled, disabled := true, false
	budget, zero := 2048, 0

	tests := []struct {
		name     string
		thinking *Thinking
		// wantNil 为 false 时校验 wantBudget，为 true 时要求不下发 thinkingConfig。
		wantBudget int
		wantNil    bool
	}{
		{name: "nil 不下发", thinking: nil, wantNil: true},
		{name: "全未设置不下发", thinking: &Thinking{}, wantNil: true},
		{name: "开启未给预算：动态", thinking: &Thinking{Enabled: &enabled}, wantBudget: -1},
		{name: "关闭：禁用", thinking: &Thinking{Enabled: &disabled}, wantBudget: 0},
		{name: "显式预算", thinking: &Thinking{BudgetTokens: &budget}, wantBudget: 2048},
		{name: "预算 0 即禁用", thinking: &Thinking{BudgetTokens: &zero}, wantBudget: 0},
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
			require.NotNil(t, got.ThinkingBudget)
			assert.Equal(t, tc.wantBudget, *got.ThinkingBudget)
		})
	}

	t.Run("关闭却给预算被拒", func(t *testing.T) {
		t.Parallel()
		_, err := geminiThinkingParam(&Thinking{Enabled: &disabled, BudgetTokens: &budget})
		require.ErrorIs(t, err, ErrInvalidRequest)
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
// redacted_thinking 是加密块，其内容不得出现在任何输出字段里。
func TestAnthropicResponseThinkingBlock(t *testing.T) {
	t.Parallel()

	parsed, err := anthropicResponseContent(anthropicResponse{
		StopReason: "end_turn",
		Content: []anthropicContentPart{
			{Type: "thinking", Thinking: "让我想想"},
			{Type: "redacted_thinking", Text: "ENCRYPTED-MUST-NOT-LEAK"},
			{Type: "text", Text: "答案是 42"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "答案是 42", parsed.Text)
	assert.Equal(t, "让我想想", parsed.Reasoning)
	assert.NotContains(t, parsed.Text, "ENCRYPTED-MUST-NOT-LEAK")
	assert.NotContains(t, parsed.Reasoning, "ENCRYPTED-MUST-NOT-LEAK")
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
				}},
			}},
		}},
	})
	require.NoError(t, err)

	assert.Equal(t, "正文", parsed.Text)
	assert.Equal(t, "思考中", parsed.Reasoning)
	assert.Equal(t, "tool_calls", parsed.FinishReason)
	require.Len(t, parsed.ToolCalls, 1, "thought part 上的 functionCall 不得被跳过")
	assert.Equal(t, "calc", parsed.ToolCalls[0].Function.Name)
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
			name:         "DeepSeek 不支持 Effort，应指向 Enabled",
			provider:     ProviderDeepSeek,
			thinking:     &Thinking{Effort: ThinkingEffortHigh},
			wantMentions: []string{"Thinking.Effort", "Thinking.Enabled"},
			wantAbsent:   []string{"Thinking.BudgetTokens"},
		},
		{
			name:         "Anthropic 不支持 Effort，应指向 Enabled 与 BudgetTokens",
			provider:     ProviderAnthropic,
			thinking:     &Thinking{Effort: ThinkingEffortHigh},
			wantMentions: []string{"Thinking.Effort", "Thinking.Enabled", "Thinking.BudgetTokens"},
		},
		{
			name:         "完全无推理映射的平台说明自身不支持",
			provider:     ProviderQwen,
			thinking:     &Thinking{Enabled: &enabled},
			wantMentions: []string{"reasoning control", string(ProviderQwen)},
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

// TestAssistantMessageExcludesReasoning 是"思考内容不参与后续轮次消息构建"的反证。
//
// 若把 Reasoning 并进 AssistantMessage，多轮对话每轮都会把思考内容当普通文本
// 回传给平台：既污染上下文，又按输入 token 重复计费。Anthropic 的思考块还要求
// 带 signature 原样回传，退化成纯文本反而可能被平台拒绝。
func TestAssistantMessageExcludesReasoning(t *testing.T) {
	t.Parallel()

	resp := &ChatResponse{
		Content:   "答案是 42",
		Reasoning: "先分解问题再验算",
		ToolCalls: []ToolCall{{ID: "t1", Function: FunctionCall{Name: "calc", Arguments: "{}"}}},
	}

	msg := resp.AssistantMessage()
	assert.Equal(t, RoleAssistant, msg.Role)
	assert.Equal(t, resp.ToolCalls, msg.ToolCalls, "工具调用仍须原样回传")

	text := contentText(msg.Content)
	assert.Contains(t, text, "答案是 42")
	assert.NotContains(t, text, "先分解问题再验算",
		"思考内容不得进入回传给平台的 assistant 消息")
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

		// DeepSeek 内置映射只支持 Enabled；对它声明 SupportsReasoningEffort
		// 不应生效——内置平台的映射由库判定，不交给调用方覆盖。
		p := &openaiProvider{name: ProviderDeepSeek, model: "deepseek-chat", supportsReasoningEffort: true}
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
