package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 本文件是 ROADMAP.md「待真实 key 验证」清单的可执行版本：每条对应一个协议细节，
// 库按官方文档实现但尚未用真实平台跑过。设置对应环境变量即运行，缺 key 时跳过。
// 编号与 ROADMAP.md 一致，验证通过后在那里勾掉。

const smokeToolLoopTimeout = 3 * time.Minute

// smokeToolLoop 跑一轮"思考模型 + 工具调用"的完整循环：能走到第二轮并给出最终回复，
// 即证明推理内容往返被平台接受（DeepSeek 缺 reasoning_content 会 400，Anthropic 缺
// thinking 块会 400，Responses API 缺 reasoning 项会丢上下文）。
func smokeToolLoop(t *testing.T, p Provider, thinking *Thinking) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), smokeToolLoopTimeout)
	defer cancel()

	var rounds int
	counting := &stubProvider{
		name: p.Name(),
		chat: func(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
			rounds++
			return p.Chat(ctx, req)
		},
	}
	resp, err := RunToolLoop(ctx, counting, &ChatRequest{
		Messages: []Message{
			SystemText("你必须先调用 get_time 工具拿到当前时间，再用一句话回答。"),
			UserText("现在几点？"),
		},
		Tools: []Tool{{Function: FunctionDef{
			Name:        "get_time",
			Description: "返回当前时间",
			Parameters:  ParamSchema{Type: "object", Properties: map[string]ParamSchema{}},
		}}},
		Thinking:  thinking,
		MaxTokens: 512,
	}, 4, func(context.Context, string, string) (string, error) {
		return `{"time":"2026-09-20T10:00:00+08:00"}`, nil
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Content)
	assert.GreaterOrEqual(t, rounds, 2, "应至少经历一次工具调用回传")
}

// V-01：Anthropic 原生结构化输出接受无属性的对象 schema（JSONObjectFormat 的落点）。
func TestVerifyAnthropicJSONObjectFormatSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("set ANTHROPIC_API_KEY to run V-01")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	p, err := NewProviderFromPreset(ProviderAnthropic, apiKey, "")
	require.NoError(t, err)
	resp, err := p.Chat(ctx, &ChatRequest{
		Messages:       []Message{UserText("用 JSON 对象返回 city=Paris 与 country=France")},
		ResponseFormat: JSONObjectFormat(),
		MaxTokens:      256,
	})
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &out), "content=%q", resp.Content)
}

// V-02：Gemini 2.5 系接受 responseJsonSchema（3.x 已确认）。
func TestVerifyGemini25ResponseJSONSchemaSmoke(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("set GEMINI_API_KEY to run V-02")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	p, err := NewProviderFromPreset(ProviderGemini, apiKey, "gemini-2.5-flash")
	require.NoError(t, err)
	type city struct {
		City string `json:"city"`
	}
	out, _, err := GenerateJSON[city](ctx, p, &ChatRequest{
		Messages: []Message{UserText("返回巴黎所在城市字段 city")},
		ResponseFormat: JSONSchemaFormat("city", ParamSchema{
			Type: "object", Properties: map[string]ParamSchema{"city": {Type: "string"}}, Required: []string{"city"},
		}),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.City)
}

// V-03：DeepSeek V4 思考开关 + 档位 + 工具循环回传 reasoning_content 全链路。
func TestVerifyDeepSeekThinkingToolLoopSmoke(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run V-03")
	}
	p, err := NewProviderFromPreset(ProviderDeepSeek, apiKey, "")
	require.NoError(t, err)
	smokeToolLoop(t, p, &Thinking{Enabled: new(true), Effort: ThinkingEffortLow})
}

// V-04：智谱 GLM-5.3 与 Kimi K2.6 对 reasoning_content 回传的接受情况（默认开启思考）。
func TestVerifyZhipuReasoningEchoSmoke(t *testing.T) {
	apiKey := os.Getenv("ZHIPU_API_KEY")
	if apiKey == "" {
		t.Skip("set ZHIPU_API_KEY to run V-04（智谱）")
	}
	p, err := NewProviderFromPreset(ProviderZhipu, apiKey, "")
	require.NoError(t, err)
	smokeToolLoop(t, p, &Thinking{Effort: ThinkingEffortLow})
}

func TestVerifyMoonshotReasoningEchoSmoke(t *testing.T) {
	apiKey := os.Getenv("MOONSHOT_API_KEY")
	if apiKey == "" {
		t.Skip("set MOONSHOT_API_KEY to run V-04（Kimi）")
	}
	p, err := NewProviderFromPreset(ProviderMoonshot, apiKey, "")
	require.NoError(t, err)
	smokeToolLoop(t, p, &Thinking{Enabled: new(true)})
}

// V-05：Claude 5 上 adaptive + output_config.effort 的组合，以及带签名思考块的工具循环回放。
func TestVerifyAnthropicAdaptiveThinkingToolLoopSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("set ANTHROPIC_API_KEY to run V-05")
	}
	p, err := NewProviderFromPreset(ProviderAnthropic, apiKey, "")
	require.NoError(t, err)
	smokeToolLoop(t, p, &Thinking{Enabled: new(true), Effort: ThinkingEffortLow})
}

// V-06：Responses API 在 store:false 下以 id + encrypted_content 回放推理项的工具循环。
func TestVerifyResponsesReasoningReplaySmoke(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("set OPENAI_API_KEY to run V-06")
	}
	p, err := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: apiKey, Model: "gpt-5.6-terra"})
	require.NoError(t, err)
	smokeToolLoop(t, p, &Thinking{Effort: ThinkingEffortLow})
}
