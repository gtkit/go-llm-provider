# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Changed

- 通用 `CombineObserveHooks` 移至 observability.go，签名和行为不变；新增业务自有身份、稳定调用 ID 和有限写入预算的 Example。

### Deprecated


### Removed

- **不兼容、尚未发布**：按明确授权删除工具执行、实例注册及渠道策略 API；v2 同时删除账务/定价/额度、历史策略、相似度和反射/JSON 工作流辅助。专属测试、过时示例和关联 CI 入口同步删除。保留协议与通用观察能力，完整清单见 `MIGRATION.md`；模块路径与版本未变，发布版本以后再定。

### Fixed

- OpenAI 兼容 Chat/ChatStream 保留显式 `Temperature=0`、`TopP=0`，修复 go-openai 标量 `omitempty` 省略零值的问题；nil 仍不下发，非零值不重复编码。

### Security

## [2.17.0] - 2026-09-28

### Added

- 智谱与百炼支持平台服务端内建联网搜索：`WebSearchTool()` 在这两个平台上可用，不再返回 `ErrInvalidRequest`；两家预设的 `CapabilityWebSearch` 随之置位
- `SearchMetadata` 新增 `SourcesUnavailable` 字段，用于区分"模型未搜索"与"已搜索但平台协议不回传来源"（百炼的 OpenAI 兼容协议属于后者）
- `RunToolLoopOptions` 新增 `ToolSelector` 钩子：工具循环每轮下发前由调用方裁剪工具集，用于工具数量大时降低 token 成本与选错率；流式与非流式语义一致，为 `nil` 时全量下发、行为不变
- 工具循环在调用 handler 前检查参数：参数非空且不是合法 JSON（常见于输出被 `max_tokens` 截断）时不调用 handler、不进入重试，以新增哨兵 `ErrToolArgumentsInvalid` 经 `ToolErrorEncoder` 生成工具结果回传模型，循环继续；空参数照常交给 handler
- 新增 `NewIdleTimeoutDoer(next, timeout)`：包装 `HTTPDoer`，响应体自拿到响应起、以及每次读到数据后超过 `timeout` 未读到任何字节即关闭，读取返回 `Code` 为 `ErrorCodeTimeout`、`Cause` 为新增哨兵 `ErrStreamIdleTimeout` 的 `*ProviderError`，同时满足 `errors.Is(err, ErrTimeout)`。用于流式响应中途停止发送数据时及时失败；OpenAI 兼容与原生平台均可用，赋给 `ProviderConfig.HTTPClient` / `NativeProviderConfig.HTTPClient` 即生效
- 新增结构化判断接口 `Decider` 与构造函数 `NewDecider`，对接 TypeSafe AI 的 Jev 模型（`POST {BaseURL}/systemone`，默认 `https://api.typesafe.ai/v1`、模型 `jev-latest`）：一次请求对同一状态评估多个是/否（`DecisionNoul`）、多选一（`DecisionChoice`）、有序评分（`DecisionScore`）问题，返回概率与置信度；发送前校验请求，响应缺题或答案类型不符时报错；401/422/429/529 映射为对应 `ErrorCode`（529 可重试），`x-typesafe-request-id` 写入 `ResponseMetadata.RequestID`。新增 `ProviderTypeSafe` 常量与哨兵 `ErrNilDecider`、`ErrNilDecideRequest`、`ErrInvalidDecideRequest`、`ErrInvalidDeciderConfig`

### Changed

- ⚠ 工具循环（`RunToolLoop` / `RunToolLoopWithOptions` / `RunToolLoopStream` / `RunToolLoopStreamWithOptions`）只执行本轮下发的函数工具：模型请求的函数名不在本轮工具集（`req.Tools` 或 `ToolSelector` 的返回值）中时，handler 不被调用、不进入重试，该调用以新增哨兵 `ErrToolNotOffered` 经 `ToolErrorEncoder` 生成工具结果回传模型，循环继续。此前模型可以调用任意未下发、或已被 `ToolSelector` 裁掉的工具，裁剪无法充当权限边界。**迁移**：依赖 handler 处理未声明工具的调用方，把这些工具加入 `req.Tools`（或 `ToolSelector` 的返回值）；`WebSearch` 服务端工具不受影响
- ⚠ 工具循环执行工具期间的回调（handler、`ToolResultTransformer`、`ToolErrorEncoder`、`ToolRetryOptions` 的回调）发生 panic 时被恢复，循环以包装新增哨兵 `ErrToolHandlerPanic` 的错误中止，错误文本含工具名、panic 值与堆栈；串行与并行执行一致。此前并行执行时 panic 发生在库内 goroutine 中，调用方无法 recover，进程直接退出；串行执行时 panic 向调用方传播。**迁移**：依赖外层 `recover` 捕获工具 panic 的调用方，改为检查 `errors.Is(err, ErrToolHandlerPanic)`
- `DefaultToolErrorEncoder` 对库自身的哨兵输出可供模型自我纠正的文案：`ErrToolNotOffered` 为 `tool not available in this round; use only the tools provided`，`ErrToolArgumentsInvalid` 为 `tool arguments are not valid JSON (possibly truncated); regenerate the call with complete arguments`；handler 返回的业务错误仍统一为 `tool execution failed`

### Fixed

- `ResponseValidator` 与 `RunToolLoopStreamWithOptions` 的文档写明流式校验时机：校验在最后一轮全部 chunk 经 `onChunk` 送出之后进行，只决定返回值与错误；需要未通过校验的内容不外露时，在 `onChunk` 中缓冲
- `ToolRetryOptions` 文档补充幂等约束：有副作用的工具开启重试时，用幂等键去重或通过 `ShouldRetry` 只放行可安全重放的错误
- `example/toolsecurity` 与 `example/websearch` 的注入特征检测改在规范化副本上进行（全角折半角、删除零宽字符、各类空白归一），全角字母、零宽字符、NBSP 打散的注入短语不再漏检；截断与日志预览改为按字符计，不再切出非法 UTF-8
- `example/websearch` 对搜索结果的 URL 与站点名同样净化并限长，标题、URL、站点名、正文任一命中都对该条结果回调一次 `OnSuspicious`（此前只检测正文，标题命中不告警）
- `PROMPT_INJECTION_DEFENSE.md` 模板同步：新增 `NormalizeForDetection` 与 `TruncateRunes`，`Sanitize` 的 `maxLen` 按字符计，用例表补充全角与零宽绕过样例

## [2.16.0] - 2026-09-20

> 面向 2026-09 各厂商现状的一次对齐：修复会直接失败的默认模型与参数形态，补齐思考模型在工具循环中必需的推理往返机制，并新增 OpenAI Responses API 路径、Ollama 完整能力映射与联网搜索示例模块。

### Added

- Ollama 原生路径补齐 `/api/chat` 的完整能力映射，此前 Tools / ResponseFormat / Thinking 一律返回 `ErrInvalidRequest`，图片片段被静默丢弃：
  - Tool Use：函数工具下发、响应 `tool_calls`（对象入参归一为 JSON 字符串）、合成调用 ID（`ollama_<函数名>_<序号>`，Ollama 不签发 ID）、`done_reason` 对齐为 `tool_calls`、历史回放带 `tool_name`；流式下整块到达的工具调用按到达顺序编号。`ToolChoice` 仅接受自动选择、`ParallelToolCalls` 无映射，非默认取值 fail-closed
  - 结构化输出：`json_object` → `format: "json"`，`json_schema` → schema 对象
  - 思考：`Thinking.Enabled` → `think` 布尔开关，`Thinking.Effort` → `think` 档位字符串；响应 `thinking` 归位到 `Reasoning` / `ReasoningDelta`，历史回放写回 assistant 的 `thinking`（推理往返）
  - 图片：内联字节与 data URL 映射为 `images`，远程 URL 拒绝；`Seed` 映射 `options.seed`
  - `ProviderOllama` 预设声明 `CapabilityTools` / `CapabilityStructuredOutput` / `CapabilityReasoning` / `CapabilityVision`，推理支持表加入 Ollama（Enabled + Effort）
- 新增 `NewOpenAIResponsesProvider` / `OpenAIResponsesConfig`：走 OpenAI Responses API（`/responses`）的 `Provider` 实现，OpenAI 官方已将其作为推理 / 工具调用 / 多轮的推荐入口，DeepSeek、方舟、智谱、Kimi 也提供同形端点（`Name` 填对应 ProviderName 即可归账）。映射覆盖：system → `instructions`，历史拆为 reasoning / message / function_call / function_call_output 输入项；默认 `store: false` 并请求 `reasoning.encrypted_content`，推理项以 `ThinkingBlocks{Signature: 项 id, Redacted: encrypted_content}` 往返；`Thinking.Effort` → `reasoning.effort`（附 `summary: auto`，摘要进 `Reasoning`）；`WebSearchTool` → 内建 `web_search`（支持 `AllowedDomains`），查询与 `url_citation` 进 `Search`、次数计入 `WebSearchRequests`；结构化输出 → `text.format`；`FunctionDef.Strict`、扁平 `tool_choice`；流式事件归一为 `StreamChunk`（推理项完成帧同时给出 `ThinkingSignature` 与 `RedactedThinking`）。`Stop` / `Seed` / `CandidateCount` 该协议无对应字段，返回 `ErrInvalidRequest`
- `thinkingAssembler`（`RunToolLoopStream` 内部）同帧到达的 `ThinkingSignature` 与 `RedactedThinking` 现并入同一个 `ThinkingBlock`，以承载 Responses API 的推理项；Anthropic 的两类块仍分别到达，行为不变
- 新增示例子模块 `example/websearch`（独立 go.mod）：给 Agent 接联网搜索的参考实现。国产 OpenAI 兼容平台没有平台内建搜索，只能走"函数工具 + RunToolLoop"，本模块提供完整接法——应用层 `SearchClient` 契约、博查（Bocha）Web Search 实现（含条数裁剪、长短摘要回退、错误分类与可重试判定）、`Toolset` 包装成 `provider.Tool` / `ToolHandler`、三道提示注入防线（内容净化 + `WrapToolResultInTag` 标签隔离 + 系统提示词声明）、`[ref:N]` 引用校验防编造出处，以及可运行 demo。换搜索服务商只需替换 `SearchClient` 实现；核心包依赖不受影响
- 新增仓库根目录 `ROADMAP.md` 与 `provider/verification_smoke_test.go`：记录 2.16 起按官方文档实现、尚未用真实 key 实测的 6 条协议细节（V-01 ~ V-06，`TestVerify*` 用例缺 key 自动跳过）以及待排期功能（Anthropic compaction、音视频 ContentPart、Batch API）
- **推理内容往返**：`Message` 新增 `Reasoning`、`ThinkingBlocks`、`ToolName`，`ToolCall` / `ToolCallDelta` 新增 `ThoughtSignature`，`ChatResponse` 新增 `ThinkingBlocks`，`StreamChunk` 新增 `ThinkingSignature` / `RedactedThinking`。思考模型在工具循环中普遍要求回传上一轮推理（DeepSeek 带 tools 时缺失直接 400、Kimi K3 要求回传完整 assistant 消息、Anthropic 要求回传带签名的 thinking 块、Gemini 3 要求回传 functionCall 的 thoughtSignature），此前 `AssistantMessage()` 全部丢弃，`RunToolLoop` 在这些模型上第二轮必然失败。现在 `AssistantMessage()`、`RunToolLoop`、`RunToolLoopStream` 自动携带；OpenAI 兼容路径把 `Reasoning` 映射为 `reasoning_content`，只对已知接受该字段的平台下发（DeepSeek / 智谱 / Kimi / 百炼 / 方舟），自定义平台用新增的 `ProviderConfig.SupportsReasoningContent` 声明
- 新增 `ToolResultMessageFor` / `ToolResultMessageJSONFor`：按 `ToolCall` 构造工具结果并带上函数名。Gemini 的 `functionResponse.name` 必须是函数名，此前被填成 `ToolCallID`（Gemini 3 同时校验 id 与 name，必被拒绝）；只给 `ToolCallID` 的既有调用在同一请求内按 ID 反查函数名，反查不到返回 `ErrInvalidRequest`
- `Thinking.Effort` 现已映射到 DeepSeek / 智谱 / Moonshot（`reasoning_effort`）、Anthropic（`output_config.effort`）与 Gemini（`thinkingConfig.thinkingLevel`，Gemini 3 系推荐口径，与 `BudgetTokens` 互斥）；`Thinking.Enabled` 现已映射到 DeepSeek / 智谱 / Moonshot（顶层 `thinking.type`，与方舟同形，共享 `compat_thinking.go` 的映射）。新增常量 `ThinkingEffortNone` / `ThinkingEffortXHigh` / `ThinkingEffortMax`
- Anthropic 自适应思考：`Thinking.Enabled: true` 且无预算时下发 `thinking.type = adaptive`（Claude 4.6 起的推荐口径，Claude 5 默认）；`BudgetTokens` 保留为 4.5 及更早模型的旧式固定预算
- Anthropic 原生结构化输出：`ResponseFormat` 改走 `output_config.format`（json_schema），不再借用"强制调用假工具"——Fable 5.1 拒绝强制工具调用，且原生格式可与真正的工具调用、原生搜索并存（此前互斥）
- Anthropic 提示词缓存补齐：`CacheControl` 新增 `TTL`（`CacheControlTTL5m` / `CacheControlTTL1h`）与 `CacheControlEphemeralTTL`；system 提示词改以内容块数组下发，片段级 `CacheControl` 不再在 system 上丢失（此前 system 被拼成字符串，最常见的缓存对象反而缓存不了）
- `NativeProviderConfig` 新增 `Headers`：附加到每个请求的自定义请求头，在库默认头之后写入、同名覆盖，是 `anthropic-beta`（服务端 compaction、按轮次 effort 等）与企业网关鉴权头的入口；Anthropic / Gemini provider 与 Gemini embedder 均支持
- `FunctionDef` 新增 `Strict`，映射 OpenAI 兼容路径与 Anthropic 的工具级 `strict`
- Embedding 面向 RAG 的两项补齐：`EmbeddingRequest` 新增 `TaskType`（`EmbeddingTaskQuery` / `Document` / `Similarity` / `Classification` / `Clustering`，当前映射 Gemini `task_type`，OpenAI 兼容端点无该字段、模型对称编码，该提示被忽略）；新增 `EmbedBatchWithOptions` 与 `EmbedBatchOptions`，按平台已知单次上限（OpenAI 2048、Gemini 100、Cohere 96、百炼 10，未知平台 10）自动分片、可选并发，任一分片失败即整体报错并取消在途请求，不返回带空洞的半成品；`EmbedBatch` 现等价于零值选项调用
- Gemini 结构化输出改走 `responseJsonSchema`（接受标准 JSON Schema，含 `additionalProperties`），此前的 `responseSchema` 只认 OpenAPI 子集
- 预设能力矩阵更新：智谱与 Moonshot 声明 `CapabilityReasoning`；百炼、千帆、Moonshot 声明 `CapabilityVision`

### Changed

- ⚠ 预设默认模型对照 2026-09 官方现状全面更新：DeepSeek `deepseek-chat` → `deepseek-v4-flash`（旧名已于 2026-07-24 停用）、Groq `llama-3.3-70b-versatile` → `openai/gpt-oss-120b`（已于 2026-08-16 关停）、Gemini `gemini-2.5-flash` → `gemini-3.5-flash`（2.5 系计划 2026-10 关停）、Anthropic `claude-sonnet-4-5` → `claude-sonnet-5`、OpenAI `gpt-5.4-mini` → `gpt-5.6-terra`、百炼 `qwen3.6-plus` → `qwen3.7-plus` 且 embedding `text-embedding-v3` → `text-embedding-v4`、智谱 `glm-5.1` → `glm-5.3`、千帆 `ernie-4.5-turbo-32k` → `ernie-5.1`、硅基流动 `deepseek-ai/DeepSeek-V3` → `deepseek-ai/DeepSeek-V4-Flash`、Moonshot `kimi-k2-turbo-preview` → `kimi-k2.6`、方舟 `doubao-seed-2-0-pro-260215` → `doubao-seed-2-1-pro-260628`、xAI `grok-4-1-fast-non-reasoning` → `grok-4.6`、Cohere `command-a-03-2025` → `command-a-plus-05-2026`。显式指定 `Model` 的调用方不受影响
- ⚠ OpenAI 与 Azure OpenAI 路径的 `ChatRequest.MaxTokens` 改下发 `max_completion_tokens`：OpenAI 推理模型（gpt-5 系 / o 系）已拒绝 `max_tokens`。其余 OpenAI 兼容平台仍下发 `max_tokens`
- ⚠ DeepSeek 的 `Thinking.Enabled` 改为顶层 `thinking.type`，不再写 `chat_template_kwargs.enable_thinking`——后者是 vLLM 自部署的约定，DeepSeek 官方端点并不识别，此前该开关实际无效
- ⚠ Anthropic 的 `Thinking.Enabled: true` 且无 `BudgetTokens` 从返回 `ErrInvalidRequest` 改为下发 `adaptive`；`Thinking.Effort` 从被拒改为映射 `output_config.effort`。Gemini 的 `Thinking.Effort` 从被拒改为映射 `thinkingLevel`
- ⚠ `DefaultHTTPClient` 的 `ResponseHeaderTimeout` 从 60 秒放宽到 15 分钟：非流式调用下平台要等整段生成完才发响应头，思考模型高强度推理一次可达数分钟，60 秒会把正常的长推理请求误判为网络故障。总时长预算仍由调用方 ctx 控制
- Anthropic `tool_result` 的内容改为字符串（纯文本）或平台内容块数组（含图片），此前直接序列化库内 `ContentPart` 会发出平台不认识的字段名
- `AssistantMessage()` 与 `RunToolLoopStream` 组装的 assistant 消息现携带推理内容与签名思考块（见 Added）；不需要回传推理文本时可自行清空 `Message.Reasoning`
- Moonshot 开放平台入口更新为 platform.kimi.com；README 模型速查表更新到 2026-09（含 GPT-5.6 / GPT-6 Astra、Claude 5 系、Gemini 3.x、DeepSeek V4、Qwen 3.7 / 3.8、GLM-5.3、Kimi K3、Doubao Seed 2.1、ERNIE 5.1、Grok 4.6、Command A+）

### Fixed

- Gemini 3 系工具循环第二轮 400：响应 `functionCall` 上的 `thoughtSignature` 此前被解析丢弃，现在随 `ToolCall.ThoughtSignature` 保留并在回传时原样携带（流式路径同样保留）
- Anthropic 开启思考并使用工具时第二轮被拒：响应中的 `thinking` 块签名与 `redacted_thinking` 数据此前被丢弃，现在随 `ThinkingBlocks` 保留并按平台要求置于 assistant 内容最前回传；流式 `signature_delta` 从被忽略改为透出 `StreamChunk.ThinkingSignature`

## [2.15.0] - 2026-09-07

### Added

- `ProviderQwen` 支持深度思考控制：`Thinking.Enabled` 映射阿里云百炼请求体顶层的 `enable_thinking`，`Thinking.BudgetTokens` 映射顶层 `thinking_budget`，两者互相独立，字段为 nil 时不下发。此前对百炼设置任一 `Thinking` 字段都会返回 `ErrInvalidRequest`。`Thinking.Effort` 仍不映射——百炼 OpenAI 兼容 Chat Completions 端点没有对应字段，传入时返回 `ErrInvalidRequest` 并列出已映射的两个字段
- `ProviderQwen` 预设的 `ModelCapabilities` 增加 `CapabilityReasoning`，`Supports(CapabilityReasoning)` 现在返回 `true`

### Changed

- 平台专属实现的文件命名统一为 `native_<平台>.go`（原生协议）与 `compat_<平台>.go`（OpenAI 兼容协议下的专属映射），DeepSeek 的 `chat_template_kwargs` 映射从请求构建函数移入 `compat_deepseek.go`，改由 `thinkingAppliers` 表分发。纯内部整理，导出 API 与请求体均无变化
- ⚠ 破坏性变更：`ProviderQwen` 进入库内推理映射表后，其 `Thinking` 字段支持范围改由库判定，`ProviderConfig.SupportsReasoningEffort` 对该平台不再生效。此前用 `ProviderConfig{Name: provider.ProviderQwen, SupportsReasoningEffort: true}` 下发 `reasoning_effort` 的接入会开始返回 `ErrInvalidRequest`；继续按调用方声明生效需改用非 `qwen` 的自定义 `Name`

### Deprecated

### Removed

### Fixed

### Security

## [2.14.0] - 2026-09-01

### Added

- `ProviderConfig` 新增 `SupportsReasoningEffort`，让用 `NewProvider` 自定义接入的平台（未收录的 OpenAI 兼容平台、自建推理服务）也能使用 `Thinking.Effort`。库对未收录平台默认拒绝全部 `Thinking` 字段以免静默丢弃调用方意图，此前这条规则把这类平台一并堵死且无绕过办法；声明后 `Effort` 映射为 OpenAI 标准的 `reasoning_effort`。仅解锁 `Effort`——`Enabled` 与 `BudgetTokens` 各家落在不同私有字段上，库无从代为映射；内置预设的支持范围仍由库判定，对其声明该字段不生效
- `BedrockOpenAIConfig` 同步新增 `SupportsReasoningEffort`：`ProviderBedrock` 不在库内的推理映射表中（能否使用 `reasoning_effort` 取决于所选底层模型，库不代为断言），该声明是 `NewBedrockOpenAIProvider` 使用 `Thinking.Effort` 的入口

### Fixed

- `Thinking.Effort` 的下发改由能力判定驱动，不再按平台名枚举：此前校验放行后，只有 `ProviderOpenAI` / `ProviderAzureOpenAI` / `ProviderArk` 三个硬编码分支会真正写入 `reasoning_effort`，其余平台校验通过却被静默丢弃

## [2.13.0] - 2026-09-01

### Added

- `Breaker` 新增 `ReadyToTrip` 选项与 `FailureRateTrip` 助手，支持按滑动窗口内的失败率判定跳闸。此前只有绝对次数阈值 `FailureThreshold`，它与流量规模无关——1 分钟窗口配 5 次失败，在 1000 QPS 下只要上游有 0.1% 的偶发错误率就会持续跳闸，把 99.9% 本可成功的请求挡在本地。新选项与 QPS 解耦，`FailureRateTrip(minSamples, maxFailureRate)` 内置样本下限保护（样本不足时不跳闸）。配置后 `FailureThreshold` 不再参与判定；未配置时行为与之前完全一致，不记录成功样本、不分配分桶。启用后 `BreakerStats` 新增的 `Successes` 字段与 `Failures` 一同反映窗口内统计，供健康检查读取
- `BalancedProvider` 新增会话粘性策略 `BalanceSessionAffinity`：按会话键哈希稳定选中成员，同一会话的多轮请求落到同一成员，让该成员上游的提示词缓存能连续命中。此前三种策略都会把同一会话打散到不同成员，各成员缓存均为冷启动，而缓存命中的输入单价通常只有常规输入的一小部分。会话键默认取 `ConversationIDFromContext` 再回落 `UserIDFromContext`，可用新增的 `BalanceOptions.SessionKey` 自定义；哈希不含随机种子，多副本与重启后归属一致；会话键为空时退化为平滑加权轮询，故障转移语义不变
- `Usage` 新增 `CacheWrite5mTokens` / `CacheWrite1hTokens`，承载 Anthropic 缓存写入的 TTL 分档明细（`usage.cache_creation.ephemeral_5m/1h_input_tokens`），二者均为 `CacheWriteTokens` 的子集；流式累计与 `RunToolLoop` 的多轮用量聚合同步覆盖
- `ModelRate` 新增 `CacheWrite5mPer1M` / `CacheWrite1hPer1M` 分档写入单价，`PricingTable.Cost` 按档计价。长 TTL 缓存写入单价高于短 TTL，此前只有单一 `CacheWritePer1M`，使用 1 小时缓存的调用会被系统性少算写入成本。两项为 0 时该档回落到 `CacheWritePer1M`，且平台未上报分档明细时写入总量整体按 `CacheWritePer1M` 计价——既有费率表与既有账目金额不变
- `Thinking` 新增 `BudgetTokens` 字段，补齐"按 token 预算控制推理深度"的口径（对应 Anthropic 的 `thinking.budget_tokens` 与 Gemini 的 `thinkingConfig.thinkingBudget`）；原有 `Effort` 仍是档位口径，新增便利常量 `ThinkingEffortMinimal`
- Anthropic 原生路径支持推理控制：下发 `thinking` 参数，响应中的 `thinking` 块归位到 `ChatResponse.Reasoning`、流式 `thinking_delta` 归位到 `StreamChunk.ReasoningDelta`，均不混入正文；开启思考时要求显式的正数 `BudgetTokens`，缺失或非正数返回 `ErrInvalidRequest`（预算大小直接决定费用，不由库代为推导）
- Gemini 原生路径支持推理控制：下发 `generationConfig.thinkingConfig`，未给出预算时按平台语义取值表达意图（`Enabled: true` → `thinkingBudget: -1` 由模型动态决定，`Enabled: false` → `thinkingBudget: 0` 禁用）；响应中 `thought` 标记的 part 归位到 `Reasoning` / `ReasoningDelta`，不再混入正文
- Azure OpenAI 路径支持 `Thinking.Effort`（此前该字段仅对 `ProviderOpenAI` 生效）
- `ProviderAnthropic` 与 `ProviderGemini` 预设新增 `CapabilityReasoning` 声明，可用 `ModelCapabilities.Supports` 提前预检
- `v2/README.md` 的"Thinking（思考模式）"章节重写：两套口径的适用平台矩阵、各平台示例、响应归位说明与流式接收思考过程的写法；能力矩阵中 Anthropic / Gemini 的 Reasoning 列更新为"是"

### Changed

- ⚠ 破坏性变更：`ChatRequest.Thinking` 中平台未映射的字段不再被静默忽略，改为在请求构建阶段返回 `ErrInvalidRequest`，错误信息列出该平台已映射的字段。此前对 Anthropic / Gemini 原生路径设置 `Thinking` 完全无效果，对国产 OpenAI 兼容平台设置 `Enabled` / `Effort` 也被丢弃，调用方无从察觉思考并未开启，却仍可能按推理 token 付费（推理 token 计入输出、按输出价计费）。迁移方式：按 `v2/README.md` 的平台矩阵改用该平台已映射的字段，或移除对该平台无效的 `Thinking` 设置

### Fixed

- Anthropic 思考预算超过本库默认 `max_tokens` 时自动抬高上限：思考预算需小于 `max_tokens`，而调用方只设 `Thinking.BudgetTokens`、不设 `ChatRequest.MaxTokens` 时会拿到一个针对自己从未设置过的参数的平台错误。现在这种情况下 `max_tokens` 取"预算 + 默认余量"；显式设置过 `MaxTokens` 的请求一律尊重原值，冲突交由平台裁决
- Gemini 流式：只携带思考摘要的事件此前会被"无正文即跳过"的判定整块丢弃，导致 `ReasoningDelta` 缺失；跳过判定改为同时检查思考内容
- `PricingTable.Cost` / `Validate` 补齐分档相关校验：分档 token 之和超过 `CacheWriteTokens`、分档 token 为负、分档费率越界均返回 `ErrInvalidPricing`，不静默算出偏低金额
- Ollama 原生路径此前静默忽略 `Thinking`，改为返回 `ErrInvalidRequest`（原生 thinking 控制尚未实现）

## [2.12.0] - 2026-08-31

### Added

- 新增 Prompt Injection 防御规则模板文档 `PROMPT_INJECTION_DEFENSE.md`：与本库解耦，只依赖标准库，可直接复制到任何项目使用；含可复制的正则特征词表、Markdown 转义、标签隔离、输出解析校验的完整代码模板，以及已验证的攻击载荷/正常文本用例表
- 新增 `PromptTask[P]`：将润色、翻译、摘要等单轮任务的 system prompt 构造逻辑与常用请求参数（Model / Temperature / MaxTokens / ResponseFormat）绑定为可复用值，调用时按运行时参数 `P` 拼装 system prompt；配套 `Run` 返回纯文本、`RunPromptTaskJSON` 解码为结构化类型，新增 sentinel `ErrNilTaskSystem`。`System` 闭包由调用方实现且不经过 `RunToolLoop` 的注入防护钩子，GoDoc 与 `PROMPT_INJECTION_DEFENSE.md` 新增一节说明该路径的风险与缓解方式

## [2.11.1] - 2026-08-31

### Fixed

- `example/toolsecurity` 的可疑指令特征词检测修复漏检：早期正则要求限定词与名词严格相邻，实测连"ignore all previous instructions"这类最常见的英文注入短语、以及文章原文"忘记你之前的处理任务"都会漏检；改为允许中间插入代词/修饰词，并补充"系统提示词"等中文特征词

## [2.11.0] - 2026-08-28

### Added

- `RunToolLoopOptions` 新增 `ToolResultTransformer` / `ResponseValidator` 两个可选钩子，用于抵御工具结果携带间接提示注入内容：前者在工具结果写回对话历史前加工，后者在返回最终响应前校验，二者返回 error 均会中止工具循环、不将未处理或未通过校验的内容交还给调用方；`RunToolLoopWithOptions` 与 `RunToolLoopStreamWithOptions` 均已接入，均为 nil 时行为与之前版本完全一致。配套内置便捷函数 `WrapToolResultInTag`，将工具结果包裹进指定标签并转义结果中的尖括号，防止结果内容本身携带同名标签文本提前闭合标签；`example/toolsecurity` 提供组合长度截断、特征词检测、Markdown 结构符转义、标签结构隔离、`ResponseFormat` 强制 JSON Schema 输出、字段与敏感词校验的完整可运行示例

## [2.10.0] - 2026-08-21

> 向后兼容的新功能（MINOR）。新增导出 API 与能力位，keyed struct literal 调用方不受影响。

### Added

- 新增火山方舟（Ark）平台预设 `ProviderArk`：预置 OpenAI 兼容端点 `https://ark.cn-beijing.volces.com/api/v3`、默认 Chat 模型 `doubao-seed-2-0-pro-260215` 与 embedding 模型 `doubao-embedding-text-240515`，传 APIKey 即可通过 `NewProviderFromPreset` / `NewEmbedderFromPreset` / `QuickRegistry` 接入；`Model` 同时接受方舟模型 ID 与推理接入点 ID（`ep-` 开头）
- `Thinking` 现已映射方舟深度思考控制：`Enabled` 下发请求体顶层 `thinking` 字段（`enabled` / `disabled`，nil 时不下发该字段，由方舟按模型的默认行为决定），`Effort` 下发 `reasoning_effort`；两者互相独立下发，本库不做取舍；非流式与流式调用均生效
- 新增内置熔断器 `Breaker`：滑动窗口失败计数达阈值即跳闸，冷却期内请求以 `ErrBreakerOpen` 在本地快速失败、不发往平台；冷却到期放行半开探测，探测成功即闭合、失败则冷却时长翻倍（上限 `MaxOpenDuration`）。配套 `WithBreaker` / `BreakerMiddleware` / `BreakerStreamMiddleware` / `WithEmbedderBreaker` / `BreakerEmbedMiddleware`，`State()`、`Stats()`、`Reset()` 供健康检查与人工放行使用
- 新增客户端侧限流器 `RateLimiter`：RPM 与 TPM 各走一个令牌桶，超额请求以 `ErrLocalRateLimited` 挡在发出之前；token 额度采用"预扣 + 真实 `Usage` 结算"（流式延后到流终止结算），`AdaptFromHeaders` 可用 `x-ratelimit-remaining-*` 响应头把本地额度下调到平台口径。配套 `WithRateLimit` / `RateLimitMiddleware` / `RateLimitStreamMiddleware` / `WithEmbedderRateLimit` / `RateLimitEmbedMiddleware` 与 `EstimateChatRequestTokens`
- 新增加权负载均衡 `BalancedProvider`：按权重把流量分摊到多个成员（多 key 分摊配额、多地域就近、按成本混流），支持平滑加权轮询、加权随机、加权最少在途三种策略；成员可各带独立熔断器，熔断打开的成员自动被跳过并在冷却到期后恢复接流；`Stats()` 暴露每个成员的权重、在途数与熔断状态
- 新增价格表原子容器 `PricingRegistry`：构造与 `Replace` 时整表拷贝并强制走 `Validate`，计价读取走原子指针，把"运行中改价会 data race"的文档约束变成 API 保证；每一代价格带 `Version` 标识，便于账务对账还原当时口径
- 新增重排序能力 `Reranker`：`POST {BaseURL}/rerank` 的 OpenAI 兼容映射（硅基流动、Jina、Cohere 等平台形态一致），用量字段各家口径统一归一到 `RerankResponse.Usage`；平台返回越界 `Index` 时直接报错，不把越界值透给调用方；`RerankResponse.SortedDocuments` 把精排结果接回本地候选集。配套 `NewReranker` / `NewRerankerFromPreset`、`CapabilityRerank` 与 `Preset.RerankModel`（硅基流动预置 `BAAI/bge-reranker-v2-m3`）
- 新增本地拦截类 sentinel：`ErrBreakerOpen`、`ErrLocalRateLimited`、`ErrNilBreaker`、`ErrNilRateLimiter`、`ErrInvalidBalanceStrategy`、`ErrNilPricingRegistry`、`ErrNilReranker`、`ErrNilRerankRequest`、`ErrEmptyRerankQuery`、`ErrEmptyRerankDocuments`、`ErrInvalidRerankerConfig`

### Changed

- 降级链 `FallbackProvider` 的默认切换判定从"仅平台侧可重试错误"扩展为"平台侧可重试错误 + `ErrBreakerOpen` + `ErrLocalRateLimited`"：熔断打开与本地限流都意味着当前成员短期不可用，留在原地重试必然继续失败。两个新错误只在使用内置熔断器 / 限流器后才可能出现，既有调用方行为不变；显式传入 `FallbackOptions.ShouldFallback` 的调用方完全不受影响
- `Preset` 与 `ModelCapabilities` 新增 `RerankModel` 字段（keyed struct literal 调用方不受影响）

### Deprecated

### Removed

### Fixed

### Security

## [2.9.0] - 2026-07-20

> 向后兼容的新功能（MINOR）。新增导出 API 与能力位，keyed struct literal 调用方不受影响。

### Added

- 新增 `FileService` 文件管理接口（`UploadFile` / `FileContent` / `DeleteFile`）：OpenAI 兼容 provider（含 Azure / Bedrock 构造器）通过类型断言获取，覆盖国内平台"上传文件 → 文档问答"流程；文件操作错误与 Chat 走同一 `ProviderError` 体系。注意 `WithRetry` / `WithMiddlewares` / `NewFallbackProvider` 包装后的 Provider 不透传该接口，需在包装前保留原始句柄
- 新增 `FileIDSystemMessage`：按阿里百炼 qwen-long 约定构造 `fileid://` 文件引用 system 消息，多文件以英文逗号分隔
- 新增文件用途常量：`FilePurposeFileExtract`（Moonshot / 千问 / 智谱文档抽取）、`FilePurposeUserData`、`FilePurposeAssistants`、`FilePurposeBatch`
- 新增 `CapabilityFileUpload` 能力维度：OpenAI / Moonshot / 通义千问 / 智谱 preset 标注平台 Files API 支持，切换平台前可用 `ModelCapabilities.Supports` 预检

### Changed

- OpenAI 兼容路径拒绝消息内 file part 时的错误信息补充指引，指向 Files API 上传引用流程（错误类别仍为 `ErrInvalidRequest`，判断逻辑不受影响）

### Deprecated

### Removed

### Fixed

### Security

## [2.8.0] - 2026-07-15

> 向后兼容的新功能（MINOR）。
> 兼容性说明：`Usage`、`Tool`、`ModelRate`、`ChatResponse`、`StreamChunk` 新增了字段，
> 常规 keyed struct literal 调用方不受影响；使用跨包 unkeyed struct literal 构造
> 这些类型的下游会编译失败，需改为 keyed 写法。

### Added

- 新增厂商原生联网搜索工具透传：`WebSearchTool` / `WebSearchToolWithOptions` 声明后，Anthropic 映射为 `web_search_20250305` server tool、Gemini 映射为 `google_search`（grounding）；不支持的 provider 返回 `ErrInvalidRequest`，不静默丢弃。当前限制：不能与函数工具混用（Anthropic 服务端工具块尚不支持跨轮往返、Gemini 2.5 系平台不支持该组合），Anthropic 侧亦不能与结构化输出组合，均在客户端返回 `ErrInvalidRequest`；Anthropic 返回 `pause_turn` 时报 `ErrUnsupportedCapability` 明确错误
- 新增 `SearchMetadata`：搜索查询、来源、Google Search Suggestions 入口与搜索错误通过 `ChatResponse.Search` 与最终 `StreamChunk.Search` 返回，支撑下游的来源展示与 Gemini 合规要求；平台经 HTTP 200 报告的搜索失败（如 `max_uses_exceeded`）进入 `SearchMetadata.Errors`，不静默丢弃（回复文本级引用区间暂不透出）。Anthropic 原生搜索当前仅保证单轮
- 新增 `PauseTurnError`：Anthropic `stop_reason: "pause_turn"` 以类型化错误返回（`errors.Is` 命中 `ErrUnsupportedCapability`），携带暂停前已产生的 `Usage` 与 `Search`，观测/计费层自动提取用量避免漏账；不可原样重发请求（会二次执行搜索计费）
- `Usage` 新增双口径搜索用量：`WebSearchRequests`（按次数：Anthropic 实际搜索次数 / Gemini 去空去重后的 query 数）与 `WebSearchGroundedPrompts`（按触发 grounding 的请求：Gemini，0/1）；`ModelRate` 新增 `WebSearchPer1K` 与 `GroundedPromptPer1K` 费率，按平台计费规则二选一配置——双配无条件返回 `ErrInvalidPricing`（配置错误），全缺或口径与用量不符返回 `ErrModelNotPriced` 防漏账
- 新增 `PricingTable.Validate()`：启动期整表校验费率范围与搜索双费率互斥，把配置错误挡在计价之前
- Anthropic 原生 provider 实现 `TokenCounter`：`CountTokens` 调用免费的 `/v1/messages/count_tokens` 端点（官方口径为估算值），适配摘要压缩阈值与额度预检
- 新增 `CapabilityFile` 与 `CapabilityWebSearch` 能力维度：Anthropic / Gemini 预设标注文件输入与原生搜索能力，切换平台前可用 `ModelCapabilities.Supports` 预检（OpenAI Chat Completions 路径不支持文件输入，故不标注 File）
- billingstore 参考实现：`usage_record` 流水新增 `web_search_requests` / `web_search_grounded_prompts` 两列（`AutoMigrate` 自动补齐），历史流水可重算与核对搜索费用
- 搜索工具输入约束：重复声明搜索工具、同一 `Tool` 同时设 `Function` 与 `WebSearch`、`ToolChoiceRequired`/`ToolChoiceFunction`/`ToolChoiceNone` 与服务端搜索工具组合、Gemini 搜索叠加 `CandidateCount > 1`，均返回 `ErrInvalidRequest`；纯搜索请求仅接受 `nil`/`ToolChoiceAuto`，Gemini 侧不再下发无意义的 `functionCallingConfig`

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
