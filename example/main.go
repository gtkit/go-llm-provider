package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/gtkit/go-llm-provider/provider"
)

func main() {
	// ============================================================
	// 方式一：QuickRegistry — 最快上手
	// 通过环境变量读取各平台 API Key，一行创建注册表
	// ============================================================
	reg := provider.QuickRegistry(map[provider.ProviderName]string{
		provider.ProviderDeepSeek:    os.Getenv("DEEPSEEK_API_KEY"),    // deepseek
		provider.ProviderQwen:        os.Getenv("QWEN_API_KEY"),        // qwen
		provider.ProviderZhipu:       os.Getenv("ZHIPU_API_KEY"),       // zhipu
		provider.ProviderSiliconFlow: os.Getenv("SILICONFLOW_API_KEY"), // siliconflow
		provider.ProviderMoonshot:    os.Getenv("MOONSHOT_API_KEY"),    // moonshot
		provider.ProviderQianfan:     os.Getenv("QIANFAN_API_KEY"),     // qianfan，千帆官方文档：https://github.com/baidubce/bce-qianfan-sdk
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ---- 使用默认 provider（第一个注册的）----
	//p, err := reg.Default()
	//if err != nil {
	//	fmt.Println("没有可用的 provider，请检查环境变量")
	//	os.Exit(1)
	//}
	//fmt.Printf("使用默认 provider: %s\n\n", p.Name())

	// ---- 非流式：SimpleChat 一问一答 ----
	//fmt.Println("=== 非流式调用 ===")
	//reply, err := provider.SimpleChat(ctx, p, "用一句话介绍 Go 语言")
	//if err != nil {
	//	fmt.Printf("调用失败: %v\n", err)
	//	os.Exit(1)
	//}
	//fmt.Printf("回复: %s\n\n", reply)

	// ---- 流式：实时打印 ----
	//fmt.Println("=== 流式调用 ===")
	//stream, err := p.ChatStream(ctx, &provider.ChatRequest{
	//	Messages: []provider.Message{
	//		{Role: provider.RoleSystem, Content: "你是一个 Go 语言专家"},
	//		{Role: provider.RoleUser, Content: "简述 Go 的 goroutine 调度模型"},
	//	},
	//	MaxTokens: 512,
	//})
	//if err != nil {
	//	fmt.Printf("流式调用失败: %v\n", err)
	//	os.Exit(1)
	//}
	//defer stream.Close()
	//
	//for {
	//	chunk, err := stream.Recv()
	//	if err != nil {
	//		if err == io.EOF {
	//			break
	//		}
	//		fmt.Printf("\n流式错误: %v\n", err)
	//		break
	//	}
	//	fmt.Print(chunk.Delta)
	//}
	//fmt.Println()
	//select {}

	// ============================================================
	// 方式二：按名称切换 provider
	// ============================================================
	//fmt.Println("\n=== 切换到智谱 ===")
	zhipu, err := reg.Get(provider.ProviderZhipu)
	if err != nil {
		fmt.Println("智谱未注册，跳过")
	} else {
		//fmt.Println("=== 非流式调用 ===")
		//reply, err := provider.SimpleChatWithSystem(
		//	ctx, zhipu,
		//	"你是智谱 AI 助手",
		//	"你好，介绍一下你自己",
		//)
		//if err != nil {
		//	fmt.Printf("智谱调用失败: %v\n", err)
		//} else {
		//	fmt.Printf("智谱回复: %s\n", reply)
		//}

		fmt.Println("=== 流式调用 ===")
		stream, err := zhipu.ChatStream(ctx, &provider.ChatRequest{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "你是智谱 AI 助手"},
				{Role: provider.RoleUser, Content: "详细介绍一下智谱 AI 产品 怎么使用skills，可以兼容 claude code 风格 skills 吗"},
			},
			MaxTokens: 512,
		})
		if err != nil {
			fmt.Printf("流式调用失败: %v\n", err)
			os.Exit(1)
		}
		defer stream.Close()

		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				fmt.Printf("\n流式错误: %v\n", err)
				break
			}
			fmt.Print(chunk.Delta)
		}
	}

	select {}

	// ============================================================
	// 方式三：手动创建自定义 provider（例如私有部署的模型）
	// ============================================================
	//fmt.Println("\n=== 自定义 provider ===")
	//_ = provider.NewProvider(provider.ProviderConfig{
	//	Name:    "my-private-llm",
	//	BaseURL: "http://192.168.1.100:8080/v1",
	//	APIKey:  "no-key-needed",
	//	Model:   "my-model",
	//})
	//fmt.Println("自定义 provider 创建成功（用于私有部署场景）")

	// ============================================================
	// 方式四：CollectStream — 流式收集 + 实时回调
	// ============================================================
	//fmt.Println("\n=== CollectStream 带回调 ===")
	//full, err := provider.CollectStream(ctx, p, &provider.ChatRequest{
	//	Messages: []provider.Message{
	//		{Role: provider.RoleUser, Content: "1+1等于几？"},
	//	},
	//}, func(delta string) {
	//	fmt.Print(delta) // 实时打印
	//})
	//if err != nil {
	//	fmt.Printf("\nCollectStream 错误: %v\n", err)
	//} else {
	//	fmt.Printf("\n完整回复长度: %d 字符\n", len(full))
	//}
}
