# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- 新增 `ContentPart` / `ContentType` / `ImageDetail` 以及 `TextPart` / `ImageURLPart` / `ImageDataPart` 等便捷构造器，支持多模态消息内容
- 新增 `example/vision/main.go`，演示文本 + 图像输入的最小调用方式
- 新增 `Thinking` 结构与 `ThinkingEffortLow/Medium/High` 常量，统一抽象 reasoning 模式
- 新增 `ResponseFormat` / `ResponseFormatType` 与 `TextFormat` / `JSONObjectFormat` / `JSONSchemaFormat` / `JSONSchemaFormatStrict`
- 新增 `example/reasoning/main.go` 与 `example/structured/main.go`

### Changed

- **⚠ 破坏性变更**：`Message.Content` 从 `string` 升级为 `[]ContentPart`，旧写法 `Message{Content: "..."}` 不再编译
- `openaiProvider.buildRequest` 现在按消息内容自动选择 `Content string` 或 `MultiContent []ChatMessagePart` 映射路径
- 仓库内 Message 示例与调用代码统一改为 `UserText` / `SystemText` / `UserMessage(...parts)` 构造器写法
- `go.mod` module path 已切换为 `github.com/gtkit/go-llm-provider/v2`
- **⚠ 破坏性变更**：`ChatRequest.EnableThinking` 已移除，统一改为 `ChatRequest.Thinking`
- `ChatResponse` 新增 `Reasoning`，`StreamChunk` 新增 `ReasoningDelta`，`Usage` 新增 `ReasoningTokens`
- `buildRequest` 现在支持 `response_format` 和 OpenAI `reasoning_effort`

### Removed

- 移除 `Message.Content string` 纯文本直传写法，统一改为 parts 模型
- 移除 `ErrUnsupportedThinking`

## [1.2.0] - 2026-04-21

### Added

- 新增 `Middleware` / `StreamMiddleware` / `EmbedMiddleware` 类型与 `WithMiddlewares` / `WithEmbedderMiddlewares` 装饰器，为 Chat / Stream / Embed 提供统一横切扩展点
- 新增 `ProviderError` 结构化错误：含 `ErrorCode` 分类、HTTP 状态码、`Retryable` 标记；支持 `errors.Is`（与 `ErrRateLimit` / `ErrAuth` 等 sentinel 互认）与 `errors.As`
- 新增 8 个错误 sentinel：`ErrAuth` / `ErrRateLimit` / `ErrTimeout` / `ErrContextLength` / `ErrContentFilter` / `ErrInvalidRequest` / `ErrServerError` / `ErrNetwork`
- 新增 9 个 `ErrorCode` 常量
- 新增 `RunToolLoopWithOptions` / `RunToolLoopOptions` / `ToolErrorEncoder`，允许调用方自定义工具错误回传格式，并显式开启并行 tool calls
- 新增 `TryWithMiddlewares` / `TryWithEmbedderMiddlewares`，为 middleware 装饰提供非 panic 构造入口
- 新增 `example/middleware/main.go`，演示 Logging / TokenStats / Retry 三类中间件的参考实现

### Changed

- 内部实现：仓库所有 `encoding/json` 引用迁移到 `github.com/gtkit/json`，对外行为不变
- `RunToolLoop` 默认改为向模型回传脱敏后的工具错误 JSON，不再默认暴露原始内部错误字符串
- `ProviderError` 新增原始诊断字段，保留 provider-native `code` / `type` / `param` 信息
- README 明确 `QuickRegistry` 默认 provider 采用 `ProviderName` 排序后的首个成功注册项

## [1.0.1] - 2026-04-17

### Fixed

- 基础能力稳定性修复与文档完善

## [1.0.0] - 初始发布

### Added

- `Provider` 接口 + `Registry` 注册表，支持 7 家 OpenAI 兼容平台预设（OpenAI / DeepSeek / 通义千问 / 智谱 / 百度千帆 / 硅基流动 / Moonshot）
- 非流式 `Chat` 与流式 `ChatStream`
- `SimpleChat` / `SimpleChatWithSystem` / `CollectStream` 便捷函数
- Tool Use / Function Calling 完整支持，含 `RunToolLoop` 自动多轮执行器
- `ParamSchema` JSON Schema 构建器
- `NewStreamReader` 开放式流读取器，供扩展包复用

## [1.1.1] - 2026-04-20

### Added

- 新增 `Embedder` 接口，统一抽象各平台「文本 → 向量」调用；支持 OpenAI / 通义千问 / 智谱 / 百度千帆 / 硅基流动 五家官方 embedding 接口
- 新增 `EmbeddingRequest` / `EmbeddingResponse` / `Embedding` / `EmbedderConfig` 类型
- 新增 `NewEmbedder` / `NewEmbedderFromPreset` 构造函数
- 新增便捷函数 `SimpleEmbed`（单条文本 → 向量）和 `EmbedBatch`（批量文本 → 向量数组，自动按 `Index` 重排）
- `Registry` 扩展 embedder 独立管理能力：`RegisterEmbedder` / `GetEmbedder` / `DefaultEmbedder` / `SetDefaultEmbedder` / `EmbedderNames`
- `QuickRegistry` / `QuickRegistryStrict` 在注册 chat provider 的同时，自动为有 embedding 预设的平台注册 embedder；DeepSeek / Moonshot 等无官方 embedding 接口的平台静默跳过不报错
- `Preset` 结构新增 `EmbeddingModel` 字段（向后兼容的新增字段）
- 新增错误变量：`ErrNilEmbedder` / `ErrNilEmbeddingRequest` / `ErrEmptyEmbeddingInput` / `ErrInvalidEmbedderConfig`
- 新增 `example/embedding/main.go` 演示基于 Embedding 的最小 RAG 检索闭环
- README 新增「Embedding（文本向量化）」章节与「常用 Embedding 模型速查」子章节，"支持的平台"表格补充 Embedding 默认模型列
