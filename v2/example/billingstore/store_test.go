package billingstore

import (
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
	store, _, db := newTestStore(t, nil)
	ctx := t.Context()

	entry := testEntry(100)
	entry.EntryID = "fixed-entry-id"
	// 同一 EntryID 重复写入（模拟重放）：流水只落一条。
	require.NoError(t, store.Record(ctx, entry))
	require.NoError(t, store.Record(ctx, entry))

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
