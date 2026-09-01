# llm-provider

Go 语言统一多模型 LLM 调用库。一套代码接入 OpenAI 以及 DeepSeek、通义千问、智谱、百度千帆、硅基流动、Moonshot 等 OpenAI 兼容平台。

> 版本说明：仓库根目录维护 `v1` 代码线，服务存量下游——除缺陷修复外，可回移与类型演进解耦的可靠性能力（重试/降级/用量统计）；多模态、结构化输出、计费体系等类型演进类能力仅在 [`./v2/`](./v2/README.md) 主开发线。

## v2 比 v1 多了什么

新项目建议直接使用 `github.com/gtkit/go-llm-provider/v2`。相比 v1，v2 额外提供：

- 多模态消息：`Message.Content` 升级为 `[]ContentPart`，支持文本、图片 URL、图片 bytes、文件 bytes/URL/ID。
- Thinking / Reasoning：`ChatRequest.Thinking`（档位与 token 预算两套口径，覆盖 OpenAI / Ark / DeepSeek / Anthropic / Gemini）、`ChatResponse.Reasoning`、`StreamChunk.ReasoningDelta`、`Usage.ReasoningTokens`。
- Structured Output：`ResponseFormat`、`JSONObjectFormat`、`JSONSchemaFormatStrict` 与 `GenerateJSON` 类型化解码助手。
- 确定性与多候选：`Seed`、`CandidateCount`。
- Anthropic prompt caching：`WithCacheControl(..., CacheControlEphemeral())`。
- Gemini 原生 token counting：`TokenCounter` / `CountTokens`。
- 本地与企业入口：`ProviderOllama`、`NewAzureOpenAIProvider`、`NewBedrockOpenAIProvider`。
- 更多 OpenAI 兼容 preset：xAI、Groq、Mistral、Cohere。
- 更完整的横切能力：`ResponseMetadata`、`WithObservability`、客户端限流 `RateLimiter`（RPM / TPM 令牌桶 + 响应头自适应）、价格表原子热替换 `PricingRegistry`、重排序 `Reranker`。可靠性层的 `WithRetry`、`NewFallbackProvider`、`NewBreaker`、`NewBalancedProvider` 已回移 v1。
- 按用户计费全链路：流式/缓存/推理 token 统一统计、计费 hook 与用量查询、定价表与配额/余额硬限、上下文摘要压缩、流式工具循环、多模态输出（Gemini 图像）。

v1 继续保留兼容维护，并跟进与类型演进解耦的可靠性能力（重试、降级、熔断、负载均衡）；
多模态、结构化输出、本地推理、token counting、计费体系等依赖 `Message.Content` 类型演进
或观测基座的能力只在 v2 增加。

## 为什么做这个

国内主流大模型平台现在都兼容了 OpenAI Chat Completions 协议，本质上只是 BaseURL 和 APIKey 的差异。但每次接入新平台还是要翻文档查地址、记模型名、写一堆重复的初始化代码。

这个库做的事情很简单：

- 预置了各平台的 BaseURL 和推荐模型，传个 APIKey 就能用
- 统一的 `Provider` 接口，业务代码不需要关心底层是哪个平台
- `Registry` 注册表管理多个 Provider，运行时按名称切换
- 支持非流式和流式两种调用模式
- 完整的 Tool Use / Function Calling 支持，包含自动循环执行的 `RunToolLoop`
- 主包保持轻量，零额外厂商 SDK 依赖；OpenAI 兼容路径复用 `sashabaranov/go-openai`，非兼容路径用标准库 `net/http`

## 项目结构

```
llm-provider/
├── go.mod
├── README.md
├── CHANGELOG.md
├── provider/
│   ├── provider.go            # 核心：Provider 接口、Registry、请求/响应、Tool Use 类型
│   ├── presets.go             # 各平台预设配置（BaseURL + Chat/Embedding 默认模型）
│   ├── helpers.go             # Chat 便捷函数：SimpleChat、CollectStream
│   ├── toolrun.go             # RunToolLoop：Tool Use 自动循环执行器
│   ├── embedder.go            # Embedder 接口、请求/响应、openaiEmbedder 实现
│   ├── embedder_helpers.go    # Embedding 便捷函数：SimpleEmbed、EmbedBatch
│   ├── errors.go              # ProviderError / ErrorCode / WrapProviderError
│   ├── middleware.go          # Middleware / Handler 类型 + WithMiddlewares 装饰器
│   ├── retry.go               # WithRetry / RetryMiddleware / BackoffFunc
│   ├── fallback.go            # FallbackProvider 多 provider 失败切换
│   ├── breaker.go             # Breaker 熔断器：滑动窗口计数 + 指数退避冷却 + 半开探测
│   ├── balance.go             # BalancedProvider 加权负载均衡 + 故障转移
│   ├── provider_test.go       # Chat / Tool Use 单测
│   ├── embedder_test.go       # Embedding 单测
│   ├── errors_test.go         # ProviderError / ErrorCode / WrapProviderError 单测
│   ├── middleware_test.go     # Middleware 装饰器 + 洋葱顺序测试
│   ├── retry_test.go          # 重试与退避单测
│   ├── fallback_test.go       # 降级链切换单测
│   ├── breaker_test.go        # 熔断状态机 / 退避 / 中间件单测
│   ├── balance_test.go        # 权重分布 / 故障转移 / 策略单测
│   └── runtime_test.go        # 运行时集成测试
└── example/
    ├── main.go                # 基础使用示例（Chat）
    ├── native/main.go         # Claude / Gemini 原生 HTTP provider 示例
    ├── tooluse/main.go        # Tool Use 手动多轮示例
    ├── toolloop/main.go       # RunToolLoop 自动循环示例
    ├── toolsecurity/main.go   # 工具结果间接提示注入防护示例
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
| DeepSeek | `deepseek` | `https://api.deepseek.com/v1` | `deepseek-chat` | — | [platform.deepseek.com](https://platform.deepseek.com/) |
| 通义千问（百炼） | `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen3.6-plus` | `text-embedding-v3` | [百炼控制台](https://bailian.console.aliyun.com/) |
| 智谱 AI / GLM | `zhipu`（别名：`ProviderGLM`） | `https://open.bigmodel.cn/api/paas/v4/` | `glm-5.1` | `embedding-3` | [open.bigmodel.cn](https://open.bigmodel.cn/) |
| 百度千帆 | `qianfan` | `https://qianfan.baidubce.com/v2` | `ernie-4.5-turbo-32k` | `embedding-v1` | [千帆控制台](https://console.bce.baidu.com/qianfan/) |
| 硅基流动 | `siliconflow` | `https://api.siliconflow.cn/v1` | `deepseek-ai/DeepSeek-V3` | `BAAI/bge-m3` | [siliconflow.cn](https://siliconflow.cn/) |
| Moonshot / Kimi | `moonshot`（别名：`ProviderKimi`） | `https://api.moonshot.cn/v1` | `kimi-k2-turbo-preview` | — | [platform.moonshot.cn](https://platform.moonshot.cn/) |
| OpenAI | `openai` | `https://api.openai.com/v1` | `gpt-5.4-mini` | `text-embedding-3-small` | [platform.openai.com](https://platform.openai.com/) |
| Anthropic / Claude | `anthropic` | `https://api.anthropic.com` | `claude-sonnet-4-5` | — | [console.anthropic.com](https://console.anthropic.com/) |
| Google Gemini | `gemini` | `https://generativelanguage.googleapis.com/v1beta` | `gemini-2.5-flash` | — | [aistudio.google.com](https://aistudio.google.com/) |

> 预设地址和默认模型可能随平台更新而变化，建议定期对照各平台官方文档确认。
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
    "", // 留空使用 claude-sonnet-4-5
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
    Model:  "gemini-2.5-flash",
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
// 用千问的 qwen-max 模型而不是默认的 qwen3.6-plus
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

#### 内置重试与多 provider 降级

```go
// 重试：基于 ProviderError.Retryable（限流/超时/5xx/网络才重试）
p = provider.WithRetry(base, provider.RetryOptions{MaxAttempts: 3})

// 降级链：按序尝试，可自定义切换判定（如 key 失效、业务熔断错也切换）；
// 各成员在构造时配置模型，req.Model 留空（显式指定会覆盖所有成员、导致降级失效）
fb, _ := provider.NewFallbackProviderWithOptions([]provider.Provider{primary, backup},
    provider.FallbackOptions{ShouldFallback: func(err error) bool {
        return provider.IsRetryableError(err) || errors.Is(err, provider.ErrAuth)
    }})
```

ctx 取消/超时后两者都会立即停止，不再发起无意义的尝试。
`FallbackProvider` 可嵌套组合实现"厂商内穷尽 model 后再切厂商"的两级降级。
与 v2 的差异：v1 的重试不解析 `Retry-After` 响应头（统一按本地退避策略）。

```go
// 熔断：连续失败的上游进入冷却，冷却期内请求在本地快速失败、不发往平台
breaker := provider.NewBreaker(provider.BreakerOptions{Name: "deepseek"})
p = provider.WithBreaker(base, breaker)

// 加权负载均衡：按权重把流量分摊到多个 key / 地域，成员可各带独立熔断器
lb, _ := provider.NewBalancedProvider(
    provider.BalanceMember{Provider: keyA, Weight: 3, Breaker: provider.NewBreaker(provider.BreakerOptions{Name: "key-a"})},
    provider.BalanceMember{Provider: keyB, Weight: 1, Breaker: provider.NewBreaker(provider.BreakerOptions{Name: "key-b"})},
)
```

详见 [熔断](#熔断circuit-breaker) 与 [加权负载均衡](#加权负载均衡balancedprovider)。

> 生产注意：重试/降级为 **at-least-once** 语义——供应商已处理但响应丢失时，重试会再次真实调用，造成重复执行与重复计费（Chat 接口通常无幂等键，无法在客户端去重）。成本敏感场景需保守设置重试次数与超时，并结合供应商账单对账。

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

#### 工具结果加工与响应校验

工具执行结果如果来自外部不可信数据源（网页抓取、第三方 API、用户上传内容等），可能携带试图让模型偏离原任务的指令文本（间接提示注入）。`RunToolLoopOptions` 提供两个可选钩子，让调用方在结果进入对话历史前、以及最终响应返回前接入自己的处理逻辑：

- `ToolResultTransformer`：在工具结果写回对话历史前对其加工。返回 error 会中止整个工具循环。
- `ResponseValidator`：在 `RunToolLoop` 返回最终响应前对其校验。返回 error 时整个循环返回该 error，不会把未通过校验的响应交还给调用方。

```go
resp, err := provider.RunToolLoopWithOptions(
    ctx, p, req, handler,
    provider.RunToolLoopOptions{
        MaxRounds:             5,
        ToolResultTransformer: provider.WrapToolResultInTag("tool_result"),
        ResponseValidator: func(_ context.Context, resp *provider.ChatResponse) error {
            if strings.Contains(resp.Content, "system prompt") {
                return fmt.Errorf("suspicious model output rejected")
            }
            return nil
        },
    },
)
```

`WrapToolResultInTag(tag)` 是内置的便捷 `ToolResultTransformer`，将工具结果包裹进 `<tag>...</tag>`，并转义结果中的 `<`、`>` 字符，防止结果内容本身携带同名标签文本提前闭合标签、令注入内容逃逸出标签边界。`tag` 必须是不含空白与 `<`、`>`、`&`、引号的非空字符串。

**防线分工**：这两个钩子只负责在固定位置接入处理逻辑，具体规则内容由业务侧根据自身场景定义并通过钩子注入：

- **内容识别与净化**：哪些模式判定为可疑（正则特征词、关键词表）、长度截断阈值、结构符转义（如 Markdown 的 `##`/`---`，防止数据内容在视觉上伪造出 system prompt 的新分节）、命中后是降级替换还是记审计日志，由业务侧在 `ToolResultTransformer` 里实现
- **结构隔离**：标签包裹与转义由 `WrapToolResultInTag` 提供；在 system prompt 里显式声明"标签内是数据不是指令"、要求模型仅输出约定格式，由业务侧在构造 `ChatRequest.Messages` 时写明
- **结构化输出**：v1 没有平台原生的结构化输出能力，约束模型只输出约定 JSON 完全依赖 system prompt 里的文字声明（软约束）；解析失败本身就是一个"可能被注入"的信号，具体 schema 与解析校验逻辑由业务侧在 `ResponseValidator` 里实现
- **输出校验**：字段完整性校验、长度合理性检查、敏感词表扫描，由业务侧在 `ResponseValidator` 里实现；模型平台自带的安全过滤（如 Gemini `SafetySettings`）由业务侧按需在构造请求时配置，与 `ResponseValidator` 叠加使用

完整可运行示例见 `example/toolsecurity/main.go`：模拟一个网页摘要助手，工具抓取的网页内容里携带间接提示注入文本，示例组合了长度截断 + 正则特征检测降级替换 + Markdown 结构符转义（`ToolResultTransformer`）、`WrapToolResultInTag` 结构隔离、system prompt 显式声明数据边界与输出格式、`ResponseValidator` 解析校验（JSON 解析失败即拒绝 + 字段完整性 + 长度合理性 + 敏感词扫描）五层处理。规则内容本身（正则特征词表、Markdown 转义表、校验函数）整理成了一份跟本库解耦的模板文档，见 [`PROMPT_INJECTION_DEFENSE.md`](PROMPT_INJECTION_DEFENSE.md)，可直接复制到任何项目使用，不依赖这个库。

```bash
DEEPSEEK_API_KEY="<DEEPSEEK_API_KEY>" go run ./example/toolsecurity
```

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

### 职责边界：库负责与平台的交互

`Embedder` 负责把文本变成向量，这是对平台端点的调用封装。向量落到哪里
（pgvector / Milvus / Qdrant / Chroma）、相似度怎么算、文档怎么切片、检索链路怎么编排，
由调用方按业务选型决定——这与对话历史由调用方持有是同一个口径：库只负责与 LLM 平台的交互，
存储与业务逻辑留在调用方手里。

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

横切关注点走统一的装饰器 + Handler 抽象。本库内置了一组与业务口径无关的可靠性策略，
开箱可用；口径因项目而异的部分（日志格式、指标后端、审计存储、脱敏字段、缓存键）
由调用方用同一套 Middleware 类型自行实现，30 行以内即可。

内置策略：

| 能力 | 入口 | 说明 |
|------|------|------|
| 重试 | `WithRetry` / `RetryMiddleware` | 按 `ProviderError.Retryable` 判定，支持指数退避与全抖动 |
| 熔断 | `WithBreaker` / `BreakerMiddleware` | 滑动窗口计数 + 指数退避冷却 + 半开探测 |
| 降级 | `NewFallbackProvider` | 多成员按序切换 |
| 负载均衡 | `NewBalancedProvider` | 加权轮询 / 加权随机 / 加权最少在途 |

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
- **与降级链联动**：`ErrBreakerOpen` 已在默认切换判定内，熔断打开会立刻切到
  下一个成员，无需自定义 `ShouldFallback`。
- **装配位置**：熔断放在**每个上游成员**上（各自独立跳闸、独立恢复），
  不要包在整条降级链外——那样一个上游故障会把整条链熔断。
- **状态查询**：`breaker.State()` 与 `breaker.Stats()` 供健康检查与监控读取；
  确认上游已恢复（如换了新 key）时用 `breaker.Reset()` 手动放行。
- 状态保存在进程内存，不跨进程共享；多副本部署时每个副本独立熔断。
- `Embedder` 侧用 `WithEmbedderBreaker` / `BreakerEmbedMiddleware`，语义相同。

### 加权负载均衡（BalancedProvider）

降级链是"链首承担全部流量、失败才用下一个"；`BalancedProvider` 是"按权重分摊
流量"，适合多 key 分摊配额、多地域就近、按成本比例混流。故障转移语义与降级链一致。

```go
lb, err := provider.NewBalancedProviderWithOptions([]provider.BalanceMember{
    {
        Provider: keyA,
        Weight:   3,                                                    // 承担 3/4 流量
        Breaker:  provider.NewBreaker(provider.BreakerOptions{Name: "key-a"}),
    },
    {
        Provider: keyB,
        Weight:   1,
        Breaker:  provider.NewBreaker(provider.BreakerOptions{Name: "key-b"}),
    },
}, provider.BalanceOptions{
    Strategy:    provider.BalanceWeightedRoundRobin,
    MaxAttempts: 2, // 单次调用最多尝试 2 个成员，≤0 表示全部
})
if err != nil {
    log.Fatal(err)
}

resp, err := lb.Chat(ctx, req)
```

四种策略：

| 策略 | 行为 | 适用 |
|------|------|------|
| `BalanceWeightedRoundRobin`（默认） | 平滑加权轮询，按权重把流量均匀铺开，低权重成员插在中间而非攒到末尾 | 多 key 分摊配额 |
| `BalanceWeightedRandom` | 加权随机，长期分布与权重一致，短期可能连续命中同一成员 | 无状态、成员很多 |
| `BalanceLeastPending` | 加权最少在途，选 `在途数/权重` 最小的成员 | 各成员时延差异大 |
| `BalanceSessionAffinity` | 会话粘性，按会话键哈希稳定选中成员 | 多轮会话 + 提示词缓存 |

#### 会话粘性与提示词缓存

前三种策略把同一会话的多轮请求打散到不同成员，每个成员上游的提示词缓存都是冷的。
缓存命中的输入单价通常只有常规输入的一小部分，把长会话打散等于让这部分收益消失——
`BalanceSessionAffinity` 用来解决这个问题。

本代码线不内置会话标识，会话键一律由 `BalanceOptions.SessionKey` 提供
（缺失时构造返回 `ErrInvalidBalanceStrategy`，不会静默退化成普通轮询）：

```go
lb, err := provider.NewBalancedProviderWithOptions(members, provider.BalanceOptions{
    Strategy: provider.BalanceSessionAffinity,
    SessionKey: func(ctx context.Context, _ *provider.ChatRequest) string {
        return myapp.ConversationIDFromContext(ctx) // 业务自己的会话标识
    },
})
if err != nil {
    log.Fatal(err)
}
resp, err := lb.Chat(ctx, req)
```

行为约定：

- **按权重分配会话**：权重大的成员占据更宽的哈希区间、承载更多会话，
  粘的是"同一会话"，不是把所有流量压到一个成员。
- **哈希确定性**：不含随机种子，同一会话键在所有副本、进程重启前后都落到同一成员，
  多副本部署无需共享状态。
- **会话键为空**时该次调用退化为平滑加权轮询，不会恒选同一成员。
- **故障转移不受影响**：粘附成员失败或熔断时，从粘附位置环形向后取下一个未尝试的成员，
  顺序确定；转移后该轮请求落在冷缓存成员上，属于预期代价。
- **成员增减会重新分布会话**：哈希区间随权重总和变化，增删 key 后大部分会话会换成员、
  缓存需要重新预热。调整成员列表宜安排在低峰期。

- **成员级熔断**：`BalanceMember.Breaker` 由均衡器负责申请与上报，不要再用
  `WithBreaker` 包一层（会双重计数）。熔断打开的成员会以 `ErrBreakerOpen`
  快速失败并自动转移到下一个成员，冷却到期后自动恢复接流。
- **故障转移判定**同降级链：默认切换平台侧可重试错误与 `ErrBreakerOpen`，
  可用 `BalanceOptions.ShouldFallback` 覆盖。ctx 已取消/超时时立即返回、
  不再尝试后续成员。
- **`req.Model` 通常留空**（同降级链的理由），让各成员用自己的默认模型。
- **流式只在创建阶段转移**：打字机开始输出后中途断流不会切换。
- **`lb.Stats()`** 返回每个成员的权重、在途数与熔断状态，可直接喂给健康检查端点。
- `BalancedProvider` 本身实现 `Provider`，可与 `FallbackProvider`、`WithRetry`
  嵌套组合：常见装配是"均衡器内每个成员各自带 retry + 熔断"。

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

因为 OpenAI、本仓库内置的国内平台，本质上都走 OpenAI 兼容协议。给每个平台写一个 struct 是过度设计。Claude、Google Gemini 不是 OpenAI 兼容协议，因此分别提供 `NewAnthropicProvider` / `NewGeminiProvider` 原生 HTTP 实现，但仍复用同一套 `Provider` 接口。

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

    reg := provider.NewRegistry()
    reg.Register(claude)

    p, err := reg.Get("claude")
    if err != nil {
        fmt.Println("get provider:", err)
        return
    }

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
- 只要 `Chat` / `ChatStream` 的输出符合统一类型，`provider.CollectStream` 和 `provider.RunToolLoop` 就能直接继续使用

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
