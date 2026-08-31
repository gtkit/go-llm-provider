# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- 新增 Prompt Injection 防御规则模板文档 `docs/prompt-injection-defense.md`：与本库解耦，只依赖标准库，可直接复制到任何项目使用；含可复制的正则特征词表、Markdown 转义、标签隔离、输出解析校验的完整代码模板，以及已验证的攻击载荷/正常文本用例表

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [1.7.1] - 2026-08-31

### Fixed

- `example/toolsecurity` 的可疑指令特征词检测修复漏检：早期正则要求限定词与名词严格相邻，实测连"ignore all previous instructions"这类最常见的英文注入短语、以及文章原文"忘记你之前的处理任务"都会漏检；改为允许中间插入代词/修饰词，并补充"系统提示词"等中文特征词

## [1.7.0] - 2026-08-28

### Added

- `RunToolLoopOptions` 新增 `ToolResultTransformer` / `ResponseValidator` 两个可选钩子，用于抵御工具结果携带间接提示注入内容：前者在工具结果写回对话历史前加工，后者在 `RunToolLoop` 返回最终响应前校验，二者返回 error 均会中止工具循环、不将未处理或未通过校验的内容交还给调用方；均为 nil 时行为与之前版本完全一致。配套内置便捷函数 `WrapToolResultInTag`，将工具结果包裹进指定标签并转义结果中的尖括号，防止结果内容本身携带同名标签文本提前闭合标签；`example/toolsecurity` 提供组合长度截断、特征词检测、Markdown 结构符转义、标签结构隔离、强制输出格式、字段与敏感词校验的完整可运行示例

## [1.6.0] - 2026-08-21

### Added

- 新增内置熔断器 `Breaker`（与 v2 同源回移）：滑动窗口失败计数达阈值即跳闸，冷却期内请求以 `ErrBreakerOpen` 在本地快速失败、不发往平台；冷却到期放行半开探测，探测成功即闭合、失败则冷却时长翻倍（上限 `MaxOpenDuration`）。配套 `WithBreaker` / `BreakerMiddleware` / `BreakerStreamMiddleware` / `WithEmbedderBreaker` / `BreakerEmbedMiddleware`，`State()`、`Stats()`、`Reset()` 供健康检查与人工放行使用
- 新增加权负载均衡 `BalancedProvider`（与 v2 同源回移）：按权重把流量分摊到多个成员（多 key 分摊配额、多地域就近、按成本混流），支持平滑加权轮询、加权随机、加权最少在途三种策略；成员可各带独立熔断器，熔断打开的成员自动被跳过并在冷却到期后恢复接流；`Stats()` 暴露每个成员的权重、在途数与熔断状态
- 新增 sentinel：`ErrBreakerOpen`、`ErrNilBreaker`、`ErrInvalidBalanceStrategy`

### Changed

- 降级链 `FallbackProvider` 的默认切换判定从"仅平台侧可重试错误"扩展为"平台侧可重试错误 + `ErrBreakerOpen`"：熔断打开意味着当前成员正在冷却，留在原地重试必然继续失败。新错误只在使用内置熔断器后才可能出现，既有调用方行为不变；显式传入 `FallbackOptions.ShouldFallback` 的调用方完全不受影响

## [1.5.0] - 2026-07-10

### Added

- 从 v2 回移 `WithRetry` / `TryWithRetry` / `RetryMiddleware` / `RetryStreamMiddleware`：基于 `ProviderError.Retryable` 的内置重试（暂不解析 Retry-After 响应头，该能力仅 v2 提供）
- 从 v2 回移 `NewFallbackProvider` / `NewFallbackProviderWithOptions`：多 provider 降级链，含自定义切换判定 `FallbackOptions.ShouldFallback`、ctx 取消即停、嵌套组合实现"厂商内穷尽 model 后再切厂商"
- 流式调用支持 token 统计：自动下发 `stream_options.include_usage`，`StreamChunk` 新增 `Usage` 字段（统计位于 `FinishReason` 之后、`io.EOF` 之前的收尾 chunk）；`ChatRequest` 新增 `StreamUsage` 开关兼容老网关
- 重试与降级链补 GoDoc Example 与热路径基准测试

### Fixed

- 修复 `ExponentialBackoffWithJitter` 在上界为 `math.MaxInt64` 时整数溢出触发 panic 的问题
- 修正 `Version` 常量滞后（v1.4.2 发布时仍为 v1.4.1，随本版本更正为 v1.5.0；发布校验工作流已增加 tag 与版本常量一致性检查）

## [1.4.2] - 2026-07-10

### Fixed

- 修复 `go.sum` 缺少 `gtkit/json/v2 v2.0.7` 内容 hash 导致下游构建与质量门禁无法运行的问题

## [1.4.1] - 2026-05-15

### Added

- README 新增 v1 provider 能力矩阵，明确 Chat、Streaming、Tools、Structured Output、Vision 与 Embedding 的当前覆盖范围
- 新增 v1 `example/native` 与 GoDoc 示例，展示 Claude / Gemini 原生 HTTP provider 的预设构造、直接构造和流式调用方式

### Changed

- README 补充 v1 Claude / Gemini 原生 HTTP provider 的实现位置、最小用法和真实接口运行命令
- Claude / Gemini 原生流式调用复用统一的 HTTP/SSE 打开逻辑，保持对外行为不变

## [1.4.0] - 2026-05-15

### Added

- 新增 `NewAnthropicProvider` / `NewGeminiProvider` 与 `ProviderAnthropic` / `ProviderGemini`，无需官方 SDK 即可通过原生 HTTP API 接入 Claude 与 Gemini 文本对话
- 新增 `NativeProviderConfig` / `HTTPDoer` / `DefaultHTTPClient`，支持原生 provider 注入自定义 HTTP client
- 新增 Claude / Gemini 原生基础 SSE 流式响应与 Tool Use / Function Calling 映射
- 新增 `ProviderGLM` / `ProviderKimi` 别名，便于从 GLM / Kimi 命名迁移到 `ProviderZhipu` / `ProviderMoonshot`
- 新增 Claude / Gemini 真实接口 smoke test，设置 `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` 后可验证原生 provider 链路

### Changed

- `NewProviderFromPreset` 支持通过 `ProviderAnthropic` / `ProviderGemini` 创建原生 provider

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
