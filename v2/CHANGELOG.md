# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [2.7.0] - 2026-07-13

### Added

- 新增 `ErrInvalidPricing`，统一表示负费率、负 token、token 子集关系不成立、费率越界与最终金额溢出
- billingstore 新增 `Config.RedisCluster`，开启后同一用户的幂等集合与累计 key 使用相同 hash tag，兼容 Redis Cluster Lua 多 key 限制
- 补齐 `NewBillingHook`、`CostBudgetMiddleware`、`CompactMessages` 与 `RunToolLoopStream` 的可验证 GoDoc Example

### Changed

- billingstore 的用户级 `EntryID` 幂等集合改为与累计总量同生命周期，避免固定时间窗口到期后旧记录重放造成 Redis 与数据库账目分叉
- billingstore 当日配额 key 改为按 **UTC** 自然日切分（原按进程本地时区），多地区部署日界线一致。⚠ 升级迁移：日中滚动升级会使当天已按本地日期累计的用量搁浅、当日配额近似重置，建议在 UTC 日界线发布，或对当天旧 key 做一次性合并（迁移步骤见 billingstore README）

### Fixed

- 修复 `PricingTable.Cost` 在合法极值费率与 token 数下发生整数回绕的问题，乘加改为 128 位中间值并校验最终 `int64` 金额
- 修复 `CostBudgetMiddleware` 未校验费率且金额与 token 换算可能溢出的问题
- 修复缓存 token 或推理 token 超出所属总量时被重复计费或产生负计费基数的问题
- 修复 billingstore 的 Redis 累计缺少持久幂等、损坏数值被静默视为零、`Record` 与 `Close` 并发时已接纳流水可能丢失的问题
- billingstore 的 Redis 累计脚本在中途失败（累计字段被写脏、累加溢出 `int64`）时逆向回滚已应用增量并撤销幂等标记，杜绝"已去重未累计"坏账；负用量增量被拒绝而非倒扣累计值
- billingstore 后台刷库失败触发的 `OnError` 回调内调用 `Close` 不再死锁（刷库错误改由独立 goroutine 派发，避免 `flushLoop` 自等待）
- 修复流式观测在底层 `Close` 失败时未把错误写入 `stream_complete` 事件的问题
- 修复 `NewEntryID` 在系统熵源失败且同纳秒并发调用时可能生成重复值的问题
- 修复 `ExponentialBackoffWithJitter` 在上界为 `math.MaxInt64` 时整数溢出触发 panic 的问题（v1 同步修复）
- 修正 billingstore 独立模块对 v2 的版本依赖声明

## [2.6.0] - 2026-07-10

### Added

- `Usage` 新增 `CacheReadTokens` / `CacheWriteTokens` 字段，暴露提示词缓存的读/写 token 数（Anthropic `cache_read_input_tokens` / `cache_creation_input_tokens`、OpenAI `prompt_tokens_details.cached_tokens`、Gemini `cachedContentTokenCount`），便于按缓存价格分档计费；`RunToolLoopWithOptions` 的 `AccumulateUsage` 同步累加新字段
- 观测 Hook 新增 `stream_complete` 事件（`ObserveOperationStreamComplete`）：流终止时（读到 `io.EOF`、`Recv` 出错或提前 `Close`）上报一次，携带流上观测到的最终 `Usage` 与整个流的持续时长，流式调用的用量记账可统一接入
- `ObserveEvent` 新增 `StreamFinish` 字段（`eof` / `error` / `closed`），区分流的终止方式，计费方可识别"提前关闭导致 usage 缺失"的漏单场景
- 新增按用户计费能力：`WithUserID` / `WithConversationID` ctx 传递工具、`UsageRecorder` 接口与 `RecordEntry` 计量记录、`NewBillingHook` 观测适配器（一处挂载全局归账）、`CombineObserveHooks` 组合器、`MemoryUsageStore` 进程内存储（含 `UserTotals` / `ConversationTotals` 按用户与会话查询累计用量）
- 新增费用计算：`ModelRate` / `PricingTable` / `Cost`（金额为 int64 微元，费率可注入不硬编码，缓存与推理分档计价、未配置自动回落）与 `FormatMicros` 展示工具
- 新增配额拦截：`QuotaChecker` 接口（支持按用户与模型限额）、`QuotaMiddleware` / `QuotaStreamMiddleware` 请求前预检，超限返回新增 sentinel `ErrQuotaExceeded`，不产生真实调用
- 新增剩余额度硬限：`WithTokenBudget` 传入用户剩余 token 数，`TokenBudgetMiddleware` / `TokenBudgetStreamMiddleware` 预检输入估算并收缩 `MaxTokens`，从输出侧保证单次调用不超出剩余额度
- 新增金额口径的余额硬限：`WithCostBudget` 传入用户剩余余额（微元），`CostBudgetMiddleware` / `CostBudgetStreamMiddleware` 按 `PricingTable` 预检输入估算费用、按余额反推输出 token 上限收缩 `MaxTokens`；预算生效但 model 未配价时返回 `ErrModelNotPriced` 显式暴露配置缺失
- 新增 `RunToolLoopStream` / `RunToolLoopStreamWithOptions` 流式工具循环：每轮流式输出实时回调、工具调用增量由库内拼装、工具执行自动衔接，选项语义与 `RunToolLoopWithOptions` 一致
- 新增上下文管理工具：`EstimateTokens` 启发式 token 估算、`TrimMessagesToTokenBudget` 组感知历史裁剪（不拆散 tool_calls 与其结果）、`CompactMessages` 对话摘要压缩（可指定低价模型，摘要 usage 正常计费；返回 `CompactResult`，含摘要正文 `Summary` 与覆盖条数 `CompactedCount`，供业务按会话缓存摘要、支持增量摘要）
- 新增 `CollectStreamResult`：流式收集完整文本、推理内容与最终 `Usage`（`StreamResult`）
- `ChatRequest` 新增 `StreamUsage` 开关：默认对 OpenAI 兼容路径下发 `stream_options.include_usage`，可显式关闭以兼容不认识该参数的老网关
- 新增 DeepSeek 缓存字段预检 smoke 用例（env 门禁）；已实测确认 DeepSeek 回传标准 `cached_tokens` 字段（第二次同 prompt 调用 `CacheReadTokens=384`），现有映射直接可用
- 新增 `example/billingstore/` 计费存储参考实现（独立 go.mod，reference 级）：Redis 原子累计（总量 + 当日 TTL key）、流水异步批量刷入 GORM、`user_quota` 表支持 token/金额上限与 total/daily 口径、Redis 故障 fail-open，测试基于 miniredis 与纯 Go sqlite
- README 新增 `Usage` 统一语义说明与流式 token 用量统计用法，含流中断时 usage 缺失的漏单处理建议
- 新增多模态输出：`ChatRequest.OutputModalities` 声明输出模态，非文本结果经 `ChatResponse.Parts` / `StreamChunk.Parts` 返回（复用 `ContentPart` 载体）；当前 Gemini 原生支持图像输出，其他 provider 收到非文本模态显式返回 `ErrInvalidRequest`；`AssistantMessage` 自动并入非文本输出
- 流式调用补全计费元数据：`StreamChunk` 新增 `Model` 字段（响应侧实际模型名），`StreamReader` 新增 `Metadata()`（创建时的 RequestID 与响应头，`NewStreamReaderWithMetadata` 构造）；`stream_complete` 事件与 `RecordEntry` 由此携带有效模型名与 RequestID
- `FallbackProvider` 实现 `DefaultModel()` 探测接口（返回链首默认模型），降级链配合计费时 `RequestModel` 不再为空；README 补充多模型降级链用法与注意事项（`req.Model` 须留空、延迟叠加、流式降级仅在创建阶段）
- 新增 `NewFallbackProviderWithOptions` 与 `FallbackOptions.ShouldFallback`：自定义降级切换判定，多供应商冗余场景可放宽为 key 失效（401）、模型下线（404）、业务熔断错误也触发切换（默认仍为仅可重试错误）；`FallbackProvider` 支持嵌套组合实现"厂商内穷尽 model 后再切厂商"的两级降级（附测试与 README 说明）
- `RecordEntry` 新增 `EntryID` 幂等键（`NewEntryID` 生成）；billingstore 流水以唯一索引 + OnConflict DoNothing 实现幂等写入，并新增 `PricingVersion` 费率版本字段

### Changed

- `Usage` 各字段跨 provider 统一语义：`PromptTokens` 包含缓存读/写部分（Anthropic 原始 `input_tokens` 不含，已归一化），`CompletionTokens` 包含推理部分（Gemini 原始 `candidatesTokenCount` 不含 `thoughtsTokenCount`，已归一化）；使用了 prompt caching 或 Gemini 思考模式的调用方会观察到相应字段数值变化

### Fixed

- 修复 `FallbackProvider` 在调用方 ctx 已取消后仍继续尝试后续 provider 的问题：取消/超时后立即返回，不再发起无意义的尝试
- 修复客户端中断请求时计费记账随之失败的问题：`NewBillingHook` 交给 Recorder 的 ctx 已剥离取消信号（保留全部 value）——中断场景恰恰最需要落账（漏单审计），记账不再随请求一同取消
- 修复按模型别名配价时计费查价 miss 的问题（DeepSeek 真实流式验证发现：请求 `deepseek-chat` 实际回传 `deepseek-v4-flash`）：`ObserveEvent` / `RecordEntry` 新增 `RequestModel`（定价口径），`Model` 保留响应侧实际模型名（审计）；请求未指定 model 时自动探测 provider 默认模型（可选接口 `DefaultModel() string`，库内 provider 均已实现，`ObserveOptions.DefaultModel` 可手动覆盖）；billingstore 查价优先 `RequestModel`，流水两个模型名都记录
- 修复 `FormatMicros` 在 `math.MinInt64` 输入下因取负溢出输出非法格式（`--…`）的问题，改为十进制字符串移位实现，全域无溢出（fuzz 边界测试发现）
- 修复流式计费在请求未指定 model 时（使用 provider 默认模型）拿不到模型名、导致按 0 费用计的问题：`stream_complete` 事件现携带响应侧回传的实际模型名
- 修复根模块（v1）`go.sum` 缺少 `gtkit/json/v2 v2.0.7` 内容 hash 导致质量门禁无法运行的问题
- 修复 OpenAI 兼容平台流式调用拿不到 token 统计的问题：请求自动开启 `stream_options.include_usage`，统计随 `io.EOF` 前的收尾 chunk 给出
- 修复 Anthropic 原生流式调用丢弃 usage 的问题：`message_start` / `message_delta` 事件的统计现随 `FinishReason` 非空的 chunk 完整给出
- 修复 Gemini 原生流式调用丢弃 `usageMetadata` 的问题：统计随 `FinishReason` 非空的 chunk 完整给出
- 修复 Gemini 思考模式下 `Usage.ReasoningTokens` 恒为 0、分项相加与 `TotalTokens` 不一致的问题（解析 `thoughtsTokenCount`）

## [2.5.0] - 2026-07-01

### Added

- 新增 `SchemaFromType` / `JSONSchemaFormatFor` / `GenerateJSONWithSchema` / `GenerateJSONWithSchemaValidator`，通过反射从 Go 类型派生 json_schema 并解码，支持 `json` tag、`omitempty`/指针可选字段、`jsonschema:"enum=..."` 枚举与匿名嵌入扁平化
- `RunToolLoopOptions` 新增 `ToolRetry`（`ToolRetryOptions`），可为工具 handler 错误配置重试次数、退避与重试判定；零值保持既有「不重试」行为，context 取消不参与重试
- 新增 `MaskSecret`，用于在调用方日志中对密钥类字符串脱敏
- 新增 `ExponentialBackoffWithJitter` 全抖动退避，缓解并发重试惊群
- 重试现在遵守供应商的 `Retry-After` 响应头（通过新增的 `ProviderError.RetryAfter` 暴露）；仅原生 HTTP provider（Claude / Gemini / Ollama）可获取，OpenAI 兼容路径回退到退避策略
- `RunToolLoopOptions` 新增 `AccumulateUsage`，开启后返回响应的 `Usage` 为所有轮次 token 消耗的累加值；默认关闭，保持返回最后一轮 Usage 的既有行为

### Changed

- README 补充 v2 相比 v1 的新增能力说明，明确多模态、结构化输出、本地推理、token counting 等新能力只在 v2 增加
- README 新增本地 LLM 接入（Ollama / vLLM / LM Studio / LocalAI / llama.cpp）详细示例

## [2.4.0] - 2026-05-21

### Added

- 新增 `FileDataPart` / `FileURLPart` / `FileIDPart` 与 `CacheControlEphemeral`，支持文件类多模态内容与 Anthropic prompt caching
- 新增 `ChatRequest.Seed` / `ChatRequest.CandidateCount`，支持确定性采样与多候选生成
- 新增 Claude / Gemini 原生流式 Tool Use 增量映射，调用方可通过 `StreamChunk.ToolCalls` 读取工具调用片段
- 新增 `TokenCounter` / `CountTokens` / `TokenCountResponse` 与 Gemini 原生 token counting 支持
- 新增 Groq / Mistral / Cohere OpenAI 兼容预设
- 新增 `NewAzureOpenAIProvider` 与 `NewBedrockOpenAIProvider`，用于需要资源或 region 信息的企业平台入口

### Changed

- README 补充 v2 相比 v1 的新增能力说明，并更新平台矩阵、多模态文件、prompt caching、token counting、Azure OpenAI 与 Bedrock 用法

## [2.3.0] - 2026-05-15

### Added

- 新增 `NewGeminiEmbedder`，通过 Gemini 原生 `embedContent` / `batchEmbedContents` 接口支持 `gemini-embedding-001`
- 新增 Gemini embedding 预设、能力元数据和真实接口 smoke test，`QuickRegistry` 会在提供 `GEMINI_API_KEY` 时注册 Gemini embedder
- README 新增 v2 provider 能力矩阵，并补充 Gemini embedding 用法

### Changed

- Claude / Gemini 原生流式调用复用统一的 HTTP/SSE 打开逻辑，保持对外行为不变

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
