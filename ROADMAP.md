# 排期与待验证清单

本文件记录两类事项：**待真实 key 验证的协议细节**（代码已按官方文档实现、单测已覆盖，但未用真实平台跑过）和**已评估待排期的功能**。完成一项就把它从这里移走并记入 CHANGELOG。

## 一、待真实 key 验证（v2）

每条都有对应的 smoke 用例（[`v2/provider/verification_smoke_test.go`](v2/provider/verification_smoke_test.go)），设置环境变量即运行、缺 key 自动跳过：

```bash
cd v2 && ANTHROPIC_API_KEY=... GEMINI_API_KEY=... DEEPSEEK_API_KEY=... ZHIPU_API_KEY=... MOONSHOT_API_KEY=... OPENAI_API_KEY=... \
  go test ./provider -run 'TestVerify' -count=1 -v
```

| 编号 | 平台 | 验证点 | 用例 | 若失败的处置 |
|---|---|---|---|---|
| V-01 | Anthropic | `JSONObjectFormat()` 下发的 `output_config.format` 使用无属性 schema `{"type":"object"}`，平台是否接受 | `TestVerifyAnthropicJSONObjectFormatSmoke` | 改为在 `anthropicOutputConfigParam` 里对 JSONObject 生成带 `additionalProperties: true` 或 `properties: {}` 的 schema |
| V-02 | Gemini 2.5 系 | `responseJsonSchema` 是否被 2.5 系接受（3.x 已确认） | `TestVerifyGemini25ResponseJSONSchemaSmoke` | 按模型前缀回退到 `responseSchema`（OpenAPI 子集，需剔除 `additionalProperties`） |
| V-03 | DeepSeek V4 | 顶层 `thinking.type` + `reasoning_effort` 生效；带 tools 的多轮回传 `reasoning_content` 不再 400 | `TestVerifyDeepSeekThinkingToolLoopSmoke` | 核对 `compat_thinking.go` 字段名与 `buildOpenAIMessage` 的 `reasoning_content` 下发条件 |
| V-04 | 智谱 GLM-5.3 / Kimi K2.6 | assistant 消息携带 `reasoning_content` 是否被接受；GLM 的 `reasoning_effort` 档位是否生效 | `TestVerifyZhipuReasoningEchoSmoke` / `TestVerifyMoonshotReasoningEchoSmoke` | 若某平台拒绝该字段，把它从 `thinkingSupportByProvider` 的 `reasoningEcho` 中移除 |
| V-05 | Claude 5 | `thinking.type = adaptive` + `output_config.effort` 组合；开启思考的工具循环回放带签名 `thinking` 块 | `TestVerifyAnthropicAdaptiveThinkingToolLoopSmoke` | 核对 `anthropicThinkingParam` / `anthropicThinkingParts` 的块顺序与字段 |
| V-06 | OpenAI Responses API | `store: false` 下以 `id` + `encrypted_content` 回放 `reasoning` 输入项；`include` 是否返回加密内容 | `TestVerifyResponsesReasoningReplaySmoke` | 核对 `responsesAssistantItems` 的 reasoning 项形态（是否需要 `summary` 非空、`id` 是否可省） |

以上验证不阻塞现有单测与 CI；它们验证的是"平台是否按文档行为"，不是库内逻辑。

## 二、已评估、待排期的功能

按建议顺序排列。三项都在"对接大模型并交互"的定位内，但工作量或验证条件不满足，本轮未做。

### R-01 Anthropic 服务端 compaction 类型化

- **现状**：可通过 `NativeProviderConfig.Headers` 附加 `anthropic-beta: compact-2026-01-12`，但请求体的 `context_management` 字段没有入口，响应中的 `compaction` 内容块也未建模。
- **要做**：`ChatRequest` 增加平台侧上下文管理选项（压缩阈值），Anthropic 映射为 `context_management.edits[{type: compact_20260112, compact_threshold}]` 并自动附加 beta 头；响应的 `compaction` 块需随 `AssistantMessage()` 原样回放（平台收到该块后忽略它之前的所有内容）。
- **阻塞点**：`compaction` 块的回放契约需对照官方文档逐字核实，离线不做。
- **验证**：需 Anthropic key，长对话触发压缩后继续多轮。

### R-02 音视频 `ContentPart`

- **现状**：Gemini 经 `FileDataPart(bytes, "audio/wav", ...)` 已能传任意 MIME 的音视频（映射为 `inline_data`）；OpenAI Chat Completions 的 `input_audio`、Qwen-Omni 的音视频输入、Responses API 的 `input_audio` 未映射，传入会返回 `ErrInvalidRequest`。
- **要做**：新增 `ContentTypeAudio` / `ContentTypeVideo` 与构造器，按平台映射：OpenAI 兼容 → `input_audio{data, format}`，Gemini → `inline_data`，Anthropic → 拒绝。
- **优先级**：低。与 Agent / RAG 主线关系弱，有具体场景再做。

### R-03 Batch API

- **现状**：无。离线建索引、批量评测目前只能用在线端点，成本是 Batch 的两倍。
- **要做**：OpenAI / Anthropic / 百炼的 Batch 端点（提交 JSONL 作业、轮询状态、取回结果文件），需要一套异步任务类型（`BatchJob`、状态机、结果解析），与现有同步 `Provider` 接口并列。
- **优先级**：中。单独排一个版本做，不与同步路径混在一起。

## 三、不做的事（定位之外）

向量库对接、文档切片、检索链路编排、Agent 框架、MCP 客户端、提示词模板引擎。这些留给编排层（如 Eino），库只负责与 LLM 平台的交互。
