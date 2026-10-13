# llm-provider

Go 语言统一多模型 LLM 调用库。一套代码接入 OpenAI 以及 DeepSeek、通义千问、智谱、百度千帆、硅基流动、Moonshot、火山方舟等 OpenAI 兼容平台。

⚠ v2.18.0 已删除清单中的业务策略 API。这是用户明确授权的同主版本不兼容例外，模块路径保持不变；旧调用方升级前必须迁移。请按 [迁移说明](MIGRATION.md) 将身份、账务、工具执行、历史与路由留在应用；Eino 通过独立 adapter 接入。

## 为什么做这个

国内主流大模型平台现在都兼容了 OpenAI Chat Completions 协议，本质上只是 BaseURL 和 APIKey 的差异。但每次接入新平台还是要翻文档查地址、记模型名、写一堆重复的初始化代码。

这个库做的事情很简单：

- 预置了各平台的 BaseURL 和推荐模型，传个 APIKey 就能用
- 统一的 `Provider` 接口，业务代码不需要关心底层是哪个平台
- 支持非流式和流式两种调用模式
- Tool Use / Function Calling 协议：声明工具、接收调用、回传结果，执行由应用管理
- 厂商原生联网搜索工具透传（`WebSearchTool`，映射 Anthropic / Gemini / OpenAI Responses 三条原生路径，以及智谱与百炼两个 OpenAI 兼容平台），搜索用量进入统一 Usage
- OpenAI 兼容 Files API 文件管理（`FileService`：上传 / 内容抽取 / 删除），覆盖 Moonshot、通义千问、智谱的文档问答流程
- 主包保持轻量，零额外厂商 SDK 依赖；OpenAI 兼容路径复用 `sashabaranov/go-openai`，非兼容路径用标准库 `net/http`

## 项目结构

```
llm-provider/
├── go.mod
├── README.md
├── CHANGELOG.md
├── provider/
│   ├── provider.go            # 核心：Provider 接口、请求/响应、Tool Use 类型
│   ├── content.go             # Message 多模态 ContentPart 与便捷构造器
│   ├── files.go               # FileService：OpenAI 兼容 Files API（上传/抽取/删除）
│   ├── presets.go             # 各平台预设配置（BaseURL + Chat/Embedding 默认模型）
│   ├── helpers.go             # Chat 便捷函数：SimpleChat、CollectStream
│   ├── reasoning.go           # Thinking 结构与推理模式常量
│   ├── compat_thinking.go     # DeepSeek / 智谱 / Kimi / 方舟共享的顶层 thinking 扩展字段映射
│   ├── compat_qwen.go         # 百炼专属映射（顶层 enable_thinking / thinking_budget）
│   ├── native_anthropic.go    # Anthropic Messages API 原生实现
│   ├── native_gemini.go       # Gemini Generative Language API 原生实现
│   ├── native_ollama.go       # Ollama /api/chat 原生实现
│   ├── native_openai_responses.go # OpenAI Responses API（/responses）实现
│   ├── response_format.go     # Structured Output 结构与构造器
│   ├── embedder.go            # Embedder 接口、请求/响应、openaiEmbedder 实现
│   ├── embedder_helpers.go    # Embedding 便捷函数：SimpleEmbed、EmbedBatch
│   ├── errors.go              # ProviderError / ErrorCode / WrapProviderError
│   ├── middleware.go          # Middleware / Handler 类型 + WithMiddlewares 装饰器
│   ├── retry.go               # WithRetry / RetryMiddleware / BackoffFunc
│   ├── breaker.go             # Breaker 熔断器：滑动窗口计数 + 指数退避冷却 + 半开探测
│   ├── ratelimit.go           # RateLimiter 客户端限流：RPM / TPM 令牌桶 + 响应头自适应
│   ├── reranker.go            # Reranker 接口：OpenAI 兼容 /rerank 端点
│   ├── decider.go             # Decider 接口：TypeSafe AI Jev 结构化判断
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
│   ├── reranker_test.go       # rerank 协议映射与边界单测
│   ├── observability_test.go  # 观测 hook 单测
│   └── runtime_test.go        # 运行时集成测试
└── example/
    ├── main.go                # 基础使用示例（Chat）
    ├── reasoning/main.go      # Thinking / Reasoning 示例
    ├── structured/main.go     # Structured Output 示例
    ├── vision/main.go         # Vision 多模态输入示例（text + image）
    ├── tooluse/main.go        # Tool Use 手动多轮示例
    ├── middleware/main.go     # Middleware：Logging / TokenStats / Retry 参考实现
    └── embedding/main.go      # Embedding + RAG 最小闭环示例
```

> 文件前缀标记协议适配的归属：`native_<平台>.go` 是非 OpenAI 兼容协议的原生 HTTP 实现，`compat_<平台>.go` 是 OpenAI 兼容协议下该平台的专属字段映射。请求构建路径（`provider.go`）不含平台分支，平台与实现的对应关系集中在 `presetCatalog`、`thinkingSupportByProvider`、`thinkingAppliers`、`extraFieldsBuilders` 四张表里；构造入口 `presets.go` / `embedder.go` 按平台分派到原生实现。

## 安装

```bash
go get github.com/gtkit/go-llm-provider/v2
```

> 将 `github.com/gtkit/go-llm-provider/v2` 替换为你实际的模块路径。

## 支持的平台

| 平台 | ProviderName | 预设 BaseURL | 默认 Chat 模型 | 默认 Embedding 模型 | API Key 获取 |
|------|-------------|-------------|---------|---------|-------------|
| DeepSeek | `deepseek` | `https://api.deepseek.com/v1` | `deepseek-v4-flash` | — | [platform.deepseek.com](https://platform.deepseek.com/) |
| 通义千问（百炼） | `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen3.7-plus` | `text-embedding-v4` | [百炼控制台](https://bailian.console.aliyun.com/) |
| 智谱 AI / GLM | `zhipu`（别名：`ProviderGLM`） | `https://open.bigmodel.cn/api/paas/v4/` | `glm-5.3` | `embedding-3` | [open.bigmodel.cn](https://open.bigmodel.cn/) |
| 百度千帆 | `qianfan` | `https://qianfan.baidubce.com/v2` | `ernie-5.1` | `embedding-v1` | [千帆控制台](https://console.bce.baidu.com/qianfan/) |
| 硅基流动 | `siliconflow` | `https://api.siliconflow.cn/v1` | `deepseek-ai/DeepSeek-V4-Flash` | `BAAI/bge-m3` | [siliconflow.cn](https://siliconflow.cn/) |
| Moonshot / Kimi | `moonshot`（别名：`ProviderKimi`） | `https://api.moonshot.cn/v1` | `kimi-k2.6` | — | [platform.kimi.com](https://platform.kimi.com/) |
| 火山方舟 / 豆包 | `ark` | `https://ark.cn-beijing.volces.com/api/v3` | `doubao-seed-2-1-pro-260628` | `doubao-embedding-text-240515` | [方舟控制台](https://console.volcengine.com/ark) |
| OpenAI | `openai` | `https://api.openai.com/v1` | `gpt-5.6-terra` | `text-embedding-3-small` | [platform.openai.com](https://platform.openai.com/) |
| Anthropic / Claude | `anthropic` | `https://api.anthropic.com` | `claude-sonnet-5` | — | [console.anthropic.com](https://console.anthropic.com/) |
| Google Gemini | `gemini` | `https://generativelanguage.googleapis.com/v1beta` | `gemini-3.5-flash` | `gemini-embedding-001` | [aistudio.google.com](https://aistudio.google.com/) |
| Ollama | `ollama` | `http://localhost:11434` | 需调用方指定 | — | 本地服务 |
| xAI / Grok | `xai` | `https://api.x.ai/v1` | `grok-4.6` | — | [console.x.ai](https://console.x.ai/) |
| Groq | `groq` | `https://api.groq.com/openai/v1` | `openai/gpt-oss-120b` | — | [console.groq.com](https://console.groq.com/) |
| Mistral AI | `mistral` | `https://api.mistral.ai/v1` | `mistral-large-latest` | `mistral-embed` | [console.mistral.ai](https://console.mistral.ai/) |
| Cohere | `cohere` | `https://api.cohere.ai/compatibility/v1` | `command-a-plus-05-2026` | `embed-v4.0` | [dashboard.cohere.com](https://dashboard.cohere.com/) |

> 预设默认模型截至 **2026-09** 对照各厂商官方文档更新（DeepSeek 旧的 `deepseek-chat` / `deepseek-reasoner` 已于 2026-07-24 停用，Groq 的 `llama-3.3-70b-versatile` 已于 2026-08-16 关停，Gemini 2.5 系计划 2026-10 关停）。预设地址和默认模型仍会随平台更新而变化，建议定期对照各平台官方文档确认。
> Embedding 列显示"—"的平台表示官方暂无 embedding 接口，`NewEmbedderFromPreset` 会返回错误。
> Azure OpenAI 与 Amazon Bedrock 需要资源名、region 或 deployment/model ARN 等运行时信息，请使用 `NewAzureOpenAIProvider` / `NewBedrockOpenAIProvider` 显式创建。

### 能力矩阵

| 平台 | Chat | Streaming | Tools | Structured Output | Vision | File | File Upload | Reasoning | Embedding | Rerank | Web Search | 协议 |
|------|------|-----------|-------|-------------------|--------|------|-------------|-----------|-----------|------|------------|------|
| DeepSeek | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 是 | 否 | 否 | 否 | OpenAI 兼容 |
| 通义千问（百炼） | 是 | 是 | 是 | 是 | 是 | 否 | 是 | 是 | 是 | 否 | 是 | OpenAI 兼容 |
| 智谱 AI / GLM | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 是 | 是 | 否 | 是 | OpenAI 兼容 |
| 百度千帆 | 是 | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 是 | 否 | 否 | OpenAI 兼容 |
| 硅基流动 | 是 | 是 | 是 | 是 | 否 | 否 | 否 | 否 | 是 | 是 | 否 | OpenAI 兼容 |
| Moonshot / Kimi | 是 | 是 | 是 | 是 | 是 | 否 | 是 | 是 | 否 | 否 | 否 | OpenAI 兼容 |
| 火山方舟 / 豆包 | 是 | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 是 | 否 | 否 | OpenAI 兼容 |
| OpenAI | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 是 | 是 | 否 | 否 | OpenAI 兼容 |
| Anthropic / Claude | 是 | 是 | 是 | 是 | 是 | 是 | 否 | 是 | 否 | 否 | 是 | 原生 HTTP |
| Google Gemini | 是 | 是 | 是 | 是 | 是 | 是 | 否 | 是 | 是 | 否 | 是 | 原生 HTTP |
| Ollama | 是 | 是 | 是 | 是 | 是 | 否 | 否 | 是 | 否 | 否 | 否 | 原生 HTTP |
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
- 当前 native 实现覆盖文本、图片、文件输入、非流式、SSE 流式、错误分类；`Chat` 已映射 Tool Use / Function Calling 与结构化输出（Claude 走原生 `output_config.format`，Gemini 走 `responseJsonSchema`）
- Claude / Gemini native streaming 已开放 Tool Use 增量输出，调用方可从 `StreamChunk.ToolCalls` 累积工具调用参数
- 思考模型的工具循环所需的往返内容已内建：Claude 的带签名思考块（`ThinkingBlocks`）、Gemini 3 的 `ThoughtSignature` 都随 `AssistantMessage()` 回传，见「Thinking」一节的「推理内容往返」
- Claude 支持内容片段级 `CacheControlEphemeral()` / `CacheControlEphemeralTTL(CacheControlTTL1h)`，system 提示词以内容块数组下发以承载缓存断点；不支持的平台会忽略该 hint
- `NativeProviderConfig.Headers` 可附加自定义请求头（Anthropic 的 `anthropic-beta`、企业网关鉴权头），同名覆盖库默认头

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
- `Message.Content` 已从 `string` 升级为 `[]ContentPart`。旧写法 `Message{Role: ..., Content: "..."}` 不再编译，请改用 `provider.UserText(...)`、`provider.SystemText(...)`、`provider.TextPart(...)` 等构造器。
- `ChatRequest.EnableThinking` 已被移除，请改用 `ChatRequest.Thinking = &provider.Thinking{...}`。
- 如需强制 JSON 输出，改用 `ChatRequest.ResponseFormat = provider.JSONObjectFormat()` 或 `provider.JSONSchemaFormatStrict(...)`。
- 新代码优先使用 `provider.AllPresets()` 读取预设；`provider.Presets` 仅为兼容旧代码保留。

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
| 原生联网搜索 | 不支持 | `WebSearchTool`，映射 Anthropic / Gemini / OpenAI Responses 与智谱 / 百炼，搜索用量计入 `Usage.WebSearchRequests` / `Usage.WebSearchGroundedPrompts`，元数据见 `ChatResponse.Search` |
| 本地推理 | 不支持 | `ProviderOllama` |
| 企业入口 | 不支持 | `NewAzureOpenAIProvider` / `NewBedrockOpenAIProvider` |
| 典型升级动作 | 原样继续用即可 | 需要改 import 路径、消息构造方式、thinking 配置方式 |

### 方式一：NewProviderFromPreset（指定模型）

使用预设地址，但自定义模型名。适合同一个平台想用不同模型的场景。

```go
// 用千问的 qwen3.7-max 模型而不是默认的 qwen3.7-plus
p, err := provider.NewProviderFromPreset(
    provider.ProviderQwen,
    os.Getenv("QWEN_API_KEY"),
    "qwen-max",  // 留空则使用预设的默认模型
)

// 应用直接保存并调用 p。
```

### 方式二：NewProvider（完全自定义）

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

### 方式三：走网关 / 代理覆盖 Host

内置平台的流量要经过自建网关、企业代理或中转服务时，保留原来的 `ProviderName`，只把 `BaseURL` 换成网关地址即可。`Name` 不变，平台能力判定与平台专属字段注入（如火山方舟的 `thinking`）照旧生效。

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

Embedding 与 Rerank 同理，`EmbedderConfig` / `RerankerConfig` 上都有 `BaseURL`：

```go
e, err := provider.NewEmbedder(provider.EmbedderConfig{
    Name:    provider.ProviderQwen,
    BaseURL: "https://llm-gw.corp.internal/dashscope/compatible-mode/v1",
    APIKey:  os.Getenv("QWEN_API_KEY"),
    Model:   "text-embedding-v4",
})

r, err := provider.NewReranker(provider.RerankerConfig{
    Name:    provider.ProviderSiliconFlow,
    BaseURL: "https://llm-gw.corp.internal/siliconflow/v1",
    APIKey:  os.Getenv("SILICONFLOW_API_KEY"),
    Model:   "BAAI/bge-reranker-v2-m3",
})
```

网关地址写到哪一层，取决于各构造函数的路径拼接规则：

| 构造函数 | 实际请求路径 | `BaseURL` 写到 | 留空时 |
| --- | --- | --- | --- |
| `NewProvider` / `NewEmbedder`（OpenAI 兼容） | `{BaseURL}/chat/completions`、`{BaseURL}/embeddings` | 含 `/v1` 这类版本段 | 使用 OpenAI 官方地址 `https://api.openai.com/v1` |
| `NewAnthropicProvider` | `{BaseURL}/v1/messages` | 版本段之前（库会补 `/v1`） | 使用预设的 `https://api.anthropic.com` |
| `NewGeminiProvider` | `{BaseURL}/models/{model}:generateContent` | 含 `/v1beta` | 使用预设的 `https://generativelanguage.googleapis.com/v1beta` |
| `NewOllamaProvider` | `{BaseURL}/api/chat` | 主机名（不带路径） | 使用 `http://localhost:11434` |
| `NewReranker` | `{BaseURL}/rerank` | 含 `/v1` 这类版本段 | `BaseURL` 为必填项 |
| `NewDecider` | `{BaseURL}/systemone` | 含 `/v1` 这类版本段 | 使用 `https://api.typesafe.ai/v1` |

末尾多余的 `/` 会被忽略，`https://gw.example.com/v1` 与 `https://gw.example.com/v1/` 等价。


### Claude / Gemini 原生 HTTP Provider

Claude 和 Gemini 可以直接走预设：

```go
claude, err := provider.NewProviderFromPreset(
    provider.ProviderAnthropic,
    os.Getenv("ANTHROPIC_API_KEY"),
    "", // 留空使用 claude-sonnet-5
)

gemini, err := provider.NewProviderFromPreset(
    provider.ProviderGemini,
    os.Getenv("GEMINI_API_KEY"),
    "", // 留空使用 gemini-3.5-flash
)
```

如果要覆盖 BaseURL、注入自定义 HTTP client 或附加请求头，使用原生构造函数：

```go
claude, err := provider.NewAnthropicProvider(provider.NativeProviderConfig{
    APIKey:     os.Getenv("ANTHROPIC_API_KEY"),
    Model:      "claude-opus-5",
    HTTPClient: httpClient,
    // 自定义请求头在库默认头之后写入、同名覆盖：beta 特性开关、企业网关鉴权头都走这里
    Headers: map[string]string{"anthropic-beta": "compact-2026-01-12"},
})

gemini, err := provider.NewGeminiProvider(provider.NativeProviderConfig{
    APIKey:  os.Getenv("GEMINI_API_KEY"),
    BaseURL: "https://generativelanguage.googleapis.com/v1beta",
    Model:   "gemini-3.8-flash",
})
```

当前原生实现支持文本、图片、文件输入、非流式、SSE 流式、request id 元数据与错误分类。`Chat` 已支持 Tool Use / Function Calling 和结构化输出；流式 Tool Use 会通过 `StreamChunk.ToolCalls` 暴露增量工具调用。Gemini 3 系要求把 `functionCall` 上的 `thoughtSignature` 原样回传、`functionResponse` 携带函数名，库已在 `ToolCall.ThoughtSignature` 与 `Message.ToolName` 上承载并自动回填。

### OpenAI Responses API

OpenAI 官方已把 Responses API（`POST {BaseURL}/responses`）作为推理、工具调用与多轮的推荐入口，
DeepSeek、火山方舟、智谱、Kimi 也提供同形端点。`NewOpenAIResponsesProvider` 返回的仍是统一的
`Provider`，可通过统一接口调用并接入观测：

```go
p, err := provider.NewOpenAIResponsesProvider(provider.OpenAIResponsesConfig{
    APIKey: os.Getenv("OPENAI_API_KEY"),
    Model:  "gpt-5.6-terra",
    // Name 留空即 ProviderOpenAI；接 DeepSeek 等平台的 Responses 端点时填对应 ProviderName 并覆盖 BaseURL
})
```

与 Chat Completions 路径的差异：

- **推理项一等往返**：默认 `store: false`（零数据保留友好），请求 `reasoning.encrypted_content`，推理项以
  `ThinkingBlocks{Signature: 项 id, Redacted: encrypted_content}` 返回并随 `AssistantMessage()` 回放；
  `Thinking.Effort` 映射 `reasoning.effort` 并请求 `summary: auto`，摘要进 `Reasoning`。
- **原生联网搜索**：`WebSearchTool()` 映射内建 `web_search`（支持 `AllowedDomains`，不支持 `MaxUses` / `BlockedDomains`），
  查询与 `url_citation` 引用进 `ChatResponse.Search`，搜索次数计入 `Usage.WebSearchRequests`。
- **不支持的采样参数**：`Stop`、`Seed`、`CandidateCount` 在该协议下没有对应字段，返回 `ErrInvalidRequest`。
- 结构化输出走 `text.format`，函数工具与 `tool_choice` 用 Responses API 的扁平形态，`FunctionDef.Strict` 生效。

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


原生路径已映射 `/api/chat` 的完整能力，与云平台共用同一套请求类型：

- **Tool Use**：`Tools` 下发为 Ollama 的函数工具，由应用执行。Ollama 不签发调用 ID，库按 `ollama_<函数名>_<序号>` 合成；工具结果带 `tool_name`（`ToolResultMessageFor` 或按 ID 反查）。`ToolChoice` 只接受默认的自动选择，`ParallelToolCalls` 无对应参数，非默认取值返回 `ErrInvalidRequest`。
- **结构化输出**：`JSONObjectFormat()` 映射为 `format: "json"`，`JSONSchemaFormat` 把 schema 原样下发。
- **思考**：`Thinking.Enabled` 映射 `think: true/false`，`Thinking.Effort` 映射 `think: "low"|"medium"|"high"`（gpt-oss 等支持档位的模型）；响应的 `thinking` 归位到 `Reasoning` / `ReasoningDelta`，回传历史时写回 assistant 的 `thinking`。
- **图片**：`ImageDataPart` 与 data URL 映射为 `images`；Ollama 不拉取远程 URL，`ImageURLPart("https://...")` 返回 `ErrInvalidRequest`。
- **Seed**：映射 `options.seed`。

具体模型是否支持工具 / 思考 / 图片由 Ollama 按模型返回错误，能力矩阵中 Ollama 一行描述的是协议映射。

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
    Name:    "local-vllm",                    // 自定义名称，用于实例识别与日志，不影响请求
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
- **横切能力照常可用**：`WithRetry`、`WithObservability` 可用于本地 provider；本地和云端模型的路由由应用决定。

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
    Model: "deepseek-v4-pro",  // 可选，覆盖默认模型
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

部分平台提供服务端执行的联网搜索：模型自行决定何时搜索、搜索结果直接参与生成，无需自建搜索 API 与工具循环。用 `WebSearchTool()` 声明：

```go
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("Go 1.26 有哪些新特性？")},
    Tools: []provider.Tool{
        provider.WebSearchTool(),
        // 需要限制搜索行为时（各平台支持的选项见下表；不支持的选项一律返回
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

- **平台支持矩阵**：未列出的 provider（DeepSeek、Kimi、火山方舟、千帆、Ollama 等）收到该工具返回 `ErrInvalidRequest`，不静默丢弃；切换平台前可用 `caps.Supports(provider.CapabilityWebSearch)` 预检。

| 平台 | 映射为 | `MaxUses` | `AllowedDomains` | `BlockedDomains` | 回传来源 | 可与函数工具混用 |
|---|---|---|---|---|---|---|
| Anthropic | `web_search_20250305` server tool | 支持 | 支持多值 | 支持 | 是 | 否 |
| Gemini | `google_search`（Grounding） | 否 | 否 | 否 | 是 | 否 |
| OpenAI Responses | 内建 `web_search` | 否 | 支持多值 | 否 | 是 | 是 |
| 智谱 | `tools` 中的 `web_search` 工具项 | 否 | **仅单值** | 否 | 是 | 是 |
| 百炼 | 顶层 `enable_search` / `search_options` | 否 | 支持多值 | 否 | **否** | 是 |

  智谱的 `search_domain_filter` 是单值字符串，`AllowedDomains` 传入多个域名返回 `ErrInvalidRequest`——
  静默只取第一个会让白名单形同虚设，那是安全边界，只能拒绝。

  百炼的 OpenAI 兼容协议不回传搜索来源（官方文档明示"不支持搜索来源返回和角标标注"），
  此时 `resp.Search.SourcesUnavailable` 为 `true` 且 `Sources` 为空。该字段是**平台协议的静态属性**，
  用来把"模型没搜"和"搜了但协议不回传"区分开：`SourcesUnavailable == false` 且 `Sources` 为空，
  才表示模型判断本次无需搜索。

  智谱的来源回传形状**尚未用真实 key 实测**（官方文档只给出独立搜索端点的示例，Chat 路径与流式
  落点未公开），解析器在响应顶层与 `choices[].message` / `choices[].delta` 三处查找，取不到时
  `Sources` 为空而非报错。进展见 [ROADMAP](../ROADMAP.md)。
- **仅保证单轮**：Anthropic 的 `AssistantMessage()` 不保存服务端工具块、引用与 `encrypted_content`，无法无损回传续跑，因此原生搜索当前**仅保证单轮**（一次 `Chat`/`ChatStream`）；多轮追问请每轮重新发起，或改用函数工具模式。
- **能否与函数工具混用**：智谱、百炼与 OpenAI Responses 允许混用。Anthropic 的服务端工具块尚不支持跨轮往返、Gemini 2.5 系平台不支持 `google_search` 与函数声明同用，这两条路径在客户端返回 `ErrInvalidRequest`；Anthropic 侧同理禁止与结构化输出组合。Gemini 3 官方已支持搜索与函数工具组合，但本库当前不做模型嗅探、统一按 2.5 系口径拒绝，该组合待后续版本支持。纯搜索由服务端执行，不产生客户端 `ToolCall`。
- **输入约束**：纯搜索请求的 `ToolChoice` 只接受 `nil` / `ToolChoiceAuto`（由模型决定是否搜索，Gemini 侧此时不下发 `functionCallingConfig`）；重复声明搜索工具、同一 `Tool` 同时设 `Function` 与 `WebSearch`、`ToolChoiceRequired`/`ToolChoiceFunction`/`ToolChoiceNone` 与服务端搜索工具组合、Gemini 搜索叠加 `CandidateCount > 1`，均返回 `ErrInvalidRequest`。
- **pause_turn**：Anthropic 在服务端工具长时间运行时可能暂停回合（`stop_reason: "pause_turn"`），续跑需要回传服务端工具块，当前不支持——返回 `*PauseTurnError`（`errors.Is` 命中 `ErrUnsupportedCapability`）。**不要原样重发请求**，那会重新执行搜索、二次计费；应降低 `MaxUses` 或改用函数工具模式。该错误携带暂停前已产生的 `Usage` 与 `Search`，观测层会提取用量供调用方记录。
- **搜索元数据与错误**：查询、来源、Google Search 入口通过 `ChatResponse.Search` / 最终 `StreamChunk.Search` 返回（`SearchMetadata`）；平台经 HTTP 200 报告的搜索失败（如 `max_uses_exceeded`、`too_many_requests`）进入 `SearchMetadata.Errors`，调用方据此区分"未搜索"与"搜索失败"，本库不静默丢弃。**合规提示**：Gemini 响应携带 `SearchEntryPoint` 时，Google 要求向终端用户展示 Search Suggestions；回复文本级的引用区间暂不透出。
- **搜索用量**：`Usage.WebSearchRequests` 和 `Usage.WebSearchGroundedPrompts` 保留平台返回的计次事实；费用计算与对账由应用按供应商规则处理。智谱与百炼目前不填充搜索次数，以平台账单为准。
- **流式**：搜索过程中的服务端工具事件不会出现在 `StreamChunk.ToolCalls` 中，搜索计次与元数据随最终 chunk（`FinishReason` 非空）给出。

#### 未覆盖平台的联网搜索接入（函数工具模式）

将搜索声明为普通函数工具，应用接收 `ToolCalls` 后调用自己的搜索客户端，再用 `ToolResultMessageFor` 回传结果。授权、域名限制、超时、结果数量、缓存与费用由应用处理；来源链接随工具结果保存。厂商原生搜索仍使用 `WebSearchTool`。

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

> 注意：`WithRetry` / `WithMiddlewares` 等包装后的 Provider 不再实现 `FileService`，请在包装前保留原始句柄用于文件操作。平台是否提供 Files API 以能力矩阵 File Upload 列与平台官方文档为准（如 DeepSeek 无此接口，调用会返回平台侧错误）。

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

| 平台 | `Enabled` | `Effort` | `BudgetTokens` | 下发到 |
| --- | --- | --- | --- | --- |
| OpenAI / Azure OpenAI | | ✅ | | `reasoning_effort` |
| DeepSeek | ✅ | ✅ | | 顶层 `thinking.type` + `reasoning_effort`（low / high / max） |
| 智谱 GLM | ✅ | ✅ | | 顶层 `thinking.type` + `reasoning_effort`（GLM-5.3 不可关闭） |
| Moonshot / Kimi | ✅ | ✅ | | 顶层 `thinking.type`（K2.6）+ `reasoning_effort`（K3） |
| 火山方舟 Ark | ✅ | ✅ | | 顶层 `thinking.type` + `reasoning_effort` |
| 阿里云百炼（通义千问） | ✅ | | ✅ | 顶层 `enable_thinking` + `thinking_budget` |
| Anthropic（原生） | ✅ | ✅ | ✅（仅 4.5 及更早） | `thinking.type` adaptive / disabled + `output_config.effort` |
| Gemini（原生） | ✅ | ✅ | ✅（与 Effort 互斥） | `thinkingConfig.thinkingLevel` / `thinkingBudget` |
| Ollama（原生） | ✅ | ✅ | | `think`（bool 或 low / medium / high 档位字符串） |

传入表中未标注的字段时，请求在构建阶段返回 `ErrInvalidRequest`，错误信息会列出该平台
已映射的字段。推理 token 计入输出、按输出价计费，静默忽略会让调用方误以为思考已开启，
并为未发生的思考付费。需要在运行前判断时，用 `ModelCapabilitiesFromPreset` 取回
`ModelCapabilities` 再调 `Supports(provider.CapabilityReasoning)`。

`Effort` 的取值原样透传，各平台接受的档位集合不同：OpenAI 为 `none` / `minimal` / `low` /
`medium` / `high` / `xhigh` / `max`（按模型而异）；DeepSeek、智谱、Kimi K3 为 `low` / `high` /
`max`；Anthropic 为 `low` / `medium` / `high` / `xhigh` / `max`；Gemini 为 `minimal` / `low` /
`medium` / `high`。`ThinkingEffort*` 常量覆盖了这些取值，取值是否被目标模型接受由平台校验。

#### 推理内容往返（思考模型 + 工具循环必读）

思考模型在工具循环中普遍要求把上一轮的推理内容原样回传：DeepSeek 带 `tools` 时缺失
`reasoning_content` 直接 400；Kimi K3 要求回传完整 assistant 消息；Anthropic 开启思考并
使用工具时必须回传带签名的 `thinking` 块；Gemini 3 要求回传 `functionCall` 上的
`thoughtSignature`。库把这些载体统一放在 `Message` 上，`resp.AssistantMessage()` 可构造需要回传的 assistant 消息，调用方负责追加和保存：

- `Message.Reasoning`：推理文本，OpenAI 兼容路径映射为 `reasoning_content`，只对已知接受该字段的
  平台下发（DeepSeek / 智谱 / Kimi / 百炼 / 方舟；OpenAI 官方端点会拒绝该字段）。自定义接入的平台用
  `ProviderConfig.SupportsReasoningContent` 声明。不需要回传时置空即可省下这部分输入 token。
- `Message.ThinkingBlocks`：Anthropic 的签名思考块与加密块（`redacted_thinking`），原样回传、
  不得改写；没有签名的块不会下发。流式下由 `StreamChunk.ThinkingSignature` / `RedactedThinking`
  标记块边界，由应用或 adapter 拼装。
- `ToolCall.ThoughtSignature`：Gemini 3 的工具调用签名，随 `ToolCalls` 回传。
- `Message.ToolName`：工具结果对应的函数名，Gemini 的 `functionResponse` 必需；用
  `ToolResultMessageFor(call, result)` 构造即可自动带上，只给 `ToolCallID` 时库会在同一请求内反查。

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
  （DeepSeek / 智谱 / Kimi / 火山方舟用顶层 `thinking`、百炼用顶层
  `enable_thinking`），库无从代为映射，
  对未收录平台始终返回 `ErrInvalidRequest`。同理，`ProviderConfig.SupportsReasoningContent`
  解锁的是 assistant 消息上 `reasoning_content` 的回传（思考模型工具循环所需）。
- **内置预设优先。** 对已收录的平台声明该字段不生效，它们的支持范围由库判定——
  内置平台的映射属于库的实现，不交给调用方覆盖。

#### 档位口径（OpenAI / Azure / DeepSeek / 智谱 / Kimi / 火山方舟）

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

`Effort` 取值原样透传给平台的 `reasoning_effort`。DeepSeek、智谱、Kimi 与火山方舟另外把
`Enabled` 映射为请求体顶层的 `thinking`（`{"type":"enabled"|"disabled"}`，nil 时不下发该字段，
由平台按模型默认行为决定：DeepSeek V4、GLM-5、Kimi K2.6 默认开启）。两者互相独立：同时设置
`Enabled: false` 与非空 `Effort` 时都会原样下发，本库不做取舍，最终以平台侧裁决为准。
注意 GLM-5.3、Kimi K3 不允许关闭思考，`Enabled: false` 由平台报错。

#### 阿里云百炼（通义千问）

百炼映射 `Enabled` 与 `BudgetTokens` 两个字段，都落在请求体顶层：`Enabled` 映射
`enable_thinking`（true / false），`BudgetTokens` 映射 `thinking_budget`。两者互相独立，
只设置其一时只下发其一；字段为 nil 时不下发，由百炼按模型自身的行为决定。

```go
enabled, budget := true, 1024
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("逐步分析这道题")},
    Thinking: &provider.Thinking{
        Enabled:      &enabled,
        BudgetTokens: &budget,
    },
})
```

`BudgetTokens` 原样透传，取值是否被目标模型接受由平台校验。响应中的 `reasoning_content`
进入 `ChatResponse.Reasoning`（流式为 `StreamChunk.ReasoningDelta`），不混入正文。

#### Anthropic（原生路径）

Claude 4.6 起的推荐口径是**自适应思考**：`Enabled: true` 下发 `thinking.type = adaptive`，
由模型按任务复杂度决定是否思考与思考多少，推理强度经 `Effort` 映射到 `output_config.effort`
（`low` / `medium` / `high` / `xhigh` / `max`，按模型而异）；`Enabled: false` 下发 `disabled`。
Claude 5 系（`claude-sonnet-5`、`claude-opus-5`、`claude-fable-5-1`）默认即自适应思考，
只在需要调档位或关闭时才传 `Thinking`。

```go
enabled := true
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("逐步分析这段代码的复杂度")},
    Thinking: &provider.Thinking{Enabled: &enabled, Effort: provider.ThinkingEffortMedium},
})
```

`BudgetTokens` 是 Claude 4.5 及更早模型的旧式固定预算口径（`thinking.type = enabled` +
`budget_tokens`）：给出即开启，须为正数且需小于本次请求的 `MaxTokens`（未显式设置时库会
把 `max_tokens` 抬到"预算 + 4096"）。Claude 4.6 已废弃、4.7 起平台直接拒绝该参数，
新模型请改用 `Enabled` + `Effort`。

#### Gemini（原生路径）

Gemini 3 系的推荐口径是档位：`Effort` 映射为 `thinkingConfig.thinkingLevel`
（`minimal` / `low` / `medium` / `high`，按模型而异；3 系不可关闭思考）。
`BudgetTokens` 映射为 2.5 系的 `thinkingBudget`，与 `Effort` 互斥、同时设置返回 `ErrInvalidRequest`。
未给出档位与预算时，借用平台自身的语义取值表达意图：`Enabled: true` 下发 `thinkingBudget: -1`
（由模型动态决定），`Enabled: false` 下发 `thinkingBudget: 0`（禁用，仅 2.5 系接受）。

```go
resp, err := p.Chat(ctx, &provider.ChatRequest{
    Messages: []provider.Message{provider.UserText("解释这段推导")},
    Thinking: &provider.Thinking{Effort: provider.ThinkingEffortLow},
})
```

两个平台的响应形态也已归位：Anthropic 的 `thinking` 内容块、Gemini 中带 `thought`
标记的 part，都进入 `Reasoning` 而不混入 `Content`。Anthropic 的加密思考块
（`redacted_thinking`）无法读取，不会出现在任何文本字段里，但会随 `ThinkingBlocks` 原样保留供回传。

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

流式签名与加密块通过 `StreamChunk.ThinkingSignature`、`RedactedThinking` 和对应块索引透出。调用方按块顺序保留这些字段；Eino adapter 提供流消息转换。

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

各协议的落点：OpenAI 兼容路径下发 `response_format`；Anthropic 走原生 `output_config.format`
（`json_schema`），不再借用"强制调用假工具"，因此可与真正的工具调用、原生搜索并存；Gemini 走
`responseJsonSchema`（接受标准 JSON Schema，含 `additionalProperties` 等关键字）。函数工具的
入参也可用 `FunctionDef.Strict` 请求严格约束（OpenAI / Anthropic），schema 需满足平台的严格模式要求。

回复解码和业务校验由调用方完成：检查 `Chat` 的错误后，用 `github.com/gtkit/json/v2` 的 `json.Unmarshal([]byte(resp.Content), &result)` 解码。完整示例见 [`example/structured/main.go`](example/structured/main.go)。

### Token counting

支持原生 token counting 的 provider 会实现 `TokenCounter`。当前 Gemini（`countTokens`）与 Anthropic（`/v1/messages/count_tokens`，免费端点）原生 provider 支持 `CountTokens`；其他 provider 会返回 `ErrUnsupportedCapability`。注意：Anthropic 官方将该计数定义为**估算值**，与实际计费 token 可能有少量偏差——可供应用的历史与额度策略参考，不应作为无安全余量的硬额度判定，结算一律以响应 `Usage` 为准。无原生支持时由应用决定是否另行估算。

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

### 手动管理多轮对话

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

        // 将结果追加到历史（带函数名，Gemini functionResponse 必需）
        messages = append(messages, provider.ToolResultMessageFor(tc, result))
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

### 完整调用（自定义维度 / User / 任务类型 / 模型覆盖）

```go
dims := 512 // OpenAI v3 和 Qwen v3/v4 支持按维度截断
resp, err := emb.Embed(ctx, &provider.EmbeddingRequest{
    Model:      "text-embedding-3-large", // 可选，覆盖默认模型
    Input:      []string{"hello", "world"},
    Dimensions: &dims,
    User:       "user-123",
    TaskType:   provider.EmbeddingTaskDocument, // 非对称编码：建索引侧用 Document、查询侧用 Query
})

for _, e := range resp.Data {
    fmt.Printf("Index=%d, len=%d\n", e.Index, len(e.Vector))
}
fmt.Printf("Usage: %d tokens\n", resp.Usage.TotalTokens)
```

`TaskType` 当前映射到 Gemini 的 `task_type`（`RETRIEVAL_QUERY` / `RETRIEVAL_DOCUMENT` /
`SEMANTIC_SIMILARITY` / `CLASSIFICATION` / `CLUSTERING`）；OpenAI 兼容端点的模型对称编码、
协议上也没有该字段，该提示被忽略——与 `Thinking` 的 fail-closed 不同，忽略它不产生费用或安全影响。

### 批量建索引：自动分片与并发

各平台 embeddings 端点单次请求的文本条数上限差异很大（OpenAI 2048、Gemini 100、Cohere 96、
百炼 10）。`EmbedBatch` 按 Embedder 名取已知上限自动分片、顺序请求；需要并发或自定义分片时用
`EmbedBatchWithOptions`：

```go
vecs, err := provider.EmbedBatchWithOptions(ctx, emb, chunks, provider.EmbedBatchOptions{
    TaskType:    provider.EmbeddingTaskDocument,
    BatchSize:   64, // <= 0 时按平台已知上限，未知平台用 10
    Concurrency: 4,  // 同时在途的分片数，配合 WithEmbedderRateLimit 控制 RPM / TPM
})
```

任一分片失败即返回错误并取消其余在途请求，不返回带空洞的半成品——RAG 建索引宁可整批重跑，
也不要悄悄漏掉一段文档。

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

// 4. 把匹配文档拼进 prompt 让 LLM 回答
reply, _ := provider.SimpleChatWithSystem(ctx, chat,
    "基于以下资料回答: "+docs[best.Index],
    query,
)
```

完整可运行示例见 [`example/embedding/main.go`](example/embedding/main.go)。

### 自定义 Embedder

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

## Decide（结构化判断，TypeSafe AI Jev）

[TypeSafe AI](https://docs.typesafe.ai/introduction) 的 Jev 模型面向程序内部的高频判断：给定状态（对话、工单、检索结果、应用状态）与一组预定义问题，一次请求返回全部问题的类型化答案及概率，不生成文本。适合 Agent 工具路由、高风险操作前的护栏、工单分流、检索结果相关性判断、按置信度转人工。

```go
decider, err := provider.NewDecider(provider.DeciderConfig{
    APIKey: os.Getenv("TYPESAFE_API_KEY"),
    Model:  "jev-1.13.0", // 用于阈值门控时固定具体版本，见下
})
if err != nil {
    log.Fatal(err)
}

resp, err := decider.Decide(ctx, &provider.DecideRequest{
    State: map[string]any{"ticket": "昨天扣了两次款，退一笔给我"},
    Questions: map[string]provider.DecisionQuestion{
        "is_refund": {Type: provider.DecisionNoul, Instructions: "`ticket` 是否在申请退款"},
        "queue": {
            Type:         provider.DecisionChoice,
            Instructions: "`ticket` 应分配到哪个队列",
            Criteria:     map[string]any{"billing": "扣费、发票、退款", "tech": "故障与报错", "other": nil},
        },
        "urgency": {
            Type:         provider.DecisionScore,
            Instructions: "`ticket` 的紧急程度",
            Criteria:     []string{"可延后", "当天处理", "立即处理"},
        },
    },
})
if err != nil {
    log.Fatal(err)
}

refund := resp.Answers["is_refund"].Noul // 「是」的概率，0–1
queue := resp.Answers["queue"]
if queue.Confidence < 0.6 {
    // 置信度低，转人工
}
fmt.Println(refund, queue.Choice, resp.Answers["urgency"].Score, resp.Model)
```

- **协议**：`POST {BaseURL}/systemone`，默认 `BaseURL` 为 `https://api.typesafe.ai/v1`，默认模型 `jev-latest`；`Authorization: Bearer` 鉴权。
- **三类问题**：
  - `DecisionNoul`（是/否）：`Criteria` 可选，形如 `map[string]any{"true": "…", "false": "…"}`；答案读 `Noul`（「是」的概率）。
  - `DecisionChoice`（多选一）：`Criteria` 必填，为选项名到描述的映射，描述可为 `nil`；答案读 `Choice`、`Probabilities`（以选项名为 key）、`Confidence`。
  - `DecisionScore`（有序评分）：`Criteria` 必填，为由低到高的等级数组；答案读 `Score`（按概率加权，可能落在两级之间）、`Legend` 与 `Probabilities`（均以等级序号字符串 `"0"`、`"1"`… 为 key）、`Confidence`。
- **State、Instructions、Criteria 均可传字符串或可 JSON 序列化的结构**，原样发送；已有的结构化数据直接传入即可。可在 `Instructions` 中用反引号引用 `State` 的字段名。
- **问题 key 由调用方自定**，只用于匹配答案，不参与模型推理；答案按相同 key 返回。
- **发送前校验**：`State` 为 nil、`Questions` 为空、key 为空、`Type` 非法、choice/score 缺 `Criteria` 时不发请求，返回包装 `ErrInvalidDecideRequest` 的错误。选项数、等级数等上限由服务端以 422 判定。
- **响应校验**：任一问题缺答案或答案类型与问题不符时返回错误，不交还残缺结果。
- **固定版本**：别名 `jev-latest` 会随平台升级指向新版本，相同输入可能得到不同的概率。用概率或置信度做阈值门控时，把 `DeciderConfig.Model` 设为具体版本（如 `jev-1.13.0`）；`DecideResponse.Model` 始终是实际作答的具体版本，可用于记录与回溯。
- **错误**：非 2xx 返回 `*ProviderError`——401 为 `ErrorCodeAuth`，422 为 `ErrorCodeInvalidRequest`，429 为 `ErrorCodeRateLimit`，529（服务过载）为可重试的 `ErrorCodeServerError`，`Retry-After` 写入 `RetryAfter`；响应头 `x-typesafe-request-id` 写入 `Metadata.RequestID`。
- **用量**：`Usage.PromptTokens` 为输入 token，`CompletionTokens` 为输出 token。

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

默认情况下，`NewProvider` / `NewEmbedder` 会使用 `DefaultHTTPClient()`。它不设置 `http.Client.Timeout`，请求总预算仍由调用方传入的 `context.Context` 控制；但会设置 dial、TLS handshake、response header、idle connection 等传输层超时，避免底层 IO 无限等待。其中等待响应头的上限为 15 分钟：非流式调用下平台要等整段生成完才发响应头，思考模型高强度推理一次可达数分钟，该值过小会把正常的长推理请求误判为网络故障；长推理仍推荐走流式。

需要统一超时、代理、链路追踪、测试替身或自定义 transport 时，可以向 `ProviderConfig` / `EmbedderConfig` 注入实现了 `Do(*http.Request) (*http.Response, error)` 的客户端：

```go
httpClient := &http.Client{Timeout: 30 * time.Second}

p, err := provider.NewProvider(provider.ProviderConfig{
    Name:       provider.ProviderOpenAI,
    BaseURL:    "https://api.openai.com/v1",
    APIKey:     os.Getenv("OPENAI_API_KEY"),
    Model:      "gpt-5.6-terra",
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

流式响应在响应头之后中途停止发送数据时，用 `NewIdleTimeoutDoer` 包装客户端设置空闲超时：自拿到响应起、以及每次读到数据后，超过阈值未读到任何字节即关闭响应体，`Recv` 返回满足 `errors.Is(err, provider.ErrStreamIdleTimeout)` 与 `errors.Is(err, provider.ErrTimeout)` 的错误。计时按字节进行，SSE 保活注释同样会重置计时；阈值按平台首个 token 前的最长静默时间设置，思考模型推理期间可能长时间无输出，阈值需留足余量。OpenAI 兼容平台与原生平台均可使用：

```go
p, err := provider.NewProvider(provider.ProviderConfig{
    Name:       provider.ProviderOpenAI,
    BaseURL:    "https://api.openai.com/v1",
    APIKey:     os.Getenv("OPENAI_API_KEY"),
    Model:      "gpt-5.6-terra",
    HTTPClient: provider.NewIdleTimeoutDoer(nil, 2*time.Minute), // nil 表示包装 DefaultHTTPClient()
})
```

---

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
| 观测 | `WithObservability` | 零依赖事件 hook，不绑定 slog / Prometheus / OTel |

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
- **限流预扣**：使用本地启发式估算（`EstimateChatRequestTokens`：输入估算 + `MaxTokens`，未设 `MaxTokens` 时用
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
| `ErrBreakerOpen` | 熔断器打开，冷却期内快速失败 | `Breaker` |
| `ErrLocalRateLimited` | 客户端限流拦下，区别于平台回传的 `ErrRateLimit` | `RateLimiter` |

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

## 在 Gin/HTTP 服务中使用

在服务启动时构造 Provider 并注入 handler。每次调用传入 `c.Request.Context()`，让请求取消传播到上游。业务负责认证、渠道选择、历史和额度；核心只接收已构造的 `ChatRequest`。

SSE 转发使用 `ChatStream`，持续读取到 EOF；收到 `FinishReason` 后仍需读取尾帧 Usage。客户端断开时关闭流，并处理 `Close` 错误。v2 的 `WithObservability` 提供流终止事件，应用通过自己的 context value 关联身份和调用 ID；示例见 [迁移说明](MIGRATION.md)。

## 各平台常用模型速查

> 以下为截至 **2026-09** 各厂商在线可调用的主流模型。模型名随平台更新变化较快，建议调用前对照官方文档。

**DeepSeek**

| 模型名 | 说明 |
|--------|------|
| `deepseek-v4-flash` | V4 Flash，1M 上下文，思考由 `thinking` / `reasoning_effort` 控制（默认开启、默认 high） |
| `deepseek-v4-pro` | V4 Pro 旗舰，1M 上下文，推理档位 low / high / max |

> `deepseek-chat` / `deepseek-reasoner` 已于 2026-07-24 停用。带 `tools` 的思考模式请求必须回传 `reasoning_content`，库已自动处理。

**通义千问（百炼 DashScope）**

| 模型名 | 说明 |
|--------|------|
| `qwen3.8-max` | 最新旗舰，1M 上下文，原生全模态 |
| `qwen3.8-flash` | 轻量高速 |
| `qwen3.7-max` | 3.7 旗舰，混合思考、默认开启 |
| `qwen3.7-plus` | 3.7 高性价比，1M 上下文，视觉 + GUI Agent |
| `qwen3-coder-plus` | 代码 Agent 专精 |
| `qwen-long` | 长文档处理，最高 10M tokens |

> 3.7 系新增 `preserve_thinking` 参数保留跨轮推理；百炼建议迁移到业务空间专属域名 `https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1`，用 `NewProvider` 覆盖 `BaseURL` 即可。

**智谱 AI**

| 模型名 | 说明 |
|--------|------|
| `glm-5.3` | 最新旗舰，1M 上下文，始终思考（`reasoning_effort` low / high / max） |
| `glm-5.3-flash` | 原生多模态轻量版 |
| `glm-5.2` | 上一代旗舰 |
| `glm-5.1` / `glm-5` | 早期 5 系 |

**百度千帆**

| 模型名 | 说明 |
|--------|------|
| `ernie-5.1` | 文心 5.1 旗舰，原生全模态 |
| `ernie-5.0-thinking-preview` | 文心 5.0 思考版 |
| `ernie-x1.1-preview` | X1.1 推理模型 |
| `ernie-4.5-turbo-128k` | 4.5 Turbo 长上下文 |

> 千帆的思考模型不支持 `thinking_budget`，`max_tokens` 同时限制 `reasoning_content + content`。

**硅基流动**

| 模型名 | 说明 |
|--------|------|
| `deepseek-ai/DeepSeek-V4-Flash` | DeepSeek V4 Flash 高速版 |
| `deepseek-ai/DeepSeek-V4-Pro` | DeepSeek V4 Pro |
| `Pro/zai-org/GLM-5.2` | 智谱 GLM-5.2（Pro 付费通道） |
| `moonshotai/Kimi-K2.7-Code` | Kimi K2.7 Code |
| `Qwen/Qwen3-Embedding-8B` | 千问 Embedding（MTEB 多语言榜首） |
| `Qwen/Qwen3-Reranker-8B` | 千问 Reranker |

> `Pro/` 前缀为付费稳定通道，不带前缀为社区免费通道，能力相同但限流更严。

**Moonshot / Kimi**

| 模型名 | 说明 |
|--------|------|
| `kimi-k3` | 旗舰，1M 上下文，始终思考，`reasoning_effort` low / high / max（默认 max）；采样参数固定、勿传 |
| `kimi-k2.7-code-highspeed` | 编程专用高速版 |
| `kimi-k2.6` | 通用模型，256K 上下文，思考可开关（`thinking.type`） |

> Kimi K3 要求多轮与工具调用时原样回传完整 assistant 消息（含 `reasoning_content`），库已自动处理；开放平台已迁至 platform.kimi.com。

**火山方舟 / 豆包**

| 模型名 | 说明 |
|--------|------|
| `doubao-seed-2-1-pro-260628` | Seed 2.1 Pro，Coding / Agent / 多模态全面升级 |
| `doubao-seed-2-1-turbo-260628` | Seed 2.1 Turbo，规模化生产场景，价格为 Pro 一半 |
| `doubao-seed-evolving` | 固定 ID、周级迭代的 Coding & Agent 模型（latest 语义） |
| `doubao-seed-2-0-pro-260215` | Seed 2.0 Pro |
| `doubao-embedding-text-240515` | 文本向量化 |

> 方舟的 `model` 同时接受模型 ID（如上）与推理接入点 ID（`ep-` 开头）。`reasoning_effort` 只有推理模型接受，普通模型传入返回 400。

**OpenAI**

| 模型名 | 说明 |
|--------|------|
| `gpt-6-astra` | 官方模型页当前列出的旗舰 |
| `gpt-5.6-sol` | 5.6 旗舰档（`gpt-5.6` 别名指向它） |
| `gpt-5.6-terra` | 智能 / 成本均衡档（对应早期 mini） |
| `gpt-5.6-luna` | 高并发低成本档（对应早期 nano） |
| `gpt-5.5` / `gpt-5.4` | 上两代 |

> `reasoning_effort` 支持 none / low / medium / high / xhigh / max（按模型而异）。库对 OpenAI / Azure 下发 `max_completion_tokens` 而非已被推理模型拒绝的 `max_tokens`。

**Anthropic**

| 模型名 | 说明 |
|--------|------|
| `claude-fable-5-1` | 最高能力档（Mythos 级，含额外安全措施） |
| `claude-opus-5` | 复杂 Agent 编码与企业工作负载 |
| `claude-sonnet-5` | 默认推荐，1M 上下文，自适应思考默认开启 |
| `claude-haiku-4-5-20251001` | 低延迟档 |

> 5 代起无日期后缀的 ID 即固定快照。`budget_tokens` 已不被新模型接受，用 `Thinking.Enabled` + `Effort`。

**Google Gemini**

| 模型名 | 说明 |
|--------|------|
| `gemini-3.8-flash` | 最新 Flash（GA），长程软件工程与 Agent |
| `gemini-3.5-flash` | 3.5 Flash（GA，默认推荐） |
| `gemini-3.1-pro-preview` | 当前可用的 Pro |
| `gemini-3.1-flash-lite` | 低成本高并发 |

> 3 系用 `thinkingLevel` 控制推理、要求回传 `thoughtSignature`、不建议再传 temperature / top_p（3.8 直接忽略）。2.5 系计划 2026-10 关停。

**xAI / Groq / Mistral / Cohere**

| 平台 | 模型名 |
|--------|------|
| xAI | `grok-4.6`（旗舰）、`grok-4.5`、`grok-4-1-fast` |
| Groq | `openai/gpt-oss-120b`、`openai/gpt-oss-20b`、`qwen/qwen3.6-27b` |
| Mistral | `mistral-large-latest`（Large 3）、`mistral-small-latest`（Small 4，`reasoning_effort` 开推理）、`magistral-medium-latest` |
| Cohere | `command-a-plus-05-2026`、`command-a-03-2025`（兼容接口 `reasoning_effort` 只接受 none / high） |

> 模型列表会随平台更新而变化，建议使用前查阅各平台最新文档。

## 常用 Embedding 模型速查

> 以下维度数为官方文档给出的**最大值**，部分模型（如 OpenAI `text-embedding-3-*`、Qwen `text-embedding-v3/v4`、智谱 `embedding-3`）支持通过 `Dimensions` 参数按需截断。具体可选维度值、计费方式、向量归一化行为以各平台最新文档为准。

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
| `qwen3.7-text-embedding` | — | 3.7 系新一代向量模型，以官方文档为准 |
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

**Google Gemini**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `gemini-embedding-001` | 3072 | 默认推荐（GA、纯文本），支持 `task_type` 与 `outputDimensionality` 截断 |
| `gemini-embedding-2-preview` | 128–3072 | 多模态预览版，8K 上下文，截断向量自动归一化；不支持 `task_type`，向量空间与 001 不兼容需重建索引 |

**硅基流动**

| 模型名 | 维度 | 说明 |
|--------|------|------|
| `BAAI/bge-m3` | 1024 | 默认推荐，多语言 + 稀疏/稠密混合 |
| `Qwen/Qwen3-Embedding-8B` | 可变 | MTEB 多语言榜首，支持自定义指令 |
| `BAAI/bge-large-zh-v1.5` | 1024 | 中文专用 |
| `Pro/BAAI/bge-m3` | 1024 | 付费稳定通道 |
| `netease-youdao/bce-embedding-base_v1` | 768 | 网易有道中英双语 |

> DeepSeek / Moonshot 官方暂无 embedding 模型，需要请转硅基流动或自部署。

## 与编排框架集成（Eino 等）

使用独立 [einoadapter](../einoadapter/README.md) 把 Provider 接到固定 Eino v0.9.21。每次 Generate/Stream 对应一次核心 Chat/ChatStream；消息、思考签名、工具增量、完整 Usage 与元数据由 adapter 转换，核心不依赖 Eino。

应用负责 Chain/Graph、工具执行、历史、路由和计费。计量接到通用 `ObserveHook`，通过应用自己的 context value 关联调用 ID 与身份，保留唯一写入入口。adapter 尚未发布，本地联调方式见其 README；迁移细节见 [MIGRATION.md](MIGRATION.md)。

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
    Role           Role            // RoleSystem / RoleUser / RoleAssistant / RoleTool
    Content        []ContentPart
    Reasoning      string          // Role == RoleAssistant 时，上一轮推理文本（映射为 reasoning_content）
    ThinkingBlocks []ThinkingBlock // Role == RoleAssistant 时，Anthropic 签名思考块，原样回传
    ToolCalls      []ToolCall      // Role == RoleAssistant 时，模型请求的工具调用
    ToolCallID     string          // Role == RoleTool 时，关联的 ToolCall.ID
    ToolName       string          // Role == RoleTool 时，被调用的函数名（Gemini 必需）
}

type ThinkingBlock struct {
    Thinking  string // 思考文本；平台省略显示时可能为空
    Signature string // 平台签名，原样回传
    Redacted  string // 加密思考块数据（redacted_thinking）
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

type CacheControl struct {
	Type CacheControlType // "ephemeral"
	TTL  CacheControlTTL  // "" 沿用平台默认 / "5m" / "1h"
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
provider.WithCacheControl(provider.TextPart("cache 1h"), provider.CacheControlEphemeralTTL(provider.CacheControlTTL1h))
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
    Strict      *bool  // 严格约束入参（OpenAI / Anthropic），nil 沿用平台默认
}

// 模型返回的工具调用
type ToolCall struct {
    ID               string       // 唯一标识，回传结果时需要
    Function         FunctionCall
    ThoughtSignature string       // Gemini 3 签名，回传时原样携带
}

type FunctionCall struct {
    Name      string // 函数名
    Arguments string // JSON 格式参数
}

// 便捷方法
fc.ParseArguments(&target) error // 解析 JSON 参数到结构体

// 便捷构造函数
provider.ToolResultMessageFor(call, content) Message              // 纯文本结果，带函数名（推荐）
provider.ToolResultMessageJSONFor(call, result) (Message, error)  // 自动序列化，带函数名
provider.ToolResultMessage(toolCallID, content) Message           // 仅 ID（Gemini 需在同一请求内可反查函数名）
provider.ToolResultMessageJSON(toolCallID, result) (Message, error)
```

### StreamChunk（含 Tool Use）

```go
type StreamChunk struct {
    Delta             string          // 增量文本
    ReasoningDelta    string          // 增量推理文本
    FinishReason      string          // 非空表示流结束
    ThinkingSignature string          // Anthropic 思考块签名，随块结束到达一次（块边界）
    RedactedThinking  string          // Anthropic 加密思考块，整块到达
    Model             string          // 响应侧回传的实际模型名（部分 chunk 携带）
    Usage             Usage           // 流尾部 chunk 携带完整 token 统计，其余 chunk 为零值
    Parts             []ContentPart   // 非文本输出（如图像），通常整块到达
    ToolCalls         []ToolCallDelta // 流式 tool call 增量
}

type ToolCallDelta struct {
    Index            int               // tool call 索引
    ID               string            // 首个 chunk 中非空
    Function         FunctionCallDelta
    ThoughtSignature string            // Gemini 3 签名
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

### 便捷函数

```go
// Chat
provider.SimpleChat(ctx, p, "你好")                           // 一问一答
provider.SimpleChatWithSystem(ctx, p, "你是助手", "你好")       // 带 system prompt
provider.CollectStream(ctx, p, req, onChunkFn)                // 流式收集+回调
provider.DefaultHTTPClient()                                  // 默认传输层超时 HTTP 客户端
provider.NewIdleTimeoutDoer(next, timeout)                    // 响应体读取空闲超时
provider.WithRetry(p, provider.RetryOptions{...})             // 为 Chat / ChatStream 创建阶段添加重试
provider.WithObservability(p, provider.ObserveOptions{...})   // Chat / ChatStream 观测 hook

// Embedding
provider.SimpleEmbed(ctx, emb, "你好")                        // 单条文本 → 向量
provider.EmbedBatch(ctx, emb, []string{"a", "b"})             // 批量 → 向量数组
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

用指针可以区分“未设置”和“设置为 0”。nil 时不下发对应字段，由平台决定默认行为；显式 `0` 在 OpenAI 兼容 Chat/ChatStream 的最终 JSON 请求中保留为 `0`，不受 SDK 的 `omitempty` 影响。具体取值约束与含义以目标平台为准。

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
# 待真实 key 验证的协议细节（清单见仓库根目录 ROADMAP.md），缺 key 的用例自动跳过
go test ./provider -run 'TestVerify' -count=1 -v
```

不要把真实 API Key 写入源码、README、测试 fixture 或 shell history；本命令中的值只应使用临时密钥或本地安全注入方式。

## License

MIT
