# llm-provider

Go 语言统一多模型 LLM 调用库。一套代码接入 OpenAI 以及 DeepSeek、通义千问、智谱、百度千帆、硅基流动、Moonshot 等 OpenAI 兼容平台。

Eino 用户可使用独立的 [`einoadapter/`](einoadapter/README.md) 开发模块：适配固定 Eino v0.9.21，核心保持不依赖框架。adapter v0.1.0 将在核心 v2.18.0 发布并通过独立验证后发布。

> 根目录为 v1，`v2/` 提供多模态与更多原生协议接口。本次直接精简两条代码线；此次发布 v1.11.0 / v2.18.0，保留旧标签。

⚠ v1.11.0 / v2.18.0 已删除清单中的业务策略 API。这是用户明确授权的同主版本不兼容例外，模块路径保持不变；旧调用方升级前必须迁移。请按 [迁移说明](MIGRATION.md) 将身份、账务、工具执行、历史与路由留在应用；Eino 通过独立 adapter 接入。

## v2 比 v1 多了什么

新项目建议直接使用 `github.com/gtkit/go-llm-provider/v2`。相比 v1，v2 额外提供：

- 多模态消息：`Message.Content` 升级为 `[]ContentPart`，支持文本、图片 URL、图片 bytes、文件 bytes/URL/ID。
- Thinking / Reasoning：`ChatRequest.Thinking`（档位与 token 预算两套口径，覆盖 OpenAI / Ark / DeepSeek / Anthropic / Gemini）、`ChatResponse.Reasoning`、`StreamChunk.ReasoningDelta`、`Usage.ReasoningTokens`。
- Structured Output：`ResponseFormat`、`JSONObjectFormat`、`JSONSchemaFormatStrict`，解码和校验由调用方负责。
- 确定性与多候选：`Seed`、`CandidateCount`。
- Anthropic prompt caching：`WithCacheControl(..., CacheControlEphemeral())`。
- Gemini 原生 token counting：`TokenCounter` / `CountTokens`。
- 本地与企业入口：`ProviderOllama`、`NewAzureOpenAIProvider`、`NewBedrockOpenAIProvider`。
- 更多 OpenAI 兼容 preset：xAI、Groq、Mistral、Cohere。
- 更完整的横切能力：`ResponseMetadata`、`WithObservability`、客户端限流 `RateLimiter` 与重排序 `Reranker`。`WithRetry`、`NewBreaker` 同时在 v1 提供。
- 完整原始用量事实：缓存、推理和搜索用量供应用计量；支持 Gemini 图像输出。


## 为什么做这个

国内主流大模型平台现在都兼容了 OpenAI Chat Completions 协议，本质上只是 BaseURL 和 APIKey 的差异。但每次接入新平台还是要翻文档查地址、记模型名、写一堆重复的初始化代码。

这个库做的事情很简单：

- 预置了各平台的 BaseURL 和推荐模型，传个 APIKey 就能用
- 统一的 `Provider` 接口，业务代码不需要关心底层是哪个平台
- 支持非流式和流式两种调用模式
- Tool Use / Function Calling 协议：声明工具、接收调用、回传结果，执行由应用管理
- 主包保持轻量，零额外厂商 SDK 依赖；OpenAI 兼容路径复用 `sashabaranov/go-openai`，非兼容路径用标准库 `net/http`

## 项目结构

```
llm-provider/
├── go.mod
├── README.md
├── CHANGELOG.md
├── provider/
│   ├── provider.go            # 核心：Provider 接口、请求/响应、Tool Use 类型
│   ├── presets.go             # 各平台预设配置（BaseURL + Chat/Embedding 默认模型）
│   ├── helpers.go             # Chat 便捷函数：SimpleChat、CollectStream
│   ├── embedder.go            # Embedder 接口、请求/响应、openaiEmbedder 实现
│   ├── embedder_helpers.go    # Embedding 便捷函数：SimpleEmbed、EmbedBatch
│   ├── errors.go              # ProviderError / ErrorCode / WrapProviderError
│   ├── middleware.go          # Middleware / Handler 类型 + WithMiddlewares 装饰器
│   ├── retry.go               # WithRetry / RetryMiddleware / BackoffFunc
│   ├── breaker.go             # Breaker 熔断器：滑动窗口计数 + 指数退避冷却 + 半开探测
│   ├── provider_test.go       # Chat / Tool Use 单测
│   ├── embedder_test.go       # Embedding 单测
│   ├── errors_test.go         # ProviderError / ErrorCode / WrapProviderError 单测
│   ├── middleware_test.go     # Middleware 装饰器 + 洋葱顺序测试
│   ├── retry_test.go          # 重试与退避单测
│   ├── breaker_test.go        # 熔断状态机 / 退避 / 中间件单测
│   └── runtime_test.go        # 运行时集成测试
└── example/
    ├── main.go                # 基础使用示例（Chat）
    ├── native/main.go         # Claude / Gemini 原生 HTTP provider 示例
    ├── tooluse/main.go        # Tool Use 手动多轮示例
    ├── middleware/main.go     # Middleware：Logging / TokenStats / Retry 参考实现
    └── embedding/main.go      # Embedding + RAG 最小闭环示例
```

## 安装

```bash
go get github.com/gtkit/go-llm-provider
```

> 将 `github.com/gtkit/go-llm-provider` 替换为你实际的模块路径。

## 支持的平台

| 平台 | ProviderName | 预设 BaseURL | 默认 Chat 模型 | 默认 Embedding 模型 | API Key 获取 |
|------|-------------|-------------|---------|---------|-------------|
| DeepSeek | `deepseek` | `https://api.deepseek.com/v1` | `deepseek-v4-flash` | — | [platform.deepseek.com](https://platform.deepseek.com/) |
| 通义千问（百炼） | `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen3.7-plus` | `text-embedding-v4` | [百炼控制台](https://bailian.console.aliyun.com/) |
| 智谱 AI / GLM | `zhipu`（别名：`ProviderGLM`） | `https://open.bigmodel.cn/api/paas/v4/` | `glm-5.3` | `embedding-3` | [open.bigmodel.cn](https://open.bigmodel.cn/) |
| 百度千帆 | `qianfan` | `https://qianfan.baidubce.com/v2` | `ernie-5.1` | `embedding-v1` | [千帆控制台](https://console.bce.baidu.com/qianfan/) |
| 硅基流动 | `siliconflow` | `https://api.siliconflow.cn/v1` | `deepseek-ai/DeepSeek-V4-Flash` | `BAAI/bge-m3` | [siliconflow.cn](https://siliconflow.cn/) |
| Moonshot / Kimi | `moonshot`（别名：`ProviderKimi`） | `https://api.moonshot.cn/v1` | `kimi-k2.6` | — | [platform.kimi.com](https://platform.kimi.com/) |
| OpenAI | `openai` | `https://api.openai.com/v1` | `gpt-5.6-terra` | `text-embedding-3-small` | [platform.openai.com](https://platform.openai.com/) |
| Anthropic / Claude | `anthropic` | `https://api.anthropic.com` | `claude-sonnet-5` | — | [console.anthropic.com](https://console.anthropic.com/) |
| Google Gemini | `gemini` | `https://generativelanguage.googleapis.com/v1beta` | `gemini-3.5-flash` | — | [aistudio.google.com](https://aistudio.google.com/) |

> 预设默认模型截至 2026-09 对照官方文档更新（DeepSeek 旧的 `deepseek-chat` 已于 2026-07-24 停用）。预设地址和默认模型仍可能随平台更新而变化，建议定期对照各平台官方文档确认。
> v1 的 `EnableThinking` 仍按旧口径下发，思考模型的推理往返、Claude 5 / Gemini 3 的新参数形态仅 v2 支持，思考模型 + 工具循环的场景请使用 v2。
> Embedding 列显示"—"的平台表示官方暂无 embedding 接口，`NewEmbedderFromPreset` 会返回错误。

### 能力矩阵

| 平台 | Chat | Streaming | Tools | Structured Output | Vision | Embedding | 协议 |
|------|------|-----------|-------|-------------------|--------|-----------|------|
| DeepSeek | 是 | 是 | 是 | 是 | 否 | 否 | OpenAI 兼容 |
| 通义千问（百炼） | 是 | 是 | 是 | 是 | 否 | 是 | OpenAI 兼容 |
| 智谱 AI / GLM | 是 | 是 | 是 | 是 | 否 | 是 | OpenAI 兼容 |
| 百度千帆 | 是 | 是 | 是 | 是 | 否 | 是 | OpenAI 兼容 |
| 硅基流动 | 是 | 是 | 是 | 是 | 否 | 是 | OpenAI 兼容 |
| Moonshot / Kimi | 是 | 是 | 是 | 是 | 否 | 否 | OpenAI 兼容 |
| OpenAI | 是 | 是 | 是 | 是 | 否 | 是 | OpenAI 兼容 |
| Anthropic / Claude | 是 | 是 | 是 | 否 | 否 | 否 | 原生 HTTP |
| Google Gemini | 是 | 是 | 是 | 否 | 否 | 否 | 原生 HTTP |

> 矩阵描述当前 v1 内置 preset 默认模型和已映射能力；Vision、Reasoning、Gemini Embedding 等能力请使用 v2。

### 关于 Claude / Google Gemini

Claude 和 Gemini 不是 OpenAI 兼容协议，但主包不引入官方 SDK，而是通过标准库 `net/http` 直接实现各自的原生 HTTP API：

- `ProviderAnthropic` 走 Anthropic Messages API：`POST /v1/messages`
- `ProviderGemini` 走 Gemini Generative Language API：`generateContent` / `streamGenerateContent`
- 两者都复用统一的 `Provider`、`ChatRequest`、`ChatResponse`、`StreamReader` 和 `ProviderError`
- v1 原生实现覆盖文本对话、基础 SSE 流式、Tool Use / Function Calling 和错误分类；多模态与结构化输出建议使用 v2

实现位置：

- 构造函数和 HTTP 调用主流程：`provider/native.go`
- Claude 请求 / 响应映射：`provider/native_anthropic.go`
- Gemini 请求 / 响应映射：`provider/native_gemini.go`
- 原生 provider 单元测试：`provider/native_provider_test.go`
- 真实接口 smoke test：`provider/native_smoke_test.go`

最小用法：

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

claude, err := provider.NewProviderFromPreset(
    provider.ProviderAnthropic,
    os.Getenv("ANTHROPIC_API_KEY"),
    "", // 留空使用 claude-sonnet-5
)
if err != nil {
    return err
}

reply, err := provider.SimpleChatWithSystem(ctx, claude,
    "You are a concise assistant.",
    "用一句话介绍 Go 语言",
)
```

Gemini 可以走同样的预设构造，也可以显式覆盖 BaseURL / HTTP client：

```go
gemini, err := provider.NewGeminiProvider(provider.NativeProviderConfig{
    APIKey: os.Getenv("GEMINI_API_KEY"),
    Model:  "gemini-3.5-flash",
})
if err != nil {
    return err
}

streamText, err := provider.CollectStream(ctx, gemini, &provider.ChatRequest{
    Messages: []provider.Message{
        {Role: provider.RoleUser, Content: "给我两个 Go 服务稳定性建议"},
    },
    MaxTokens: 128,
}, nil)
```

完整可运行示例见 [`example/native/main.go`](example/native/main.go)：

```bash
ANTHROPIC_API_KEY="<ANTHROPIC_API_KEY>" go run ./example/native
GEMINI_API_KEY="<GEMINI_API_KEY>" go run ./example/native
```

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
    p, err := provider.NewProviderFromPreset(provider.ProviderOpenAI, os.Getenv("OPENAI_API_KEY"), "")
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        return
    }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    reply, err := provider.SimpleChat(ctx, p, "用一句话介绍 Go 语言")
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        return
    }
    fmt.Println(reply)
}
```

设置 `OPENAI_API_KEY` 后运行 `go run ./example`。密钥为空时构造返回错误；模型留空使用厂商预设。其他平台使用对应的 `ProviderName` 和密钥。

## 使用方式

使用厂商预设或自定义连接配置创建单个实例；多实例装配与路由由应用负责。

### 升级说明

如果你从旧版本迁移，使用方式有这几处变化：

- `NewProvider` 现在返回 `(Provider, error)`，并在创建时校验 `Name`、`APIKey`、`Model`。
- `StreamReader.Close()` 现在返回 `error`，推荐显式处理，或像示例一样在 `defer` 中忽略。
- `ToolChoice` 不再接受任意 `string/any`，请改用 `provider.ToolChoiceAuto`、`provider.ToolChoiceNone`、`provider.ToolChoiceRequired` 或 `provider.ToolChoiceFunction{...}`。
- `EnableThinking` 目前只对 `DeepSeek` 生效；对其他 provider 开启会直接返回错误。
- 新代码优先使用 `provider.AllPresets()` 读取预设；`provider.Presets` 仅为兼容旧代码保留。

### 方式一：NewProviderFromPreset（指定模型）

使用预设地址，但自定义模型名。适合同一个平台想用不同模型的场景。

```go
// 用千问的 qwen-max 模型而不是默认的 qwen3.6-plus
p, err := provider.NewProviderFromPreset(
    provider.ProviderQwen,
    os.Getenv("QWEN_API_KEY"),
    "qwen-max",  // 留空则使用预设的默认模型
)

// 应用直接保存并调用 p。
```

### 方式二：NewProvider（完全自定义）

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

### 方式三：走网关 / 代理覆盖 Host

内置平台的流量要经过自建网关、企业代理或中转服务时，保留原来的 `ProviderName`，只把 `BaseURL` 换成网关地址即可，平台标识不变。

```go
preset := provider.AllPresets()[provider.ProviderDeepSeek] // 复用预设的默认模型

p, err := provider.NewProvider(provider.ProviderConfig{
    Name:    provider.ProviderDeepSeek,                    // 平台标识保持不变
    BaseURL: "https://llm-gw.corp.internal/deepseek/v1",   // 换成网关地址
    APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
    Model:   preset.DefaultModel,
})
if err != nil {
    log.Fatal(err)
}
reg.Register(p)
```

Claude / Gemini 走网关用原生构造函数的同名字段：

```go
claude, err := provider.NewAnthropicProvider(provider.NativeProviderConfig{
    APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
    BaseURL: "https://llm-gw.corp.internal/anthropic",  // 版本段 /v1 由库补
    Model:   "claude-sonnet-5",
})
```

Embedding 同理，`EmbedderConfig` 上也有 `BaseURL`：

```go
e, err := provider.NewEmbedder(provider.EmbedderConfig{
    Name:    provider.ProviderQwen,
    BaseURL: "https://llm-gw.corp.internal/dashscope/compatible-mode/v1",
    APIKey:  os.Getenv("QWEN_API_KEY"),
    Model:   "text-embedding-v4",
})
```

网关地址写到哪一层，取决于各构造函数的路径拼接规则：

| 构造函数 | 实际请求路径 | `BaseURL` 写到 | 留空时 |
| --- | --- | --- | --- |
| `NewProvider` / `NewEmbedder`（OpenAI 兼容） | `{BaseURL}/chat/completions`、`{BaseURL}/embeddings` | 含 `/v1` 这类版本段 | 使用 OpenAI 官方地址 `https://api.openai.com/v1` |
| `NewAnthropicProvider` | `{BaseURL}/v1/messages` | 版本段之前（库会补 `/v1`） | 使用预设的 `https://api.anthropic.com` |
| `NewGeminiProvider` | `{BaseURL}/models/{model}:generateContent` | 含 `/v1beta` | 使用预设的 `https://generativelanguage.googleapis.com/v1beta` |

末尾多余的 `/` 会被忽略，`https://gw.example.com/v1` 与 `https://gw.example.com/v1/` 等价。


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

#### 流式 token 用量统计

流式调用会自动下发 `stream_options.include_usage`，完整 token 统计随
`FinishReason` 之后、`io.EOF` 之前的收尾 chunk 返回（`StreamChunk.Usage`）。
需要统计用量时读取至 `io.EOF` 并采用最后一个非零 `Usage`；
遇到不认识该参数的老网关，用 `ChatRequest.StreamUsage = &off` 关闭。

#### 内置重试

```go
p = provider.WithRetry(base, provider.RetryOptions{MaxAttempts: 3})
```

重试按 `ProviderError.Retryable` 分类，context 取消或超时后停止。v1 按本地退避策略等待。供应商已处理但响应丢失时，重试可能再次产生调用与费用，调用方需按业务设置次数与超时。跨模型降级与渠道均衡由应用或网关处理。

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

### 手动管理多轮对话

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

> 流式工具调用参数可能跨帧到达，应用必须收齐并校验后再执行。

## Embedding（文本向量化）

除了 Chat，本库同时提供统一的 `Embedder` 接口，用于把文本转成向量，支撑 RAG、语义搜索、聚类、去重、推荐等场景。

通过 `NewEmbedderFromPreset` 单独构造 Embedder；实例由调用方持有。

### 基础用法

```go
emb, err := provider.NewEmbedderFromPreset(provider.ProviderOpenAI, os.Getenv("OPENAI_API_KEY"), "")
if err != nil {
    return err
}

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

### 自定义 Embedder

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

### 职责边界：库负责与平台的交互

`Embedder` 负责把文本变成向量，这是对平台端点的调用封装。向量落到哪里
（pgvector / Milvus / Qdrant / Chroma）、相似度怎么算、文档怎么切片、检索链路怎么编排，
由调用方按业务选型决定——这与对话历史由调用方持有是同一个口径：库只负责与 LLM 平台的交互，
存储与业务逻辑留在调用方手里。

这和"不管理对话历史"是同一个设计哲学 —— 库只负责与 LLM 平台的交互，存储与业务逻辑交给调用方。

---

## Middleware（中间件扩展）

横切关注点走统一的装饰器 + Handler 抽象。本库内置了一组与业务口径无关的可靠性策略，
开箱可用；口径因项目而异的部分（日志格式、指标后端、审计存储、脱敏字段、缓存键）
由调用方用同一套 Middleware 类型自行实现，30 行以内即可。

内置策略：

| 能力 | 入口 | 说明 |
|------|------|------|
| 重试 | `WithRetry` / `RetryMiddleware` | 按 `ProviderError.Retryable` 判定，支持指数退避与全抖动 |
| 熔断 | `WithBreaker` / `BreakerMiddleware` | 滑动窗口计数 + 指数退避冷却 + 半开探测 |

策略参数一律由调用方注入（熔断阈值、退避曲线、成员权重），本库不预设业务口径。
观测、计费与客户端限流在 v2 提供。

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

### 熔断（Circuit Breaker）

`Breaker` 是进程内熔断器：滑动窗口累计失败达阈值即跳闸，冷却期内的请求在本地被
挡下、不发往平台；冷却到期放行少量探测请求，探测成功即闭合，失败则延长冷却（指数退避）。

```go
breaker := provider.NewBreaker(provider.BreakerOptions{
    Name:             "deepseek",         // 仅用于错误消息与 Stats，便于定位
    FailureThreshold: 5,                  // 窗口内失败 5 次跳闸，默认 5
    Window:           time.Minute,        // 滑动窗口，默认 1 分钟
    OpenDuration:     30 * time.Second,   // 首次冷却时长，默认 30 秒
    MaxOpenDuration:  5 * time.Minute,    // 冷却上限，默认 5 分钟
    BackoffReset:     10 * time.Minute,   // 距上次跳闸超过该时长则退避重新起算
    HalfOpenProbes:   1,                  // 半开态在途探测数，默认 1
})

guarded := provider.WithBreaker(base, breaker)

resp, err := guarded.Chat(ctx, req)
if errors.Is(err, provider.ErrBreakerOpen) {
    // 熔断期内的快速失败，请求没有发出
}
```

状态机与计数口径：

```text
closed    ──窗口内失败达阈值──▶ open       （冷却 = OpenDuration × 2^(连续跳闸-1)，上限 MaxOpenDuration）
open      ──冷却到期───────────▶ half_open  （放行至多 HalfOpenProbes 个探测）
half_open ──探测成功───────────▶ closed     （清空失败窗口）
half_open ──探测失败───────────▶ open       （冷却翻倍）
```

- **计入失败的错误**由 `ShouldTrip` 判定，默认与重试口径一致（`IsRetryableError`：
  限流 / 超时 / 5xx / 网络）。鉴权失败、参数非法不计入——换上游也修不了；
  ctx 取消同样不计入。要把 key 失效也纳入熔断，显式传入判定函数：

  ```go
  provider.BreakerOptions{ShouldTrip: func(err error) bool {
      return provider.IsRetryableError(err) || errors.Is(err, provider.ErrAuth)
  }}
  ```

- **成功不清空窗口**：失败记录只随时间滑出窗口，语义是"最近 Window 内累计
  FailureThreshold 次失败即视为该上游不可用"。
- **高 QPS 服务改用失败率判定**：`FailureThreshold` 是绝对次数，与流量规模无关。
  1 分钟窗口配 5 次失败，在 1000 QPS 下只要上游有 0.1% 的偶发错误率，
  窗口内就会累计到 5 次失败而跳闸——把 99.9% 本可成功的请求一起挡在本地。
  `ReadyToTrip` 按窗口内的成功/失败统计判定，与 QPS 解耦：

  ```go
  provider.BreakerOptions{
      // 窗口内至少 20 次调用、且失败率超过 50% 才跳闸
      ReadyToTrip: provider.FailureRateTrip(20, 0.5),
  }
  ```

  `minSamples` 是必需的下限保护——样本太少时失败率没有统计意义（只有一次调用
  且失败就是 100%），样本不足时一律不跳闸。需要更复杂的条件可自行实现该回调，
  入参 `BreakerCounts` 提供窗口内的 `Successes` / `Failures`，
  并带 `Total()` 与 `FailureRate()` 两个便捷方法。

  三点使用约定：

  - 配置 `ReadyToTrip` 后 **`FailureThreshold` 不再参与判定**（替代关系，非叠加）。
  - 与 `ShouldTrip` 的加锁语义相反：`ShouldTrip` 在锁外调用，回调内可以读熔断器
    状态；**`ReadyToTrip` 在锁内调用，回调内不得访问该熔断器**（调用 `State` /
    `Stats` / `Report` 会死锁）。判定所需信息已全部通过入参给出，回调应为纯函数。
  - 启用后熔断器同时记录成功与失败样本（分桶计数，内存与 QPS 无关，固定 32 个桶）；
    不配置时只记录失败时刻，行为与之前完全一致，`Stats().Successes` 恒为 0。

- **连续跳闸退避**：`BackoffReset` 内再次跳闸视为连续故障，冷却时长翻倍；
  超过 `BackoffReset` 才跳闸则退回 `OpenDuration` 重新起算。
- **流式只统计创建阶段**：`BreakerStreamMiddleware` 以"流是否创建成功"为口径；
  建流成功后中途断流不计入，需要覆盖时在 `Recv` 出错处自行调用 `breaker.Report(err)`。
- **装配位置**：熔断放在**每个上游成员**上（各自独立跳闸、独立恢复），
  不要包在整条降级链外——那样一个上游故障会把整条链熔断。
- **状态查询**：`breaker.State()` 与 `breaker.Stats()` 供健康检查与监控读取；
  确认上游已恢复（如换了新 key）时用 `breaker.Reset()` 手动放行。
- 状态保存在进程内存，不跨进程共享；多副本部署时每个副本独立熔断。
- `Embedder` 侧用 `WithEmbedderBreaker` / `BreakerEmbedMiddleware`，语义相同。

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

如果你希望在装饰阶段显式处理空值，改用 `TryWithMiddlewares` / `TryWithEmbedderMiddlewares`，由调用方直接接收 `ErrNilProvider` / `ErrNilEmbedder`。

### 错误处理：`ProviderError` + 8 个 Sentinel

除下面 8 个与 `*ProviderError` 互认的分类 sentinel 外，还有一个本地拦截类
sentinel：`ErrBreakerOpen`（熔断器打开，请求未发往平台，不是 `*ProviderError`，
用 `errors.Is` 判定）。它默认会触发降级链与均衡器的成员切换。

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

在服务启动时构造 Provider 并注入 handler。每次调用传入 `c.Request.Context()`，让请求取消传播到上游。业务负责认证、渠道选择、历史和额度；核心只接收已构造的 `ChatRequest`。

SSE 转发使用 `ChatStream`，持续读取到 EOF；收到 `FinishReason` 后仍需读取尾帧 Usage。客户端断开时关闭流，并处理 `Close` 错误。v2 的 `WithObservability` 提供流终止事件，应用通过自己的 context value 关联身份和调用 ID；示例见 [迁移说明](MIGRATION.md)。

## 各平台常用模型速查

> 以下为截至 **2026-09** 各厂商在线可调用的主流模型。模型名随平台更新变化较快，建议调用前对照官方文档；完整速查（含 Anthropic / Gemini / xAI 等）见 [`v2/README.md`](./v2/README.md#各平台常用模型速查)。

**DeepSeek**

| 模型名 | 说明 |
|--------|------|
| `deepseek-v4-flash` | V4 Flash，1M 上下文，思考由 `thinking` / `reasoning_effort` 参数控制 |
| `deepseek-v4-pro` | V4 Pro 旗舰 |

> `deepseek-chat` / `deepseek-reasoner` 已于 2026-07-24 停用。v1 的 `EnableThinking` 仍按旧口径下发，DeepSeek V4 的思考控制与工具循环所需的 `reasoning_content` 回传请使用 v2。

**通义千问（百炼 DashScope）**

| 模型名 | 说明 |
|--------|------|
| `qwen3.8-max` / `qwen3.8-flash` | 最新旗舰 / 轻量 |
| `qwen3.7-max` / `qwen3.7-plus` | 3.7 旗舰 / 高性价比（默认） |
| `qwen3-coder-plus` | 代码 Agent 专精 |
| `qwen-long` | 长文档处理，最高 10M tokens |

**智谱 AI**

| 模型名 | 说明 |
|--------|------|
| `glm-5.3` | 最新旗舰，1M 上下文，始终思考 |
| `glm-5.3-flash` | 原生多模态轻量版 |
| `glm-5.2` / `glm-5.1` | 上两代 |

**百度千帆**

| 模型名 | 说明 |
|--------|------|
| `ernie-5.1` | 文心 5.1 旗舰，原生全模态 |
| `ernie-5.0-thinking-preview` | 文心 5.0 思考版 |
| `ernie-x1.1-preview` | X1.1 推理模型 |

**硅基流动**

| 模型名 | 说明 |
|--------|------|
| `deepseek-ai/DeepSeek-V4-Flash` / `deepseek-ai/DeepSeek-V4-Pro` | DeepSeek V4 |
| `Pro/zai-org/GLM-5.2` | 智谱 GLM-5.2（Pro 付费通道） |
| `Qwen/Qwen3-Embedding-8B` | 千问 Embedding |

> `Pro/` 前缀为付费稳定通道，不带前缀为社区免费通道，能力相同但限流更严。

**Moonshot / Kimi**

| 模型名 | 说明 |
|--------|------|
| `kimi-k3` | 旗舰，1M 上下文，始终思考，采样参数固定 |
| `kimi-k2.7-code-highspeed` | 编程专用高速版 |
| `kimi-k2.6` | 通用模型（默认），思考可开关 |

**OpenAI**

| 模型名 | 说明 |
|--------|------|
| `gpt-6-astra` | 官方模型页当前列出的旗舰 |
| `gpt-5.6-sol` / `gpt-5.6-terra` / `gpt-5.6-luna` | 5.6 旗舰 / 均衡（默认）/ 低成本 |

> OpenAI 推理模型已拒绝 `max_tokens`；v1 仍下发该字段，需要 `MaxTokens` 的 OpenAI 调用请使用 v2（下发 `max_completion_tokens`）。

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
| `text-embedding-v4` | 最高 2048 | 默认推荐，多语言增强，支持 `dimensions` 截断 |
| `text-embedding-v3` | 1024 | 上一代，与 v4 维度兼容、可平滑迁移 |

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

### 便捷函数

```go
// Chat
provider.SimpleChat(ctx, p, "你好")                           // 一问一答
provider.SimpleChatWithSystem(ctx, p, "你是助手", "你好")       // 带 system prompt
provider.CollectStream(ctx, p, req, onChunkFn)                // 流式收集+回调

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

因为 OpenAI、本仓库内置的国内平台，本质上都走 OpenAI 兼容协议。给每个平台写一个 struct 是过度设计。Claude、Google Gemini 不是 OpenAI 兼容协议，因此分别提供 `NewAnthropicProvider` / `NewGeminiProvider` 原生 HTTP 实现，但仍复用同一套 `Provider` 接口。

**为什么不管理对话历史？**

对话历史的存储方式千差万别（内存、Redis、数据库），强行内置只会限制使用者。库只负责「发请求、收响应」，历史管理交给业务层。

**为什么 Temperature 和 TopP 是指针类型？**

因为这两个参数的零值 `0.0` 是有意义的（表示贪婪采样），用指针可以区分"未设置"和"设置为 0"。未设置时由各平台使用自己的默认值。

**工具执行失败如何处理？**

应用决定中止、重试或把经过脱敏的错误转换成工具结果；核心只负责传输结果消息。

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

> 说明：Claude / Google Gemini 已有内置原生 HTTP 实现。这个扩展示例主要面向其他不兼容 OpenAI 协议的平台。

### 可选扩展包怎么接

如果你要自己实现其他平台扩展包，推荐按下面的方式组织：

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

    "github.com/gtkit/go-llm-provider/provider"
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

    "github.com/gtkit/go-llm-provider/provider"
    "github.com/your-org/your-llm-extension/anthropicprovider"
)

func main() {
    claude, err := anthropicprovider.New(
        os.Getenv("ANTHROPIC_API_KEY"),
        "claude-sonnet-4-0",
    )
    if err != nil {
        fmt.Println("create provider:", err)
        return
    }

    p := claude

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    reply, err := provider.SimpleChat(ctx, p, "用一句话介绍 Go")
    if err != nil {
        fmt.Println("chat:", err)
        return
    }

    fmt.Println(reply)
}
```

实现时只要遵守这几个映射规则，主包里的辅助能力就能继续复用：

- 非流式响应把文本映射到 `provider.ChatResponse.Content`
- 如果厂商支持 tool use，把工具调用映射到 `provider.ChatResponse.ToolCalls`
- 流式响应把文本增量映射到 `provider.StreamChunk.Delta`
- 流结束时把厂商结束原因映射到 `provider.StreamChunk.FinishReason`
- 只要 `Chat` / `ChatStream` 的输出符合统一类型，`provider.CollectStream` 就能直接继续使用

## 运行测试

```bash
go test ./provider/ -v
```

发布前检查会分别验证根模块与 v2 子模块，并按核心 `provider` 包执行 80% 覆盖率门禁：

```bash
make release-check
```

真实接口 smoke test 默认跳过，只有设置对应环境变量时才会访问外部接口：

```bash
ANTHROPIC_API_KEY="<ANTHROPIC_API_KEY>" go test ./provider -run TestAnthropicSmoke -count=1 -v
GEMINI_API_KEY="<GEMINI_API_KEY>" go test ./provider -run TestGeminiSmoke -count=1 -v
ANTHROPIC_API_KEY="<ANTHROPIC_API_KEY>" GEMINI_API_KEY="<GEMINI_API_KEY>" go run ./example/native
```

不要把真实 API Key 写入源码、README、测试 fixture 或 shell history；本命令中的值只应使用临时密钥或本地安全注入方式。

## License

MIT
