package billingstore

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	provider "github.com/gtkit/go-llm-provider/v2/provider"
)

// newTestDB 构造单连接的内存 sqlite：
// :memory: 库下每个新连接都是全新的空库，必须限制连接池为 1，
// 否则 AutoMigrate 建的表对其他连接不可见（表现为偶发 no such table）。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	return db
}

func newTestStore(t *testing.T, pricing provider.PricingTable) (*Store, *miniredis.Miniredis, *gorm.DB) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	db := newTestDB(t)

	store, err := New(Config{
		Redis:         client,
		DB:            db,
		Pricing:       pricing,
		FlushInterval: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store, mr, db
}

func testPricing() provider.PricingTable {
	return provider.PricingTable{
		"deepseek-chat": {InputPer1M: 2_000_000, OutputPer1M: 8_000_000, Currency: "CNY"},
	}
}

func testEntry(totalTokens int) provider.RecordEntry {
	return provider.RecordEntry{
		UserID:         "u1",
		ConversationID: "c1",
		Provider:       provider.ProviderDeepSeek,
		Model:          "deepseek-chat",
		Usage: provider.Usage{
			PromptTokens:     totalTokens / 2,
			CompletionTokens: totalTokens - totalTokens/2,
			TotalTokens:      totalTokens,
		},
	}
}

func TestStoreRecordAccumulatesAndFlushes(t *testing.T) {
	store, mr, db := newTestStore(t, testPricing())
	ctx := t.Context()

	require.NoError(t, store.Record(ctx, testEntry(1000)))
	require.NoError(t, store.Record(ctx, testEntry(500)))

	// Redis 总量与当日 key 均已累计。
	totalKey := "llm:usage:u1:total"
	dailyKey := "llm:usage:u1:" + time.Now().Format("20060102")
	assert.Equal(t, "1500", mr.HGet(totalKey, fieldTotalTokens))
	assert.Equal(t, "1500", mr.HGet(dailyKey, fieldTotalTokens))
	assert.Equal(t, "2", mr.HGet(totalKey, fieldCalls))
	// 1000 token 半入半出：0.5k*2 + 0.5k*8 = 5000 微元；1500 token 共 7500 微元。
	assert.Equal(t, "7500", mr.HGet(totalKey, fieldCostMicros))
	// 当日 key 带 TTL。
	assert.Positive(t, mr.TTL(dailyKey))

	// 异步流水落库。
	require.Eventually(t, func() bool {
		var count int64
		return db.Model(&UsageRecord{}).Count(&count).Error == nil && count == 2
	}, 2*time.Second, 20*time.Millisecond)

	var record UsageRecord
	require.NoError(t, db.First(&record, "user_id = ?", "u1").Error)
	assert.Equal(t, "deepseek-chat", record.Model)
	assert.Equal(t, "c1", record.ConversationID)
	assert.Equal(t, int64(5000), record.CostMicros)
	assert.Equal(t, "CNY", record.Currency)
}

func TestStoreRecordIdempotentByEntryID(t *testing.T) {
	store, mr, db := newTestStore(t, nil)
	ctx := t.Context()

	entry := testEntry(100)
	entry.EntryID = "fixed-entry-id"
	// 同一 EntryID 重复写入（模拟重放）：Redis 与流水都只计一次。
	require.NoError(t, store.Record(ctx, entry))
	require.NoError(t, store.Record(ctx, entry))
	mr.FastForward(25 * time.Hour)
	require.NoError(t, store.Record(ctx, entry), "幂等不能因时间窗口过期而失效")

	// Redis 幂等：token、调用次数均只累计一次（Lua 脚本整体跳过重放）。
	assert.Equal(t, "100", mr.HGet("llm:usage:u1:total", fieldTotalTokens))
	assert.Equal(t, "1", mr.HGet("llm:usage:u1:total", fieldCalls))

	require.Eventually(t, func() bool {
		var count int64
		return db.Model(&UsageRecord{}).Count(&count).Error == nil && count == 1
	}, 2*time.Second, 20*time.Millisecond)

	var record UsageRecord
	require.NoError(t, db.First(&record).Error)
	assert.Equal(t, "fixed-entry-id", record.EntryID)
}

func TestStoreAllowQuota(t *testing.T) {
	store, _, db := newTestStore(t, testPricing())
	ctx := t.Context()

	t.Run("未配置限额放行", func(t *testing.T) {
		require.NoError(t, store.Allow(ctx, "u1", "deepseek-chat"))
	})

	require.NoError(t, db.Create(&UserQuota{
		UserID: "u1", TokenLimit: 1200, Period: QuotaPeriodTotal,
	}).Error)

	t.Run("未超限放行", func(t *testing.T) {
		require.NoError(t, store.Record(ctx, testEntry(1000)))
		require.NoError(t, store.Allow(ctx, "u1", "deepseek-chat"))
	})

	t.Run("超限拦截并返回 ErrQuotaExceeded", func(t *testing.T) {
		require.NoError(t, store.Record(ctx, testEntry(500))) // 累计 1500 > 1200
		err := store.Allow(ctx, "u1", "deepseek-chat")
		require.ErrorIs(t, err, provider.ErrQuotaExceeded)
	})

	t.Run("金额限额独立生效", func(t *testing.T) {
		require.NoError(t, db.Create(&UserQuota{
			UserID: "u2", CostLimitMicros: 1000, Period: QuotaPeriodTotal,
		}).Error)
		entry := testEntry(1000)
		entry.UserID = "u2"
		require.NoError(t, store.Record(ctx, entry)) // 5000 微元 > 1000
		err := store.Allow(ctx, "u2", "deepseek-chat")
		require.ErrorIs(t, err, provider.ErrQuotaExceeded)
	})
}

func TestStoreAllowFailOpenOnRedisDown(t *testing.T) {
	var reported []error
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	db := newTestDB(t)

	store, err := New(Config{
		Redis:   client,
		DB:      db,
		OnError: func(e error) { reported = append(reported, e) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	require.NoError(t, db.Create(&UserQuota{UserID: "u1", TokenLimit: 1}).Error)
	mr.Close() // 模拟 Redis 故障

	// fail-open：放行且通过 OnError 上抛。
	require.NoError(t, store.Allow(t.Context(), "u1", ""))
	assert.NotEmpty(t, reported)
}

func TestStoreRecordWithoutDB(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	store, err := New(Config{Redis: client})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	require.NoError(t, store.Record(t.Context(), testEntry(100)))
	assert.Equal(t, "100", mr.HGet("llm:usage:u1:total", fieldTotalTokens))
	// DB 为 nil：限额视为不限。
	require.NoError(t, store.Allow(t.Context(), "u1", ""))
}

func TestStoreCloseIsIdempotent(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	store, err := New(Config{Redis: client})
	require.NoError(t, err)

	require.NoError(t, store.Close())
	require.NoError(t, store.Close(), "重复 Close 不应 panic")

	// 关闭后 Record 显式拒绝，不静默丢失。
	err = store.Record(t.Context(), testEntry(10))
	require.ErrorIs(t, err, ErrStoreClosed)
}

func TestStoreRecordErrorCallbackCanClose(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	var store *Store
	var err error
	store, err = New(Config{
		Redis: client,
		Pricing: provider.PricingTable{
			"deepseek-chat": {InputPer1M: -1},
		},
		OnError: func(error) { _ = store.Close() },
	})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- store.Record(t.Context(), testEntry(10)) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Record 与 OnError 触发的 Close 发生死锁")
	}
	require.ErrorIs(t, store.Record(t.Context(), testEntry(10)), ErrStoreClosed)
}

func TestStoreConcurrentRecordAndCloseDoesNotLoseAcceptedRecords(t *testing.T) {
	store, _, db := newTestStore(t, nil)

	var accepted atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, 100)
	start := make(chan struct{})
	for i := range 100 {
		wg.Go(func() {
			<-start
			entry := testEntry(1)
			entry.EntryID = fmt.Sprintf("concurrent-%d", i)
			if err := store.Record(t.Context(), entry); err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrStoreClosed) {
				errCh <- err
			}
		})
	}
	close(start)
	closeDone := make(chan struct{})
	go func() {
		_ = store.Close()
		close(closeDone)
	}()
	wg.Wait()
	close(errCh)
	<-closeDone
	for err := range errCh {
		require.NoError(t, err)
	}

	var count int64
	require.NoError(t, db.Model(&UsageRecord{}).Count(&count).Error)
	assert.Equal(t, accepted.Load(), count)
}

func TestStoreClusterKeysShareHashTag(t *testing.T) {
	store := &Store{cfg: Config{KeyPrefix: defaultKeyPrefix, RedisCluster: true}}
	now := time.Now()
	keys := []string{
		store.entrySetKey("u1"),
		store.usageKey("u1", QuotaPeriodTotal, now),
		store.usageKey("u1", QuotaPeriodDaily, now),
	}
	tag := redisHashTag(keys[0])
	require.NotEmpty(t, tag)
	for _, key := range keys[1:] {
		assert.Equal(t, tag, redisHashTag(key))
	}
}

func TestStoreAllowReportsCorruptRedisValue(t *testing.T) {
	var reported []error
	store, mr, db := newTestStore(t, nil)
	store.cfg.OnError = func(err error) { reported = append(reported, err) }
	require.NoError(t, db.Create(&UserQuota{UserID: "u1", TokenLimit: 1}).Error)
	mr.HSet(store.usageKey("u1", QuotaPeriodTotal, time.Now()), fieldTotalTokens, "broken")

	require.NoError(t, store.Allow(t.Context(), "u1", ""))
	require.NotEmpty(t, reported)
}

func redisHashTag(key string) string {
	start := strings.IndexByte(key, '{')
	end := strings.IndexByte(key, '}')
	if start < 0 || end <= start+1 {
		return ""
	}
	return key[start+1 : end]
}

// errSink 并发安全地收集 OnError 上报的错误，供故障注入测试断言。
type errSink struct {
	mu   sync.Mutex
	errs []error
}

func (s *errSink) onError(err error) {
	s.mu.Lock()
	s.errs = append(s.errs, err)
	s.mu.Unlock()
}

func (s *errSink) all() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.errs...)
}

// newFaultStore 构造一个可注入 Redis 故障的 Store（不落 DB，聚焦 Redis 累计路径），
// 额外返回一个独立客户端用于直接观察 Redis 状态。
func newFaultStore(t *testing.T, pricing provider.PricingTable) (*Store, *miniredis.Miniredis, *redis.Client, *errSink) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	inspect := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = inspect.Close() })

	sink := &errSink{}
	store, err := New(Config{
		Redis:         client,
		Pricing:       pricing,
		FlushInterval: 20 * time.Millisecond,
		OnError:       sink.onError,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store, mr, inspect, sink
}

// TestStoreRecordWrongTypeIdempotencySetIsAtomic 验证幂等集合 key 类型错误时，
// SADD 在任何累计前中止，不留下部分账目。
func TestStoreRecordWrongTypeIdempotencySetIsAtomic(t *testing.T) {
	store, mr, inspect, sink := newFaultStore(t, nil)
	ctx := t.Context()

	// 幂等集合 key 被写成 string：SADD 触发 WRONGTYPE。
	require.NoError(t, mr.Set(store.entrySetKey("u1"), "corrupt"))

	entry := testEntry(100)
	entry.EntryID = "wrongtype-entry"
	require.NoError(t, store.Record(ctx, entry), "错误应经 OnError 上抛，不影响主请求")

	require.NotEmpty(t, sink.all(), "WRONGTYPE 必须经 OnError 上抛")
	assert.Empty(t, inspect.HGetAll(ctx, store.usageKey("u1", QuotaPeriodTotal, time.Now())).Val(),
		"SADD 中止后累计从未发生")
}

// TestStoreRecordCorruptCounterFieldRollsBackClaim 验证累计字段被脏数据污染导致
// HINCRBY 失败时，脚本回滚并撤销幂等标记，避免"已去重未累计"坏账。
func TestStoreRecordCorruptCounterFieldRollsBackClaim(t *testing.T) {
	store, mr, inspect, sink := newFaultStore(t, nil)
	ctx := t.Context()

	totalKey := store.usageKey("u1", QuotaPeriodTotal, time.Now())
	mr.HSet(totalKey, fieldTotalTokens, "garbage") // 非整数，首个 HINCRBY 失败

	entry := testEntry(100)
	entry.EntryID = "corrupt-field-entry"
	require.NoError(t, store.Record(ctx, entry))

	require.NotEmpty(t, sink.all(), "脏字段导致的累计失败必须上抛")

	isMember, err := inspect.SIsMember(ctx, store.entrySetKey("u1"), entry.EntryID).Result()
	require.NoError(t, err)
	assert.False(t, isMember, "累计失败后必须撤销幂等标记，修复后可重新记账")
	assert.Equal(t, "garbage", mr.HGet(totalKey, fieldTotalTokens), "失败字段不得被改写")
	assert.Empty(t, mr.HGet(totalKey, fieldCalls), "后续字段不得被部分累计")
}

// TestStoreRecordMidwayFailureReversesAppliedIncrements 验证累计中途失败时，
// 已成功应用的增量被逆向回滚、幂等标记被撤销（脚本回滚循环的核心路径）。
//
// 触发方式：污染第二个累计字段 cost_micros，使第一个字段 total_tokens 先累计成功、
// cost_micros 的 HINCRBY 再失败。int64 累加溢出会触发同一条 HINCRBY 报错→逆向回滚
// 路径，此处用脏字段注入模拟该失败（miniredis 不模拟 HINCRBY 溢出报错）。
func TestStoreRecordMidwayFailureReversesAppliedIncrements(t *testing.T) {
	store, mr, inspect, sink := newFaultStore(t, nil)
	ctx := t.Context()

	totalKey := store.usageKey("u1", QuotaPeriodTotal, time.Now())
	mr.HSet(totalKey, fieldCostMicros, "garbage") // 第二个字段非整数，其 HINCRBY 失败

	entry := testEntry(100)
	entry.EntryID = "midway-fail-entry"
	require.NoError(t, store.Record(ctx, entry))

	require.NotEmpty(t, sink.all(), "中途失败必须上抛")

	got := mr.HGet(totalKey, fieldTotalTokens)
	assert.True(t, got == "" || got == "0", "先累计成功的 total_tokens 必须被回滚为 0，实际 %q", got)
	assert.Equal(t, "garbage", mr.HGet(totalKey, fieldCostMicros), "失败字段不得被改写")

	isMember, err := inspect.SIsMember(ctx, store.entrySetKey("u1"), entry.EntryID).Result()
	require.NoError(t, err)
	assert.False(t, isMember, "回滚后必须撤销幂等标记")
}

// TestStoreRecordRejectsNegativeUsage 验证负 token 增量被 Go 侧拒绝，
// 不会倒扣 Redis 累计值。
func TestStoreRecordRejectsNegativeUsage(t *testing.T) {
	store, _, inspect, sink := newFaultStore(t, nil)
	ctx := t.Context()

	entry := testEntry(0)
	entry.EntryID = "negative-entry"
	entry.Usage.TotalTokens = -100

	require.NoError(t, store.Record(ctx, entry))
	require.NotEmpty(t, sink.all(), "负增量必须被拒绝并上抛")
	assert.Empty(t, inspect.HGetAll(ctx, store.usageKey("u1", QuotaPeriodTotal, time.Now())).Val(),
		"负增量不得写入 Redis 累计值")
}
