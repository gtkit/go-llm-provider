# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [1.3.0] - 2026-04-22

### Added

- 新增 `Middleware` / `StreamMiddleware` / `EmbedMiddleware` 类型与 `WithMiddlewares` / `WithEmbedderMiddlewares` 装饰器，为 Chat / Stream / Embed 提供统一横切扩展点
- 新增 `ProviderError` 结构化错误：含 `ErrorCode` 分类、HTTP 状态码、`Retryable` 标记；支持 `errors.Is`（与 `ErrRateLimit` / `ErrAuth` 等 sentinel 互认）与 `errors.As`
- 新增 8 个错误 sentinel：`ErrAuth` / `ErrRateLimit` / `ErrTimeout` / `ErrContextLength` / `ErrContentFilter` / `ErrInvalidRequest` / `ErrServerError` / `ErrNetwork`
- 新增 9 个 `ErrorCode` 常量
- 新增 `RunToolLoopWithOptions` / `RunToolLoopOptions` / `ToolErrorEncoder`，允许调用方自定义工具错误回传格式，并显式开启并行 tool calls
- 新增 `TryWithMiddlewares` / `TryWithEmbedderMiddlewares`，为 middleware 装饰提供非 panic 构造入口
- 新增 `example/middleware/main.go`，演示 Logging / TokenStats / Retry 三类中间件的参考实现

### Changed

- `RunToolLoop` 默认改为向模型回传脱敏后的工具错误 JSON，不再默认暴露原始内部错误字符串
- `ProviderError` 新增原始诊断字段，保留 provider-native `code` / `type` / `param` 信息
- 各平台预设默认 Chat 模型同步更新到 2026-04 官方推荐值，覆盖 OpenAI / 通义千问 / 智谱 / 百度千帆 / Moonshot 等平台
- README 补充 `Middleware`、工具错误脱敏与 `v1` / `v2` 差异说明，便于调用方选择升级路径

## [1.2.0] - 2026-04-21

### Changed

- 该版本标签已发布，但实际对应代码快照与 `v1.1.1` 相同，未引入额外 API 或行为变化

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

## [1.0.2] - 2026-04-20

### Added

- 新增 OpenAI 官方平台预设与快速注册支持，统一纳入现有 `Provider` / `Registry` 使用方式

### Changed

- 流式响应读取器重构为可复用的 `StreamReader`，便于扩展包复用
- 示例与 README 补充 OpenAI 使用方式，以及 Claude / Gemini 扩展接入说明

## [1.0.1] - 2026-03-27

### Fixed

- 基础能力稳定性修复与文档完善

## [1.0.0] - 2026-03-06

### Added

- `Provider` 接口 + `Registry` 注册表，支持 7 家 OpenAI 兼容平台预设（OpenAI / DeepSeek / 通义千问 / 智谱 / 百度千帆 / 硅基流动 / Moonshot）
- 非流式 `Chat` 与流式 `ChatStream`
- `SimpleChat` / `SimpleChatWithSystem` / `CollectStream` 便捷函数
- Tool Use / Function Calling 完整支持，含 `RunToolLoop` 自动多轮执行器
- `ParamSchema` JSON Schema 构建器
- `NewStreamReader` 开放式流读取器，供扩展包复用
