// Package main 演示 go-llm-provider 的 embedding 能力：最小 RAG 检索闭环。
//
// 流程：
//  1. QuickRegistry 同时注册 chat + embedding provider
//  2. EmbedBatch 把 FAQ 文档库向量化（离线索引阶段）
//  3. SimpleEmbed 把用户问题转向量（在线查询阶段）
//  4. 手写余弦相似度，取 Top-1 最相近的 FAQ 条目
//  5. 把匹配到的 FAQ 拼进 system prompt，调 Chat 生成最终回复
//
// 注意：
//   - 余弦相似度是业务层逻辑，本库不内置 —— 几行代码搞定，内置反而限制自由度
//   - 真实 RAG 场景应使用向量数据库（pgvector / Milvus / Qdrant 等）持久化向量；
//     这里为演示简化，直接用内存切片
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/gtkit/go-llm-provider/provider"
)

// FAQ 知识库：生产环境应来自数据库或文件
var faqs = []string{
	"退款政策：支持七天无理由退款，需保持商品完好并提供订单号。",
	"发货时效：现货商品 48 小时内发货，预售商品以详情页标注为准。",
	"会员等级：消费满 1000 元升级银卡，满 5000 元升级金卡，享受额外折扣。",
	"发票开具：下单后 7 天内可在订单详情页申请电子发票，支持企业抬头。",
	"配送范围：全国除港澳台外包邮，偏远地区可能延迟 1-2 天送达。",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	reg := provider.QuickRegistry(map[provider.ProviderName]string{
		provider.ProviderOpenAI:      os.Getenv("OPENAI_API_KEY"),
		provider.ProviderQwen:        os.Getenv("QWEN_API_KEY"),
		provider.ProviderZhipu:       os.Getenv("ZHIPU_API_KEY"),
		provider.ProviderSiliconFlow: os.Getenv("SILICONFLOW_API_KEY"),
		provider.ProviderQianfan:     os.Getenv("QIANFAN_API_KEY"),
	})

	emb, err := reg.DefaultEmbedder()
	if err != nil {
		return fmt.Errorf("至少设置一个支持 embedding 的平台 API Key（OpenAI / Qwen / Zhipu / SiliconFlow / Qianfan）: %w", err)
	}
	chat, err := reg.Default()
	if err != nil {
		return fmt.Errorf("至少设置一个 chat provider 的 API Key: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("Embedder: %s\n", emb.Name())
	fmt.Printf("Chat:     %s\n\n", chat.Name())

	// === 离线索引：FAQ 向量化 ===
	fmt.Println("正在为 FAQ 构建向量索引...")
	docVecs, err := provider.EmbedBatch(ctx, emb, faqs)
	if err != nil {
		return fmt.Errorf("索引 FAQ 失败: %w", err)
	}
	fmt.Printf("已索引 %d 条 FAQ（维度 %d）\n\n", len(docVecs), len(docVecs[0]))

	// === 在线查询 ===
	query := "我的订单什么时候能收到"
	fmt.Printf("用户提问: %s\n", query)

	queryVec, err := provider.SimpleEmbed(ctx, emb, query)
	if err != nil {
		return fmt.Errorf("query 向量化失败: %w", err)
	}

	// 手写余弦相似度，找 Top-1（N < 1000 时暴力扫全量完全可接受）
	bestIdx, bestScore := -1, float32(-1)
	for i, v := range docVecs {
		score := cosineSimilarity(queryVec, v)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	matched := faqs[bestIdx]
	fmt.Printf("匹配到 FAQ（相似度 %.4f）: %s\n\n", bestScore, matched)

	// === 拼进 prompt 让 LLM 生成最终回复 ===
	reply, err := provider.SimpleChatWithSystem(ctx, chat,
		"你是客服助手。基于以下 FAQ 条目回答用户问题，不要编造未提及的信息：\n"+matched,
		query,
	)
	if err != nil {
		return fmt.Errorf("生成回复失败: %w", err)
	}

	fmt.Printf("助手回复: %s\n", reply)
	return nil
}

// cosineSimilarity 计算两个向量的余弦相似度。
// 本库不内置此函数，实现交给调用方；这里仅为示例。
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(normA))*math.Sqrt(float64(normB)))
}
