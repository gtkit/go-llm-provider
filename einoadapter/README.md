# Eino adapter

独立模块 `github.com/gtkit/go-llm-provider/einoadapter`，面向 Go 1.27、Eino **v0.9.21** 和 `go-llm-provider/v2`。实现 `model.ToolCallingChatModel`，一次 Generate/Stream 只调用一次底层 Provider。核心模块不依赖 Eino。

当前为未发布的 v0.x 开发模块。仓库联调使用未入库的 Go workspace 引用 `v2/`、`einoadapter/` 和业务模块；不要在发布的 go.mod 中加入本机绝对路径 replace。模块声明的核心兼容基线是 v2.17.0；显式零温度/TopP 的最终 HTTP 行为需要本次核心修复，联调必须使用本地 v2。

```go
chatModel, err := einoadapter.New(p) // p 是已经创建的 provider.Provider
if err != nil { return err }
messages, options, err := einoadapter.FromRequest(request)
if err != nil { return err }
message, err := chatModel.Generate(ctx, messages, options...)
if err != nil { return err }
response, err := einoadapter.ToResponse(message)
```

可运行用法见 [Example](example_test.go)。业务 Chain 编译一次，通过 `compose.WithChatModelOption(options...).DesignateNode("model")` 传递每次请求的选项。覆盖顺序为绑定工具 → FromRequest 请求快照 → Eino common options；显式空 Tools 会清空绑定工具，nil 与零值采样参数保持区别。WithTools 返回独立实例，不修改原模型。

## 流生命周期

每次 Stream 使用自己的 `context.WithCancel` 和 `NewStreamControl(cancel)`，通过 `WithStreamControl` 传入。底层 Provider 必须支持单消费者 Recv 与并发 Close，并让 Close 解除阻塞 Recv。

1. Stream 创建后第一帧只含 opening Metadata，用 `OpeningMetadata` 消费；不能转成业务内容或重置首 token 计时。
2. 后续帧用 `ToChunk` 转换。FinishReason 后继续读取到 EOF，以取得收尾 Usage。
3. 无论正常、失败或提前退出，先调用 Eino reader.Close，再检查 control.Close 的错误。control 可重复或并发关闭，返回同一底层关闭结果，并等待 pump 退出。

桥接仅使用容量为 1 的管道。取消同时解除发送与上游读取阻塞；正常 EOF 保持 EOF，底层关闭错误通过 control.Close 返回。Eino v0.9.21 的 `SetAutomaticClose` 在发布 reader 前启用，使内部取消与 Eino 自身的关闭共用原子关闭路径。此行为已经过 race 测试，升级 Eino 时须重新验证。

## 数据与支持边界

原生字段承载文本、Reasoning、工具及常用 token 数；`Extra["go_llm_provider_events_v1"]` 使用带版本的 JSON Lines 字符串补充完整 Usage、模型、白名单 Metadata、签名、Parts、Search 及需回放的输入内容。每行一个完整事件并以换行结尾，可经 Eino concat 和 JSON 往返。文本 delta 不再在扩展中复制。用量取最后一个非零完整快照，不累加，不采用 Eino concat 的逐字段最大值。

支持文本、图片和文件输入及 Provider 自有字段。不支持 Eino 音频/视频、已弃用 MultiContent、生成模态原生结构、LogProbs、DeferredTools、ToolSearchTool、AgenticToolChoice；遇到无法表达的字段返回包含字段名的 `provider.ErrInvalidRequest`。多候选请求拒绝，CandidateCount 只能是 0 或 1。未知扩展、错误版本和损坏事件均拒绝。Eino 会忽略其他组件的私有 option，因此只应传 common options 和本模块提供的 options。

构造不启动 goroutine、不发请求。模型并发安全取决于底层 Provider；每个 reader 仅允许一个消费者。没有自动工具执行、重试、模型切换、身份或用量落库。`Name` 等业务 Provider facade 由调用方装配。

## 验证

在本模块中运行 `go test -race ./...`、`go vet ./...` 和仓库 lint。`GOWORK=off go test ./...` 验证已发布核心基线的独立编译；workspace 模式验证本次核心修复。Benchmark 使用 `go test -run '^$' -bench=. -benchmem`。

2026-10-13 本地验证：adapter 覆盖率 87.3%，build/vet/lint/race/Examples 与扩展 concat/JSON 回放通过。业务 1000×1KiB、10 并发的固定 fake 在用户确认的 1ms/帧下完整耗时增加 2.02%、首帧 P95 增加 1.261ms；零延迟完整耗时增加 291.68%（绝对 +3.506ms）。后者不满足 5% 预算，不能将前者结果推广为所有负载的性能承诺。
