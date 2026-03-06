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
// 示例：使用 RunToolLoop 自动循环执行 Tool Use
// 比手动管理多轮对话更简洁，适合 Agent 场景
// ============================================================

func main() {
	p, err := provider.NewProviderFromPreset(
		provider.ProviderDeepSeek,
		os.Getenv("DEEPSEEK_API_KEY"),
		"",
	)
	if err != nil {
		fmt.Printf("创建 provider 失败: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 定义工具
	tools := []provider.Tool{
		{
			Function: provider.FunctionDef{
				Name:        "calculate",
				Description: "执行数学计算，支持加减乘除和常见数学运算",
				Parameters: provider.ParamSchema{
					Type: "object",
					Properties: map[string]provider.ParamSchema{
						"expression": {
							Type:        "string",
							Description: "数学表达式，如 '2 + 3 * 4'、'sqrt(16)'",
						},
					},
					Required: []string{"expression"},
				},
			},
		},
		{
			Function: provider.FunctionDef{
				Name:        "get_current_time",
				Description: "获取当前时间",
				Parameters: provider.ParamSchema{
					Type:       "object",
					Properties: map[string]provider.ParamSchema{},
				},
			},
		},
	}

	// 一行搞定：自动循环执行 tool call，直到模型给出最终回复
	resp, err := provider.RunToolLoop(ctx, p, &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "你是一个有用的助手，可以进行数学计算和查询时间。"},
			{Role: provider.RoleUser, Content: "现在几点了？另外帮我算一下 (15 + 27) * 3 等于多少。"},
		},
		Tools: tools,
	}, 5, func(ctx context.Context, name, arguments string) (string, error) {
		// 工具分发
		switch name {
		case "calculate":
			var args struct {
				Expression string `json:"expression"`
			}
			json.Unmarshal([]byte(arguments), &args)
			fmt.Printf("[tool] calculate(%s)\n", args.Expression)
			// 模拟计算结果
			return `{"result": 126, "expression": "(15 + 27) * 3"}`, nil

		case "get_current_time":
			fmt.Println("[tool] get_current_time()")
			now := time.Now().Format("2006-01-02 15:04:05")
			return fmt.Sprintf(`{"time": "%s", "timezone": "Asia/Shanghai"}`, now), nil

		default:
			return "", fmt.Errorf("unknown tool: %s", name)
		}
	})

	if err != nil {
		fmt.Printf("调用失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n最终回复:\n%s\n", resp.Content)
}
