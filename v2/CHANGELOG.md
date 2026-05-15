# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [2.2.0] - 2026-05-15

### Added

- 新增 `ResponseMetadata`，在 `ChatResponse` / `EmbeddingResponse` 中暴露 provider、模型、request id 与白名单响应头，便于线上排障
- 新增 `WithRetry` / `TryWithRetry` / `RetryMiddleware` / `RetryStreamMiddleware`，基于 `ProviderError.Retryable` 提供内置重试能力
- 新增 `NewFallbackProvider`，支持按顺序在多个 provider 之间做可重试错误 fallback
- 新增 `WithObservability` / `WithEmbedderObservability` 与 `ObserveEvent`，以零外部依赖 hook 暴露 Chat、Stream、Embed 的 duration、usage、metadata 和错误分类
- 新增 `NewAnthropicProvider` / `NewGeminiProvider` 与 `ProviderAnthropic` / `ProviderGemini`，无需官方 SDK 即可通过原生 HTTP API 接入 Claude 与 Gemini
- 新增 `ProviderXAI` 预设，通过 OpenAI 兼容接口接入 xAI Grok
- 新增 Claude / Gemini 原生非流式 Tool Use / Function Calling 映射
- 新增 Claude / Gemini 原生结构化输出映射；Claude 通过强制 tool 返回 JSON，Gemini 通过 `generationConfig.responseMimeType` / `responseSchema`
- 新增 `NewOllamaProvider` / `OllamaProviderConfig` / `ProviderOllama`，通过本地 `/api/chat` 接入 Ollama 文本对话与 NDJSON 流式响应
- 新增 `ProviderGLM` / `ProviderKimi` 别名，便于从 GLM / Kimi 命名迁移到 `ProviderZhipu` / `ProviderMoonshot`
- 新增 Claude / Gemini / Ollama 真实接口 smoke test，设置 `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` / `OLLAMA_MODEL` 后可验证原生 provider 链路

### Changed

- `ProviderAnthropic` / `ProviderGemini` 的能力标记新增 `tools` 与 `structured_output`
- `StreamChunk` 新增 `Usage` 字段，用于承载部分 provider 在最终流式片段返回的 token 统计

## [2.1.0] - 2026-05-14

### Added

- 新增 `Capability` / `ModelCapabilities` 与 `ModelCapabilitiesFromPreset` / `AllModelCapabilities` / `ModelCapabilitiesByCapability`，让调用方可查询预设模型是否支持 Chat、Stream、Tools、Structured Output、Vision、Reasoning、Embedding
- 新增 `HTTPDoer` 以及 `ProviderConfig.HTTPClient` / `EmbedderConfig.HTTPClient`，支持注入自定义 HTTP 客户端用于超时、代理、观测、测试和传输层治理
- 新增 `DefaultHTTPClient`，默认 Provider / Embedder 现在具备传输层超时保护，同时继续由调用方 context 控制请求总预算
- 新增 `GenerateJSON` / `GenerateJSONInto` / `GenerateJSONWithValidator` / `GenerateJSONIntoWithValidator`，在 `ResponseFormat` 基础上提供类型化 JSON 结构化输出解码与业务校验助手
- 新增 DeepSeek 真实接口 smoke test，设置 `DEEPSEEK_API_KEY` 后可验证 Chat 与结构化输出链路
- 新增 `CosineSimilarity` / `RankBySimilarity` / `MostSimilar` 与 `SimilarityResult`，为 Embedding/RAG 场景提供轻量相似度工具
- 新增结构化输出和相似度工具的可验证 Example 与 Benchmark

## [2.0.0] - 2026-04-22

### Added

- 首个 `v2` 主版本发布，模块路径切换为 `github.com/gtkit/go-llm-provider/v2`
- 新增 `ContentPart` / `ContentType` / `ImageDetail` 以及 `TextPart` / `ImageURLPart` / `ImageDataPart` 等便捷构造器，支持多模态消息内容
- 新增 `Thinking` 结构与 `ThinkingEffortLow/Medium/High` 常量，统一抽象 reasoning 模式
- 新增 `ResponseFormat` / `ResponseFormatType` 与 `TextFormat` / `JSONObjectFormat` / `JSONSchemaFormat` / `JSONSchemaFormatStrict`
- 新增 `example/vision/main.go`、`example/reasoning/main.go` 与 `example/structured/main.go`

### Changed

- **⚠ 破坏性变更**：`Message.Content` 从 `string` 升级为 `[]ContentPart`，旧写法 `Message{Content: "..."}` 不再编译
- `openaiProvider.buildRequest` 现在按消息内容自动选择 `Content string` 或 `MultiContent []ChatMessagePart` 映射路径
- 仓库内 Message 示例与调用代码统一改为 `UserText` / `SystemText` / `UserMessage(...parts)` 构造器写法
- **⚠ 破坏性变更**：`ChatRequest.EnableThinking` 已移除，统一改为 `ChatRequest.Thinking`
- `ChatResponse` 新增 `Reasoning`，`StreamChunk` 新增 `ReasoningDelta`，`Usage` 新增 `ReasoningTokens`
- `buildRequest` 现在支持 `response_format` 和 OpenAI `reasoning_effort`
- 各平台预设默认 Chat 模型同步更新到 2026-04 官方推荐值，覆盖 OpenAI / 通义千问 / 智谱 / 百度千帆 / Moonshot 等平台

### Removed

- 移除 `Message.Content string` 纯文本直传写法，统一改为 parts 模型
- 移除 `ErrUnsupportedThinking`
