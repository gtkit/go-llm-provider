package billingstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	provider "github.com/gtkit/go-llm-provider/v2/provider"
)

const (
	defaultKeyPrefix     = "llm:usage"
	defaultFlushInterval = 5 * time.Second
	defaultBufferSize    = 256
	defaultFlushBatch    = 64
	dailyKeyTTL          = 48 * time.Hour

	fieldTotalTokens = "total_tokens"
	fieldCostMicros  = "cost_micros"
	fieldCalls       = "calls"
)

// ErrBufferFull 表示流水缓冲已满、本条记录被丢弃（Redis 计数不受影响）。
var ErrBufferFull = errors.New("billingstore: usage record buffer full, record dropped")

// Config 配置 Store。Redis 为必填；其余均有默认值。
type Config struct {
	// Redis 承担热路径的原子累计计数。生产可传 gtkit/redisx 的
	// Client.GetClient(db) 返回的 *redis.Client，测试可传 miniredis 客户端。
	Redis redis.Cmdable

	// DB 为流水与限额存储；nil 时只做 Redis 计数，不落流水、限额视为不限。
	// 生产可传 gtkit/ormx 的 Client.DB()。
	DB *gorm.DB

	// Pricing 可选：配置后按 RecordEntry 的 model 计算费用（微元）并随流水累计。
	// 未配价的 model 费用记 0 并通过 OnError 上抛 ErrModelNotPriced。
	Pricing provider.PricingTable

	// KeyPrefix 是 Redis key 前缀，默认 "llm:usage"。
	KeyPrefix string

	// FlushInterval 是流水批量刷库周期，默认 5s。
	FlushInterval time.Duration

	// BufferSize 是待刷库流水的缓冲容量，默认 256；写满时丢弃并上抛 ErrBufferFull。
	BufferSize int

	// OnError 接收后台及旁路错误（刷库失败、Redis 累计失败、未配价等）。
	// nil 时静默丢弃。回调必须快速返回且并发安全。
	OnError func(error)
}

// Store 是 provider.UsageRecorder 与 provider.QuotaChecker 的 Redis+GORM 参考实现。
// 通过 New 构造，用完调用 Close 冲刷缓冲。
type Store struct {
	cfg     Config
	pending chan UsageRecord
	done    chan struct{}
	stopped chan struct{}
}

var (
	_ provider.UsageRecorder = (*Store)(nil)
	_ provider.QuotaChecker  = (*Store)(nil)
)

// New 构造 Store 并启动后台刷库循环（cfg.DB 为 nil 时不落流水）。
func New(cfg Config) (*Store, error) {
	if cfg.Redis == nil {
		return nil, errors.New("billingstore: redis client is required")
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = defaultKeyPrefix
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultFlushInterval
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = defaultBufferSize
	}
	if cfg.DB != nil {
		if err := cfg.DB.AutoMigrate(&UsageRecord{}, &UserQuota{}); err != nil {
			return nil, fmt.Errorf("billingstore: auto migrate: %w", err)
		}
	}

	s := &Store{
		cfg:     cfg,
		pending: make(chan UsageRecord, cfg.BufferSize),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go s.flushLoop()
	return s, nil
}

// Record 实现 provider.UsageRecorder：Redis 原子累计（总量 + 当日），
// 流水投入缓冲异步刷库。错误一律通过 OnError 上抛，不影响主请求。
func (s *Store) Record(ctx context.Context, entry provider.RecordEntry) error {
	if entry.UserID == "" {
		return nil
	}

	costMicros := s.entryCost(entry)
	s.incrRedis(ctx, entry, costMicros)

	if s.cfg.DB == nil {
		return nil
	}
	record := UsageRecord{
		UserID:           entry.UserID,
		ConversationID:   entry.ConversationID,
		RequestID:        entry.RequestID,
		Provider:         string(entry.Provider),
		Model:            entry.Model,
		PromptTokens:     entry.Usage.PromptTokens,
		CompletionTokens: entry.Usage.CompletionTokens,
		ReasoningTokens:  entry.Usage.ReasoningTokens,
		CacheReadTokens:  entry.Usage.CacheReadTokens,
		CacheWriteTokens: entry.Usage.CacheWriteTokens,
		TotalTokens:      entry.Usage.TotalTokens,
		CostMicros:       costMicros,
		Currency:         s.entryCurrency(entry),
		Streaming:        entry.Streaming,
		Terminated:       entry.Terminated,
		TerminateReason:  string(entry.TerminateReason),
		ErrorCode:        string(entry.ErrorCode),
		CreatedAt:        time.Now(),
	}
	select {
	case s.pending <- record:
	default:
		s.reportError(ErrBufferFull)
	}
	return nil
}

// Allow 实现 provider.QuotaChecker：读取用户限额与 Redis 累计值比较，
// 超限返回包装的 provider.ErrQuotaExceeded。存储故障 fail-open（放行并上抛 OnError）。
func (s *Store) Allow(ctx context.Context, userID, _ string) error {
	if userID == "" || s.cfg.DB == nil {
		return nil
	}

	var quota UserQuota
	err := s.cfg.DB.WithContext(ctx).First(&quota, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // 未配置限额 = 不限
	}
	if err != nil {
		s.reportError(fmt.Errorf("billingstore: load quota: %w", err))
		return nil // fail-open
	}
	if quota.TokenLimit <= 0 && quota.CostLimitMicros <= 0 {
		return nil
	}

	key := s.usageKey(userID, quota.Period, time.Now())
	values, err := s.cfg.Redis.HMGet(ctx, key, fieldTotalTokens, fieldCostMicros).Result()
	if err != nil {
		s.reportError(fmt.Errorf("billingstore: read usage: %w", err))
		return nil // fail-open
	}
	usedTokens := redisInt(values[0])
	usedMicros := redisInt(values[1])

	if quota.TokenLimit > 0 && usedTokens >= quota.TokenLimit {
		return fmt.Errorf("%w: user %s used %d tokens (limit %d, period %s)",
			provider.ErrQuotaExceeded, userID, usedTokens, quota.TokenLimit, quota.Period)
	}
	if quota.CostLimitMicros > 0 && usedMicros >= quota.CostLimitMicros {
		return fmt.Errorf("%w: user %s spent %s (limit %s, period %s)",
			provider.ErrQuotaExceeded, userID,
			provider.FormatMicros(usedMicros), provider.FormatMicros(quota.CostLimitMicros), quota.Period)
	}
	return nil
}

// Close 停止后台循环并冲刷剩余流水。
func (s *Store) Close() error {
	close(s.done)
	<-s.stopped
	return nil
}

func (s *Store) entryCost(entry provider.RecordEntry) int64 {
	if len(s.cfg.Pricing) == 0 || entry.Model == "" {
		return 0
	}
	micros, _, err := s.cfg.Pricing.Cost(entry.Model, entry.Usage)
	if err != nil {
		s.reportError(err)
		return 0
	}
	return micros
}

func (s *Store) entryCurrency(entry provider.RecordEntry) string {
	if rate, ok := s.cfg.Pricing[entry.Model]; ok {
		return rate.Currency
	}
	return ""
}

// incrRedis 对总量与当日两个 HASH 做原子累计。
func (s *Store) incrRedis(ctx context.Context, entry provider.RecordEntry, costMicros int64) {
	now := time.Now()
	for _, key := range []string{
		s.usageKey(entry.UserID, QuotaPeriodTotal, now),
		s.usageKey(entry.UserID, QuotaPeriodDaily, now),
	} {
		if err := s.incrKey(ctx, key, entry, costMicros); err != nil {
			s.reportError(fmt.Errorf("billingstore: incr %s: %w", key, err))
		}
	}
	if err := s.cfg.Redis.Expire(ctx, s.usageKey(entry.UserID, QuotaPeriodDaily, now), dailyKeyTTL).Err(); err != nil {
		s.reportError(fmt.Errorf("billingstore: expire daily key: %w", err))
	}
}

func (s *Store) incrKey(ctx context.Context, key string, entry provider.RecordEntry, costMicros int64) error {
	if err := s.cfg.Redis.HIncrBy(ctx, key, fieldTotalTokens, int64(entry.Usage.TotalTokens)).Err(); err != nil {
		return err
	}
	if err := s.cfg.Redis.HIncrBy(ctx, key, fieldCostMicros, costMicros).Err(); err != nil {
		return err
	}
	return s.cfg.Redis.HIncrBy(ctx, key, fieldCalls, 1).Err()
}

// usageKey 返回累计 HASH 的 key：总量 {prefix}:{uid}:total，当日 {prefix}:{uid}:{yyyymmdd}。
func (s *Store) usageKey(userID string, period QuotaPeriod, now time.Time) string {
	if period == QuotaPeriodDaily {
		return fmt.Sprintf("%s:%s:%s", s.cfg.KeyPrefix, userID, now.Format("20060102"))
	}
	return fmt.Sprintf("%s:%s:total", s.cfg.KeyPrefix, userID)
}

func (s *Store) flushLoop() {
	defer close(s.stopped)
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]UsageRecord, 0, defaultFlushBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.cfg.DB.CreateInBatches(batch, defaultFlushBatch).Error; err != nil {
			s.reportError(fmt.Errorf("billingstore: flush %d records: %w", len(batch), err))
		}
		batch = batch[:0]
	}

	for {
		select {
		case record := <-s.pending:
			batch = append(batch, record)
			if len(batch) >= defaultFlushBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.done:
			for {
				select {
				case record := <-s.pending:
					batch = append(batch, record)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (s *Store) reportError(err error) {
	if s.cfg.OnError != nil && err != nil {
		s.cfg.OnError(err)
	}
}

func redisInt(value any) int64 {
	str, ok := value.(string)
	if !ok {
		return 0
	}
	var n int64
	_, _ = fmt.Sscanf(str, "%d", &n)
	return n
}
