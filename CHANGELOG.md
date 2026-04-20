# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。


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