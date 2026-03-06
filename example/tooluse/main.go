package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gtkit/go-llm-provider/provider"
)

// ============================================================
// 示例：使用 Tool Use 让模型调用"查天气"和"查汇率"两个工具
// ============================================================

func main() {
	p, err := provider.NewProviderFromPreset(
		provider.ProviderDeepSeek,
		os.Getenv("DEEPSEEK_API_KEY"),
		"", // 使用默认模型
	)
	if err != nil {
		fmt.Printf("创建 provider 失败: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// ---- 第一步：定义工具 ----
	tools := []provider.Tool{
		{
			Function: provider.FunctionDef{
				Name:        "get_weather",
				Description: "获取指定城市的当前天气信息",
				Parameters: provider.ParamSchema{
					Type: "object",
					Properties: map[string]provider.ParamSchema{
						"city": {
							Type:        "string",
							Description: "城市名称，如 '北京'、'上海'",
						},
						"unit": {
							Type:        "string",
							Description: "温度单位",
							Enum:        []string{"celsius", "fahrenheit"},
						},
					},
					Required: []string{"city"},
				},
			},
		},
		{
			Function: provider.FunctionDef{
				Name:        "get_exchange_rate",
				Description: "查询两种货币之间的实时汇率",
				Parameters: provider.ParamSchema{
					Type: "object",
					Properties: map[string]provider.ParamSchema{
						"from_currency": {
							Type:        "string",
							Description: "源货币代码，如 'USD'、'CNY'",
						},
						"to_currency": {
							Type:        "string",
							Description: "目标货币代码，如 'USD'、'CNY'",
						},
					},
					Required: []string{"from_currency", "to_currency"},
				},
			},
		},
	}

	// ---- 第二步：发起对话（模型可能触发 tool call）----
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "你是一个有用的助手，可以查询天气和汇率。"},
		{Role: provider.RoleUser, Content: "北京今天天气怎么样？顺便帮我查一下美元兑人民币的汇率。"},
	}

	fmt.Println("=== 第一轮：发送用户消息 ===")
	resp, err := p.Chat(ctx, &provider.ChatRequest{
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		fmt.Printf("调用失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("FinishReason: %s\n", resp.FinishReason)
	fmt.Printf("HasToolCalls: %v\n", resp.HasToolCalls())

	if !resp.HasToolCalls() {
		// 模型直接回复了，没有调用工具
		fmt.Printf("模型直接回复: %s\n", resp.Content)
		return
	}

	// ---- 第三步：执行工具调用 ----
	fmt.Printf("\n模型请求调用 %d 个工具:\n", len(resp.ToolCalls))

	// 将模型的 tool_calls 响应追加到对话历史
	messages = append(messages, resp.AssistantMessage())

	for _, tc := range resp.ToolCalls {
		fmt.Printf("  - %s(%s)\n", tc.Function.Name, tc.Function.Arguments)

		// 分发到对应的工具函数
		result := dispatchTool(tc.Function.Name, tc.Function.Arguments)
		fmt.Printf("    结果: %s\n", result)

		// 将工具执行结果追加到对话历史
		messages = append(messages, provider.ToolResultMessage(tc.ID, result))
	}

	// ---- 第四步：将工具结果回传给模型，获取最终回复 ----
	fmt.Println("\n=== 第二轮：回传工具结果 ===")
	finalResp, err := p.Chat(ctx, &provider.ChatRequest{
		Messages: messages,
		Tools:    tools, // 仍然传入 tools，模型可能需要再次调用
	})
	if err != nil {
		fmt.Printf("第二轮调用失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("FinishReason: %s\n", finalResp.FinishReason)
	fmt.Printf("最终回复:\n%s\n", finalResp.Content)
	fmt.Printf("Token 消耗: prompt=%d, completion=%d, total=%d\n",
		finalResp.Usage.PromptTokens, finalResp.Usage.CompletionTokens, finalResp.Usage.TotalTokens)
}

// ============================================================
// 工具实现（模拟）
// ============================================================

// dispatchTool 根据函数名分发到对应的工具实现。
// 生产环境中这里会调用真实的 API。
func dispatchTool(name, arguments string) string {
	switch name {
	case "get_weather":
		return handleGetWeather(arguments)
	case "get_exchange_rate":
		return handleGetExchangeRate(arguments)
	default:
		return fmt.Sprintf(`{"error": "unknown tool: %s"}`, name)
	}
}

func handleGetWeather(arguments string) string {
	var args struct {
		City string `json:"city"`
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return fmt.Sprintf(`{"error": "%s"}`, err.Error())
	}

	if args.Unit == "" {
		args.Unit = "celsius"
	}

	// 模拟返回天气数据
	result := map[string]any{
		"city":        args.City,
		"temperature": 28,
		"unit":        args.Unit,
		"condition":   "晴",
		"humidity":    45,
		"wind":        "东南风 3-4级",
	}
	data, _ := json.Marshal(result)
	return string(data)
}

func handleGetExchangeRate(arguments string) string {
	var args struct {
		FromCurrency string `json:"from_currency"`
		ToCurrency   string `json:"to_currency"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return fmt.Sprintf(`{"error": "%s"}`, err.Error())
	}

	// 模拟返回汇率数据
	result := map[string]any{
		"from":       args.FromCurrency,
		"to":         args.ToCurrency,
		"rate":       7.2450,
		"updated_at": "2025-07-01T10:00:00Z",
	}
	data, _ := json.Marshal(result)
	return string(data)
}
