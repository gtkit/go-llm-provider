# billingstore — Redis + GORM 计费存储参考实现

> **定位：reference 级示例，不承诺生产级一致性。**
> 实现 `provider.UsageRecorder`（计量落库）与 `provider.QuotaChecker`（配额拦截）两个接口；
> 接口才是稳定契约，本包是可抄改的起点。独立 go.mod，不污染核心包依赖。

## 架构

- **Redis 热路径**：单个 Lua 脚本先按 `EntryID` 幂等去重，再原子累计到两个 HASH——
  `llm:usage:{uid}:total`（累计）与 `llm:usage:{uid}:{yyyymmdd}`（当日，48h TTL），
  字段含 `total_tokens` / `cost_micros` / `calls`。配额预检直接读 Redis。
  累计中途失败（字段被写脏、累加溢出 `int64`）时脚本逆向回滚已应用增量并撤销幂等标记，
  保证"要么全部记账、要么完全不记"，不留"已去重未累计"坏账。
- **GORM 流水**：逐次调用明细（`usage_record` 表，含会话、RequestID、缓存/推理分项、
  费用、流终止方式）经内存缓冲**异步批量**刷库，供对账与审计。
- **限额配置**：`user_quota` 表，支持 token 上限与金额上限、`total` / `daily` 两种口径。

## 明确放弃的保证（使用前必读）

- 刷库是 **best-effort**：进程崩溃丢缓冲内流水；刷库失败不重试，仅经 `OnError` 上抛。
- Redis 与 DB **不保证一致**：Redis 是热路径加速，最终对账以 DB 流水聚合为准。
- 配额存在**一次调用的滞后**（先放行后记账），Redis 故障时 **fail-open**。
- Redis 幂等集合按用户累计**所有** `EntryID`，与累计总量同生命周期、不自动过期。
  内存占用是 **O(用户请求数)**（累计 HASH 仅 O(1)/用户），高频用户会持续增长，
  生产必须结合流水保留策略定期归档（如批量清理"重放安全窗口"之外的记录）。
  不能单独缩短幂等集合生命周期，否则超窗口的旧 `EntryID` 重放会再次增加累计值、造成账目分叉。

需要更强保证时的强化方向（生产化优先级从高到低，均属下游职责，本参考实现不内置）：

1. 流水落库改用持久化 outbox / 消息队列（如 Redis Stream、Kafka）+ 失败重试，替代进程内缓冲，消除崩溃丢流水；
2. 幂等集合按"重放安全窗口"定期归档，约束 Redis 内存增长；
3. 支持从 DB 流水重建 Redis 计数（灾备与漂移校正）；
4. 配额加本地缓存降低 Redis 读放大；按 `Terminated=true` 的记录做漏单补账。

## 用法

```go
store, err := billingstore.New(billingstore.Config{
    Redis:        redisClient,          // redis.Cmdable
    DB:           gormDB,               // *gorm.DB；nil = 只计数不落流水
    Pricing:      pricingTable,         // 可选：配置后自动算费用
    RedisCluster: true,                 // 仅 Redis Cluster 部署开启；同一用户 key 固定到同一 slot
    OnError:      func(err error) { logger.Warn("billing", "err", err) },
})
defer func() { _ = store.Close() }() // 拒绝新记录并冲刷已接纳流水

p := provider.WithObservability(base, provider.ObserveOptions{
    OnEvent: provider.NewBillingHook(store),
})
guarded, _ := provider.TryWithMiddlewares(p, provider.MiddlewareOptions{
    Chat:   []provider.Middleware{provider.QuotaMiddleware(store)},
    Stream: []provider.StreamMiddleware{provider.QuotaStreamMiddleware(store)},
})
```

`RedisCluster` 默认为 `false`，保持单节点既有 key 格式。开启后 key 会增加由用户 ID
摘要生成的 hash tag；已有数据不会自动迁移。`OnError` 可能由调用线程或后台刷库线程调用，
实现必须并发安全并快速返回。

### 用 gtkit 生态装配（生产）

```go
rdb, _ := redisx.NewClient(/* ... */)
redisClient, _ := rdb.GetClient(0)       // 底层 *redis.Client

ormClient, _ := ormx.NewClient(/* ... */)
gormDB := ormClient.DB()                  // 底层 *gorm.DB
```

### 测试装配

本包自身的测试使用 miniredis + 纯 Go sqlite（`glebarez/sqlite`），无 cgo、无真实服务，
可直接参考 `store_test.go`。
