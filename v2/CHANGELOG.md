# Changelog

本项目变更记录遵循 [Keep a Changelog 1.1.0](https://keepachangelog.com/zh-CN/1.1.0/) 规范，并严格使用 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Changed

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
