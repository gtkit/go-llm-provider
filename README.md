# llm-provider

Go 语言统一多模型 LLM 调用库。一套代码接入 DeepSeek、通义千问、智谱、百度千帆、硅基流动、Moonshot 等国内主流大模型平台。

## 为什么做这个

国内主流大模型平台现在都兼容了 OpenAI Chat Completions 协议，本质上只是 BaseURL 和 APIKey 的差异。但每次接入新平台还是要翻文档查地址、记模型名、写一堆重复的初始化代码。

这个库做的事情很简单：

- 预置了各平台的 BaseURL 和推荐模型，传个 APIKey 就能用
- 统一的 `Provider` 接口，业务代码不需要关心底层是哪个平台
- `Registry` 注册表管理多个 Provider，运行时按名称切换
- 支持非流式和流式两种调用模式
- 完整的 Tool Use / Function Calling 支持，包含自动循环执行的 `RunToolLoop`
- 零额外依赖，底层只用 `sashabaranov/go-openai`

## 项目结构

```
llm-provider/
├── go.mod
├── README.md
├── provider/
│   ├── provider.go        # 核心：Provider 接口、Registry、请求/响应、Tool Use 类型
│   ├── presets.go          # 各平台预设配置（BaseURL + 默认模型）
│   ├── helpers.go          # 便捷函数：SimpleChat、CollectStream
│   ├── toolrun.go          # RunToolLoop：Tool Use 自动循环执行器
│   └── provider_test.go    # 单元测试
└── example/
    ├── main.go             # 基础使用示例
    ├── tooluse/main.go     # Tool Use 手动多轮示例
    └── toolloop/main.go    # RunToolLoop 自动循环示例
```

## 安装

```bash
go get github.com/gtkit/go-llm-provider
```

> 将 `github.com/gtkit/go-llm-provider` 替换为你实际的模块路径。

## 支持的平台

| 平台 | ProviderName | 预设 BaseURL | 默认模型 | API Key 获取 |
|------|-------------|-------------|---------|-------------|
| DeepSeek | `deepseek` | `https://api.deepseek.com/v1` | `deepseek-chat` | [platform.deepseek.com](https://platform.deepseek.com/) |
| 通义千问（百炼） | `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-plus` | [百炼控制台](https://bailian.console.aliyun.com/) |
| 智谱 AI | `zhipu` | `https://open.bigmodel.cn/api/paas/v4/` | `glm-4-plus` | [open.bigmodel.cn](https://open.bigmodel.cn/) |
| 百度千帆 | `qianfan` | `https://qianfan.baidubce.com/v2` | `ernie-4.0-8k` | [千帆控制台](https://console.bce.baidu.com/qianfan/) |
| 硅基流动 | `siliconflow` | `https://api.siliconflow.cn/v1` | `deepseek-ai/DeepSeek-V3` | [siliconflow.cn](https://siliconflow.cn/) |
| Moonshot / Kimi | `moonshot` | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` | [platform.moonshot.cn](https://platform.moonshot.cn/) |

> 预设地址和默认模型可能随平台更新而变化，建议定期对照各平台官方文档确认。

## 快速开始

### 30 秒上手

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/gtkit/go-llm-provider/provider"
)

func main() {
    // 一行创建注册表，传入各平台 API Key（空值自动跳过）
    reg := provider.QuickRegistry(map[provider.ProviderName]string{
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
export DEEPSEEK_API_KEY="sk-xxxxxxxx"
go run main.go
```

## 使用方式

库提供三个层级的创建方式，由简到灵活。

### 方式一：QuickRegistry（推荐日常使用）

传一组 `ProviderName -> APIKey` 的映射，自动使用预设的 BaseURL 和默认模型。空 APIKey 会被自动跳过，不会报错。

```go
reg := provider.QuickRegistry(map[provider.ProviderName]string{
    provider.ProviderDeepSeek:    os.Getenv("DEEPSEEK_API_KEY"),
    provider.ProviderQwen:        os.Getenv("QWEN_API_KEY"),
    provider.ProviderZhipu:       os.Getenv("ZHIPU_API_KEY"),
    provider.ProviderSiliconFlow: os.Getenv("SILICONFLOW_API_KEY"),
    provider.ProviderMoonshot:    os.Getenv("MOONSHOT_API_KEY"),
})

// 第一个注册成功的自动成为默认 provider
p, err := reg.Default()
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
p := provider.NewProvider(provider.ProviderConfig{
    Name:    "my-vllm",                          // 自定义名称
    BaseURL: "http://192.168.1.100:8080/v1",     // 你的服务地址
    APIKey:  "no-key-needed",                     // 没有鉴权可以随便填
    Model:   "Qwen2.5-72B-Instruct",             // 你部署的模型
})
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
        {Role: provider.RoleSystem, Content: "你是一个翻译助手"},
        {Role: provider.RoleUser, Content: "把下面的话翻译成英文：今天天气真好"},
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
        {Role: provider.RoleUser, Content: "写一首关于 Go 的诗"},
    },
})
if err != nil {
    log.Fatal(err)
}
defer stream.Close()

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

### 方式一：RunToolLoop（推荐）

`RunToolLoop` 自动处理 Tool Use 的完整循环：发请求 → 检测 tool_calls → 执行工具 → 回传结果 → 再次请求 → ... 直到模型给出最终文本回复。

```go
resp, err := provider.RunToolLoop(ctx, p, &provider.ChatRequest{
    Messages: []provider.Message{
        {Role: provider.RoleUser, Content: "北京天气怎么样？"},
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
- 如果 handler 返回 error，RunToolLoop 会将错误信息包装为 JSON 回传给模型，让模型有机会纠正

### 方式二：手动管理多轮对话

如果你需要在每轮 tool call 之间插入自定义逻辑（如日志、权限检查、结果缓存等），可以手动管理循环：

```go
// 第一步：发送带 tools 的请求
messages := []provider.Message{
    {Role: provider.RoleUser, Content: "北京天气怎么样？"},
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
req.ToolChoice = "auto"

// 禁止调用工具，强制文本回复
req.ToolChoice = "none"

// 强制必须调用工具（至少一个）
req.ToolChoice = "required"

// 强制调用指定的函数
req.ToolChoice = provider.ToolChoiceFunction{Name: "get_weather"}
```

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
defer stream.Close()

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

## 多轮对话

库本身不管理对话历史（保持无状态），多轮对话通过 `Messages` 切片传入上下文：

```go
history := []provider.Message{
    {Role: provider.RoleSystem, Content: "你是一个 Go 语言助手"},
    {Role: provider.RoleUser, Content: "什么是 channel？"},
    {Role: provider.RoleAssistant, Content: "Channel 是 Go 中 goroutine 之间通信的管道..."},
    {Role: provider.RoleUser, Content: "给我一个带缓冲 channel 的例子"},
}

resp, err := p.Chat(ctx, &provider.ChatRequest{Messages: history})

// 把新回复追加到 history 继续对话
history = append(history, provider.Message{
    Role:    provider.RoleAssistant,
    Content: resp.Content,
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
        Messages: []provider.Message{{Role: provider.RoleUser, Content: req.Message}},
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

**DeepSeek**

| 模型名 | 说明 |
|--------|------|
| `deepseek-chat` | DeepSeek-V3，通用对话（默认） |
| `deepseek-reasoner` | DeepSeek-R1，推理增强 |

**通义千问（百炼 DashScope）**

| 模型名 | 说明 |
|--------|------|
| `qwen-plus` | 性价比之选（默认） |
| `qwen-max` | 旗舰模型 |
| `qwen-turbo` | 速度优先 |
| `qwen-long` | 长文档处理 |

**智谱 AI**

| 模型名 | 说明 |
|--------|------|
| `glm-4-plus` | 旗舰模型（默认） |
| `glm-4-air` | 轻量快速 |
| `glm-4-flash` | 极速推理 |

**百度千帆**

| 模型名 | 说明 |
|--------|------|
| `ernie-4.0-8k` | 文心一言 4.0（默认） |
| `ernie-3.5-8k` | 文心一言 3.5 |
| `deepseek-v3` | 千帆托管的 DeepSeek-V3 |
| `deepseek-r1` | 千帆托管的 DeepSeek-R1 |

**硅基流动**

| 模型名 | 说明 |
|--------|------|
| `deepseek-ai/DeepSeek-V3` | DeepSeek-V3（默认） |
| `Qwen/Qwen2.5-72B-Instruct` | 千问 72B |
| `THUDM/glm-4-9b-chat` | 智谱 GLM-4 9B |

**Moonshot / Kimi**

| 模型名 | 说明 |
|--------|------|
| `moonshot-v1-8k` | 8K 上下文（默认） |
| `moonshot-v1-32k` | 32K 上下文 |
| `moonshot-v1-128k` | 128K 长上下文 |

> 模型列表会随平台更新而变化，建议使用前查阅各平台最新文档。

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
    Tools             []Tool // 可用工具列表
    ToolChoice        any    // "auto" / "none" / "required" / ToolChoiceFunction{}
    ParallelToolCalls *bool  // 是否允许并行 tool calls
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
    Content    string
    ToolCalls  []ToolCall // Role == RoleAssistant 时，模型请求的工具调用
    ToolCallID string     // Role == RoleTool 时，关联的 ToolCall.ID
}
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
provider.SimpleChat(ctx, p, "你好")                           // 一问一答
provider.SimpleChatWithSystem(ctx, p, "你是助手", "你好")       // 带 system prompt
provider.CollectStream(ctx, p, req, onChunkFn)                // 流式收集+回调
provider.RunToolLoop(ctx, p, req, maxRounds, handler)         // Tool Use 自动循环
```

## 设计决策

**为什么只有一个 `openaiProvider` 实现？**

因为国内这几家平台全都走 OpenAI 兼容协议，接口格式一模一样。给每个平台写一个 struct 是过度设计。如果将来某个平台出了不兼容的 API，给它单独实现 `Provider` 接口就行。

**为什么不管理对话历史？**

对话历史的存储方式千差万别（内存、Redis、数据库），强行内置只会限制使用者。库只负责「发请求、收响应」，历史管理交给业务层。

**为什么 Temperature 和 TopP 是指针类型？**

因为这两个参数的零值 `0.0` 是有意义的（表示贪婪采样），用指针可以区分"未设置"和"设置为 0"。未设置时由各平台使用自己的默认值。

**Tool Use 的 handler 返回 error 时会怎样？**

`RunToolLoop` 会将 error 信息包装为 `{"error": "..."}` 回传给模型。这样模型有机会换一种方式重试或告知用户，而不是直接中断整个流程。

## 扩展

### 添加新平台

在 `presets.go` 的 `Presets` map 中添加一项：

```go
var Presets = map[ProviderName]Preset{
    // ... 已有平台 ...
    "my-new-platform": {
        BaseURL:      "https://api.new-platform.com/v1",
        DefaultModel: "their-model",
    },
}
```

### 实现自定义 Provider

如果某个平台的 API 不兼容 OpenAI 协议：

```go
type myCustomProvider struct{}

func (p *myCustomProvider) Name() provider.ProviderName { return "custom" }

func (p *myCustomProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
    // 自定义调用逻辑 ...
}

func (p *myCustomProvider) ChatStream(ctx context.Context, req *provider.ChatRequest) (*provider.StreamReader, error) {
    // 流式实现 ...
}

reg.Register(&myCustomProvider{})
```

## 运行测试

```bash
go test ./provider/ -v
```

## License

MIT