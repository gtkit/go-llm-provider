# 策略边界与兼容迁移

⚠ v1.11.0 / v2.18.0（2026-10-13）按用户明确授权删除策略 API，并作为一次同主版本不兼容例外发布。模块路径保持不变，旧标签保留；这两个版本不满足通常的 MINOR 向后兼容承诺。旧调用方需按下表迁移后才能编译。

推荐链路为业务 → 单次模型调用 → 协议核心。Eino 调用方使用独立 [einoadapter](einoadapter/README.md)，业务自行编译 Chain。当前 gin_llm_chat 已完成先保留旧唯一计量、再接管业务身份和用量转换的迁移。

## 按职责迁移

| 旧能力 | 调用方应负责 | 核心仍保留 |
| --- | --- | --- |
| 身份 context、BillingHook、内存用量存储 | 认证身份、每次实际调用 ID、幂等写入、独立写入预算及对账 | ObserveEvent、ObserveHook、CombineObserveHooks、Usage、Metadata |
| 价格、费用和额度 | 价格版本快照、并发预留、结算、失败补偿 | 完整原始 Usage 与错误分类 |
| 工具循环、结果加工、自动重试 | 工具授权、副作用、幂等、执行状态机或应用 Agent/Graph | 工具声明、选择、调用增量、结果消息和签名 |
| 提示任务、历史裁剪和摘要 | 产品提示词、权限、历史与存储规则 | 已构造消息的 Chat/ChatStream，供应商 CountTokens |
| 均衡、粘性、失败切换、Registry | 网关路由或应用实例装配、可信历史的固定目标 | Provider 与单实例构造、厂商预设 |
| 相似度、反射 schema、JSON 解码工作流 | 检索计算、schema 生成、输出解码和业务验证 | Embedding/Rerank、ParamSchema、ResponseFormat、协议序列化 |

不应把暂时未使用的策略复制成多个新 module。`retry`、`breaker`、`ratelimit` 保留为显式启用的现有可靠性能力，本次不加入 ChatModel；原生 `Decider` 是独立协议接口，保留。Files、Embedding、Rerank、原生 CountTokens、搜索和签名均不能因为 Chain 未使用而删除。

## 业务计量迁移

应用在每次实际模型调用前生成调用 ID，并把自己的身份和调用状态放入 context；通用观察器读取业务 value，不要求核心认识用户/会话。单流完成事件与重复回调使用同一 ID，存储唯一索引负责同键重放；再次实际调用模型应产生新 ID，不能将同一会话内的不同消耗去重。

保留唯一写入入口。Chat 完成、流完成或创建失败分别记录，成功创建流不记账；FinishReason 后继续读取 Usage 到 EOF，采用最后一个非零完整快照。取消后写入用 `context.WithoutCancel` 保留 value，再设置有限截止；不要把没有截止的旧 hook 原样迁入应用。可运行示例见 [usage_migration_example_test.go](v2/provider/usage_migration_example_test.go)。

gin_llm_chat 已使用业务 `UsageIdentity`、调用前生成的 UUID、单调用 `sync.Once` 和 5s 写入预算。生产代码不再引用核心 `WithUserID`、`WithConversationID`、读取函数、`NewBillingHook`、`UsageRecorder`、`RecordEntry`。新链路通过 direct/Chain 的最终 HTTP、尾帧 Usage、终止方式和单次记录回归。现有业务表没有缓存 TTL 分档与搜索次数列，完整 Usage 的协议保真不等于这些明细已持久化。

已删除的旧账务、预算实现不作为应用参考：应用需自行保证调用 ID 幂等、并发额度预留、搜索及缓存写入费用覆盖，并从同一价格快照取得金额与版本。

## 兼容性与回退

核心版本为 v1.11.0 / v2.18.0，adapter v0.1.0 随后发布并依赖 v2.18.0。显式 Temperature=0 / TopP=0 的 OpenAI 兼容 HTTP 修复包含在 v2.18.0，旧 v2.17.0 不包含该修复。

gin_llm_chat 的 `execution_engine` 默认 direct，可显式选择 eino_chain 后重启；回退改回 direct 并重启即可。旧会话、历史 JSON、工具续接、lease/fence/nonce 和 Finish 规则继续由业务维护。切换不能重放已输出流、自动重新请求模型或同时启用两个计量入口。

## 已删除的公开声明

以下为本次已删除的声明清单，包含类型方法；相关类型字段及接口契约随之删除。混合文件保留协议主体，未整文件移除。

| 文件 | 声明 |
| --- | --- |
| `provider/toolrun.go` | `ToolHandler`, `ToolErrorEncoder`, `ToolResultTransformer`, `ResponseValidator`, `RunToolLoopOptions`, `DefaultToolErrorEncoder`, `WrapToolResultInTag`, `RunToolLoop`, `RunToolLoopWithOptions` |
| `provider/balance.go` | `BalanceStrategy`, `BalanceMember`, `BalanceOptions`, `BalanceMemberStats`, `BalancedProvider`, `NewBalancedProvider`, `NewBalancedProviderWithOptions`, `BalancedProvider.Name`, `BalancedProvider.Chat`, `BalancedProvider.ChatStream`, `BalancedProvider.Stats` |
| `provider/fallback.go` | `FallbackProvider`, `FallbackOptions`, `NewFallbackProvider`, `NewFallbackProviderWithOptions`, `FallbackProvider.Name`, `FallbackProvider.Chat`, `FallbackProvider.ChatStream` |
| `v2/provider/toolrun.go` | `ToolHandler`, `ToolErrorEncoder`, `ToolResultTransformer`, `ResponseValidator`, `ToolSelector`, `RunToolLoopOptions`, `ToolRetryOptions`, `DefaultToolErrorEncoder`, `WrapToolResultInTag`, `RunToolLoop`, `RunToolLoopWithOptions` |
| `v2/provider/balance.go` | `BalanceStrategy`, `BalanceMember`, `BalanceOptions`, `BalanceMemberStats`, `BalancedProvider`, `NewBalancedProvider`, `NewBalancedProviderWithOptions`, `BalancedProvider.Name`, `BalancedProvider.DefaultModel`, `BalancedProvider.Chat`, `BalancedProvider.ChatStream`, `BalancedProvider.Stats` |
| `v2/provider/fallback.go` | `FallbackProvider`, `FallbackOptions`, `NewFallbackProvider`, `NewFallbackProviderWithOptions`, `FallbackProvider.Name`, `FallbackProvider.DefaultModel`, `FallbackProvider.Chat`, `FallbackProvider.ChatStream` |
| `v2/provider/toolrunstream.go` | `RunToolLoopStream`, `RunToolLoopStreamWithOptions` |
| `v2/provider/billing.go` | `NewEntryID`, `WithUserID`, `UserIDFromContext`, `WithConversationID`, `ConversationIDFromContext`, `RecordEntry`, `UsageRecorder`, `NewBillingHook`, `UsageTotals`, `MemoryUsageStore`, `NewMemoryUsageStore`, `MemoryUsageStore.Record`, `MemoryUsageStore.UserTotals`, `MemoryUsageStore.ConversationTotals` |
| `v2/provider/pricing.go` | `ModelRate`, `PricingTable`, `PricingTable.Cost`, `PricingTable.Validate`, `FormatMicros` |
| `v2/provider/pricing_registry.go` | `PricingRegistry`, `NewPricingRegistry`, `PricingRegistry.Replace`, `PricingRegistry.Cost`, `PricingRegistry.Rate`, `PricingRegistry.Version`, `PricingRegistry.Models`, `PricingRegistry.Snapshot` |
| `v2/provider/quota.go` | `QuotaChecker`, `QuotaMiddleware`, `QuotaStreamMiddleware`, `WithTokenBudget`, `TokenBudgetFromContext`, `TokenBudgetMiddleware`, `TokenBudgetStreamMiddleware`, `WithCostBudget`, `CostBudgetFromContext`, `CostBudgetMiddleware`, `CostBudgetStreamMiddleware` |
| `v2/provider/compact.go` | `CompactOptions`, `CompactResult`, `CompactResult.Compacted`, `CompactMessages` |
| `v2/provider/task.go` | `ErrNilTaskSystem`, `PromptTask`, `PromptTask.Run`, `RunPromptTaskJSON` |
| `v2/provider/similarity.go` | `SimilarityResult`, `CosineSimilarity`, `RankBySimilarity`, `MostSimilar` |
| `v2/provider/schema.go` | `SchemaFromType`, `JSONSchemaFormatFor`, `GenerateJSONWithSchema`, `GenerateJSONWithSchemaValidator` |
| `v2/provider/structured.go` | `StructuredValidator`, `GenerateJSON`, `GenerateJSONWithValidator`, `GenerateJSONInto`, `GenerateJSONIntoWithValidator` |
| `v2/provider/tokens.go` | `EstimateTokens`, `TrimMessagesToTokenBudget` |
| `provider/provider.go` | `Registry`, `NewRegistry`, `Registry.Register`, `Registry.SetDefault`, `Registry.Get`, `Registry.Default`, `Registry.Names`, `Registry.RegisterEmbedder`, `Registry.GetEmbedder`, `Registry.DefaultEmbedder`, `Registry.SetDefaultEmbedder`, `Registry.EmbedderNames` |
| `provider/presets.go` | `QuickRegistry`, `QuickRegistryStrict` |
| `v2/provider/provider.go` | `Registry`, `NewRegistry`, `Registry.Register`, `Registry.SetDefault`, `Registry.Get`, `Registry.Default`, `Registry.Names`, `Registry.RegisterEmbedder`, `Registry.GetEmbedder`, `Registry.DefaultEmbedder`, `Registry.SetDefaultEmbedder`, `Registry.EmbedderNames` |
| `v2/provider/presets.go` | `QuickRegistry`, `QuickRegistryStrict` |

关联的渠道常量包括 v1/v2 的 BalanceWeightedRoundRobin、BalanceWeightedRandom、BalanceLeastPending、BalanceSessionAffinity；关联错误包括工具执行的 ErrToolHandlerRequired、ErrToolNotOffered、ErrToolArgumentsInvalid、ErrToolHandlerPanic，渠道策略的 ErrInvalidBalanceStrategy，以及 v2 价格/额度的 ErrModelNotPriced、ErrInvalidPricing、ErrNilPricingRegistry、ErrQuotaExceeded、结构化工作流的 ErrNilStructuredTarget、ErrStructuredDecode、ErrStructuredValidation、相似度的 ErrEmptyEmbeddingVector、ErrZeroEmbeddingVector、ErrEmbeddingDimensionMismatch。上述策略常量和错误均已删除；`ErrInvalidToolChoice` 属于请求协议校验，继续保留。

`CombineObserveHooks` 已从 billing.go 移至 observability.go，签名与顺序调用行为保持一致；该项继续保留。客户端限流所需消息估算改为私有实现，限流行为不变。

策略专属测试、toolloop/toolsecurity/chatbilling 示例及 billingstore、第三方 websearch 示例子模块一并删除，对应 CI、漏洞扫描、发布验证入口同步移除。原生搜索和手动工具续接示例保留。

## 本轮验证结果

- 删除公开声明 177 项：v1 50 项、v2 127 项。保留声明的签名、类型布局、常量值未变；核心生产代码净减少 4,716 行。
- v1/v2/adapter 的 build、vet、lint、race/Examples 和三轮 benchmark 通过；provider 覆盖率 83.1% / 86.8%，adapter 87.3%。lint 使用本地 Go 1.27 构建的 2.14.0。
- 聊天项目在本地 workspace 下全量 race 与 lint 通过，包含 HTTP/SSE、计量、Exact 和工具历史切换回退用例。没有调用真实模型服务。
- `go fix -diff` 仍有原有 Gemini `omitempty` 建议（v1 一处、v2 两处），未改动其 JSON 编码行为。业务正式依赖解析仍待 adapter 发布，已有依赖漏洞与发布版本单独处理；这不代表所有发布门禁已通过。
