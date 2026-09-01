# llm-provider

Go 语言统一多模型 LLM 调用库。一套代码接入 OpenAI 以及 DeepSeek、通义千问、智谱、百度千帆、硅基流动、Moonshot、火山方舟等 OpenAI 兼容平台。

## 为什么做这个

国内主流大模型平台现在都兼容了 OpenAI Chat Completions 协议，本质上只是 BaseURL 和 APIKey 的差异。但每次接入新平台还是要翻文档查地址、记模型名、写一堆重复的初始化代码。

这个库做的事情很简单：

- 预置了各平台的 BaseURL 和推荐模型，传个 APIKey 就能用
- 统一的 `Provider` 接口，业务代码不需要关心底层是哪个平台
- `Registry` 注册表管理多个 Provider，运行时按名称切换
- 支持非流式和流式两种调用模式
- 完整的 Tool Use / Function Calling 支持，包含自动循环执行的 `RunToolLoop`
- 厂商原生联网搜索工具透传（`WebSearchTool`，当前映射 Anthropic / Gemini），搜索次数进入统一计费口径
- OpenAI 兼容 Files API 文件管理（`FileService`：上传 / 内容抽取 / 删除），覆盖 Moonshot、通义千问、智谱的文档问答流程
- 主包保持轻量，零额外厂商 SDK 依赖；OpenAI 兼容路径复用 `sashabaranov/go-openai`，非兼容路径用标准库 `net/http`

## 项目结构

```
llm-provider/
├── go.mod
├── README.md
├── CHANGELOG.md
├── provider/
│   ├── provider.go            # 核心：Provider 接口、Registry、请求/响应、Tool Use 类型
│   ├── content.go             # Message 多模态 ContentPart 与便捷构造器
│   ├── files.go               # FileService：OpenAI 兼容 Files API（上传/抽取/删除）
│   ├── presets.go             # 各平台预设配置（BaseURL + Chat/Embedding 默认模型）
│   ├── helpers.go             # Chat 便捷函数：SimpleChat、CollectStream
│   ├── toolrun.go             # RunToolLoop：Tool Use 自动循环执行器
│   ├── reasoning.go           # Thinking 结构与推理模式常量
│   ├── response_format.go     # Structured Output 结构与构造器
│   ├── embedder.go            # Embedder 接口、请求/响应、openaiEmbedder 实现
│   ├── embedder_helpers.go    # Embedding 便捷函数：SimpleEmbed、EmbedBatch
│   ├── errors.go              # ProviderError / ErrorCode / WrapProviderError
│   ├── middleware.go          # Middleware / Handler 类型 + WithMiddlewares 装饰器
│   ├── retry.go               # WithRetry / RetryMiddleware / BackoffFunc
│   ├── fallback.go            # FallbackProvider 多 provider 失败切换
│   ├── breaker.go             # Breaker 熔断器：滑动窗口计数 + 指数退避冷却 + 半开探测
│   ├── ratelimit.go           # RateLimiter 客户端限流：RPM / TPM 令牌桶 + 响应头自适应
│   ├── balance.go             # BalancedProvider 加权负载均衡 + 故障转移
│   ├── pricing_registry.go    # PricingRegistry 价格表原子热替换
│   ├── reranker.go            # Reranker 接口：OpenAI 兼容 /rerank 端点
│   ├── observability.go       # WithObservability / ObserveEvent 观测 hook
│   ├── provider_test.go       # Chat / Tool Use 单测
│   ├── embedder_test.go       # Embedding 单测
│   ├── content_test.go        # ContentPart 构造器与映射测试
│   ├── reasoning_test.go      # Thinking / Reasoning 映射测试
│   ├── response_format_test.go # Structured Output 构造器与映射测试
│   ├── errors_test.go         # ProviderError / ErrorCode / WrapProviderError 单测
│   ├── middleware_test.go     # Middleware 装饰器 + 洋葱顺序测试
│   ├── breaker_test.go        # 熔断状态机 / 退避 / 中间件单测
│   ├── ratelimit_test.go      # 令牌桶 / 预扣结算 / 头自适应单测
│   ├── balance_test.go        # 权重分布 / 故障转移 / 策略单测
│   ├── pricing_registry_test.go # 价格热替换与并发计价单测
│   ├── reranker_test.go       # rerank 协议映射与边界单测
│   ├── observability_test.go  # 观测 hook 单测
│   └── runtime_test.go        # 运行时集成测试
└── example/
    ├── main.go                # 基础使用示例（Chat）
    ├── reasoning/main.go      # Thinking / Reasoning 示例
    ├── structured/main.go     # Structured Output 示例
    ├── vision/main.go         # Vision 多模态输入示例（text + image）
    ├── tooluse/main.go        # Tool Use 手动多轮示例
    ├── toolloop/main.go       # RunToolLoop 自动循环示例
    ├── toolsecurity/main.go   # 工具结果间接提示注入防护示例
    ├── middleware/main.go     # Middleware：Logging / TokenStats / Retry 参考实现
    ├── chatbilling/main.go    # 按用户计费端到端：计费 hook / 配额 / 余额硬限 / 流式工具循环 / 摘要压缩 / 账单
    ├── billingstore/          # 计费存储参考实现（Redis + GORM，独立 go.mod）
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
| 通义千问（百炼） | `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen3.6-plus` | `text-embedding-v3` | [百炼控制台](https://bailian.console.aliyun.com/) |
| 智谱 AI / GLM | `zhipu`（别名：`ProviderGLM`） | `https://open.bigmodel.cn/api/paas/v4/` | `glm-5.1` | `embedding-3` | [open.bigmodel.cn](https://open.bigmodel.cn/) |
| 百度千帆 | `qianfan` | `https://qianfan.baidubce.com/v2` | `ernie-4.5-turbo-32k` | `embedding-v1` | [千帆控制台](https://console.bce.baidu.com/qianfan/) |
| 硅基流动 | `siliconflow` | `https://api.siliconflow.cn/v1` | `deepseek-ai/DeepSeek-V3` | `BAAI/bge-m3` | [siliconflow.cn](https://siliconflow.cn/) |
| Moonshot / Kimi | `moonshot`（别名：`ProviderKimi`） | `https://api.moonshot.cn/v1` | `kimi-k2-turbo-preview` | — | [platform.moonshot.cn](https://platform.moonshot.cn/) |
| 火山方舟 / 豆包 | `ark` | `https://ark.cn-beijing.volces.com/api/v3` | `doubao-seed-2-0-pro-260215` | `doubao-embedding-text-240515` | [方舟控制台](https://console.volcengine.com/ark) |
| OpenAI | `openai` | `https://api.openai.com/v1` | `gpt-5.4-mini` | `text-embedding-3-small` | [platform.openai.com](https://platform.openai.com/) |
| Anthropic / Claude | `anthropic` | `https://api.anthropic.com` | `claude-sonnet-4-5` | — | [console.anthropic.com](https://console.anthropic.com/) |
| Google Gemini | `gemini` | `https://generativelanguage.googleapis.com/v1beta` | `gemini-2.5-flash` | `gemini-embedding-001` | [aistudio.google.com](https://aistudio.google.com/) |
| Ollama | `ollama` | `http://localhost:11434` | 需调用方指定 | — | 本地服务 |
| xAI / Grok | `xai` | `https://api.x.ai/v1` | `grok-4-1-fast-non-reasoning` | — | [console.x.ai](https://console.x.ai/) |
| Groq | `groq` | `https://api.groq.com/openai/v1` | `llama-3.3-70b-versatile` | — | [console.groq.com](https://console.groq.com/) |
| Mistral AI | `mistral` | `https://api.mistral.ai/v1` | `mistral-large-latest` | `mistral-embed` | [console.mistral.ai](https://console.mistral.ai/) |
| Cohere | `cohere` | `https://api.cohere.ai/compatibility/v1` | `command-a-03-2025` | `embed-v4.0` | [dashboard.cohere.com](https://dashboard.cohere.com/) |

> 预设地址和默认模型可能随平台更新而变化，建议定期对照各平台官方文档确认。
> Embedding 列显示"—"的平台表示官方暂无 embedding 接口，`NewEmbedderFromPreset` 会返回错误。
> Azure OpenAI 与 Amazon Bedrock 需要资源名、region 或 deployment/model ARN 等运行时信息，请使用 `NewAzureOpenAIProvider` / `NewBedrockOpenAIProvider` 显式创建。

### 能力矩阵

| 平台 | Chat | Streaming | Tools | Structured Output | Vision | File | File Upload | Reasoning | Embedding | Rerank | Web Search | 协议 |
|------|------|-----------|-------|-------------------|--------|------|-------------|-----------|-----------|------|------------|------|
| DeepSeek | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 是 | 否 | 否 | 否 | OpenAI 兼容 |
| 通义千问（百炼） | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 否 | 是 | 否 | 否 | OpenAI 兼容 |
| 智谱 AI / GLM | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 否 | 是 | 否 | 否 | OpenAI 兼容 |
| 百度千帆 | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 否 | 是 | 否 | 否 | OpenAI 兼容 |
| 硅基流动 | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 否 | 是 | 是 | 否 | OpenAI 兼容 |
| Moonshot / Kimi | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 否 | 否 | 否 | 否 | OpenAI 兼容 |
| 火山方舟 / 豆包 | 是 | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 是 | 否 | 否 | OpenAI 兼容 |
| OpenAI | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 是 | 是 | 否 | 否 | OpenAI 兼容 |
| Anthropic / Claude | 是 | 是 | 是 | 是 | 是 | 是 | 否 | 是 | 否 | 否 | 是 | 原生 HTTP |
| Google Gemini | 是 | 是 | 是 | 是 | 是 | 是 | 否 | 是 | 是 | 否 | 是 | 原生 HTTP |
| Ollama | 是 | 是 | 否 | 否 | 否 | 否 | 否 | 否 | 否 | 否 | 否 | 原生 HTTP |
| xAI / Grok | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 否 | 否 | 否 | 否 | OpenAI 兼容 |
| Groq | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 否 | 否 | 否 | 否 | OpenAI 兼容 |
| Mistral AI | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 否 | 是 | 否 | 否 | OpenAI 兼容 |
| Cohere | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 否 | 是 | 否 | 否 | OpenAI 兼容 |
| Azure OpenAI | 是 | 是 | 是 | 是 | 取决于部署模型 | 取决于部署模型 | 未验证 | 取决于部署模型 | 可自定义 | 否 | 否 | Azure OpenAI |
| Amazon Bedrock | 是 | 是 | 是 | 是 | 取决于模型 | 取决于模型 | 未验证 | 取决于模型 | 可自定义 | 否 | 否 | OpenAI 兼容 |

> 矩阵有两个口径，注意区分：**协议映射能力**（本库把请求字段翻译到该平台协议的能力，如图像 ContentPart 在 OpenAI 兼容平台会映射下发；文件 ContentPart 仅 Anthropic / Gemini 原生路径支持，OpenAI 兼容路径返回 `ErrInvalidRequest`）与**预设默认模型能力**（上表 Vision / File / Reasoning 等列描述的是各 preset 默认模型的已知能力，即 `ModelCapabilitiesFromPreset` 的返回值）。如果覆盖 `Model`，请以具体模型官方文档为准——协议映射仍然生效，模型不支持时由平台返回错误。
>
> **File 与 File Upload 是两个独立能力**：File 描述消息内文件片段的协议映射；File Upload（`CapabilityFileUpload`）描述平台是否提供 OpenAI 兼容 Files API（对应 `FileService` 接口，见[文件上传与文档问答](#文件上传与文档问答files-api)）。`FileService` 在所有 OpenAI 兼容 provider 上都可断言获取，实际能否调通取决于平台端点；标"未验证"的平台请以官方文档实测为准。

### 关于 Claude / Google Gemini

Claude 和 Gemini 不是 OpenAI 兼容协议，但本库不引入官方 SDK，而是用标准库 `net/http` 直接实现各自的原生 HTTP API：

- `ProviderAnthropic` 走 Anthropic Messages API：`POST /v1/messages`
- `ProviderGemini` 走 Gemini Generative Language API：`generateContent` / `streamGenerateContent`；`NewGeminiEmbedder` 走 `embedContent` / `batchEmbedContents`
- `NewGeminiEmbedder` 走 Gemini Embeddings API：`embedContent` / `batchEmbedContents`
- 两者都复用统一的 `Provider`、`ChatRequest`、`ChatResponse`、`StreamReader`、`ProviderError`、`ResponseMetadata`
- 当前 native 实现覆盖文本、图片、文件输入、非流式、SSE 流式、错误分类；`Chat` 已映射 Tool Use / Function Calling 与结构化输出
- Claude / Gemini native streaming 已开放 Tool Use 增量输出，调用方可从 `StreamChunk.ToolCalls` 累积工具调用参数
- Claude 支持内容片段级 `CacheControlEphemeral()`，用于 Anthropic prompt caching；不支持的平台会忽略该 hint

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
        provider.ProviderOpenAI:    os.Getenv("OPENAI_API_KEY"),
        provider.ProviderDeepSeek:  os.Getenv("DEEPSEEK_API_KEY"),
        provider.ProviderQwen:      os.Getenv("QWEN_API_KEY"),
        provider.ProviderZhipu:     os.Getenv("ZHIPU_API_KEY"),
        provider.ProviderAnthropic: os.Getenv("ANTHROPIC_API_KEY"),
        provider.ProviderGemini:    os.Getenv("GEMINI_API_KEY"),
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
- `ChatRequest.EnableThinking` 已被移除，请改用 `ChatRequest.Thinking = &provider.Thinking{...}`。
- 如需强制 JSON 输出，改用 `ChatRequest.ResponseFormat = provider.JSONObjectFormat()` 或 `provider.JSONSchemaFormatStrict(...)`。
- 新代码优先使用 `provider.AllPresets()` 读取预设；`provider.Presets` 仅为兼容旧代码保留。
- 如果你不希望 `QuickRegistry` 静默跳过失败项，请改用 `QuickRegistryStrict`。

| 项目 | v1 | v2 |
|------|----|----|
| Module Path | `github.com/gtkit/go-llm-provider` | `github.com/gtkit/go-llm-provider/v2` |
| `Message.Content` | `string` | `[]ContentPart` |
| 文本消息构造 | `Message{Role: ..., Content: "..."}` | `UserText(...)` / `SystemText(...)` / `AssistantText(...)` |
| 多模态输入 | 不支持 | 支持 `TextPart` / `ImageURLPart` / `ImageDataPart` / `FileDataPart` / `FileURLPart` / `FileIDPart` |
| Thinking 请求字段 | `EnableThinking bool` | `Thinking *Thinking` |
| Structured Output | 不支持 | `ResponseFormat *ResponseFormat` |
| 推理输出 | 只暴露最终 `Content` | 新增 `ChatResponse.Reasoning`、`StreamChunk.ReasoningDelta`、`Usage.ReasoningTokens` |
| 确定性与多候选 | 不支持 | `Seed` / `CandidateCount` |
| Prompt caching | 不支持 | `WithCacheControl(..., CacheControlEphemeral())`，当前映射 Anthropic |
| Token counting | 不支持 | `TokenCounter` / `CountTokens`，当前 Gemini / Anthropic 原生支持 |
| 原生联网搜索 | 不支持 | `WebSearchTool`，当前映射 Anthropic / Gemini，搜索用量计入 `Usage.WebSearchRequests` / `Usage.WebSearchGroundedPrompts`，元数据见 `ChatResponse.Search` |
| 本地推理 | 不支持 | `ProviderOllama` |
| 企业入口 | 不支持 | `NewAzureOpenAIProvider` / `NewBedrockOpenAIProvider` |
| 典型升级动作 | 原样继续用即可 | 需要改 import 路径、消息构造方式、thinking 配置方式 |

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
    provider.ProviderAnthropic:   os.Getenv("ANTHROPIC_API_KEY"),
    provider.ProviderGemini:      os.Getenv("GEMINI_API_KEY"),
    provider.ProviderXAI:         os.Getenv("XAI_API_KEY"),
    provider.ProviderGroq:        os.Getenv("GROQ_API_KEY"),
    provider.ProviderMistral:     os.Getenv("MISTRAL_API_KEY"),
    provider.ProviderCohere:      os.Getenv("COHERE_API_KEY"),
})

// 默认 provider 按成功注册的 ProviderName 排序后取第一个
p, err := reg.Default()
```

如果你希望注册失败时立刻拿到错误，改用 `QuickRegistryStrict`：

```go
reg, err := provider.QuickRegistryStrict(map[provider.ProviderName]string{
    provider.ProviderOpenAI:    os.Getenv("OPENAI_API_KEY"),
    provider.ProviderDeepSeek:  os.Getenv("DEEPSEEK_API_KEY"),
    provider.ProviderQwen:      os.Getenv("QWEN_API_KEY"),
    provider.ProviderAnthropic: os.Getenv("ANTHROPIC_API_KEY"),
    provider.ProviderGemini:    os.Getenv("GEMINI_API_KEY"),
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

适合私有部署、自建推理服务、或新平台接入。自定义接入的平台若接受 OpenAI 标准的
`reasoning_effort`，加上 `SupportsReasoningEffort: true` 即可使用 `Thinking.Effort`
（见[自定义接入的平台](#自定义接入的平台)）。

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

### Claude / Gemini 原生 HTTP Provider

Claude 和 Gemini 可以直接走预设：

```go
claude, err := provider.NewProviderFromPreset(
    provider.ProviderAnthropic,
    os.Getenv("ANTHROPIC_API_KEY"),
    "", // 留空使用 claude-sonnet-4-5
)

gemini, err := provider.NewProviderFromPreset(
    provider.ProviderGemini,
    os.Getenv("GEMINI_API_KEY"),
    "", // 留空使用 gemini-2.5-flash
)
```

如果要覆盖 BaseURL 或注入自定义 HTTP client，使用原生构造函数：

```go
claude, err := provider.NewAnthropicProvider(provider.NativeProviderConfig{
    APIKey:     os.Getenv("ANTHROPIC_API_KEY"),
    Model:      "claude-sonnet-4-5",
    HTTPClient: httpClient,
})

gemini, err := provider.NewGeminiProvider(provider.NativeProviderConfig{
    APIKey:  os.Getenv("GEMINI_API_KEY"),
    BaseURL: "https://generativelanguage.googleapis.com/v1beta",
    Model:   "gemini-2.5-flash",
})
```

当前原生实现支持文本、图片、文件输入、非流式、SSE 流式、request id 元数据与错误分类。`Chat` 已支持 Tool Use / Function Calling 和结构化输出；流式 Tool Use 会通过 `StreamChunk.ToolCalls` 暴露增量工具调用。

### Azure OpenAI / Amazon Bedrock

Azure OpenAI 和 Amazon Bedrock 都需要运行时资源信息，不适合放进固定 preset。请使用专用构造函数：

```go
azure, err := provider.NewAzureOpenAIProvider(provider.AzureOpenAIConfig{
    APIKey:     os.Getenv("AZURE_OPENAI_API_KEY"),
    Endpoint:   "https://example.openai.azure.com",
    Deployment: "gpt-4o-mini",
})

bedrock, err := provider.NewBedrockOpenAIProvider(provider.BedrockOpenAIConfig{
    APIKey: os.Getenv("BEDROCK_API_KEY"),
    Region: "us-east-1",
    Model:  "anthropic.claude-sonnet-4-5-20250929-v1:0",
})
```

### Ollama 本地 Provider

Ollama 走本地 `/api/chat`，不需要 API key。由于本地可用模型取决于调用方机器，`Model` 必须显式传入：

```go
ollama, err := provider.NewOllamaProvider(provider.OllamaProviderConfig{
    BaseURL: "http://localhost:11434", // 可省略
    Model:   "llama3.2",
})
```

`NewProviderFromPreset(provider.ProviderOllama, "", "llama3.2")` 也可以创建本地 provider；`QuickRegistry` 仍按 API key 注册云平台，空 key 会跳过 Ollama。

### 本地 LLM 接入（Ollama / vLLM / LM Studio / LocalAI / llama.cpp）

本地模型有两条接入路径，按你跑的本地服务**暴露的协议**选择即可，业务代码拿到的都是统一的 `provider.Provider`：

| 本地服务 | 暴露协议 | 推荐接入方式 | BaseURL（默认） |
|---------|---------|-------------|----------------|
| Ollama | 原生 `/api/chat` | `NewOllamaProvider` / `ProviderOllama` 预设 | `http://localhost:11434` |
| Ollama | OpenAI 兼容 `/v1` | `NewProvider`（自定义 BaseURL） | `http://localhost:11434/v1` |
| vLLM | OpenAI 兼容 `/v1` | `NewProvider` | `http://localhost:8000/v1` |
| LM Studio | OpenAI 兼容 `/v1` | `NewProvider` | `http://localhost:1234/v1` |
| LocalAI | OpenAI 兼容 `/v1` | `NewProvider` | `http://localhost:8080/v1` |
| llama.cpp（`llama-server`） | OpenAI 兼容 `/v1` | `NewProvider` | `http://localhost:8080/v1` |

> 经验法则：服务文档里出现 `/v1/chat/completions` 就走**路径 B**（OpenAI 兼容）；只有 Ollama 的原生 `/api/chat` 走**路径 A**。两条路径功能等价，原生路径少一层兼容层，OpenAI 兼容路径覆盖面更广。

#### 路径 A：原生 Ollama

```go
// Ollama 原生 /api/chat，无需 API key；Model 必须显式指定（取决于你 ollama pull 了哪些模型）。
ollama, err := provider.NewOllamaProvider(provider.OllamaProviderConfig{
    BaseURL: "http://localhost:11434", // 可省略，默认即此值
    Model:   "llama3.2",
})
if err != nil {
    log.Fatal(err)
}

// 本地推理可能较慢，超时交给 context 控制（本库刻意不设 http.Client.Timeout）。
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
defer cancel()

answer, err := provider.SimpleChat(ctx, ollama, "用一句话解释什么是向量数据库")
if err != nil {
    log.Fatal(err)
}
fmt.Println(answer)
```

#### 路径 B：OpenAI 兼容服务（vLLM / LM Studio / LocalAI / llama.cpp / Ollama 的 /v1）

任何兼容 OpenAI Chat Completions 协议的本地服务，都用 `NewProvider` + 自定义 `BaseURL` 接入，**不需要为它单独写 preset**：

```go
p, err := provider.NewProvider(provider.ProviderConfig{
    Name:    "local-vllm",                    // 自定义名称，仅用于 Registry 区分与日志，不影响请求
    BaseURL: "http://localhost:8000/v1",      // 指向本地服务的 OpenAI 兼容端点，注意通常要带 /v1
    APIKey:  "not-needed",                    // 多数本地服务不校验 key，但本库要求非空，随便填一个占位即可
    Model:   "Qwen2.5-72B-Instruct",          // 你实际部署/加载的模型名，必须和服务端一致
})
if err != nil {
    log.Fatal(err)
}

ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
defer cancel()

resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.SystemText("你是一个简洁的中文助手"),
        provider.UserText("用一句话介绍 Go 的 goroutine"),
    },
})
if err != nil {
    log.Fatal(err)
}
fmt.Println(resp.Content)
fmt.Println(resp.Usage.TotalTokens) // 本地服务若不返回 usage，这里可能为 0
```

各服务的差异只在 `BaseURL`、`APIKey`、`Model` 三个字段：

```go
// LM Studio：本地加载模型后开启 "Local Server"，端点默认 1234
lmStudio := provider.ProviderConfig{
    Name:    "lmstudio",
    BaseURL: "http://localhost:1234/v1",
    APIKey:  "lm-studio",                     // 占位即可
    Model:   "qwen2.5-7b-instruct",           // 用 LM Studio 里显示的模型标识
}

// LocalAI：端点默认 8080
localAI := provider.ProviderConfig{
    Name:    "localai",
    BaseURL: "http://localhost:8080/v1",
    APIKey:  "localai",
    Model:   "gpt-4",                         // LocalAI 用别名映射到本地模型文件
}

// llama.cpp 自带的 llama-server：端点默认 8080
llamaCpp := provider.ProviderConfig{
    Name:    "llamacpp",
    BaseURL: "http://localhost:8080/v1",
    APIKey:  "no-key",
    Model:   "default",                       // llama-server 只加载一个模型，名称随意
}

// Ollama 也可以走它的 OpenAI 兼容端点（注意比原生多了 /v1）
ollamaCompat := provider.ProviderConfig{
    Name:    "ollama-openai",
    BaseURL: "http://localhost:11434/v1",
    APIKey:  "ollama",
    Model:   "llama3.2",
}
```

#### 流式与自定义 HTTP client

本地服务同样支持流式；超时仍由 `context` 控制：

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

stream, err := p.ChatStream(ctx, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("写一首关于秋天的短诗")},
})
if err != nil {
    log.Fatal(err)
}
defer func() { _ = stream.Close() }()

for {
    chunk, err := stream.Recv()
    if errors.Is(err, io.EOF) {
        break // 流正常结束
    }
    if err != nil {
        log.Fatal(err)
    }
    fmt.Print(chunk.Delta) // 增量文本，逐段打印
}
```

需要为局域网内的远程主机调长拨号超时、配置代理或复用连接池时，注入自定义 `HTTPClient`（实现 `provider.HTTPDoer` 接口，`*http.Client` 即满足）：

```go
p, err := provider.NewProvider(provider.ProviderConfig{
    Name:    "remote-vllm",
    BaseURL: "http://192.168.1.100:8000/v1", // 局域网内另一台推理机
    APIKey:  "no-key",
    Model:   "Qwen2.5-72B-Instruct",
    HTTPClient: &http.Client{
        // 注意：不要在这里设 Timeout（会截断长响应），整体超时用 context 控制；
        // 这里只调传输层参数。
        Transport: &http.Transport{
            MaxIdleConnsPerHost: 4,
        },
    },
})
```

#### 注意事项

- **`Model` 必须和服务端一致**：本地服务不会像云平台那样有"默认模型"，填错模型名通常直接报 404 / model not found。
- **`APIKey` 不能留空**：本库构造时要求非空，本地服务多数不校验，填任意占位字符串即可。
- **超时用 `context`**：本库刻意不设 `http.Client.Timeout`，避免截断本地大模型的慢响应；请求级超时一律用 `context.WithTimeout`。
- **能力差异**：本地模型对 Tool Use、结构化输出、Vision 的支持取决于模型与服务实现，不要假设和云平台一致；不支持时可能返回错误或忽略相关字段。
- **`Usage` 可能为 0**：部分本地服务不回传 token 统计，`resp.Usage` 为空属正常。
- **横切能力照常可用**：`WithRetry`、`NewFallbackProvider`、`WithObservability` 对本地 provider 一视同仁，可把本地模型作为云模型的 fallback，或反之。

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

#### PromptTask — 可复用的单轮任务（润色/翻译/摘要）

把某类单轮任务的 system prompt 构造逻辑与常用参数绑定成一个可复用值，调用时按运行时参数拼装 system prompt。适合翻译（目标语言）、润色（风格）这类同一套 prompt 骨架、只是变量不同的场景。

```go
type TranslateParams struct {
    TargetLang string
}

var Translate = provider.PromptTask[TranslateParams]{
    System: func(p TranslateParams) string {
        return fmt.Sprintf("你是专业翻译，请将用户输入翻译成%s，只返回译文", p.TargetLang)
    },
}

reply, err := Translate.Run(ctx, p, TranslateParams{TargetLang: "英文"}, "今天天气真好")
```

> **安全提示**：`System` 闭包完全由调用方实现，库不对拼接内容做任何检测或转义；如果 `TargetLang` 这类字段直接来自用户输入并原样拼进 system prompt，就是把不可信数据混入 system prompt——这条路径是一次性 `Chat` 调用，不经过 `RunToolLoop` 的 `ToolResultTransformer` / `ResponseValidator` 钩子。优先用受限枚举/白名单校验这类字段的取值；确需接受自由文本，参照 [`PROMPT_INJECTION_DEFENSE.md`](../PROMPT_INJECTION_DEFENSE.md#prompttask-另一类混入路径不经过-runtoolloop-的钩子) 在闭包内部净化后再拼接。不可信的正文内容应始终走 `input` 参数（对应 user message），不要拼进 `System`。

不需要运行时参数的任务可将参数类型设为 `struct{}`：

```go
var Polish = provider.PromptTask[struct{}]{
    System: func(struct{}) string { return "你是文案润色助手，只返回润色后的文本" },
}

reply, err := Polish.Run(ctx, p, struct{}{}, rawText)
```

需要结构化输出时用 `RunPromptTaskJSON`，请求会按 `GenerateJSON` 规则自动补 `ResponseFormat`：

```go
type TranslateResult struct {
    Text string `json:"text"`
}

result, resp, err := provider.RunPromptTaskJSON[TranslateParams, TranslateResult](
    ctx, Translate, p, TranslateParams{TargetLang: "英文"}, "今天天气真好",
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
    CandidateCount: 2, // 请求多个候选，provider 不支持时可能忽略或返回错误
})

fmt.Println("回复:", resp.Content)
fmt.Printf("Token: prompt=%d, completion=%d, total=%d\n",
    resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
```

需要确定性采样时设置 `Seed`：

```go
seed := 42
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("给我一个测试用标题")},
    Seed:     &seed,
})
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

#### 流式 token 用量统计

流式调用的完整 token 统计通过流尾部 chunk 的 `Usage` 字段给出：

- **Anthropic / Gemini / Ollama**：随 `FinishReason` 非空的 chunk 一并给出；
- **OpenAI 兼容**：本库自动开启 `stream_options.include_usage`，统计位于 `FinishReason` 之后、`io.EOF` 之前的收尾 chunk（该 chunk 无文本增量）。

因此需要统计用量（如按 token 计费）时，**必须读取至 `io.EOF`**，并采用最后一个非零 `Usage`：

```go
var usage provider.Usage
for {
    chunk, err := stream.Recv()
    if err != nil {
        if err == io.EOF {
            break
        }
        log.Fatal(err)
    }
    if chunk.Usage != (provider.Usage{}) {
        usage = chunk.Usage
    }
    fmt.Print(chunk.Delta)
}
fmt.Printf("prompt=%d completion=%d total=%d\n",
    usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
```

注意：若流被提前 `Close` 或因网络中断/上下文取消未读到流尾，`Usage` 将拿不到（provider 侧仍可能已产生消耗）。计费场景应把"流异常终止且 usage 为零"当作漏单信号单独处理（如按已收文本估算或标记对账），配合下文观测 Hook 的 `stream_complete` 事件可统一捕获这类情况。

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

#### CollectStreamResult — 收集完整结果（含 Usage）

需要 token 计费或展示用量时，用 `CollectStreamResult` 一次拿到完整文本、推理内容与最终统计：

```go
result, err := provider.CollectStreamResult(ctx, p, req, func(delta string) {
    fmt.Print(delta)
})
// result.Content / result.Reasoning / result.FinishReason / result.Usage
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

### 原生联网搜索（Web Search）

Anthropic 与 Gemini 提供平台服务端执行的联网搜索工具：模型自行决定何时搜索、搜索结果直接参与生成，无需自建搜索 API 与工具循环。用 `WebSearchTool()` 声明：

```go
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("Go 1.26 有哪些新特性？")},
    Tools: []provider.Tool{
        provider.WebSearchTool(),
        // 需要限制搜索行为时（仅 Anthropic 支持；Gemini 收到非零选项返回
        // ErrInvalidRequest，不会静默忽略你的安全与费用约束）：
        // provider.WebSearchToolWithOptions(provider.WebSearchOptions{
        //     MaxUses:        3,
        //     AllowedDomains: []string{"go.dev"}, // 与 BlockedDomains 互斥
        // }),
    },
})
if err != nil {
    log.Fatal(err)
}
fmt.Println(resp.Content)
if resp.Search != nil {
    fmt.Println("搜索查询:", resp.Search.Queries)
    for _, source := range resp.Search.Sources {
        fmt.Println("来源:", source.Title, source.URL)
    }
}
```

要点：

- **平台映射**：Anthropic 映射为 `web_search_20250305` server tool；Gemini 映射为 `google_search`（Grounding with Google Search）。其他 provider（含全部 OpenAI 兼容平台与 Ollama）收到该工具返回 `ErrInvalidRequest`，不静默丢弃；切换平台前可用 `caps.Supports(provider.CapabilityWebSearch)` 预检。
- **仅保证单轮**：Anthropic 的 `AssistantMessage()` 不保存服务端工具块、引用与 `encrypted_content`，无法无损回传续跑，因此原生搜索当前**仅保证单轮**（一次 `Chat`/`ChatStream`）；多轮追问请每轮重新发起，或改用函数工具模式。
- **不能与函数工具混用**：Anthropic 的服务端工具块尚不支持跨轮往返、Gemini 2.5 系平台不支持 `google_search` 与函数声明同用，两条路径都在客户端返回 `ErrInvalidRequest`；Anthropic 侧同理禁止与结构化输出组合。Gemini 3 官方已支持搜索与函数工具组合，但本库当前不做模型嗅探、统一按 2.5 系口径拒绝，该组合待后续版本支持。纯搜索场景与 `RunToolLoop` 兼容（服务端执行，不产生客户端 `ToolCall`）。
- **输入约束**：纯搜索请求的 `ToolChoice` 只接受 `nil` / `ToolChoiceAuto`（由模型决定是否搜索，Gemini 侧此时不下发 `functionCallingConfig`）；重复声明搜索工具、同一 `Tool` 同时设 `Function` 与 `WebSearch`、`ToolChoiceRequired`/`ToolChoiceFunction`/`ToolChoiceNone` 与服务端搜索工具组合、Gemini 搜索叠加 `CandidateCount > 1`，均返回 `ErrInvalidRequest`。
- **pause_turn**：Anthropic 在服务端工具长时间运行时可能暂停回合（`stop_reason: "pause_turn"`），续跑需要回传服务端工具块，当前不支持——返回 `*PauseTurnError`（`errors.Is` 命中 `ErrUnsupportedCapability`）。**不要原样重发请求**，那会重新执行搜索、二次计费；应降低 `MaxUses` 或改用函数工具模式。该错误携带暂停前已产生的 `Usage` 与 `Search`，观测/计费层会自动提取用量，不会漏账。
- **搜索元数据与错误**：查询、来源、Google Search 入口通过 `ChatResponse.Search` / 最终 `StreamChunk.Search` 返回（`SearchMetadata`）；平台经 HTTP 200 报告的搜索失败（如 `max_uses_exceeded`、`too_many_requests`）进入 `SearchMetadata.Errors`，调用方据此区分"未搜索"与"搜索失败"，本库不静默丢弃。**合规提示**：Gemini 响应携带 `SearchEntryPoint` 时，Google 要求向终端用户展示 Search Suggestions；回复文本级的引用区间暂不透出。
- **计费口径（双口径二选一）**：`Usage.WebSearchRequests` 承载"按次数"口径（Anthropic 实际搜索次数 / Gemini 去空去重后的 query 数），`Usage.WebSearchGroundedPrompts` 承载"按 grounded prompt"口径（Gemini，0 或 1）。费率按平台计费规则在 `ModelRate.WebSearchPer1K` 与 `ModelRate.GroundedPromptPer1K` 中**二选一**配置（微元/1000 次）：Anthropic 与 Gemini 3 系按次数配前者，Gemini 2.5 系按 grounded prompt 配后者。双配无条件返回 `ErrInvalidPricing`（配置错误，启动期可用 `PricingTable.Validate()` 提前发现），全缺或口径与用量不符返回 `ErrModelNotPriced`，不静默漏账。
- **流式**：搜索过程中的服务端工具事件不会出现在 `StreamChunk.ToolCalls` 中，搜索计次与元数据随最终 chunk（`FinishReason` 非空）给出。

#### 国内平台的联网搜索接入（函数工具模式，推荐）

国内平台（智谱、千帆、百炼等）的内置联网搜索依赖各家私有请求字段，当前
`go-openai v1.41.2` 的请求结构没有承载这些字段的位置，本库不做映射（OpenAI
兼容协议本身允许厂商扩展字段，限制来自客户端结构体而非协议本身；
`CapabilityWebSearch` 表示"库内置的厂商原生搜索映射"，不代表业务侧不能接搜索）。
推荐做法是把搜索定义成**标准函数
工具**，由业务侧 `ToolHandler` 调用搜索 API（智谱独立 Web Search API
`/api/paas/v4/web_search`、千帆 AI Search `/v2/ai_search/web_search` 等），
搜索结果与来源链接作为工具结果回传模型：

```go
searchTool := provider.Tool{
    Function: provider.FunctionDef{
        Name:        "web_search",
        Description: "搜索互联网并返回相关资料与来源链接",
        Parameters: provider.ParamSchema{
            Type: "object",
            Properties: map[string]provider.ParamSchema{
                "query": {Type: "string", Description: "搜索关键词"},
            },
            Required: []string{"query"},
        },
    },
}

resp, err := provider.RunToolLoop(ctx, p, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("搜索并介绍 Go 1.26 的主要变化")},
    Tools:    []provider.Tool{searchTool},
}, 5, func(ctx context.Context, name, arguments string) (string, error) {
    if name != "web_search" {
        return "", fmt.Errorf("不支持的工具：%s", name)
    }
    // 解析 arguments 后调用智谱 / 千帆 / 博查等搜索客户端，
    // 返回 JSON 字符串（含标题、URL、摘要）。
    return searchService.SearchJSON(ctx, arguments)
})
```

这种模式的优点：不修改统一 `Provider` API、所有平台共用同一个 `web_search`
工具、域名白名单/超时/结果数量/缓存/费用全部由业务层统一控制、来源链接
完整保留可返回给终端用户。实践注意：

- 工具结果**保留标题与 URL**，不要只回摘要——模型引用与用户溯源都依赖来源；
- **限制条数与单条摘要长度**，不要塞整页正文，避免大量占用输入 token；
- 千帆若需要"搜索＋回答"一步完成，可直接调用其 AI Search Chat Completions，
  无需进入 `RunToolLoop`；
- 确需使用厂商 Chat API 内置搜索（如智谱私有 `web_search` 工具、百炼
  `enable_search`）时，在业务侧单独实现原生 HTTP 客户端，不要把私有字段
  扩散进统一的 `ChatRequest`。

### 多模态输入（图像 / 文件）

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

如果模型支持文件输入，可以使用文件 part。Claude 原生 provider 会把 `FileDataPart` 映射为 document block；Gemini 原生 provider 会把 inline file 映射为 `inline_data`。OpenAI 兼容 Chat Completions 路径会对 file part 返回 `ErrInvalidRequest`，避免发送未定义格式——OpenAI 兼容平台的文档问答请使用下文的 [Files API 流程](#文件上传与文档问答files-api)。

```go
pdfBytes, _ := os.ReadFile("brief.pdf")

resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserMessage(
            provider.TextPart("总结这份 PDF"),
            provider.FileDataPart(pdfBytes, "application/pdf", "brief.pdf"),
        ),
    },
})
```

Anthropic prompt caching 使用内容片段级 hint：

```go
resp, err := claude.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserMessage(
            provider.WithCacheControl(
                provider.TextPart("这里是很长的系统资料或文档上下文"),
                provider.CacheControlEphemeral(),
            ),
            provider.TextPart("基于上面的资料回答问题"),
        ),
    },
})
```

### 文件上传与文档问答（Files API）

OpenAI 兼容 Chat Completions 的消息里没有标准的文件片段，国内平台的文档问答走"先上传、再引用"：文件通过平台 Files API 上传，再按各平台约定进入对话。本库在 OpenAI 兼容 provider 上提供 `FileService` 文件管理接口：

```go
// NewProvider / NewProviderFromPreset / NewAzureOpenAIProvider /
// NewBedrockOpenAIProvider 返回的 Provider 都实现 FileService
fs, ok := p.(provider.FileService)
if !ok {
    // 原生 provider（Anthropic / Gemini / Ollama）不提供 Files API
}

type FileService interface {
    UploadFile(ctx context.Context, req *FileUploadRequest) (*FileObject, error)
    FileContent(ctx context.Context, fileID string) (string, error)
    DeleteFile(ctx context.Context, fileID string) error
}
```

> 注意：`WithRetry` / `WithMiddlewares` / `NewFallbackProvider` 等包装后的 Provider 不再实现 `FileService`，请在包装前保留原始句柄用于文件操作。平台是否提供 Files API 以能力矩阵 File Upload 列与平台官方文档为准（如 DeepSeek 无此接口，调用会返回平台侧错误）。

**流程一：内容抽取（Moonshot / 智谱）**——上传后用 `FileContent` 拉取平台抽取好的文档文本，作为 system 消息传入：

```go
fs := p.(provider.FileService)

file, err := fs.UploadFile(ctx, &provider.FileUploadRequest{
    Filename: "brief.pdf",
    Data:     pdfBytes,
    Purpose:  provider.FilePurposeFileExtract,
})
if err != nil {
    log.Fatal(err)
}
defer fs.DeleteFile(ctx, file.ID) // 平台限制文件保有量，抽取完成后及时清理

content, err := fs.FileContent(ctx, file.ID) // 平台抽取后的文档文本
if err != nil {
    log.Fatal(err)
}

resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.SystemText(content),
        provider.UserText("总结这份文件的要点"),
    },
})
```

**流程二：fileid:// 引用（通义千问 qwen-long）**——上传后把文件 ID 写进 system 消息，由平台侧按需读取，无需拉回全文：

```go
file, err := fs.UploadFile(ctx, &provider.FileUploadRequest{
    Filename: "report.docx",
    Data:     docBytes,
    Purpose:  provider.FilePurposeFileExtract,
})
if err != nil {
    log.Fatal(err)
}

resp, err := p.Chat(ctx, &provider.ChatRequest{
    Model: "qwen-long", // 千问文档问答需使用 qwen-long 系列模型
    Messages: []provider.Message{
        provider.FileIDSystemMessage(file.ID), // 多文件：FileIDSystemMessage(id1, id2, ...)
        provider.UserText("这份报告的结论是什么？"),
    },
})
```

**方舟（Ark）适用边界**——方舟提供 OpenAI 兼容的 `/files` 端点（上传 / 元数据检索 / 列表 / 删除，purpose 仅接受 `user_data` / `agent`），`UploadFile` / `DeleteFile` 协议上可以调通；但平台没有 `/files/{id}/content` 内容抽取端点（`FileContent` 会返回平台侧错误），Chat Completions 消息中也无法引用已上传的文件——方舟的文件输入挂在其 Responses API 下，本库不覆盖。因此上述两种文档问答流程均不适用于方舟，能力矩阵中方舟的 File Upload 列标注"否"即此含义。

行为约定：

- `FileUploadRequest.Purpose` 必填：国内文档问答平台使用 `FilePurposeFileExtract`；OpenAI 官方按场景选择 `FilePurposeUserData` / `FilePurposeAssistants` / `FilePurposeBatch`
- 文件操作的错误与 Chat 走同一错误体系（`ProviderError`，可用 `errors.Is(err, provider.ErrAuth)` 等判断类别）
- 消息内文件片段（`FileDataPart` / `FileURLPart` / `FileIDPart`）仍仅支持 Claude / Gemini 原生路径；OpenAI 兼容路径返回 `ErrInvalidRequest`
- 切换平台前可用 `ModelCapabilitiesFromPreset(name).Supports(provider.CapabilityFileUpload)` 预检

### 多模态输出（图像 / 文件）

通过 `OutputModalities` 声明期望的输出模态，非文本结果经 `ChatResponse.Parts` /
`StreamChunk.Parts` 返回（复用 `ContentPart` 载体，图像为 `ImageData` + `MIMEType`）：

```go
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages:         []provider.Message{provider.UserText("画一只橘猫")},
    OutputModalities: []provider.Modality{provider.ModalityText, provider.ModalityImage},
})
fmt.Println(resp.Content) // 文本部分
for _, part := range resp.Parts {
    os.WriteFile("cat.png", part.ImageData, 0o644) // 图像部分（已解码）
}
// resp.AssistantMessage() 会把 Parts 一并并入，可直接追加对话历史
```

支持范围与行为约定：

- **图像输出当前仅 Gemini 原生 provider 支持**，需选用支持图像生成的模型
  （如 `gemini-2.0-flash-preview-image-generation`）；流式模式下图像随单个 chunk 整块到达。
- 其他 provider 收到非文本模态会返回 `ErrInvalidRequest`，**不静默丢弃**。
- **文件输出**：类型层已就绪（`Parts` 可承载 `FileData`），但当前没有平台在 chat 响应中
  直接返回文件——实际产品中"生成文件"的标准模式是 **Tool Use**：模型调用你定义的
  `export_file` 类工具，业务侧生成文件并把 URL 回传，不需要库层新抽象。
- 图像输出同样计入 token 并进入计费链路（各平台按图像 token 折算计费）。

### Thinking（思考模式）

`Thinking` 把平台的两套推理控制口径统一到一个字段里：`Effort` 是档位口径，
`BudgetTokens` 是 token 预算口径。思考内容通过 `ChatResponse.Reasoning`
（流式为 `StreamChunk.ReasoningDelta`）单独返回，不混入正文。

```go
type Thinking struct {
    Enabled      *bool  // 显式开启 / 关闭思考
    Effort       string // 档位：minimal / low / medium / high
    BudgetTokens *int   // 思考 token 预算
}
```

各平台已映射的字段：

| 平台 | `Enabled` | `Effort` | `BudgetTokens` |
| --- | --- | --- | --- |
| OpenAI / Azure OpenAI | | ✅ | |
| 火山方舟 Ark | ✅ | ✅ | |
| DeepSeek | ✅ | | |
| Anthropic（原生） | ✅ | | ✅ |
| Gemini（原生） | ✅ | | ✅ |

传入表中未标注的字段时，请求在构建阶段返回 `ErrInvalidRequest`，错误信息会列出该平台
已映射的字段。推理 token 计入输出、按输出价计费，静默忽略会让调用方误以为思考已开启，
并为未发生的思考付费。需要在运行前判断时，用 `ModelCapabilitiesFromPreset` 取回
`ModelCapabilities` 再调 `Supports(provider.CapabilityReasoning)`。

#### 自定义接入的平台

上表只覆盖内置预设。用 `NewProvider` 自定义接入的平台（未收录的 OpenAI 兼容平台、
自建推理服务）不在表中，默认拒绝全部 `Thinking` 字段——库不认识这个平台，
无从判断它接受哪些推理参数。

这类平台里不少直接接受 OpenAI 标准的 `reasoning_effort`，用
`ProviderConfig.SupportsReasoningEffort` 声明即可解锁 `Thinking.Effort`：

```go
p, err := provider.NewProvider(provider.ProviderConfig{
    Name:    "my-vllm",
    BaseURL: "http://192.168.1.100:8080/v1",
    APIKey:  "no-key-needed",
    Model:   "Qwen3-235B",

    SupportsReasoningEffort: true, // 该平台接受 reasoning_effort
})
```

两条边界：

- **只解锁 `Effort`。** `Enabled` 与 `BudgetTokens` 在各平台落在互不相同的私有字段上
  （DeepSeek 用 `chat_template_kwargs`、火山方舟用顶层 `thinking`），库无从代为映射，
  对未收录平台始终返回 `ErrInvalidRequest`。
- **内置预设优先。** 对已收录的平台声明该字段不生效，它们的支持范围由库判定——
  内置平台的映射属于库的实现，不交给调用方覆盖。

#### 档位口径（OpenAI / Azure / 火山方舟）

```go
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserText("请解释 Go 的 goroutine 调度模型"),
    },
    Thinking: &provider.Thinking{
        Effort: provider.ThinkingEffortHigh,
    },
})
if err != nil {
    log.Fatal(err)
}

fmt.Println(resp.Content)
fmt.Println(resp.Reasoning)
fmt.Println(resp.Usage.ReasoningTokens)
```

`Effort` 取值原样透传给平台的 `reasoning_effort`，`ThinkingEffortMinimal` / `Low` /
`Medium` / `High` 是便利常量而非取值全集。

火山方舟同时映射两个字段：`Enabled` 映射请求体顶层 `thinking`（`enabled` / `disabled`，
nil 时不下发该字段，由方舟按模型默认行为决定），`Effort` 映射 `reasoning_effort`。
两者互相独立：同时设置 `Enabled: false` 与非空 `Effort` 时，`thinking.disabled` 与
`reasoning_effort` 都会原样下发，本库不做取舍，最终以方舟侧的裁决为准。

DeepSeek 只映射 `Enabled`，写入 `chat_template_kwargs.enable_thinking`。

#### 预算口径（Anthropic / Gemini 原生路径）

Anthropic 开启思考时要求显式预算，缺失或非正数返回 `ErrInvalidRequest`——预算大小直接
决定费用，由调用方决定而不由本库代为推导。预算还需小于本次请求的 `MaxTokens`
（未显式设置时为 4096），该上限由平台校验。

```go
budget := 4096
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages:  []provider.Message{provider.UserText("逐步分析这段代码的复杂度")},
    MaxTokens: 8192,
    Thinking:  &provider.Thinking{BudgetTokens: &budget},
})
```

Gemini 未给出预算时，借用平台自身的语义取值表达意图：`Enabled: true` 下发
`thinkingBudget: -1`（由模型动态决定预算），`Enabled: false` 下发 `thinkingBudget: 0`
（禁用思考）；显式给出 `BudgetTokens` 时原样下发，取值是否被目标模型接受由平台校验。

```go
enabled := true
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("解释这段推导")},
    Thinking: &provider.Thinking{Enabled: &enabled},
})
```

两个平台的响应形态也已归位：Anthropic 的 `thinking` 内容块、Gemini 中带 `thought`
标记的 part，都进入 `Reasoning` 而不混入 `Content`。Anthropic 的加密思考块
（`redacted_thinking`）无法读取，不会出现在任何字段里。

#### 流式接收思考过程

```go
for {
    chunk, err := stream.Recv()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        return err
    }
    if chunk.ReasoningDelta != "" {
        fmt.Printf("[思考] %s", chunk.ReasoningDelta)
    }
    if chunk.Delta != "" {
        fmt.Print(chunk.Delta)
    }
}
```

Anthropic 原生路径的思考内容以 `Reasoning` / `ReasoningDelta` 返回，不参与后续轮次的
消息构建；多轮对话把 `resp.AssistantMessage()` 追加进历史时携带的是正文与工具调用。

### Structured Output（结构化输出）

如果你希望模型严格按 JSON 返回，可以使用 `ResponseFormat`：

```go
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserText("返回一个包含 city 和 summary 的 JSON 对象"),
    },
    ResponseFormat: provider.JSONSchemaFormatStrict("city_summary", provider.ParamSchema{
        Type: "object",
        Properties: map[string]provider.ParamSchema{
            "city":    {Type: "string"},
            "summary": {Type: "string"},
        },
        Required: []string{"city", "summary"},
    }),
})
```

常用构造器：

- `provider.TextFormat()`
- `provider.JSONObjectFormat()`
- `provider.JSONSchemaFormat(name, schema)`
- `provider.JSONSchemaFormatStrict(name, schema)`

如果你希望直接把 JSON 回复解码到 Go 类型，可以使用类型化助手：

```go
type CitySummary struct {
    City    string `json:"city"`
    Summary string `json:"summary"`
}

result, resp, err := provider.GenerateJSON[CitySummary](ctx, p, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.UserText("返回一个包含 city 和 summary 的 JSON 对象"),
    },
})
if err != nil {
    log.Fatal(err)
}

fmt.Println(result.City, result.Summary)
fmt.Println(resp.Usage.TotalTokens)
```

`GenerateJSON` 会在请求未设置 `ResponseFormat` 时自动使用 `JSONObjectFormat()`；如果你已经传入 `JSONSchemaFormatStrict(...)`，它会保留原有格式设置。需要复用已有变量时用 `GenerateJSONInto(ctx, p, req, &target)`。

如果需要在解码后做业务校验，使用 validator 变体。校验失败时可以通过 `errors.Is(err, provider.ErrStructuredValidation)` 判断：

```go
result, resp, err := provider.GenerateJSONWithValidator[CitySummary](ctx, p, req, func(v CitySummary) error {
    if v.City == "" {
        return fmt.Errorf("city is required")
    }
    return nil
})
if err != nil {
    log.Fatal(err)
}

fmt.Println(result.City, resp.Usage.TotalTokens)
```

#### 从 Go 类型自动生成 schema

不想手写 `ParamSchema` 时，可让库通过反射从目标类型派生 json_schema，避免 schema 与结构体不一致：

```go
type CitySummary struct {
    City    string `json:"city"`
    Summary string `json:"summary"`
    Temp    int    `json:"temp,omitempty"` // omitempty / 指针字段不进 required
    Color   string `json:"color" jsonschema:"enum=red|green|blue"`
}

// 直接调用并解码（请求未设置 ResponseFormat 时自动注入派生 schema）
result, resp, err := provider.GenerateJSONWithSchema[CitySummary](ctx, p, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("描述一个城市")},
})

// 或只生成 schema / ResponseFormat 自行使用
schema, err := provider.SchemaFromType[CitySummary]()        // 返回 ParamSchema
format, err := provider.JSONSchemaFormatFor[CitySummary]("") // 返回 *ResponseFormat
```

覆盖范围为 OpenAI 兼容结构化输出的常用子集：结构体、string / bool / 整型 / 浮点、切片数组、`map[string]T`、`time.Time`、匿名嵌入字段扁平化；读取 `json` tag 决定字段名与可选性，`jsonschema:"enum=..."` 为 string 字段生成枚举。`interface{}`/`any`、channel、函数、非 string 键的 map、自引用类型会返回错误而非静默降级，需在构造请求前显式处理。`GenerateJSONWithSchema` 也有 `...WithSchemaValidator` 变体，在解码后运行业务校验。

两点取舍需注意：

- **`map[string]T` 不展开值类型**：受限于 `ParamSchema.AdditionalProperties` 为 `*bool`，map 仅生成 `{"type":"object","additionalProperties":true}`，值类型 `T` 不写入 schema；但 `T` 仍会走统一的受支持校验，`map[string]chan int`、`map[string]any` 这类仍会返回错误，不会被绕过。
- **默认非 strict**：`JSONSchemaFormatFor` / `GenerateJSONWithSchema` 生成的是非 strict 的 `json_schema`。OpenAI strict 模式要求每个属性都进 `required` 并以 nullable union 表达可选字段，而 `ParamSchema.Type` 为单一字符串无法表达 union，故默认非 strict。若你的类型所有字段都必填且需要 strict，用 `provider.JSONSchemaFormatStrict(name, schema)` 组合 `SchemaFromType[T]()` 的结果。

### Token counting

支持原生 token counting 的 provider 会实现 `TokenCounter`。当前 Gemini（`countTokens`）与 Anthropic（`/v1/messages/count_tokens`，免费端点）原生 provider 支持 `CountTokens`；其他 provider 会返回 `ErrUnsupportedCapability`。注意：Anthropic 官方将该计数定义为**估算值**，与实际计费 token 可能有少量偏差——适合摘要压缩阈值（`CompactOptions.TriggerTokens`）与额度预检，不应作为无安全余量的硬额度判定，结算一律以响应 `Usage` 为准。无原生支持时退回误差更大的 `EstimateTokens` 本地启发式。

```go
count, err := provider.CountTokens(ctx, gemini, &provider.ChatRequest{
    Messages: []provider.Message{
        provider.SystemText("be concise"),
        provider.UserText("hello"),
    },
})
if err != nil {
    if errors.Is(err, provider.ErrUnsupportedCapability) {
        fmt.Println("当前 provider 不支持 token counting")
        return
    }
    log.Fatal(err)
}

fmt.Println(count.TotalTokens)
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

- `maxRounds`：最大循环次数（推荐显式设为 5-10；`<=0` 时使用 20 轮安全上限），防止模型无限调用工具
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
        // 工具 handler 出错时按策略重试，仍区分 context 取消（不重试、直接上抛）
        ToolRetry: provider.ToolRetryOptions{
            MaxAttempts: 3,
            Backoff:     provider.ExponentialBackoff(100*time.Millisecond, time.Second),
        },
    },
)
```

默认值：

- `RunToolLoop` 等价于使用兼容默认 options 调用 `RunToolLoopWithOptions`
- `ParallelToolCalls` 默认关闭
- `ToolErrorEncoder` 默认使用安全脱敏编码器
- `ToolRetry` 零值表示不重试（仅执行一次），保持既有行为；`MaxAttempts > 1` 时按 `Backoff` 等待并重试，`ShouldRetry` 默认重试所有非 context 取消错误
- `AccumulateUsage` 默认关闭，返回最后一轮的 `Usage`（既有行为）；设为 `true` 后，返回响应的 `Usage` 为**所有轮次**的 token 消耗累加值，便于多轮工具循环的成本统计（某轮 provider 未返回 usage 则按 0 计入）

#### 工具结果加工与响应校验

工具执行结果如果来自外部不可信数据源（网页抓取、第三方 API、用户上传内容等），可能携带试图让模型偏离原任务的指令文本（间接提示注入）。`RunToolLoopOptions` 提供两个可选钩子，让调用方在结果进入对话历史前、以及最终响应返回前接入自己的处理逻辑，`RunToolLoopStreamWithOptions` 同样支持：

- `ToolResultTransformer`：在工具结果写回对话历史前对其加工。返回 error 会中止整个工具循环。
- `ResponseValidator`：在返回最终响应前对其校验。返回 error 时整个循环返回该 error，不会把未通过校验的响应交还给调用方。

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
- **结构化输出**：生成侧的强制约束用库已有的 `ResponseFormat` / `JSONSchemaFormatStrict`（见本 README「Structured Output（结构化输出）」一节），比纯 prompt 文字声明更可靠；具体 schema 定义、解析失败即拒绝、字段级校验，由业务侧在 `ResponseValidator` 里实现——即使生成侧已做 Strict 约束，Go 侧仍需要防御性解析，不同 provider 对 strict 模式的遵循程度不同
- **输出校验**：字段完整性校验、长度合理性检查、敏感词表扫描，由业务侧在 `ResponseValidator` 里实现；模型平台自带的安全过滤（如 Gemini `SafetySettings`）由业务侧按需在构造请求时配置，与 `ResponseValidator` 叠加使用

完整可运行示例见 [`example/toolsecurity/main.go`](example/toolsecurity/main.go)：模拟一个网页摘要助手，工具抓取的网页内容里携带间接提示注入文本，示例组合了长度截断 + 正则特征检测降级替换 + Markdown 结构符转义（`ToolResultTransformer`）、`WrapToolResultInTag` 结构隔离、system prompt 显式声明数据边界、`ResponseFormat` 强制 JSON Schema 输出、`ResponseValidator` 解析校验（JSON 解析失败即拒绝 + 字段完整性 + 长度合理性 + 敏感词扫描）六层处理。规则内容本身（正则特征词表、Markdown 转义表、校验函数）整理成了一份跟本库解耦的模板文档，见 [`../PROMPT_INJECTION_DEFENSE.md`](../PROMPT_INJECTION_DEFENSE.md)，可直接复制到任何项目使用，不依赖这个库。

```bash
DEEPSEEK_API_KEY="<DEEPSEEK_API_KEY>" go run ./example/toolsecurity
```

### RunToolLoopStream（流式工具循环）

对话产品的标配路径：智能体一边流式输出（打字机效果），一边自动执行工具进入下一轮。
工具调用的增量片段由库内拼装，无需手写 delta 累积：

```go
resp, err := provider.RunToolLoopStream(ctx, p, req, 10, toolHandler, func(chunk provider.StreamChunk) {
    fmt.Print(chunk.Delta) // 实时透传给前端（SSE 等）
})
// resp 语义与 RunToolLoop 一致；需要跨轮累计 usage 时用
// RunToolLoopStreamWithOptions + AccumulateUsage
```

onChunk 会收到所有轮次的全部 chunk（含工具调用轮的空文本帧），按需过滤 `chunk.Delta != ""` 即可。

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

**关键设计**：与 Chat 共用同一套 `Registry` / `Preset` / `QuickRegistry`。`QuickRegistry` 会在注册 chat provider 时，自动为有 embedding 预设的平台（OpenAI / Qwen / 智谱 / 千帆 / 硅基流动 / Gemini）同时注册对应 embedder；DeepSeek、Moonshot、Anthropic、Ollama、xAI 暂未声明 embedding 预设，静默跳过不报错。

### 基础用法

```go
reg := provider.QuickRegistry(map[provider.ProviderName]string{
    provider.ProviderQwen:   os.Getenv("QWEN_API_KEY"),
    provider.ProviderZhipu:  os.Getenv("ZHIPU_API_KEY"),
    provider.ProviderGemini: os.Getenv("GEMINI_API_KEY"),
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

// 3. 相似度检索：用内置轻量工具按余弦相似度排序
best, _ := provider.MostSimilar(queryVec, docVecs)

// 4. 把匹配文档拼进 prompt 让 LLM 回答
reply, _ := provider.SimpleChatWithSystem(ctx, chat,
    "基于以下资料回答: "+docs[best.Index],
    query,
)
```

完整可运行示例见 [`example/embedding/main.go`](example/embedding/main.go)。

### 相似度工具

Embedding 常见的第一步检索可以直接用以下函数：

```go
score, err := provider.CosineSimilarity(queryVec, docVec)
ranked, err := provider.RankBySimilarity(queryVec, docVecs)
best, err := provider.MostSimilar(queryVec, docVecs)
```

这些函数只做纯内存向量计算，不依赖任何向量数据库；向量存储、文档切片和召回策略仍由业务层决定。

### 直接构造 Embedder（不走 Registry）

```go
// 预设配置，只需 APIKey
emb, err := provider.NewEmbedderFromPreset(
    provider.ProviderOpenAI,
    os.Getenv("OPENAI_API_KEY"),
    "", // 留空使用预设的 text-embedding-3-small
)

geminiEmb, err := provider.NewGeminiEmbedder(provider.NativeProviderConfig{
    APIKey: os.Getenv("GEMINI_API_KEY"),
    Model:  "gemini-embedding-001",
})

// 完全自定义（自部署或未预设的服务）
emb, err = provider.NewEmbedder(provider.EmbedderConfig{
    Name:    "my-embedding-service",
    BaseURL: "http://localhost:8080/v1",
    APIKey:  "any",
    Model:   "bge-large-zh-v1.5",
})
```

### 职责边界：库负责与平台的交互

`Embedder` 负责把文本变成向量、`Reranker` 负责按相关性精排，两者都是对平台端点的
调用封装。向量落到哪里（pgvector / Milvus / Qdrant / Chroma）、文档怎么切片、
检索链路怎么编排，由调用方按业务选型决定——这与对话历史由调用方持有是同一个口径：
库只负责与 LLM 平台的交互，存储与业务逻辑留在调用方手里。

## Rerank（重排序）

向量检索的召回结果按余弦相似度排序，语义精度有限。rerank 模型直接对
`(query, document)` 打分，把召回的几十条候选重新排序，只把最相关的几条送进上下文——
既提升相关性，又省 token。

```go
reranker, err := provider.NewRerankerFromPreset(provider.ProviderSiliconFlow, apiKey, "")
if err != nil {
    log.Fatal(err)
}

documents := []string{
    "卡森城是内华达州的首府。",
    "华盛顿特区是美国的首都。",
    "塞班岛是北马里亚纳群岛的首府。",
}
topN := 2
resp, err := reranker.Rerank(ctx, &provider.RerankRequest{
    Query:     "美国的首都是哪里？",
    Documents: documents,
    TopN:      &topN,
})
if err != nil {
    log.Fatal(err)
}

for _, r := range resp.Results {
    fmt.Printf("index=%d score=%.4f\n", r.Index, r.RelevanceScore)
}
// 直接把精排结果接回本地候选集
fmt.Println(resp.SortedDocuments(documents))
```

自定义平台（预设未覆盖时显式给出地址与模型）：

```go
reranker, err := provider.NewReranker(provider.RerankerConfig{
    Name:    provider.ProviderSiliconFlow,
    BaseURL: "https://api.siliconflow.cn/v1",  // 实际请求 POST {BaseURL}/rerank
    APIKey:  apiKey,
    Model:   "BAAI/bge-reranker-v2-m3",
})
```

- **协议**：`POST {BaseURL}/rerank`，请求体 `model` / `query` / `documents` /
  `top_n` / `return_documents`，响应体 `results[].index` 与
  `results[].relevance_score`。硅基流动、Jina、Cohere 等平台的 rerank 端点均为
  这一形态；用量字段各家口径不同（`tokens.input_tokens` / `usage.total_tokens`），
  本库统一归一到 `RerankResponse.Usage`，缺失的项为 0，不猜测用量。
- **`Index` 恒可用于索引请求的 `Documents`**：平台返回越界下标时直接报错，
  不会把越界值透给调用方。
- **`RelevanceScore` 只用于同一次调用内的排序比较**，不同模型、不同调用之间的
  绝对值不可直接比较。
- **`ReturnDocuments` 多数场景不必开启**——用 `Index` 回查本地 `Documents` 即可，
  能省一份回传流量；未开启时 `RerankResult.Document` 为空串。
- **`TopN ≤ 0` 按未设置处理**（返回全部），不发送该字段。
- 典型 RAG 装配：向量检索召回 50 条 → rerank 精排取前 5 条 → 拼进 prompt。
  向量检索部分见 [Embedding](#embedding文本向量化) 一节。

## 模型能力元数据

预设模型会暴露一份轻量能力元数据，便于调用方在运行前判断是否支持 Vision、Reasoning、Embedding 等能力：

```go
caps, ok := provider.ModelCapabilitiesFromPreset(provider.ProviderOpenAI)
if ok && caps.Supports(provider.CapabilityEmbedding) {
    fmt.Println("该预设提供默认 embedding 模型:", caps.EmbeddingModel)
}

embedders := provider.ModelCapabilitiesByCapability(provider.CapabilityEmbedding)
for name := range embedders {
    fmt.Println("可用于 embedding:", name)
}

// rerank 同理：CapabilityRerank + caps.RerankModel
rerankers := provider.ModelCapabilitiesByCapability(provider.CapabilityRerank)
for name := range rerankers {
    fmt.Println("可用于 rerank:", name)
}
```

`AllModelCapabilities()` 会返回副本，调用方修改结果不会影响包内预设。能力元数据描述的是本库在预设配置中明确掌握的能力，不等同于平台所有模型的完整清单；`ContextWindow` 为 `0` 表示本库未声明该值。如果你覆盖 `Model`，请以具体模型官方文档为准。

## 自定义 HTTP 客户端

默认情况下，`NewProvider` / `NewEmbedder` 会使用 `DefaultHTTPClient()`。它不设置 `http.Client.Timeout`，请求总预算仍由调用方传入的 `context.Context` 控制；但会设置 dial、TLS handshake、response header、idle connection 等传输层超时，避免底层 IO 无限等待。

需要统一超时、代理、链路追踪、测试替身或自定义 transport 时，可以向 `ProviderConfig` / `EmbedderConfig` 注入实现了 `Do(*http.Request) (*http.Response, error)` 的客户端：

```go
httpClient := &http.Client{Timeout: 30 * time.Second}

p, err := provider.NewProvider(provider.ProviderConfig{
    Name:       provider.ProviderOpenAI,
    BaseURL:    "https://api.openai.com/v1",
    APIKey:     os.Getenv("OPENAI_API_KEY"),
    Model:      "gpt-5.4-mini",
    HTTPClient: httpClient,
})

emb, err := provider.NewEmbedder(provider.EmbedderConfig{
    Name:       provider.ProviderGemini,
    BaseURL:    "https://generativelanguage.googleapis.com/v1beta",
    APIKey:     os.Getenv("GEMINI_API_KEY"),
    Model:      "gemini-embedding-001",
    HTTPClient: httpClient,
})
```

请求级超时仍推荐由调用方通过 `context.WithTimeout` 控制；自定义 `HTTPClient` 主要用于传输层策略复用。`NewEmbedder` 在 `Name` 为 `ProviderGemini` 时会自动使用原生 Gemini embedding 接口。调用方传入自定义客户端后，本库不会覆盖它的超时设置。

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

横切关注点走统一的装饰器 + Handler 抽象。本库内置了一组与业务口径无关的策略，
开箱可用；口径因项目而异的部分（日志格式、指标后端、审计存储、脱敏字段、缓存键）
由调用方用同一套 Middleware 类型自行实现，30 行以内即可。

内置策略：

| 能力 | 入口 | 说明 |
|------|------|------|
| 重试 | `WithRetry` / `RetryMiddleware` | 按 `ProviderError.Retryable` 判定，支持指数退避与全抖动，优先采用平台的 `Retry-After` |
| 熔断 | `WithBreaker` / `BreakerMiddleware` | 滑动窗口计数 + 指数退避冷却 + 半开探测 |
| 限流 | `WithRateLimit` / `RateLimitMiddleware` | RPM / TPM 令牌桶，token 预扣 + 真实用量结算 |
| 降级 | `NewFallbackProvider` | 多成员按序切换 |
| 负载均衡 | `NewBalancedProvider` | 加权轮询 / 加权随机 / 加权最少在途 |
| 观测 | `WithObservability` | 零依赖事件 hook，不绑定 slog / Prometheus / OTel |
| 计费与配额 | `NewBillingHook` / `QuotaMiddleware` / `TokenBudgetMiddleware` / `CostBudgetMiddleware` | 按用户与会话归账，额度硬限 |

策略参数一律由调用方注入（熔断阈值、限流额度、价格表、退避曲线），本库不预设
业务口径；观测与计费只提供事件与接口，落地到哪个后端由调用方决定。

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

### 内置重试（基于 `ProviderError.Retryable`）

```go
retrying := provider.WithRetry(base, provider.RetryOptions{
    MaxAttempts: 3,
    Backoff:     provider.ExponentialBackoff(200*time.Millisecond, 2*time.Second),
})

resp, err := retrying.Chat(ctx, req)
```

`WithRetry` 会同时装饰 `Chat` 与 `ChatStream` 创建阶段，默认只重试 `ProviderError.Retryable == true` 的错误。需要自定义策略时设置 `RetryOptions.ShouldRetry`。

退避策略：

- `ConstantBackoff(d)`：固定间隔。
- `ExponentialBackoff(base, max)`：指数退避，封顶 `max`。
- `ExponentialBackoffWithJitter(base, max)`：在上界内做**全抖动**（结果均匀分布于 `[0, 上界]`），避免大量并发请求同步重试造成惊群，生产环境推荐。

`Retry-After`：当供应商在 429 / 503 响应里返回 `Retry-After` 头时，重试会**优先采用它建议的等待时长**，否则回退到上面的退避函数。该值通过 `ProviderError.RetryAfter` 暴露。

> 限制：`Retry-After` 仅对**原生 HTTP provider**（Claude / Gemini / Ollama）生效。OpenAI 兼容路径复用 `sashabaranov/go-openai`，其错误类型不暴露响应头，无法读取 `Retry-After`，这类 provider 会回退到退避策略（建议配 `ExponentialBackoffWithJitter`）。

> 生产注意（重复计费风险）：重试与降级是 **at-least-once** 语义。当供应商已处理请求、但响应在网络中丢失（服务端已产生用量，客户端超时未收到）时，重试会再次发起真实调用，造成供应商侧重复执行与重复计费——Chat Completions 类接口通常不提供幂等键，本库无法在客户端去重。成本敏感场景应保守设置 `MaxAttempts` 与请求超时，并以供应商账单为准做对账。

### 熔断（Circuit Breaker）

`Breaker` 是内置的进程内熔断器：滑动窗口累计失败达阈值即跳闸，冷却期内的请求
在本地被挡下、不发往平台；冷却到期放行少量探测请求，探测成功即闭合，失败则
延长冷却（指数退避）。

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
- **连续跳闸退避**：`BackoffReset` 内再次跳闸视为连续故障，冷却时长翻倍；
  超过 `BackoffReset` 才跳闸则退回 `OpenDuration` 重新起算。
- **流式只统计创建阶段**：`BreakerStreamMiddleware` 以"流是否创建成功"为口径。
  平台故障有时表现为"建流成功但立刻断流"，要覆盖这类失败，用 `stream_complete`
  观测事件回喂一次失败：

  ```go
  provider.ObserveOptions{OnEvent: func(_ context.Context, e provider.ObserveEvent) {
      if e.Operation == provider.ObserveOperationStreamComplete &&
          e.StreamFinish == provider.StreamFinishError {
          breaker.Report(e.Err)
      }
  }}
  ```

- **与降级链联动**：`ErrBreakerOpen` 已在默认切换判定内，熔断打开会立刻切到
  下一个成员，无需自定义 `ShouldFallback`。
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

- **装配位置**：熔断放在**每个上游成员**上（各自独立跳闸、独立恢复），
  不要包在整条降级链外——那样一个上游故障会把整条链熔断。
- **状态查询**：`breaker.State()` 与 `breaker.Stats()` 供健康检查与监控读取；
  确认上游已恢复（如换了新 key）时用 `breaker.Reset()` 手动放行。
- 状态保存在进程内存，不跨进程共享；多副本部署时每个副本独立熔断。
- `Embedder` 侧用 `WithEmbedderBreaker` / `BreakerEmbedMiddleware`，语义相同。
- **与观测的装配顺序**：把观测装在熔断**外层**
  （`WithObservability(WithBreaker(base, breaker), opts)`），本地拦下的请求也会产生
  观测事件（`Err` 为 `ErrBreakerOpen`、`Usage` 为零），熔断触发频率才可监控；
  反过来装则看不到被拦下的量。限流同理。

### 客户端限流（RPM / TPM）

`RateLimiter` 把超额请求挡在发出之前，减少平台 429。请求数与 token 数各走一个
令牌桶，两者在同一把锁内同时满足才放行。

```go
limiter := provider.NewRateLimiter(provider.RateLimitOptions{
    Name:              "qwen",
    RequestsPerMinute: 1200,        // RPM，≤0 表示不限请求数
    TokensPerMinute:   1_000_000,   // TPM，≤0 表示不限 token
    Wait:              true,        // 额度不足时阻塞等待（受 ctx 与 MaxWait 约束）
    MaxWait:           2 * time.Second,
    AdaptFromHeaders:  true,        // 用响应头回传的剩余配额校准本地桶
})

limited := provider.WithRateLimit(base, limiter)

resp, err := limited.Chat(ctx, req)
if errors.Is(err, provider.ErrLocalRateLimited) {
    // 本地限流拦下，请求没有发出；区别于平台回传的 ErrRateLimit
}
```

- **桶容量默认值**：请求桶默认取"1 秒额度"（`RPM/60` 向上取整，至少 1），
  让流量匀速铺开，避免整分钟额度被瞬间打出去触发平台的秒级限流；token 桶默认取
  整分钟额度（长上下文单请求需要的额度可能远超 1 秒额度，桶太小会永远拿不到令牌）。
  两者都可用 `RequestBurst` / `TokenBurst` 覆盖。
- **token 采用"预扣 + 结算"**：请求前按 `EstimateTokens` 口径预扣
  （`EstimateChatRequestTokens`：输入估算 + `MaxTokens`，未设 `MaxTokens` 时用
  `ReserveOutputTokens`），响应返回后用真实 `Usage` 结算差额；流式的结算延后到
  流终止（读到 `io.EOF`、读出错或提前 `Close`）。请求失败时保留预扣不返还，
  宁可保守也不放大超额风险。
- **单请求需求超过 token 桶容量时按容量收敛**，避免请求因永远凑不齐额度而饿死。
- **响应头自适应**：开启 `AdaptFromHeaders` 后，`x-ratelimit-remaining-requests`
  与 `x-ratelimit-remaining-tokens` 会把本地可用额度**下调**到平台口径（只下调不上调——
  本地偏保守是安全的，跟着平台放宽可能立刻超额）。也可手动调用
  `limiter.Observe(resp.Metadata)`。
- **与降级链联动**：`ErrLocalRateLimited` 已在默认切换判定内。给每个 key 配独立
  限流器，额度用尽即切到下一个 key，是多 key 分摊配额的常见组合。
- `limiter.Stats()` 返回两个桶的可用额度与容量（负值表示已透支）；
  `Embedder` 侧用 `WithEmbedderRateLimit`，token 预扣按输入文本估算。
- 额度保存在进程内存，不跨进程共享；多副本部署时按副本数拆分配额。

### 观测 Hook（日志 / 指标 / Trace）

`WithObservability` 提供零外部依赖的观测 hook，不绑定 `slog`、Prometheus 或 OpenTelemetry。每次 `Chat`、`ChatStream`、`Embed` 完成后，库会向 `OnEvent` 发送一个 `ObserveEvent`，其中包含 operation、provider、model、duration、usage、metadata、request id 和错误分类。

流式调用会产生两个事件：

- `stream`：流创建完成时发出，反映建连结果，不含 usage；
- `stream_complete`：流终止时发出（读到 `io.EOF`、`Recv` 出错或提前 `Close` 均会触发，至多一次），携带流上观测到的最终 `Usage` 与整个流的持续时长。若流未读到尾部就终止，`Usage` 可能为零值——按 token 计费时可据此识别漏单。

```go
observed := provider.WithObservability(base, provider.ObserveOptions{
    OnEvent: func(ctx context.Context, event provider.ObserveEvent) {
        slog.InfoContext(ctx, "llm call",
            "operation", event.Operation,
            "provider", event.Provider,
            "model", event.Model,
            "request_id", event.RequestID,
            "duration_ms", event.Duration.Milliseconds(),
            "tokens", event.Usage.TotalTokens,
            "error_code", event.ErrorCode,
            "retryable", event.Retryable,
        )
    },
})

resp, err := observed.Chat(ctx, req)
```

Embedding 路径使用对称的 `WithEmbedderObservability`：

```go
observedEmb := provider.WithEmbedderObservability(emb, provider.ObserveOptions{
    OnEvent: func(ctx context.Context, event provider.ObserveEvent) {
        slog.InfoContext(ctx, "llm embedding",
            "provider", event.Provider,
            "model", event.Model,
            "duration_ms", event.Duration.Milliseconds(),
        )
    },
})
```

`OnEvent` 在调用 goroutine 内同步执行，生产环境里应保持快速、非阻塞；记录日志或指标时不要输出 API Key、prompt 原文、响应正文等敏感内容。`ChatStream` 观测流创建与流终止两个节点，不跟踪中间每个 chunk。

本库自身不会向调用方回显 API Key（响应头按白名单过滤、请求不含密钥）。如需在自己的日志中保留密钥的可追溯性又不暴露完整值，可用 `provider.MaskSecret`（如 `MaskSecret("sk-1234...wxyz")` -> `"sk-1****wxyz"`）。

### 按用户计费与配额

计费能力构建在观测 Hook 之上：一处挂载，所有 `Chat` / `ChatStream` / `Embed` 调用自动按
ctx 中的用户与会话归账，业务调用点零统计代码。

```go
store := provider.NewMemoryUsageStore() // 或自行实现 UsageRecorder 接 Redis/DB

billed := provider.WithObservability(base, provider.ObserveOptions{
    OnEvent: provider.CombineObserveHooks(
        provider.NewBillingHook(store), // 计费归账
        myLoggingHook,                  // 日志观测可并存
    ),
})

// 请求入口注入身份（如 Gin 鉴权中间件里）：
ctx = provider.WithUserID(ctx, userID)
ctx = provider.WithConversationID(ctx, conversationID)

// 业务正常调用，无需任何统计代码
resp, err := billed.Chat(ctx, req)

// 随时查询累计用量（按用户 / 按会话）：
userTotals, _ := store.UserTotals(userID)
convTotals, _ := store.ConversationTotals(userID, conversationID)
fmt.Println(userTotals.Usage.TotalTokens, convTotals.Calls, convTotals.TerminatedCalls)
```

`RecordEntry` 携带 EntryID（库生成的幂等键，存储层据此去重）、UserID、ConversationID、
RequestID（对账，流式与非流式均有值）、响应侧实际模型名、Usage、终止方式等；
流式调用异常终止（网络中断 / 提前 Close）时仍会发出记录（`Terminated=true`、Usage 可能为零值），
漏账可观测——收不收钱由 Recorder 策略决定。`MemoryUsageStore` 为单实例场景设计，
多实例部署换用共享存储实现 `UsageRecorder` 即可，接口不变——
`example/billingstore/` 提供 Redis + GORM 的参考实现（独立 go.mod，reference 级），
含 `EntryID` 幂等的 Redis Lua 原子累计、流水异步落库与 `QuotaChecker` 限额；
Redis Cluster 部署需启用其 `Config.RedisCluster`，让同一用户的多 key 固定到同一 slot。
它是 **best-effort 参考实现**：计价失败仍以 `costMicros == 0` 落账并经 `OnError` 上抛
（原始用量入库、可事后重算），刷库不重试、进程崩溃丢缓冲——资金敏感的生产账务
不可直接照搬，须按业务要求强化（持久化 outbox、失败重试、对账兜底）。

#### 费用计算（PricingTable）

金额一律 int64 微元（1e-6 货币单位），杜绝 float64 精度问题；费率由调用方注入，库不硬编码任何价格：

```go
table := provider.PricingTable{
    "deepseek-chat": {
        InputPer1M:     2_000_000, // 2 元 / 1M tokens
        OutputPer1M:    8_000_000,
        CacheReadPer1M: 200_000,   // 未配置时回落到 InputPer1M
        Currency:       "CNY",
    },
}
micros, currency, err := table.Cost("deepseek-chat", resp.Usage)
fmt.Println(provider.FormatMicros(micros), currency) // 如 "0.0123 CNY"
```

缓存写入支持按 TTL 分档配置费率。Anthropic 的长 TTL 缓存写入单价高于短 TTL，
两档配同一个价会算错——按各档实际单价配置即可：

```go
table := provider.PricingTable{
    "claude-sonnet-4-5": {
        InputPer1M:        3_000_000,
        OutputPer1M:       15_000_000,
        CacheReadPer1M:    300_000,   // 缓存命中远低于常规输入
        CacheWritePer1M:   3_750_000, // 未分档部分与未上报明细时的兜底单价
        CacheWrite5mPer1M: 3_750_000,
        CacheWrite1hPer1M: 6_000_000, // 长 TTL 写入更贵
        Currency:          "CNY",
    },
}
```

分档费率只在平台上报了 `Usage.CacheWrite5mTokens` / `CacheWrite1hTokens` 时参与计算：
未上报明细、或未配置分档费率时，写入总量整体按 `CacheWritePer1M` 计价，
与不使用分档时的结果一致（示例中的单价仅为格式演示，请以官方定价页为准）。

计费公式先减子集再分档乘价（`ReasoningTokens ⊆ CompletionTokens`、
`CacheReadTokens + CacheWriteTokens ≤ PromptTokens`、
`CacheWrite5mTokens + CacheWrite1hTokens ≤ CacheWriteTokens`），不会重复计费；
原生联网搜索按次计价并与 token 项一并累加，两种口径的费率
（`WebSearchPer1K` / `GroundedPromptPer1K`，微元/1000 次）**二选一**配置，
规则见"原生联网搜索"一节。未配价模型、存在搜索用量但未配搜索价、
或配置口径与用量口径不符时返回 `ErrModelNotPriced`。
负费率、负 token、子集关系不成立、费率越界、搜索双费率同时配置或
最终金额超出 `int64` 时返回 `ErrInvalidPricing`，不回绕为错误金额。
仅支持线性单价，分时折扣、阶梯定价请在业务层处理。
底层成本价与对用户售价维护两份表即可。可在服务启动或价格表热更新时调用
`PricingTable.Validate()` 整表校验（范围与搜索双费率互斥），把配置错误挡在计价之前。

#### 价格表热替换（PricingRegistry）

直接持有 `PricingTable` 时，调价只能靠调用方自己保证"整表替换"——有人在线改条目
就会与计价路径的读发生 data race。`PricingRegistry` 把这个约束变成 API 保证：

```go
registry, err := provider.NewPricingRegistry(table, "2026-08-21") // 构造即校验 + 整表拷贝
if err != nil {
    log.Fatal(err) // 非法费率不会进入生效状态
}

micros, currency, err := registry.Cost("deepseek-chat", resp.Usage) // 原子读，无锁
fmt.Println(registry.Version())                                     // 账务落库时一并记录

// 调价：校验通过后一次原子切换，不存在"半新半旧"的中间状态
if err := registry.Replace(newTable, "2026-09-01"); err != nil {
    log.Printf("新价格表非法，继续按旧价计费: %v", err)
}
```

- 构造与 `Replace` 都会整表拷贝并走 `Validate`，调用方之后再改自己那份表影响不到已生效的价格。
- `Cost` 走 `atomic.Pointer` 读取，与 `Replace` 并发安全；每次 `Cost` 要么整体按旧价、要么整体按新价。
- `Rate(model)` 查单条费率零拷贝；`Snapshot()` 返回一致的表 + 版本拷贝（每次调用都拷贝，
  用于导出或喂给按 `PricingTable` 取价的组件，不要放在热路径）；`Models()` 返回已配价模型名。
- 空表表示暂无配价，`Cost` 一律返回 `ErrModelNotPriced`，不会静默按零计费。

#### 配额拦截（QuotaChecker）

```go
guarded, _ := provider.TryWithMiddlewares(billed, provider.MiddlewareOptions{
    Chat:   []provider.Middleware{provider.QuotaMiddleware(myChecker)},
    Stream: []provider.StreamMiddleware{provider.QuotaStreamMiddleware(myChecker)},
})
```

`QuotaChecker.Allow(ctx, userID, model)` 基于累计真实用量判断，超限返回 `ErrQuotaExceeded`，
请求不发往平台（语义为"已超额拦下一次"，存在最后一次调用的滞后）。
ctx 无 userID 的请求默认放行；fail-open / fail-close 由实现方决定。

#### 剩余额度硬限（TokenBudget）

业务从账务系统查出用户剩余 token 数后注入 ctx，单次调用的消耗被硬性限制在剩余额度内：

```go
guarded, _ := provider.TryWithMiddlewares(billed, provider.MiddlewareOptions{
    Chat:   []provider.Middleware{provider.TokenBudgetMiddleware()},
    Stream: []provider.StreamMiddleware{provider.TokenBudgetStreamMiddleware()},
})

ctx = provider.WithTokenBudget(ctx, remainingTokens)
resp, err := guarded.Chat(ctx, req) // 剩余不足 → ErrQuotaExceeded；足够 → MaxTokens 被收缩到剩余额度内
```

输入侧按 `EstimateTokens` 启发式估算（误差 ±30%），输出侧通过收缩 `MaxTokens` 硬限；
需要零误差结算时结合响应 `Usage` 事后对账。

额度体系是**余额（金额）**而非 token 包时，用金额口径的对应版本——业务注入剩余余额（微元），
middleware 按 `PricingTable` 完成金额与 token 的换算（输入按无缓存全价保守估算，
输出按剩余余额反推可负担的 token 数）：

```go
guarded, _ := provider.TryWithMiddlewares(billed, provider.MiddlewareOptions{
    Chat:   []provider.Middleware{provider.CostBudgetMiddleware(table)},
    Stream: []provider.StreamMiddleware{provider.CostBudgetStreamMiddleware(table)},
})

ctx = provider.WithCostBudget(ctx, remainingMicros) // 如余额 1 元 = 1_000_000 微元
resp, err := guarded.Chat(ctx, req)
// 余额不足 → ErrQuotaExceeded；model 未配价 → ErrModelNotPriced；非法费率 → ErrInvalidPricing
```

注意：金额预算按 `req.Model` 查价，请求需显式指定已配价的模型；
建议拦截阈值留 20-30% 余量（估算是保守近似），精确扣款始终以响应 `Usage` × 费率结算。

### Fallback Provider

```go
fallback, err := provider.NewFallbackProvider(primary, backup)
if err != nil {
    log.Fatal(err)
}

resp, err := fallback.Chat(ctx, req)
```

`FallbackProvider` 会按传入顺序调用 provider。只有当前 provider 返回可重试错误
（限流 / 超时 / 5xx / 网络错误）时，才会继续尝试下一个 provider；遇到无效请求、
鉴权失败、内容审核拦截等不可重试错误会立即返回，避免把调用方问题扩散到备用平台。

**同平台多模型降级链**：构造多个实例、各配不同默认模型即可（也可跨平台混编）：

```go
fast, _ := provider.NewProviderFromPreset(provider.ProviderDeepSeek, key, "deepseek-chat")
backup, _ := provider.NewProviderFromPreset(provider.ProviderQwen, qwenKey, "qwen-max")
p, _ := provider.NewFallbackProvider(fast, backup)
```

降级链使用须知：

- **`req.Model` 必须留空**——它的语义是"显式覆盖默认模型"，会跟随请求传给链上
  每个成员，导致降级后仍请求同一个模型、降级失效；留空让各成员用自己的默认模型。
- **切换对调用方功能无感知，但延迟会叠加**（前面成员的失败耗时 + 当前成员耗时）；
  给各成员配独立的短超时 `HTTPClient` 可以快速失败、快速切换。
- **流式的降级只发生在流创建阶段**：打字机开始输出后中途断流不会切换（半截内容
  无法无缝接续），中断会经 `stream_complete`（`Terminated=true`）正常进入计费审计。
- **计费口径**：`RequestModel` 取链首默认模型，实际服务的模型由响应侧 `Model`
  如实记录；链上所有可能的模型都应配入 `PricingTable`。
- 与 `WithRetry` 组合时，把 retry 包在**每个成员**上（先重试同模型再降级），
  而不是包在整条链外（那会整链重跑）。
- **自定义切换判定**：多供应商冗余场景常需要放宽切换条件（key 失效、模型下线、
  业务熔断错也切换），用 `NewFallbackProviderWithOptions` 注入：

  ```go
  p, _ := provider.NewFallbackProviderWithOptions([]provider.Provider{fast, backup},
      provider.FallbackOptions{ShouldFallback: func(err error) bool {
          return provider.IsRetryableError(err) ||
              errors.Is(err, provider.ErrAuth) || isBreakerOpen(err)
      }})
  ```

  无论判定如何，ctx 已取消/超时时都会立即返回、不再尝试后续成员（调用方已放弃）。
  全部失败时的"服务繁忙"降级响应属于产品 UX 语义，留在业务层包装。

**多厂商两级降级**（厂商内先穷尽所有 model，再切下一个厂商）：`FallbackProvider`
本身实现 `Provider` 接口，可嵌套组合；简单场景用扁平链把"厂商×model"按优先级
排成一列效果等价：

```go
vendorA, _ := provider.NewFallbackProvider(dsChat, dsReasoner)   // 厂商 A 的 model 链
vendorB, _ := provider.NewFallbackProvider(qwenMax, qwenPlus)    // 厂商 B 的 model 链
top, _ := provider.NewFallbackProvider(vendorA, vendorB)         // 厂商间降级
// A 的所有 model 依次失败后，才会尝试 B 的 model 链
```

嵌套的价值是**每层可配不同的切换判定**（如厂商内只切 5xx/超时、厂商间连 key
失效也切）；不需要分层策略时，扁平链更简单。

完整示例见 [`example/middleware/main.go`](example/middleware/main.go)。
示例还演示了 `tokenStatsMiddleware(stats *int64)`，用 `atomic.AddInt64` 累计总 token 消耗。

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
缓存命中的输入单价通常只有常规输入的一小部分（Anthropic 约十分之一），
把长会话打散等于让这部分收益全部消失——`BalanceSessionAffinity` 用来解决这个问题：

```go
lb, err := provider.NewBalancedProviderWithOptions(members, provider.BalanceOptions{
    Strategy: provider.BalanceSessionAffinity,
})
// 请求入口注入会话标识，同一会话的每一轮都会落到同一个 key
ctx = provider.WithConversationID(ctx, conversationID)
resp, err := lb.Chat(ctx, req)
```

会话键默认取 `ConversationIDFromContext`，再回落 `UserIDFromContext`；
需要按别的维度粘附（租户、提示词前缀指纹等）时用 `BalanceOptions.SessionKey` 自定义：

```go
provider.BalanceOptions{
    Strategy: provider.BalanceSessionAffinity,
    SessionKey: func(ctx context.Context, req *provider.ChatRequest) string {
        return tenantFromContext(ctx)
    },
}
```

行为约定：

- **按权重分配会话**：权重大的成员占据更宽的哈希区间、承载更多会话，
  粘的是"同一会话"，不是把所有流量压到一个成员。
- **哈希确定性**：不含随机种子，同一会话键在所有副本、进程重启前后都落到同一成员，
  多副本部署无需共享状态。
- **会话键为空**（未注入标识、或自定义函数返回空串）的调用退化为平滑加权轮询，
  不会恒选同一成员。
- **故障转移不受影响**：粘附成员失败或熔断时，从粘附位置环形向后取下一个未尝试的成员，
  顺序确定；转移后该轮请求落在冷缓存成员上，属于预期代价。
- **成员增减会重新分布会话**：哈希区间随权重总和变化，增删 key 后大部分会话会换成员、
  缓存需要重新预热。调整成员列表宜安排在低峰期。

- **成员级熔断**：`BalanceMember.Breaker` 由均衡器负责申请与上报，不要再用
  `WithBreaker` 包一层（会双重计数）。熔断打开的成员会以 `ErrBreakerOpen`
  快速失败并自动转移到下一个成员，冷却到期后自动恢复接流。
- **故障转移判定**同降级链：默认切换平台侧可重试错误、`ErrBreakerOpen`、
  `ErrLocalRateLimited`，可用 `BalanceOptions.ShouldFallback` 覆盖。
  ctx 已取消/超时时立即返回、不再尝试后续成员。
- **`req.Model` 通常留空**（同降级链的理由），让各成员用自己的默认模型；
  计费的 `RequestModel` 以首成员为口径，实际服务的模型由响应侧 `Model` 如实记录。
- **流式只在创建阶段转移**：打字机开始输出后中途断流不会切换。
- **`lb.Stats()`** 返回每个成员的权重、在途数与熔断状态，可直接喂给健康检查端点。
- `BalancedProvider` 本身实现 `Provider`，可与 `FallbackProvider`、`WithRetry`
  嵌套组合：常见装配是"均衡器内每个成员各自带 retry + 熔断 + 限流"。

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

**本地拦截类 sentinel**（请求未发往平台，不是 `*ProviderError`，用 `errors.Is` 判定）：

| Sentinel | 含义 | 出现位置 |
|----------|------|----------|
| `ErrBreakerOpen` | 熔断器打开，冷却期内快速失败 | `Breaker` / `BalancedProvider` |
| `ErrLocalRateLimited` | 客户端限流拦下，区别于平台回传的 `ErrRateLimit` | `RateLimiter` |
| `ErrQuotaExceeded` | 配额或预算耗尽 | `QuotaMiddleware` / `TokenBudget` / `CostBudget` |
| `ErrModelNotPriced` | 模型未配价，不静默按零计费 | `PricingTable` / `PricingRegistry` |
| `ErrInvalidPricing` | 费率或用量数据非法 | `PricingTable` / `PricingRegistry` |

这些错误默认都会触发降级链与均衡器的成员切换（`ErrBreakerOpen`、
`ErrLocalRateLimited`），或直接返回给调用方（配额与计价类）。

### 响应元数据：`ResponseMetadata`

成功响应会在 `ChatResponse.Metadata` / `EmbeddingResponse.Metadata` 中携带安全白名单内的诊断信息：

```go
resp, err := p.Chat(ctx, req)
if err != nil {
    return err
}

fmt.Println(resp.Metadata.Provider)
fmt.Println(resp.Metadata.Model)
fmt.Println(resp.Metadata.RequestID)
fmt.Println(resp.Metadata.Header("x-ratelimit-remaining-requests"))
```

`ResponseMetadata.Headers` 只保留 request id、correlation id、rate limit 等诊断头，不会复制 `Set-Cookie`、鉴权头或其他敏感响应头。

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

### 上下文窗口管理（省 token）

长对话必然触及上下文窗口与成本问题，库提供三个渐进式工具：

**1. 估算**：`EstimateTokens` 无 tokenizer 依赖的启发式估算（CJK 按字、其余按 4 字符/token，
误差 ±30%），用于预算判断，不用于计费结算：

```go
if provider.EstimateTokens(history) > 30_000 { /* 触发裁剪或压缩 */ }
```

**2. 硬裁剪**：`TrimMessagesToTokenBudget` 保留全部 system 与最新消息，从旧到新丢弃超预算部分；
以"消息组"为单位裁剪（assistant 的 tool_calls 与其结果不拆分），不会裁出非法序列：

```go
history = provider.TrimMessagesToTokenBudget(history, 30_000)
```

**3. 摘要压缩**：`CompactMessages` 把较早的历史用一次 LLM 调用总结为摘要消息，
保留最近 N 组原文——比硬裁剪多花一次摘要调用，但信息不丢失，多轮下净节省显著：

```go
result, err := provider.CompactMessages(ctx, p, history, provider.CompactOptions{
    Model:            "deepseek-chat", // 摘要用便宜模型即可
    KeepRecentGroups: 4,               // 最近 4 组保留原文
    TriggerTokens:    20_000,          // 低于阈值不压缩，避免短对话空耗
})
if err == nil && result.Compacted() {
    history = result.Messages
    // 业务缓存摘要，供后续轮次直接组装（system + 摘要 + 新原文），无需每轮压缩：
    conv.UpdateSummary(result.Summary, result.CompactedCount) // 摘要正文 + 覆盖的消息条数
}
```

正确用法是**业务侧缓存压缩结果、按阈值低频触发**，而不是每轮都压缩；
摘要调用自身的 usage 会经计费 hook 正常归账。压缩失败返回错误，可回退到硬裁剪。
固定的 system + 摘要前缀还有利于命中平台的 prompt caching，进一步降低输入成本。

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

### SSE 流式转发（含计费与停止生成）

对话产品的典型形态：前端 SSE 打字机 + 按用户计费 + 停止生成。三者组合的完整示例：

```go
func streamHandler(c *gin.Context) {
    // 鉴权后注入身份与会话（计费 hook 依赖）；剩余额度硬限按需注入
    ctx := provider.WithUserID(c.Request.Context(), c.GetString("uid"))
    ctx = provider.WithConversationID(ctx, c.Query("conv"))
    // ctx = provider.WithTokenBudget(ctx, remainingTokens)

    stream, err := billed.ChatStream(ctx, &provider.ChatRequest{
        Messages: history,
    })
    if err != nil {
        if errors.Is(err, provider.ErrQuotaExceeded) {
            c.JSON(429, gin.H{"error": "额度已用尽"})
            return
        }
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    defer stream.Close() // 客户端断开时提前 Close，stream_complete 事件仍会上报

    c.Header("Content-Type", "text/event-stream")
    c.Header("Cache-Control", "no-cache")
    for {
        chunk, err := stream.Recv()
        if err != nil {
            break // io.EOF 或错误：计费 hook 已在流终止时自动归账
        }
        if chunk.Delta != "" {
            c.SSEvent("delta", chunk.Delta)
            c.Writer.Flush()
        }
        if chunk.FinishReason != "" {
            c.SSEvent("finish", chunk.FinishReason)
        }
    }
    c.SSEvent("done", "")
}
```

前端"停止生成"按钮只需断开 SSE 连接：`c.Request.Context()` 取消会终止上游流，
`stream_complete` 事件以 `StreamFinish=closed/error` 上报，计费侧可识别并按策略处理。
智能体场景把 `ChatStream` 换成 `RunToolLoopStream`，onChunk 里做同样的 SSEvent 转发即可。

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

**火山方舟 / 豆包**

| 模型名 | 说明 |
|--------|------|
| `doubao-seed-2-0-pro-260215` | Seed 2.0 Pro，深度推理与长链任务 |
| `doubao-seed-2-0-lite-260215` | Seed 2.0 Lite，性能/成本平衡，全模态理解 |
| `doubao-seed-2-0-mini-260215` | Seed 2.0 Mini，低延迟高并发 |
| `doubao-seed-1-8-251228` | Seed 1.8 |
| `doubao-embedding-text-240515` | 文本向量化 |

> 方舟的 `model` 同时接受模型 ID（如上）与推理接入点 ID（`ep-` 开头），两种写法都可直接传入 `Model` 字段。

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

**Google Gemini**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `gemini-embedding-001` | 3072 | 默认推荐，原生 `embedContent` / `batchEmbedContents` 接口，支持 `outputDimensionality` 截断 |

**硅基流动**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `BAAI/bge-m3` | 1024 | 默认推荐，多语言 + 稀疏/稠密混合 |
| `BAAI/bge-large-zh-v1.5` | 1024 | 中文专用 |
| `Pro/BAAI/bge-m3` | 1024 | 付费稳定通道 |
| `netease-youdao/bce-embedding-base_v1` | 768 | 网易有道中英双语 |

> DeepSeek / Moonshot 官方暂无 embedding 模型，需要请转硅基流动或自部署。

## 与编排框架集成（Eino 等）

本库定位是 **provider 客户端层**：统一多平台调用、用量统计、计费、配额。
它不做 graph 编排、RAG pipeline、多 agent 协作——那是编排框架（如 [Eino](https://github.com/cloudwego/eino)）的职责。
两者不互斥，集成边界如下：

- **适配方向**：Eino 的 `ChatModel` 是接口，可写一个薄适配器把本库 `Provider` 包装成
  Eino ChatModel（`Chat` ↔ `Generate`、`ChatStream` ↔ `Stream`，`Message`/`ContentPart`
  与 `schema.Message` 的角色、多模态、tool call 字段均可无损互转）。编排归 Eino，
  provider 与计费层原样保留。
- **计费不受影响**：`WithObservability` + `NewBillingHook` 挂在 provider 实例上，
  Eino 经适配器调用时事件照常触发，ctx 中的 UserID/ConversationID 沿 Eino 的
  ctx 链路自然透传，归账逻辑无需改动。
- **不要在两层重复做**：接入编排框架后，工具循环（`RunToolLoopStream`）、
  上下文管理（`TrimMessagesToTokenBudget`/`CompactMessages`）可改由框架的
  agent/memory 能力承担，避免两层各裁一遍历史。
- **何时值得接**：多路召回 RAG、多 agent 工作流成为核心需求时再引入；
  单知识库检索（embedding + 向量库 + 拼 prompt）用本库的 Embedding 能力即可，
  不需要为此上编排框架。

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

    // Sampling
    Seed           *int // 确定性采样
    CandidateCount int  // 多候选生成

    // Reasoning / Structured Output
    Thinking       *Thinking       // 思考开关 / 档位 / token 预算，见 Thinking（思考模式）
    ResponseFormat *ResponseFormat
}
```

### ChatResponse

```go
type ChatResponse struct {
    Content      string     // assistant 回复内容（tool call 时可能为空）
    Reasoning    string     // 推理/思考内容
    FinishReason string     // "stop" / "length" / "tool_calls"
    Usage        Usage      // Token 用量统计
    ToolCalls    []ToolCall // 模型请求的工具调用列表
}

// 便捷方法
resp.HasToolCalls() bool       // 是否包含 tool calls
resp.AssistantMessage() Message // 转换为可追加到历史的 Message
```

### Usage（token 用量统计）

```go
type Usage struct {
    PromptTokens       int // 全部输入 token（已包含缓存读/写部分）
    CompletionTokens   int // 全部输出 token（已包含推理部分）
    ReasoningTokens    int // 推理/思考 token（CompletionTokens 的子集）
    CacheReadTokens    int // 命中提示词缓存的输入 token（PromptTokens 的子集）
    CacheWriteTokens   int // 写入提示词缓存的输入 token（PromptTokens 的子集，仅 Anthropic）
    CacheWrite5mTokens int // 5 分钟 TTL 的缓存写入 token（CacheWriteTokens 的子集）
    CacheWrite1hTokens int // 1 小时 TTL 的缓存写入 token（CacheWriteTokens 的子集）
    TotalTokens        int // 通常 = PromptTokens + CompletionTokens，provider 返回总量时以其为准
}
```

各字段跨 provider 遵循统一的包含关系，可直接用于按 token 计费，无需针对平台做换算：

- **Anthropic** 原始 `input_tokens` 不含缓存部分，本库已归一化为 `PromptTokens = input + cache_read + cache_write`；
  缓存写入的 TTL 分档明细（`cache_creation.ephemeral_5m/1h_input_tokens`）映射到
  `CacheWrite5mTokens` / `CacheWrite1hTokens`，供按档计价；未上报明细的模型这两项为 0；
- **Gemini** 原始 `candidatesTokenCount` 不含思考 token，本库已归一化为 `CompletionTokens = candidates + thoughts`；
- **OpenAI 兼容**平台的语义与上述统一语义天然一致，直接映射。

缓存读、缓存写与常规输入的计价通常不同（如 Anthropic 缓存写约 1.25 倍、缓存读约 0.1 倍），计费时应分别处理这三部分。

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
	FileURL     string
	FileData    []byte
	FileID      string
	Filename    string
	MIMEType    string
	ImageDetail ImageDetail
	CacheControl *CacheControl
}

const (
	ContentTypeText     ContentType = "text"
	ContentTypeImageURL ContentType = "image_url"
	ContentTypeFile     ContentType = "file"
)

// 便捷构造器
provider.TextPart("hello")
provider.ImageURLPart("https://example.com/cat.png")
provider.ImageDataPart(bytes, "image/png")
provider.FileDataPart(bytes, "application/pdf", "brief.pdf")
provider.FileURLPart("https://example.com/brief.pdf", "application/pdf")
provider.FileIDPart("file_123")
provider.WithCacheControl(provider.TextPart("cache me"), provider.CacheControlEphemeral())
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
    Delta          string          // 增量文本
    ReasoningDelta string          // 增量推理文本
    FinishReason   string          // 非空表示流结束
    Model          string          // 响应侧回传的实际模型名（部分 chunk 携带）
    Usage          Usage           // 流尾部 chunk 携带完整 token 统计，其余 chunk 为零值
    Parts          []ContentPart   // 非文本输出（如图像），通常整块到达
    ToolCalls      []ToolCallDelta // 流式 tool call 增量
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

### TokenCounter

```go
type TokenCounter interface {
    CountTokens(ctx context.Context, req *ChatRequest) (*TokenCountResponse, error)
}

type TokenCountResponse struct {
    Model       string
    TotalTokens int
    Metadata    ResponseMetadata
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
provider.GenerateJSON[MyType](ctx, p, req)                    // JSON 结构化输出 → Go 类型
provider.GenerateJSONWithValidator[MyType](ctx, p, req, fn)   // JSON 结构化输出 → Go 类型 + 业务校验
provider.GenerateJSONInto(ctx, p, req, &out)                  // 解码到已有变量
provider.GenerateJSONIntoWithValidator(ctx, p, req, &out, fn) // 解码到已有变量 + 业务校验
task.Run(ctx, p, params, input)                               // PromptTask 单轮任务 → 文本
provider.RunPromptTaskJSON[P, T](ctx, task, p, params, input) // PromptTask 单轮任务 → JSON 结构化输出
provider.DefaultHTTPClient()                                  // 默认传输层超时 HTTP 客户端
provider.WithRetry(p, provider.RetryOptions{...})             // 为 Chat / ChatStream 创建阶段添加重试
provider.NewFallbackProvider(primary, backup)                 // 多 provider fallback
provider.WithObservability(p, provider.ObserveOptions{...})   // Chat / ChatStream 观测 hook

// Embedding
provider.SimpleEmbed(ctx, emb, "你好")                        // 单条文本 → 向量
provider.EmbedBatch(ctx, emb, []string{"a", "b"})             // 批量 → 向量数组
provider.CosineSimilarity(a, b)                               // 两个向量余弦相似度
provider.RankBySimilarity(query, candidates)                  // 按相似度排序
provider.MostSimilar(query, candidates)                       // 取最相似向量
provider.NewEmbedderFromPreset(name, apiKey, model)           // 从预设构造
provider.NewEmbedder(EmbedderConfig{...})                     // 完全自定义
provider.WithEmbedderObservability(emb, provider.ObserveOptions{...}) // Embed 观测 hook
provider.ModelCapabilitiesFromPreset(name)                    // 查询预设能力元数据
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
    Name       ProviderName
    BaseURL    string
    APIKey     string
    Model      string // embedding 专用默认模型
    HTTPClient HTTPDoer
}
```

### 模型能力类型

```go
type Capability string

const (
    CapabilityChat             Capability = "chat"
    CapabilityStreaming        Capability = "streaming"
    CapabilityTools            Capability = "tools"
    CapabilityStructuredOutput Capability = "structured_output"
    CapabilityVision           Capability = "vision"
    CapabilityReasoning        Capability = "reasoning"
    CapabilityEmbedding        Capability = "embedding"
)

type ModelCapabilities struct {
    Provider       ProviderName
    ChatModel      string
    EmbeddingModel string
    ContextWindow  int
    Capabilities   []Capability
}

caps.Supports(provider.CapabilityVision) bool
```

## 设计决策

**为什么 OpenAI 兼容平台共用一个 `openaiProvider` 实现？**

因为 OpenAI、本仓库内置的国内平台以及 xAI，本质上都走 OpenAI 兼容协议。给每个平台写一个 struct 是过度设计。Claude、Google Gemini 不是 OpenAI 兼容协议，因此分别提供 `NewAnthropicProvider` / `NewGeminiProvider` 原生 HTTP 实现，但仍复用同一套 `Provider` 接口。

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

仓库根目录的发布前检查会分别验证根模块与 v2 子模块，并按核心 `provider` 包执行 80% 覆盖率门禁：

```bash
make release-check
```

真实 smoke test 默认跳过，只有设置对应环境变量时才会访问外部接口：

```bash
DEEPSEEK_API_KEY="<DEEPSEEK_API_KEY>" go test ./provider -run 'TestDeepSeek.*Smoke' -count=1 -v
ANTHROPIC_API_KEY="<ANTHROPIC_API_KEY>" go test ./provider -run TestAnthropicSmoke -count=1 -v
GEMINI_API_KEY="<GEMINI_API_KEY>" go test ./provider -run TestGeminiSmoke -count=1 -v
GEMINI_API_KEY="<GEMINI_API_KEY>" go test ./provider -run TestGeminiEmbeddingSmoke -count=1 -v
OLLAMA_MODEL="llama3.2" OLLAMA_BASE_URL="http://localhost:11434" go test ./provider -run TestOllamaSmoke -count=1 -v
```

不要把真实 API Key 写入源码、README、测试 fixture 或 shell history；本命令中的值只应使用临时密钥或本地安全注入方式。

## License

MIT
