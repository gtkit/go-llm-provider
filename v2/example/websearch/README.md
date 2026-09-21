# websearch — 给 Agent 接联网搜索的参考实现

> **定位：reference 级示例，独立 `go.mod`，不污染核心包依赖。**
> 核心库只负责与大模型平台交互，搜索服务属于应用层选型，因此不进 `provider` 包。
> 本模块演示推荐接法，可整体复制到你的项目里改。

## 为什么需要它

Anthropic、Gemini、OpenAI Responses 三条原生路径，以及智谱与百炼两个 OpenAI 兼容平台，已经有平台内建搜索，一行 `provider.WebSearchTool()` 就开，服务端执行，不需要本模块。

但 DeepSeek、Kimi、火山方舟、千帆等平台核心库没有做内建搜索映射（原因与进展见仓库 ROADMAP），联网走**普通函数工具 + `RunToolLoop`**：模型决定搜什么，你的代码去搜，结果回传模型。本模块就是这条路径的完整实现。

即使平台有内建搜索，想自己控制搜索服务商、结果清洗与注入防护时，也可以改用本模块这条路径。

## 分层

```
SearchClient 接口（应用层契约，本模块定义）
  └─ BochaClient（博查实现；换 Serper / Tavily 只需另写一个实现）
       └─ Toolset：包装成 provider.Tool + provider.ToolHandler
            └─ provider.RunToolLoop + 三道提示注入防线
```

换搜索服务商只改一处：传给 `NewToolset` 的 `SearchClient`。工具定义、防护策略、工具循环、引用校验都不用动。

## 为什么默认选博查

对比过的几家里，[博查](https://open.bochaai.com/)在国内直连环境下最稳：响应结构兼容 Bing Search，对中文内容、国内资讯与政策做过优化，`summary` 参数能直接返回长文本摘要（而不是几十字的 snippet），还配套 Reranker API 可接本库的 `provider.Reranker`。

海外场景可换 Tavily（返回已清洗内容，省 token）或 Serper（原始 SERP，单价低但要自己清洗，token 成本更高）。注意成本要合并算：原始 SERP 单价便宜，但把噪音塞给模型，输入 token 会把差价吃回去。

## 运行 demo

```bash
export BOCHA_API_KEY="sk-..."      # https://open.bochaai.com/api-keys
export DEEPSEEK_API_KEY="sk-..."   # 或 QWEN_API_KEY / ZHIPU_API_KEY / MOONSHOT_API_KEY
go run ./cmd/demo -q "2026 年 Go 语言有哪些新特性"
```

输出是带 `[ref:N]` 引用标注的回答，后面附来源列表与 token 用量。全部单测不需要任何 key（用 `httptest` 打桩）：

```bash
go test -race ./...
```

## 三道提示注入防线

搜索结果是彻头彻尾的外部不可信内容：攻击者只要让一个页面被搜到，就能把指令送进你的 prompt。本模块把三道防线接在固定位置，规则内容可按业务替换。

| 防线 | 位置 | 做什么 |
|---|---|---|
| 内容净化 | `Toolset.Handler` 内，调 `SanitizeText` | 命中可疑指令特征词整段替换为占位符并回调 `OnSuspicious` 审计；否则转义 `---`、`##` 等结构符，防止伪造 prompt 分节 |
| 结构隔离 | `LoopOptions` 的 `ToolResultTransformer` | 把结果包进 `<search_results>` 并转义内部尖括号，防止结果自带同名标签提前闭合让注入逃逸 |
| 语义声明 + 引用校验 | `SystemPrompt` 与 `HasFabricatedRef` | 提示词里显式声明标签内是数据不是指令；回答后校验 `[ref:N]` 指向真实存在的来源，拦住编造出处 |

前两道是硬约束，第三道的提示词部分是软约束，必须叠加使用。更完整的规则模板（更长的特征词表、已验证的攻击载荷与正常文本用例）见仓库根目录的 [PROMPT_INJECTION_DEFENSE.md](../../../PROMPT_INJECTION_DEFENSE.md)。

## 成本与稳定性控制

- `MaxResults`（默认 5）与 `MaxTextLen`（默认 600 字符）直接决定这一轮的输入 token。模型自己给的 `limit` 会被裁到 `MaxResults` 以内，避免外部输入放大账单。
- `Search` 的整体超时交给 `ctx`，HTTP 客户端只设传输层超时，不设 `Client.Timeout`。
- `BochaError.Retryable()` 把限流、5xx 与网络抖动判为可重试，鉴权失败和参数错误判为不可重试；`LoopOptions` 已接上 `ToolRetry`，指数退避加抖动重试三次。
- `Toolset.Sources()` 返回本轮实际交给模型的来源，用于展示出处或校验引用。一次会话用一个 `Toolset`，跨会话复用前调 `Reset()`。

## 换一个搜索服务商

实现 `SearchClient` 即可，其余不动：

```go
type tavilyClient struct{ /* ... */ }

func (c *tavilyClient) Search(ctx context.Context, query string, limit int) ([]websearch.SearchHit, error) {
    // 调 Tavily，把结果映射成 []websearch.SearchHit
}

toolset, _ := websearch.NewToolset(&tavilyClient{...}, websearch.ToolsetOptions{})
```

临时适配或测试可以直接用 `websearch.SearchFunc` 把普通函数变成 `SearchClient`。

## 明确不做的事

向量库对接、文档切片、多路召回、检索链路编排。这些属于编排层（如 Eino）或你的业务代码，不在本模块也不在核心库的定位内。
