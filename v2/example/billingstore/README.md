# billingstore — Redis + GORM 计费存储参考实现

> **定位：reference 级示例，不承诺生产级一致性。**
> 实现 `provider.UsageRecorder`（计量落库）与 `provider.QuotaChecker`（配额拦截）两个接口；
> 接口才是稳定契约，本包是可抄改的起点。独立 go.mod，不污染核心包依赖。

## 架构

- **Redis 热路径**：每次调用原子累计（`HINCRBY`）到两个 HASH——
  `llm:usage:{uid}:total`（累计）与 `llm:usage:{uid}:{yyyymmdd}`（当日，48h TTL），
  字段含 `total_tokens` / `cost_micros` / `calls`。配额预检直接读 Redis，微秒级返回。
- **GORM 流水**：逐次调用明细（`usage_record` 表，含会话、RequestID、缓存/推理分项、
  费用、流终止方式）经内存缓冲**异步批量**刷库，供对账与审计。
- **限额配置**：`user_quota` 表，支持 token 上限与金额上限、`total` / `daily` 两种口径。

## 明确放弃的保证（使用前必读）

- 刷库是 **best-effort**：进程崩溃丢缓冲内流水；刷库失败不重试，仅经 `OnError` 上抛。
- Redis 与 DB **不保证一致**：Redis 是热路径加速，最终对账以 DB 流水聚合为准。
- 配额存在**一次调用的滞后**（先放行后记账），Redis 故障时 **fail-open**。

需要更强保证时的强化方向：流水改走消息队列；Redis 累计改 Lua 脚本合并往返；
配额加本地缓存；按 `Terminated=true` 的记录做漏单补账。

## 用法

```go
store, err := billingstore.New(billingstore.Config{
    Redis:   redisClient,          // redis.Cmdable
    DB:      gormDB,               // *gorm.DB；nil = 只计数不落流水
    Pricing: pricingTable,         // 可选：配置后自动算费用
    OnError: func(err error) { logger.Warn("billing", "err", err) },
})
defer store.Close() // 冲刷缓冲

p := provider.WithObservability(base, provider.ObserveOptions{
    OnEvent: provider.NewBillingHook(store),
})
guarded, _ := provider.TryWithMiddlewares(p, provider.MiddlewareOptions{
    Chat:   []provider.Middleware{provider.QuotaMiddleware(store)},
    Stream: []provider.StreamMiddleware{provider.QuotaStreamMiddleware(store)},
})
```

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
