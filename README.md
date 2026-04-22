# llm-provider

Go 语言统一多模型 LLM 调用库。一套代码接入 OpenAI 以及 DeepSeek、通义千问、智谱、百度千帆、硅基流动、Moonshot 等 OpenAI 兼容平台。

## 为什么做这个

国内主流大模型平台现在都兼容了 OpenAI Chat Completions 协议，本质上只是 BaseURL 和 APIKey 的差异。但每次接入新平台还是要翻文档查地址、记模型名、写一堆重复的初始化代码。

这个库做的事情很简单：

- 预置了各平台的 BaseURL 和推荐模型，传个 APIKey 就能用
- 统一的 `Provider` 接口，业务代码不需要关心底层是哪个平台
- `Registry` 注册表管理多个 Provider，运行时按名称切换
- 支持非流式和流式两种调用模式
- 完整的 Tool Use / Function Calling 支持，包含自动循环执行的 `RunToolLoop`
- 主包保持轻量，零额外厂商 SDK 依赖，底层只用 `sashabaranov/go-openai`

## 项目结构

```
llm-provider/
├── go.mod
├── README.md
├── CHANGELOG.md
├── provider/
│   ├── provider.go            # 核心：Provider 接口、Registry、请求/响应、Tool Use 类型
│   ├── content.go             # Message 多模态 ContentPart 与便捷构造器
│   ├── presets.go             # 各平台预设配置（BaseURL + Chat/Embedding 默认模型）
│   ├── helpers.go             # Chat 便捷函数：SimpleChat、CollectStream
│   ├── toolrun.go             # RunToolLoop：Tool Use 自动循环执行器
│   ├── embedder.go            # Embedder 接口、请求/响应、openaiEmbedder 实现
│   ├── embedder_helpers.go    # Embedding 便捷函数：SimpleEmbed、EmbedBatch
│   ├── errors.go              # ProviderError / ErrorCode / WrapProviderError
│   ├── middleware.go          # Middleware / Handler 类型 + WithMiddlewares 装饰器
│   ├── provider_test.go       # Chat / Tool Use 单测
│   ├── embedder_test.go       # Embedding 单测
│   ├── content_test.go        # ContentPart 构造器与映射测试
│   ├── errors_test.go         # ProviderError / ErrorCode / WrapProviderError 单测
│   ├── middleware_test.go     # Middleware 装饰器 + 洋葱顺序测试
│   └── runtime_test.go        # 运行时集成测试
└── example/
    ├── main.go                # 基础使用示例（Chat）
    ├── vision/main.go         # Vision 多模态输入示例（text + image）
    ├── tooluse/main.go        # Tool Use 手动多轮示例
    ├── toolloop/main.go       # RunToolLoop 自动循环示例
    ├── middleware/main.go     # Middleware：Logging / TokenStats / Retry 参考实现
    └── embedding/main.go      # Embedding + RAG 最小闭环示例
```

## 安装

```bash
go get github.com/gtkit/go-llm-provider/v2
```

> 将 `github.com/gtkit/go-llm-provider/v2` 替换为你实际的模块路径。

## 支持的平台

| 平台 | ProviderName | 预设 BaseURL | 默认 Chat 模型 | 默认 Embedding 模型 | API Key 获取 |
|------|-------------|-------------|---------|---------|-------------|
| DeepSeek | `deepseek` | `https://api.deepseek.com/v1` | `deepseek-chat` | — | [platform.deepseek.com](https://platform.deepseek.com/) |
| 通义千问（百炼） | `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-plus` | `text-embedding-v3` | [百炼控制台](https://bailian.console.aliyun.com/) |
| 智谱 AI | `zhipu` | `https://open.bigmodel.cn/api/paas/v4/` | `glm-4-plus` | `embedding-3` | [open.bigmodel.cn](https://open.bigmodel.cn/) |
| 百度千帆 | `qianfan` | `https://qianfan.baidubce.com/v2` | `ernie-4.0-8k` | `embedding-v1` | [千帆控制台](https://console.bce.baidu.com/qianfan/) |
| 硅基流动 | `siliconflow` | `https://api.siliconflow.cn/v1` | `deepseek-ai/DeepSeek-V3` | `BAAI/bge-m3` | [siliconflow.cn](https://siliconflow.cn/) |
| Moonshot / Kimi | `moonshot` | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` | — | [platform.moonshot.cn](https://platform.moonshot.cn/) |
| OpenAI | `openai` | `https://api.openai.com/v1` | `gpt-4.1-mini` | `text-embedding-3-small` | [platform.openai.com](https://platform.openai.com/) |

> 预设地址和默认模型可能随平台更新而变化，建议定期对照各平台官方文档确认。
> Embedding 列显示"—"的平台表示官方暂无 embedding 接口，`NewEmbedderFromPreset` 会返回错误。

### 关于 Claude / Google Gemini

主包不直接内置 Claude 和 Google Gemini 的官方实现。

原因是这两家接口不是 OpenAI 兼容协议，如果把官方 SDK 直接塞进主包，会让当前这个库失去“轻量统一接入层”的定位。当前状态是：

- 主包继续内置 OpenAI 及 OpenAI 兼容平台
- 已经为 Claude / Gemini 这类非兼容协议预留了可选扩展包接入点
- 扩展包可以复用主包的 `Provider`、`ChatRequest`、`ChatResponse`、`Registry`、`RunToolLoop`
- 主包新增了可供外部 provider 复用的 `provider.NewStreamReader(...)`
- 当前仓库里还没有现成可直接 `import` 的 Claude / Gemini 扩展包实现

## 快速开始

### 30 秒上手

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/gtkit/go-llm-provider/v2/provider"
)

func main() {
    // 一行创建注册表，传入各平台 API Key（空值自动跳过）
    reg := provider.QuickRegistry(map[provider.ProviderName]string{
        provider.ProviderOpenAI:  os.Getenv("OPENAI_API_KEY"),
        provider.ProviderDeepSeek: os.Getenv("DEEPSEEK_API_KEY"),
        provider.ProviderQwen:    os.Getenv("QWEN_API_KEY"),
        provider.ProviderZhipu:   os.Getenv("ZHIPU_API_KEY"),
    })

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // 拿默认 provider，直接对话
    p, _ := reg.Default()
    reply, _ := provider.SimpleChat(ctx, p, "用一句话介绍 Go 语言")
    fmt.Println(reply)
}
```

设置环境变量后运行：

```bash
export OPENAI_API_KEY="sk-xxxxxxxx"
go run main.go
```

## 使用方式

库提供三个层级的创建方式，由简到灵活。

### 升级说明

如果你从旧版本迁移，使用方式有这几处变化：

- `NewProvider` 现在返回 `(Provider, error)`，并在创建时校验 `Name`、`APIKey`、`Model`。
- `StreamReader.Close()` 现在返回 `error`，推荐显式处理，或像示例一样在 `defer` 中忽略。
- `ToolChoice` 不再接受任意 `string/any`，请改用 `provider.ToolChoiceAuto`、`provider.ToolChoiceNone`、`provider.ToolChoiceRequired` 或 `provider.ToolChoiceFunction{...}`。
- `Message.Content` 已从 `string` 升级为 `[]ContentPart`。旧写法 `Message{Role: ..., Content: "..."}` 不再编译，请改用 `provider.UserText(...)`、`provider.SystemText(...)`、`provider.TextPart(...)` 等构造器。
- `EnableThinking` 目前只对 `DeepSeek` 生效；对其他 provider 开启会直接返回错误。
- 新代码优先使用 `provider.AllPresets()` 读取预设；`provider.Presets` 仅为兼容旧代码保留。
- 如果你不希望 `QuickRegistry` 静默跳过失败项，请改用 `QuickRegistryStrict`。

### 方式一：QuickRegistry（推荐日常使用）

传一组 `ProviderName -> APIKey` 的映射，自动使用预设的 BaseURL 和默认模型。空 APIKey 会被自动跳过，不会报错。

```go
reg := provider.QuickRegistry(map[provider.ProviderName]string{
    provider.ProviderOpenAI:      os.Getenv("OPENAI_API_KEY"),
    provider.ProviderDeepSeek:    os.Getenv("DEEPSEEK_API_KEY"),
    provider.ProviderQwen:        os.Getenv("QWEN_API_KEY"),
    provider.ProviderZhipu:       os.Getenv("ZHIPU_API_KEY"),
    provider.ProviderSiliconFlow: os.Getenv("SILICONFLOW_API_KEY"),
    provider.ProviderMoonshot:    os.Getenv("MOONSHOT_API_KEY"),
})

// 默认 provider 按成功注册的 ProviderName 排序后取第一个
p, err := reg.Default()
```

如果你希望注册失败时立刻拿到错误，改用 `QuickRegistryStrict`：

```go
reg, err := provider.QuickRegistryStrict(map[provider.ProviderName]string{
    provider.ProviderOpenAI:   os.Getenv("OPENAI_API_KEY"),
    provider.ProviderDeepSeek: os.Getenv("DEEPSEEK_API_KEY"),
    provider.ProviderQwen:     os.Getenv("QWEN_API_KEY"),
})
if err != nil {
    log.Fatal(err)
}
```

### 方式二：NewProviderFromPreset（指定模型）

使用预设地址，但自定义模型名。适合同一个平台想用不同模型的场景。

```go
// 用千问的 qwen-max 模型而不是默认的 qwen-plus
p, err := provider.NewProviderFromPreset(
    provider.ProviderQwen,
    os.Getenv("QWEN_API_KEY"),
    "qwen-max",  // 留空则使用预设的默认模型
)

// 手动注册到 Registry
reg := provider.NewRegistry()
reg.Register(p)
```

### 方式三：NewProvider（完全自定义）

适合私有部署、自建推理服务、或新平台接入。

```go
p, err := provider.NewProvider(provider.ProviderConfig{
    Name:    "my-vllm",                          // 自定义名称
    BaseURL: "http://192.168.1.100:8080/v1",     // 你的服务地址
    APIKey:  "no-key-needed",                     // 没有鉴权可以随便填
    Model:   "Qwen2.5-72B-Instruct",             // 你部署的模型
})
if err != nil {
    log.Fatal(err)
}
```

## 调用方式

### 非流式对话

#### SimpleChat — 一问一答

最简形式，传入用户消息，返回 assistant 回复文本。

```go
reply, err := provider.SimpleChat(ctx, p, "什么是 goroutine？")
```

#### SimpleChatWithSystem — 带系统提示词

```go
reply, err := provider.SimpleChatWithSystem(ctx, p,
    "你是一个资深 Go 工程师，回答简洁准确",
    "解释一下 context.Context 的作用",
)
```

#### Chat — 完整控制

需要多轮对话、调参数时使用完整的 `Chat` 方法。

```go
temp := float32(0.7)
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Model: "deepseek-reasoner",  // 可选，覆盖默认模型
    Messages: []provider.Message{
        provider.SystemText("你是一个翻译助手"),
        provider.UserText("把下面的话翻译成英文：今天天气真好"),
    },
    MaxTokens:   1024,
    Temperature: &temp,
})

fmt.Println("回复:", resp.Content)
fmt.Printf("Token: prompt=%d, completion=%d, total=%d\n",
    resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
```

### 流式对话

#### 手动读取 StreamReader

逐 chunk 读取，`io.EOF` 表示流结束。调用方负责 `Close()`。

```go
stream, err := p.ChatStream(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserText("写一首关于 Go 的诗"),
    },
})
if err != nil {
    log.Fatal(err)
}
defer func() { _ = stream.Close() }()

for {
    chunk, err := stream.Recv()
    if err != nil {
        if err == io.EOF {
            break
        }
        log.Fatal(err)
    }
    fmt.Print(chunk.Delta)  // 实时打印增量文本
}
```

如果你希望保留 `Close` 错误，也可以显式处理：

```go
if err := stream.Close(); err != nil {
    log.Printf("close stream: %v", err)
}
```

#### CollectStream — 流式收集 + 实时回调

边流式接收边回调，最终返回完整文本。

```go
fullText, err := provider.CollectStream(ctx, p, req, func(delta string) {
    fmt.Print(delta)  // 实时输出到终端
})
// fullText 包含完整的回复文本
```

`onChunk` 参数传 `nil` 则只做收集不回调：

```go
fullText, err := provider.CollectStream(ctx, p, req, nil)
```

## Tool Use / Function Calling

Tool Use 让模型能够调用你定义的外部工具（查天气、查数据库、执行计算等），而不是仅仅生成文本。

### 工作流程

```
用户: "北京天气怎么样？"
  ↓
模型返回: tool_call: get_weather(city="北京")     ← 模型决定要调用工具
  ↓
你的代码执行 get_weather("北京") → {"temp": 28}   ← 你执行工具并拿到结果
  ↓
把结果回传给模型
  ↓
模型回复: "北京现在 28°C，晴天。"                  ← 模型基于工具结果生成回复
```

### 定义工具

使用 `ParamSchema` 构建参数的 JSON Schema：

```go
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
}
```

`ParamSchema` 支持嵌套对象和数组：

```go
provider.ParamSchema{
    Type: "object",
    Properties: map[string]provider.ParamSchema{
        "query": {Type: "string", Description: "搜索关键词"},
        "filters": {
            Type: "object",
            Properties: map[string]provider.ParamSchema{
                "date_from": {Type: "string", Description: "开始日期 YYYY-MM-DD"},
                "date_to":   {Type: "string", Description: "结束日期 YYYY-MM-DD"},
            },
        },
        "tags": {
            Type:  "array",
            Items: &provider.ParamSchema{Type: "string"},
        },
    },
    Required: []string{"query"},
}
```

### 多模态输入（图像）

当模型支持视觉输入时，可以把文本和图片组合成同一条消息。纯文本场景仍然推荐用 `UserText` / `SystemText` 保持最简心智。

```go
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserMessage(
            provider.TextPart("请描述这张图片里的主要内容"),
            provider.ImageURLPart("https://example.com/cat.png"),
        ),
    },
})
if err != nil {
    log.Fatal(err)
}

fmt.Println(resp.Content)
```

如果图片来自本地字节流，用 `ImageDataPart`：

```go
imgBytes, _ := os.ReadFile("cat.png")

resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserMessage(
            provider.TextPart("识别这张图里的文字"),
            provider.ImageDataPart(imgBytes, "image/png"),
        ),
    },
})
```

### 方式一：RunToolLoop（推荐）

`RunToolLoop` 自动处理 Tool Use 的完整循环：发请求 → 检测 tool_calls → 执行工具 → 回传结果 → 再次请求 → ... 直到模型给出最终文本回复。

```go
resp, err := provider.RunToolLoop(ctx, p, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserText("北京天气怎么样？"),
    },
    Tools: tools,
}, 5, func(ctx context.Context, name, arguments string) (string, error) {
    switch name {
    case "get_weather":
        var args struct {
            City string `json:"city"`
        }
        json.Unmarshal([]byte(arguments), &args)
        // 调用真实的天气 API ...
        return `{"temperature": 28, "condition": "晴"}`, nil
    default:
        return "", fmt.Errorf("unknown tool: %s", name)
    }
})

fmt.Println(resp.Content) // "北京现在 28°C，天气晴朗。"
```

参数说明：

- `maxRounds`：最大循环次数（推荐 5-10），防止模型无限调用工具
- `handler`：工具执行函数，接收函数名和 JSON 参数，返回结果字符串
- 如果 handler 返回 error，`RunToolLoop` 默认会把**脱敏后的** JSON 错误结果回传给模型，让模型有机会纠正，但不会默认暴露原始内部错误细节

如果你需要自定义错误回传格式或显式开启并行工具执行，使用 `RunToolLoopWithOptions`：

```go
resp, err := provider.RunToolLoopWithOptions(
    ctx,
    p,
    req,
    handler,
    provider.RunToolLoopOptions{
        MaxRounds:          5,
        ParallelToolCalls: true,
        ToolErrorEncoder: func(_ context.Context, tc provider.ToolCall, err error) (provider.Message, error) {
            return provider.ToolResultMessageJSON(tc.ID, map[string]string{
                "error": err.Error(),
                "mode":  "custom",
            })
        },
    },
)
```

默认值：

- `RunToolLoop` 等价于使用兼容默认 options 调用 `RunToolLoopWithOptions`
- `ParallelToolCalls` 默认关闭
- `ToolErrorEncoder` 默认使用安全脱敏编码器

### 方式二：手动管理多轮对话

如果你需要在每轮 tool call 之间插入自定义逻辑（如日志、权限检查、结果缓存等），可以手动管理循环：

```go
// 第一步：发送带 tools 的请求
messages := []provider.Message{
    provider.UserText("北京天气怎么样？"),
}

resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: messages,
    Tools:    tools,
})
if err != nil {
    log.Fatal(err)
}

// 第二步：检查是否有 tool calls
if resp.HasToolCalls() {
    // 将 assistant 的 tool_calls 响应追加到历史
    messages = append(messages, resp.AssistantMessage())

    // 执行每个 tool call
    for _, tc := range resp.ToolCalls {
        fmt.Printf("模型调用: %s(%s)\n", tc.Function.Name, tc.Function.Arguments)

        // 解析参数
        var args struct {
            City string `json:"city"`
        }
        tc.Function.ParseArguments(&args)

        // 执行工具，拿到结果
        result := fmt.Sprintf(`{"temperature": 28, "city": "%s"}`, args.City)

        // 将结果追加到历史
        messages = append(messages, provider.ToolResultMessage(tc.ID, result))
    }

    // 第三步：回传工具结果，获取最终回复
    finalResp, err := p.Chat(ctx, &provider.ChatRequest{
        Messages: messages,
        Tools:    tools,
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(finalResp.Content)
}
```

### ToolChoice 控制

控制模型如何选择工具：

```go
// 模型自行决定（默认行为）
req.ToolChoice = provider.ToolChoiceAuto

// 禁止调用工具，强制文本回复
req.ToolChoice = provider.ToolChoiceNone

// 强制必须调用工具（至少一个）
req.ToolChoice = provider.ToolChoiceRequired

// 强制调用指定的函数
req.ToolChoice = provider.ToolChoiceFunction{Name: "get_weather"}
```

不要再传裸字符串 `\"auto\"` / `\"none\"` / `\"required\"`。

### ParallelToolCalls

控制模型是否可以在一次回复中并行调用多个工具：

```go
parallel := true
req.ParallelToolCalls = &parallel

// 模型可能一次返回多个 tool calls：
// resp.ToolCalls = [
//   {ID: "call_1", Function: {Name: "get_weather", Arguments: `{"city":"北京"}`}},
//   {ID: "call_2", Function: {Name: "get_weather", Arguments: `{"city":"上海"}`}},
// ]
```

### 便捷工具函数

```go
// 解析 tool call 的 JSON 参数
var args MyStruct
err := tc.Function.ParseArguments(&args)

// 构建工具结果消息（纯字符串）
msg := provider.ToolResultMessage(tc.ID, `{"temperature": 28}`)

// 构建工具结果消息（自动 JSON 序列化）
msg, err := provider.ToolResultMessageJSON(tc.ID, map[string]any{
    "temperature": 28,
    "condition":   "晴",
})

// 将模型的 tool_calls 响应转换为可追加到历史的 Message
msg = resp.AssistantMessage()

// 检查响应是否包含 tool calls
if resp.HasToolCalls() { ... }
```

### 流式 Tool Use

流式模式下 tool call 以增量方式返回，每个 chunk 可能只包含部分数据：

```go
stream, err := p.ChatStream(ctx, &provider.ChatRequest{
    Messages: messages,
    Tools:    tools,
})
defer func() { _ = stream.Close() }()

// 累积器：收集流式 tool call 的片段
type accumulator struct {
    id        string
    name      string
    arguments strings.Builder
}
var accum []accumulator

for {
    chunk, err := stream.Recv()
    if err != nil {
        if err == io.EOF {
            break
        }
        log.Fatal(err)
    }

    // 普通文本增量
    if chunk.Delta != "" {
        fmt.Print(chunk.Delta)
    }

    // 流式 tool call 增量
    for _, tcd := range chunk.ToolCalls {
        // 按 Index 扩展累积器
        for len(accum) <= tcd.Index {
            accum = append(accum, accumulator{})
        }
        if tcd.ID != "" {
            accum[tcd.Index].id = tcd.ID
        }
        if tcd.Function.Name != "" {
            accum[tcd.Index].name = tcd.Function.Name
        }
        accum[tcd.Index].arguments.WriteString(tcd.Function.Arguments)
    }
}

// 流结束后，accum 中包含完整的 tool calls
for _, a := range accum {
    fmt.Printf("Tool Call: %s(%s)\n", a.name, a.arguments.String())
}
```

> 流式 Tool Use 比较复杂，大多数场景推荐直接用非流式的 `RunToolLoop`。

## Embedding（文本向量化）

除了 Chat，本库同时提供统一的 `Embedder` 接口，用于把文本转成向量，支撑 RAG、语义搜索、聚类、去重、推荐等场景。

**关键设计**：与 Chat 共用同一套 `Registry` / `Preset` / `QuickRegistry`。`QuickRegistry` 会在注册 chat provider 时，自动为有 embedding 预设的平台（OpenAI / Qwen / 智谱 / 千帆 / 硅基流动）同时注册对应 embedder；DeepSeek 和 Moonshot 官方无 embedding 接口，静默跳过不报错。

### 基础用法

```go
reg := provider.QuickRegistry(map[provider.ProviderName]string{
    provider.ProviderQwen:  os.Getenv("QWEN_API_KEY"),
    provider.ProviderZhipu: os.Getenv("ZHIPU_API_KEY"),
})

// 同一个 Registry 同时管 chat 和 embedding
chat, _ := reg.Default()
emb,  _ := reg.DefaultEmbedder()

// 单条文本 → 向量
vec, err := provider.SimpleEmbed(ctx, emb, "什么是 goroutine")

// 批量文本 → 向量数组（顺序与输入一致，底层乱序会自动重排）
vecs, err := provider.EmbedBatch(ctx, emb, []string{
    "退款政策",
    "发货时效",
    "会员等级",
})
```

### 完整调用（自定义维度 / User / 模型覆盖）

```go
dims := 512 // OpenAI v3 和 Qwen v3 支持按维度截断
resp, err := emb.Embed(ctx, &provider.EmbeddingRequest{
    Model:      "text-embedding-3-large", // 可选，覆盖默认模型
    Input:      []string{"hello", "world"},
    Dimensions: &dims,
    User:       "user-123",
})

for _, e := range resp.Data {
    fmt.Printf("Index=%d, len=%d\n", e.Index, len(e.Vector))
}
fmt.Printf("Usage: %d tokens\n", resp.Usage.TotalTokens)
```

### 典型 RAG 最小闭环

```go
// 1. 离线索引：一次性把知识库文档转向量
docs := []string{
    "退款政策：七天无理由退款，需保持商品完好",
    "发货时效：现货 48 小时内发货",
    "会员等级：消费满 1000 元升级银卡",
}
docVecs, _ := provider.EmbedBatch(ctx, emb, docs)

// 2. 在线查询：用户问题也转向量
query := "怎么申请返还款项"
queryVec, _ := provider.SimpleEmbed(ctx, emb, query)

// 3. 相似度检索：业务层自己实现余弦相似度
bestIdx := argmaxCosine(queryVec, docVecs) // 调用方实现

// 4. 把匹配文档拼进 prompt 让 LLM 回答
reply, _ := provider.SimpleChatWithSystem(ctx, chat,
    "基于以下资料回答: "+docs[bestIdx],
    query,
)
```

完整可运行示例（含余弦相似度实现）见 [`example/embedding/main.go`](example/embedding/main.go)。

### 直接构造 Embedder（不走 Registry）

```go
// 预设配置，只需 APIKey
emb, err := provider.NewEmbedderFromPreset(
    provider.ProviderOpenAI,
    os.Getenv("OPENAI_API_KEY"),
    "", // 留空使用预设的 text-embedding-3-small
)

// 完全自定义（自部署或未预设的服务）
emb, err = provider.NewEmbedder(provider.EmbedderConfig{
    Name:    "my-embedding-service",
    BaseURL: "http://localhost:8080/v1",
    APIKey:  "any",
    Model:   "bge-large-zh-v1.5",
})
```

### 边界：本库不做什么

- ❌ 向量存储（业务侧选向量数据库：pgvector / Milvus / Qdrant / Chroma 等）
- ❌ 相似度计算（10 行代码可写完，内置反而限制）
- ❌ 文档切片 / chunking 策略
- ❌ 完整 RAG 框架（LangChain / LlamaIndex 的定位）

这和"不管理对话历史"是同一个设计哲学 —— 库只负责与 LLM 平台的交互，存储与业务逻辑交给调用方。

---

## Registry 操作

### 按名称切换 Provider

```go
zhipu, err := reg.Get(provider.ProviderZhipu)
if err != nil {
    fmt.Println("智谱未注册")
}
reply, _ := provider.SimpleChat(ctx, zhipu, "你好")
```

### 设置默认 Provider

```go
err := reg.SetDefault(provider.ProviderQwen)
p, _ := reg.Default()  // 现在返回千问的 provider
```

### 列出所有已注册的 Provider

```go
for _, name := range reg.Names() {
    fmt.Println("已注册:", name)
}
```

### Embedder 注册管理

Embedder 在 Registry 内独立管理（与 Provider 互不影响），操作方法对称：

```go
reg.RegisterEmbedder(emb)                      // 注册
reg.GetEmbedder(provider.ProviderQwen)         // 按名称获取
reg.DefaultEmbedder()                          // 获取默认
reg.SetDefaultEmbedder(provider.ProviderZhipu) // 切换默认
reg.EmbedderNames()                            // 列出所有已注册 embedder
```

## Middleware（中间件扩展）

当你需要日志、重试、限流、断路器、token 统计、审计、缓存这类横切关注点时，本库**不内置任何具体策略**，只提供扩展口子——装饰器 + Handler 类型。调用方用 30 行以内就能自行实现任一能力，策略完全由你控制。

### 为什么不内置？

- 限流的阈值、断路器的窗口、日志的格式、审计的存储、脱敏的字段 —— **每家项目口径不同**
- 一旦内置某种实现，就会产生滑坡："为什么不内置 Prometheus？为什么不内置 OTel？"
- Middleware 抽象只有几个函数类型，学习成本极低，却能让调用方完全自主

### 核心类型

```go
// 三种处理器对应 Chat / ChatStream / Embed 三条路径
type Handler       func(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
type StreamHandler func(ctx context.Context, req *ChatRequest) (*StreamReader, error)
type EmbedHandler  func(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)

// 中间件 = next → 装饰后的 next
type Middleware       func(next Handler) Handler
type StreamMiddleware func(next StreamHandler) StreamHandler
type EmbedMiddleware  func(next EmbedHandler) EmbedHandler

// 装饰 Provider / Embedder，洋葱模型组合
func WithMiddlewares(p Provider, opts MiddlewareOptions) Provider
func TryWithMiddlewares(p Provider, opts MiddlewareOptions) (Provider, error)
func WithEmbedderMiddlewares(e Embedder, mws ...EmbedMiddleware) Embedder
func TryWithEmbedderMiddlewares(e Embedder, mws ...EmbedMiddleware) (Embedder, error)
```

### 最简示例：日志中间件

```go
func loggingMiddleware(name provider.ProviderName) provider.Middleware {
    return func(next provider.Handler) provider.Handler {
        return func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
            start := time.Now()
            resp, err := next(ctx, req)
            if err != nil {
                log.Printf("[chat] provider=%s model=%s elapsed=%s err=%v", name, req.Model, time.Since(start), err)
            } else {
                log.Printf("[chat] provider=%s model=%s elapsed=%s tokens=%d", name, req.Model, time.Since(start), resp.Usage.TotalTokens)
            }
            return resp, err
        }
    }
}

p := provider.WithMiddlewares(base, provider.MiddlewareOptions{
    Chat: []provider.Middleware{loggingMiddleware(base.Name())},
})
```

### 重试中间件（基于 `ProviderError.Retryable`）

```go
func retryMiddleware(maxAttempts int) provider.Middleware {
    return func(next provider.Handler) provider.Handler {
        return func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
            var last error
            for attempt := 1; attempt <= maxAttempts; attempt++ {
                resp, err := next(ctx, req)
                if err == nil {
                    return resp, nil
                }
                last = err

                var pErr *provider.ProviderError
                if !errors.As(err, &pErr) || !pErr.Retryable || attempt == maxAttempts {
                    return nil, last
                }

                // 这里为演示保持最简；生产环境建议加退避、jitter、上限。
                select {
                case <-ctx.Done():
                    return nil, ctx.Err()
                case <-time.After(500 * time.Millisecond):
                }
            }
            return nil, last
        }
    }
}
```

完整示例见 [`example/middleware/main.go`](example/middleware/main.go)。
示例还演示了 `tokenStatsMiddleware(stats *int64)`，用 `atomic.AddInt64` 累计总 token 消耗。

### 洋葱模型执行顺序

```go
p := provider.WithMiddlewares(base, provider.MiddlewareOptions{
    Chat: []provider.Middleware{
        loggingMiddleware(),         // [0] 最外层：第一个进、最后一个出
        tokenStatsMiddleware(&cnt),  // [1]
        retryMiddleware(3, 500*time.Millisecond), // [len-1] 最内层：最贴近真实 Chat
    },
})

// 请求执行路径：
//   logging.enter → tokenStats.enter → retry.enter
//                                          → 真实 Chat
//   logging.exit  ← tokenStats.exit  ← retry.exit
```

`opts.Chat` 装饰 `Chat`，`opts.Stream` 装饰 `ChatStream`，互不影响。切片中的 `nil` 条目会被跳过。

### Embedder 侧对称能力

```go
emb := provider.WithEmbedderMiddlewares(baseEmb, loggingEmbedMiddleware())
```

签名与 Chat 侧完全对称，用 `EmbedHandler` / `EmbedMiddleware` 类型。

### 组合与叠加

`WithMiddlewares` 的返回值本身是 `Provider`，可以**再次传入** `WithMiddlewares` 外再包一层——适合"基础能力按全局注册、特定请求链路再加一层"。

如果你不希望装饰阶段因空值直接 panic，改用 `TryWithMiddlewares` / `TryWithEmbedderMiddlewares`，由调用方显式处理 `ErrNilProvider` / `ErrNilEmbedder`。

### 错误处理：`ProviderError` + 8 个 Sentinel

底层 provider 错误统一包装为 `*ProviderError`。调用方既可以用 `errors.Is` 走高频分支，也可以用 `errors.As` 拿到结构化字段：

```go
type ProviderError struct {
    Provider   ProviderName
    Code       ErrorCode
    StatusCode int
    Status     string
    RawCode    string
    RawType    string
    RawParam   string
    Retryable  bool
    Message    string
    Cause      error
}
```

字段语义：

- `Provider`：错误来自哪个 provider。
- `Code`：稳定的错误分类，适合业务分支判断。
- `StatusCode` / `Status`：底层 HTTP 信息；网络错误时可能为零值。
- `RawCode` / `RawType` / `RawParam`：厂商原始诊断字段，适合日志和告警。
- `Retryable`：是否值得调用方自行重试。
- `Message` / `Cause`：平台返回消息与原始错误链。

8 个 sentinel 如下：

- `ErrAuth`
- `ErrRateLimit`
- `ErrTimeout`
- `ErrContextLength`
- `ErrContentFilter`
- `ErrInvalidRequest`
- `ErrServerError`
- `ErrNetwork`

对应的 `ErrorCode` 常量包括：

- `ErrorCodeUnknown`
- `ErrorCodeAuth`
- `ErrorCodeRateLimit`
- `ErrorCodeTimeout`
- `ErrorCodeContextLength`
- `ErrorCodeContentFilter`
- `ErrorCodeInvalidRequest`
- `ErrorCodeServerError`
- `ErrorCodeNetwork`

消费方式：

```go
resp, err := p.Chat(ctx, req)

// 方式 1：高频分支用 errors.Is
if errors.Is(err, provider.ErrRateLimit) {
    // 被限流，自行退避
}
if errors.Is(err, provider.ErrAuth) {
    // 鉴权失败，应该告警
}

// 方式 2：拿结构化字段做更细判断
var pErr *provider.ProviderError
if errors.As(err, &pErr) {
    switch pErr.Code {
    case provider.ErrorCodeTimeout, provider.ErrorCodeServerError, provider.ErrorCodeNetwork:
        // 可恢复错误
    case provider.ErrorCodeContextLength:
        // 提示调用方裁剪输入
    }
}

// context 取消 / 超时仍然透传
if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
    // 调用方主动取消或超时
}
```

## 多轮对话

库本身不管理对话历史（保持无状态），多轮对话通过 `Messages` 切片传入上下文：

```go
history := []provider.Message{
    provider.SystemText("你是一个 Go 语言助手"),
    provider.UserText("什么是 channel？"),
    provider.AssistantText("Channel 是 Go 中 goroutine 之间通信的管道..."),
    provider.UserText("给我一个带缓冲 channel 的例子"),
}

resp, err := p.Chat(ctx, &provider.ChatRequest{Messages: history})

// 把新回复追加到 history 继续对话
history = append(history, provider.Message{
    Role:    provider.RoleAssistant,
    Content: []provider.ContentPart{provider.TextPart(resp.Content)},
})
```

## 在 Gin/HTTP 服务中使用

```go
var reg *provider.Registry

func init() {
    reg = provider.QuickRegistry(map[provider.ProviderName]string{
        provider.ProviderDeepSeek: os.Getenv("DEEPSEEK_API_KEY"),
        provider.ProviderQwen:    os.Getenv("QWEN_API_KEY"),
    })
}

func chatHandler(c *gin.Context) {
    var req struct {
        Provider string `json:"provider"`
        Model    string `json:"model"`
        Message  string `json:"message" binding:"required"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    var p provider.Provider
    var err error
    if req.Provider != "" {
        p, err = reg.Get(provider.ProviderName(req.Provider))
    } else {
        p, err = reg.Default()
    }
    if err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    resp, err := p.Chat(c.Request.Context(), &provider.ChatRequest{
        Model:    req.Model,
        Messages: []provider.Message{provider.UserText(req.Message)},
    })
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, gin.H{"content": resp.Content, "usage": resp.Usage})
}
```

请求示例：

```bash
# 使用默认 provider
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "你好"}'

# 指定使用千问 + 特定模型
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{"provider": "qwen", "model": "qwen-max", "message": "你好"}'
```

## 各平台常用模型速查

> 以下为截至 **2026-04** 各厂商在线可调用的主流模型。模型名随平台更新变化较快，建议调用前对照官方文档。

**DeepSeek**

| 模型名 | 说明 |
|--------|------|
| `deepseek-chat` | DeepSeek-V3.2 非思考模式，128K 上下文 |
| `deepseek-reasoner` | DeepSeek-V3.2 思考模式，原生链式思考（CoT） |

**通义千问（百炼 DashScope）**

| 模型名 | 说明 |
|--------|------|
| `qwen3-max` | 旗舰，复杂任务能力最强 |
| `qwen3.6-plus` | 2026 新一代 Plus，性能/成本平衡 |
| `qwen-plus-latest` | Plus 自动跟随最新快照 |
| `qwen-flash` | 速度优先，分档计费 |
| `qwen-long` | 长文档处理，最高 10M tokens |
| `qwen3-coder-plus` | 代码 Agent 专精 |
| `qwen3-vl-plus` | 视觉多模态 |
| `qwq-plus` | 深度推理模型 |

**智谱 AI**

| 模型名 | 说明 |
|--------|------|
| `glm-5.1` | 最新旗舰，Coding 能力对标 Claude Opus 4.6 |
| `glm-5` | 高智能基座，擅长长程规划 |
| `glm-4.7` | 通用对话推理升级 |
| `glm-4.7-flash` | 免费普惠版 |
| `glm-4.6` | 200K 上下文 |
| `glm-4.5-air` | 高性价比 |
| `glm-4.6v` | 视觉推理，原生工具调用 |

**百度千帆**

| 模型名 | 说明 |
|--------|------|
| `ernie-4.5-turbo-128k` | 文心 4.5 Turbo 长上下文 |
| `ernie-4.5-turbo-32k` | 文心 4.5 Turbo 通用 |
| `ernie-x1-turbo-32k` | X1 Turbo 推理模型（思维链 + 工具调用） |
| `ernie-4.5-turbo-vl-32k` | 文心 4.5 VL 多模态 |
| `ernie-speed-128k` | 经济高速 |
| `ernie-lite-8k` | 超经济轻量 |

**硅基流动**

| 模型名 | 说明 |
|--------|------|
| `deepseek-ai/DeepSeek-V3.2` | DeepSeek V3.2，含思考模式 |
| `deepseek-ai/DeepSeek-V3.1-Terminus` | V3.1 终结版 |
| `Qwen/Qwen3.5-397B-A17B` | 千问 3.5 MoE 旗舰 |
| `Qwen/Qwen3.5-122B-A10B` | 千问 3.5 MoE 中等 |
| `Qwen/Qwen3.5-35B-A3B` | 千问 3.5 MoE 轻量 |
| `moonshotai/Kimi-K2.5` | Kimi K2.5（256K 上下文） |
| `Pro/zai-org/GLM-5.1` | 智谱 GLM-5.1（Pro 付费通道） |
| `Pro/zai-org/GLM-4.7` | 智谱 GLM-4.7（Pro 付费通道） |

> `Pro/` 前缀为付费稳定通道，不带前缀为社区免费通道，能力相同但限流更严。

**Moonshot / Kimi**

| 模型名 | 说明 |
|--------|------|
| `kimi-k2-turbo-preview` | Kimi K2 Turbo 高速版，256K 上下文 |
| `kimi-k2-0905-preview` | Kimi K2.5 最新快照，1T 总参 / 32B 激活 MoE |
| `kimi-k2-thinking` | K2 思考模式，深度推理 |
| `kimi-latest` | 自动选择最新模型 |
| `moonshot-v1-128k` | 经典 V1 系列 128K |

**OpenAI**

| 模型名 | 说明 |
|--------|------|
| `gpt-5.4` | 旗舰，推理/编码综合最强，1M 上下文（2026-03） |
| `gpt-5.4-pro` | Pro 版，能力更强 |
| `gpt-5.4-mini` | 经济款，400K 上下文（2026-03） |
| `gpt-5.4-nano` | 极低成本 |
| `gpt-5.3-codex` | 代码专用（legacy） |
| `o3` | 推理系列旗舰 |
| `o3-pro` | 推理增强 |
| `o3-mini` | 轻量推理 |
| `o4-mini` | 新一代推理 |

> 模型列表会随平台更新而变化，建议使用前查阅各平台最新文档。

## 常用 Embedding 模型速查

> 以下维度数为官方文档给出的**最大值**，部分模型（如 OpenAI `text-embedding-3-*`、Qwen `text-embedding-v3`、智谱 `embedding-3`）支持通过 `Dimensions` 参数按需截断。具体可选维度值、计费方式、向量归一化行为以各平台最新文档为准。

**OpenAI**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `text-embedding-3-small` | 1536 | 默认推荐，性价比首选 |
| `text-embedding-3-large` | 3072 | 高质量，支持 `dimensions` 截断 |
| `text-embedding-ada-002` | 1536 | 上一代，兼容场景保留 |

**通义千问（百炼 DashScope）**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `text-embedding-v3` | 1024 | 默认推荐，支持 `dimensions` 截断（64/128/256/512/768/1024） |
| `text-embedding-v4` | 最高 2048 | 2026 最新版，支持多语言增强 |
| `text-embedding-v2` | 1536 | 上一代 |

**智谱 AI**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `embedding-3` | 最高 2048 | 默认推荐，支持 `dimensions` 截断 |
| `embedding-2` | 1024 | 上一代 |

**百度千帆**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `embedding-v1` | 384 | 默认通用版 |
| `bge-large-zh` | 1024 | 中文效果强 |
| `tao-8k` | 1024 | 长文本（8K 上下文） |

**硅基流动**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `BAAI/bge-m3` | 1024 | 默认推荐，多语言 + 稀疏/稠密混合 |
| `BAAI/bge-large-zh-v1.5` | 1024 | 中文专用 |
| `Pro/BAAI/bge-m3` | 1024 | 付费稳定通道 |
| `netease-youdao/bce-embedding-base_v1` | 768 | 网易有道中英双语 |

> DeepSeek / Moonshot 官方暂无 embedding 模型，需要请转硅基流动或自部署。

## 核心类型参考

### Provider 接口

```go
type Provider interface {
    Name() ProviderName
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error)
}
```

### ChatRequest

```go
type ChatRequest struct {
    Model       string     // 可选，留空使用 ProviderConfig.Model
    Messages    []Message  // 对话消息列表
    MaxTokens   int        // 最大生成 token 数
    Temperature *float32   // 采样温度（指针类型，区分"未设置"和"设为0"）
    TopP        *float32   // 核采样参数
    Stop        []string   // 停止词

    // Tool Use
    Tools             []Tool           // 可用工具列表
    ToolChoice        ToolChoiceOption // ToolChoiceAuto / ToolChoiceNone / ToolChoiceRequired / ToolChoiceFunction{}
    ParallelToolCalls *bool            // 是否允许并行 tool calls
}
```

### ChatResponse

```go
type ChatResponse struct {
    Content      string     // assistant 回复内容（tool call 时可能为空）
    FinishReason string     // "stop" / "length" / "tool_calls"
    Usage        Usage      // Token 用量统计
    ToolCalls    []ToolCall // 模型请求的工具调用列表
}

// 便捷方法
resp.HasToolCalls() bool       // 是否包含 tool calls
resp.AssistantMessage() Message // 转换为可追加到历史的 Message
```

### Message

```go
type Message struct {
    Role       Role       // RoleSystem / RoleUser / RoleAssistant / RoleTool
    Content    []ContentPart
    ToolCalls  []ToolCall // Role == RoleAssistant 时，模型请求的工具调用
    ToolCallID string     // Role == RoleTool 时，关联的 ToolCall.ID
}
```

### ContentPart

```go
type ContentPart struct {
    Type        ContentType
    Text        string
    ImageURL    string
    ImageData   []byte
    MIMEType    string
    ImageDetail ImageDetail
}

const (
    ContentTypeText     ContentType = "text"
    ContentTypeImageURL ContentType = "image_url"
)

// 便捷构造器
provider.TextPart("hello")
provider.ImageURLPart("https://example.com/cat.png")
provider.ImageDataPart(bytes, "image/png")
provider.UserText("hello")
provider.UserMessage(provider.TextPart("describe"), provider.ImageURLPart("https://..."))
```

### Tool Use 类型

```go
// 工具定义
type Tool struct {
    Function FunctionDef
}

type FunctionDef struct {
    Name        string // 函数名（snake_case）
    Description string // 函数描述
    Parameters  any    // 参数 JSON Schema（推荐用 ParamSchema）
}

// 模型返回的工具调用
type ToolCall struct {
    ID       string       // 唯一标识，回传结果时需要
    Function FunctionCall
}

type FunctionCall struct {
    Name      string // 函数名
    Arguments string // JSON 格式参数
}

// 便捷方法
fc.ParseArguments(&target) error // 解析 JSON 参数到结构体

// 便捷构造函数
provider.ToolResultMessage(toolCallID, content) Message       // 纯文本结果
provider.ToolResultMessageJSON(toolCallID, result) (Message, error) // 自动序列化
```

### StreamChunk（含 Tool Use）

```go
type StreamChunk struct {
    Delta        string          // 增量文本
    FinishReason string          // 非空表示流结束
    ToolCalls    []ToolCallDelta // 流式 tool call 增量
}

type ToolCallDelta struct {
    Index    int               // tool call 索引
    ID       string            // 首个 chunk 中非空
    Function FunctionCallDelta
}

type FunctionCallDelta struct {
    Name      string // 首个 chunk 中非空
    Arguments string // 每个 chunk 追加的参数片段
}
```

### RunToolLoop

```go
func RunToolLoop(
    ctx context.Context,
    p Provider,
    req *ChatRequest,          // 初始请求（含 Messages 和 Tools）
    maxRounds int,             // 最大循环次数，推荐 5-10
    handler ToolHandler,       // func(ctx, name, arguments) (result, error)
) (*ChatResponse, error)
```

### Registry

```go
reg := provider.NewRegistry()
reg.Register(p)                           // 注册
reg.Get(provider.ProviderDeepSeek)        // 按名称获取
reg.Default()                             // 获取默认
reg.SetDefault(provider.ProviderQwen)     // 设置默认
reg.Names()                               // 列出所有已注册名称
```

### 便捷函数

```go
// Chat
provider.SimpleChat(ctx, p, "你好")                           // 一问一答
provider.SimpleChatWithSystem(ctx, p, "你是助手", "你好")       // 带 system prompt
provider.CollectStream(ctx, p, req, onChunkFn)                // 流式收集+回调
provider.RunToolLoop(ctx, p, req, maxRounds, handler)         // Tool Use 自动循环

// Embedding
provider.SimpleEmbed(ctx, emb, "你好")                        // 单条文本 → 向量
provider.EmbedBatch(ctx, emb, []string{"a", "b"})             // 批量 → 向量数组
provider.NewEmbedderFromPreset(name, apiKey, model)           // 从预设构造
provider.NewEmbedder(EmbedderConfig{...})                     // 完全自定义
```

### Embedder 核心类型

```go
type Embedder interface {
    Name() ProviderName
    Embed(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
}

type EmbeddingRequest struct {
    Model      string    // 可选，留空时使用 EmbedderConfig.Model
    Input      []string  // 必填，至少一条
    Dimensions *int      // 可选，部分模型支持按维度截断
    User       string    // 可选，OpenAI 兼容字段
}

type EmbeddingResponse struct {
    Data  []Embedding  // 与 Input 一一对应（按 Index 自动排序）
    Model string
    Usage Usage        // 复用 Chat 场景的 Usage 类型
}

type Embedding struct {
    Index  int
    Vector []float32
}

type EmbedderConfig struct {
    Name    ProviderName
    BaseURL string
    APIKey  string
    Model   string // embedding 专用默认模型
}
```

## 设计决策

**为什么主包里只有一个内建的 `openaiProvider` 实现？**

因为 OpenAI、本仓库内置的国内平台，本质上都走 OpenAI 兼容协议。给每个平台写一个 struct 是过度设计。对于 Claude、Google Gemini 这类非兼容协议，架构上已经预留了放到可选扩展包里实现 `Provider` 接口的能力，但当前仓库还没有提供现成扩展包，以避免先引入空壳目录或额外维护成本。

**为什么不管理对话历史？**

对话历史的存储方式千差万别（内存、Redis、数据库），强行内置只会限制使用者。库只负责「发请求、收响应」，历史管理交给业务层。

**为什么 Temperature 和 TopP 是指针类型？**

因为这两个参数的零值 `0.0` 是有意义的（表示贪婪采样），用指针可以区分"未设置"和"设置为 0"。未设置时由各平台使用自己的默认值。

**Tool Use 的 handler 返回 error 时会怎样？**

`RunToolLoop` 会将 error 信息包装为 `{"error": "..."}` 回传给模型。这样模型有机会换一种方式重试或告知用户，而不是直接中断整个流程。

## 扩展

### 添加新平台

在 `presets.go` 的 `presetCatalog` 中添加一项：

```go
var presetCatalog = map[ProviderName]Preset{
    // ... 已有平台 ...
    "my-new-platform": {
        BaseURL:      "https://api.new-platform.com/v1",
        DefaultModel: "their-model",
    },
}
```

读取预设时，新增代码优先使用 `provider.AllPresets()`；`provider.Presets` 仅为兼容旧代码保留。

### 实现自定义 Provider

如果某个平台的 API 不兼容 OpenAI 协议：

```go
type myCustomProvider struct{}

func (p *myCustomProvider) Name() provider.ProviderName { return "custom" }

func (p *myCustomProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
    // 自定义调用逻辑 ...
}

func (p *myCustomProvider) ChatStream(ctx context.Context, req *provider.ChatRequest) (*provider.StreamReader, error) {
    return provider.NewStreamReader(
        func() (*provider.StreamChunk, error) {
            // 自定义流式协议到 StreamChunk 的映射 ...
        },
        func() error {
            // 关闭底层流 ...
            return nil
        },
    ), nil
}

reg.Register(&myCustomProvider{})
```

> 说明：Claude / Google Gemini 当前也属于这一类。主包已经预留扩展点，但本仓库暂未提供可直接使用的官方扩展包实现。

### 可选扩展包怎么接

如果你要自己实现 `Claude` 或 `Google Gemini` 扩展包，推荐按下面的方式组织：

```text
your-llm-extension/
├── go.mod
└── anthropicprovider/
    └── provider.go
```

最小骨架示例：

```go
package anthropicprovider

import (
    "context"
    "io"
    "net/http"

    "github.com/gtkit/go-llm-provider/v2/provider"
)

type Provider struct {
    apiKey string
    model  string
    client *http.Client
}

func New(apiKey, model string) (*Provider, error) {
    if model == "" {
        model = "claude-sonnet-4-0"
    }

    return &Provider{
        apiKey: apiKey,
        model:  model,
        client: &http.Client{},
    }, nil
}

func (p *Provider) Name() provider.ProviderName {
    return "claude"
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
    // 1. 把 provider.ChatRequest 转成厂商请求
    // 2. 发起 HTTP 调用
    // 3. 把厂商响应映射回 provider.ChatResponse
    return &provider.ChatResponse{
        Content: "hello",
    }, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *provider.ChatRequest) (*provider.StreamReader, error) {
    // 先建立底层 HTTP/SSE 流

    return provider.NewStreamReader(
        func() (*provider.StreamChunk, error) {
            // 读取一个底层事件并映射为统一的 StreamChunk
            // 文本增量写到 Delta
            // 结束原因写到 FinishReason
            // 如果厂商支持流式 tool call，就填 ToolCalls
            return nil, io.EOF
        },
        func() error {
            // 关闭底层流
            return nil
        },
    ), nil
}
```

主程序里的使用方式：

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/gtkit/go-llm-provider/v2/provider"
    "github.com/your-org/your-llm-extension/anthropicprovider"
)

func main() {
    claude, err := anthropicprovider.New(
        os.Getenv("ANTHROPIC_API_KEY"),
        "claude-sonnet-4-0",
    )
    if err != nil {
        panic(err)
    }

    reg := provider.NewRegistry()
    reg.Register(claude)

    p, err := reg.Get("claude")
    if err != nil {
        panic(err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    reply, err := provider.SimpleChat(ctx, p, "用一句话介绍 Go")
    if err != nil {
        panic(err)
    }

    fmt.Println(reply)
}
```

实现时只要遵守这几个映射规则，主包里的辅助能力就能继续复用：

- 非流式响应把文本映射到 `provider.ChatResponse.Content`
- 如果厂商支持 tool use，把工具调用映射到 `provider.ChatResponse.ToolCalls`
- 流式响应把文本增量映射到 `provider.StreamChunk.Delta`
- 流结束时把厂商结束原因映射到 `provider.StreamChunk.FinishReason`
- 只要 `Chat` / `ChatStream` 的输出符合统一类型，`provider.CollectStream` 和 `provider.RunToolLoop` 就能直接继续使用

## 运行测试

```bash
go test ./provider/ -v
```

## License

MIT
