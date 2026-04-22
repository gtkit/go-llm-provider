# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Changed

### Deprecated

### Removed

### Fixed

### Security

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
