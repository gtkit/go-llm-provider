package billingstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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

// ErrStoreClosed 表示 Store 已关闭，后续 Record 被拒绝。
var ErrStoreClosed = errors.New("billingstore: store is closed")

// usageIncrScript 以单个原子脚本完成：EntryID 幂等检查（SADD）、
// 总量与当日两个 HASH 的全部字段累计、当日 key 的 TTL 设置。
// 重放（同 EntryID）时整体跳过，返回 0；首次执行返回 1。
// KEYS: 1=幂等集合, 2=总量 key, 3=当日 key
// ARGV: 1=EntryID, 2=tokens, 3=costMicros, 4=当日 TTL 秒
//
// 一致性保证：Redis 不支持脚本内事务回滚，若 SADD 抢占幂等标记后某个
// HINCRBY 失败（累计字段被脏数据写成非整数、或累加溢出 int64），会留下
// "已去重但未累计"的坏账且无法重试。为此累计全程走 redis.pcall，任一步
// 失败即逆序回滚本次已应用的增量、SREM 撤销幂等标记，并返回错误交由调用方
// 重试——要么全部记账、要么完全不记，绝不静默丢账。SADD 自身遇集合类型
// 错误会在累计前中止，同样不留残迹。
var usageIncrScript = redis.NewScript(`
if redis.call('SADD', KEYS[1], ARGV[1]) == 0 then
  return 0
end
local ops = {
  {KEYS[2], 'total_tokens', ARGV[2]}, {KEYS[2], 'cost_micros', ARGV[3]}, {KEYS[2], 'calls', '1'},
  {KEYS[3], 'total_tokens', ARGV[2]}, {KEYS[3], 'cost_micros', ARGV[3]}, {KEYS[3], 'calls', '1'},
}
local applied = {}
for i = 1, #ops do
  local r = redis.pcall('HINCRBY', ops[i][1], ops[i][2], ops[i][3])
  if type(r) == 'table' and r.err then
    for j = #applied, 1, -1 do
      redis.call('HINCRBY', applied[j][1], applied[j][2], '-' .. applied[j][3])
    end
    redis.call('SREM', KEYS[1], ARGV[1])
    return redis.error_reply('billingstore: usage incr failed: ' .. r.err)
  end
  applied[#applied + 1] = ops[i]
end
redis.call('EXPIRE', KEYS[3], ARGV[4])
return 1
`)

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

	// PricingVersion 随流水记录的价格表版本标识（如 "2026-07"），
	// 价格调整时更新，便于对账时追溯每条流水按什么费率计算。
	PricingVersion string

	// KeyPrefix 是 Redis key 前缀，默认 "llm:usage"。
	KeyPrefix string

	// RedisCluster 为 true 时，所有同一用户的计费 key 自动使用相同 Redis Cluster
	// hash tag，确保 Lua 脚本涉及的多个 key 位于同一 slot。开启后 key 命名与单节点
	// 模式不同，迁移已有数据时需由业务侧完成一次性转换。
	RedisCluster bool

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

	closeOnce sync.Once
	lifecycle sync.Mutex
	active    sync.WaitGroup
	closed    bool
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
	if s == nil {
		return ErrStoreClosed
	}
	if entry.UserID == "" {
		return nil
	}
	if !s.beginRecord() {
		return ErrStoreClosed
	}
	var reported []error
	defer func() {
		s.active.Done()
		for _, err := range reported {
			s.reportError(err)
		}
	}()

	entryID := entry.EntryID
	if entryID == "" {
		// 直接构造 RecordEntry（未经 NewBillingHook）的调用路径：
		// 兜底生成幂等键，避免重放重复累计与唯一索引冲突。
		entryID = provider.NewEntryID()
	}

	costMicros, err := s.entryCost(entry)
	if err != nil {
		reported = append(reported, err)
	}
	if err := s.incrRedis(ctx, entryID, entry, costMicros); err != nil {
		reported = append(reported, err)
	}

	if s.cfg.DB == nil {
		return nil
	}
	record := UsageRecord{
		EntryID:          entryID,
		UserID:           entry.UserID,
		ConversationID:   entry.ConversationID,
		RequestID:        entry.RequestID,
		Provider:         string(entry.Provider),
		Model:            entry.Model,
		RequestModel:     entry.RequestModel,
		PromptTokens:     entry.Usage.PromptTokens,
		CompletionTokens: entry.Usage.CompletionTokens,
		ReasoningTokens:  entry.Usage.ReasoningTokens,
		CacheReadTokens:  entry.Usage.CacheReadTokens,
		CacheWriteTokens: entry.Usage.CacheWriteTokens,
		TotalTokens:      entry.Usage.TotalTokens,
		CostMicros:       costMicros,
		Currency:         s.entryCurrency(entry),
		PricingVersion:   s.cfg.PricingVersion,
		Streaming:        entry.Streaming,
		Terminated:       entry.Terminated,
		TerminateReason:  string(entry.TerminateReason),
		ErrorCode:        string(entry.ErrorCode),
		CreatedAt:        time.Now(),
	}
	select {
	case s.pending <- record:
	default:
		reported = append(reported, ErrBufferFull)
	}
	return nil
}

func (s *Store) beginRecord() bool {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.closed {
		return false
	}
	s.active.Add(1)
	return true
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
	usedTokens, err := redisInt(values[0])
	if err != nil {
		s.reportError(fmt.Errorf("billingstore: parse token usage: %w", err))
		return nil // fail-open
	}
	usedMicros, err := redisInt(values[1])
	if err != nil {
		s.reportError(fmt.Errorf("billingstore: parse cost usage: %w", err))
		return nil // fail-open
	}

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

// Close 停止后台循环并冲刷剩余流水；可安全重复调用。
// 关闭后 Record 返回 ErrStoreClosed。
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.lifecycle.Lock()
		s.closed = true
		s.lifecycle.Unlock()
		s.active.Wait()
		close(s.done)
		<-s.stopped
	})
	return nil
}

// pricingModel 返回查价用的模型名：优先请求侧模型（业务定价口径——
// 平台可能把别名解析为具体版本回传，如 deepseek-chat → deepseek-v4-flash），
// 请求侧为空或未配价时回落响应侧实际模型名。
func (s *Store) pricingModel(entry provider.RecordEntry) string {
	if _, ok := s.cfg.Pricing[entry.RequestModel]; ok && entry.RequestModel != "" {
		return entry.RequestModel
	}
	return entry.Model
}

func (s *Store) entryCost(entry provider.RecordEntry) (int64, error) {
	model := s.pricingModel(entry)
	if len(s.cfg.Pricing) == 0 || model == "" {
		return 0, nil
	}
	micros, _, err := s.cfg.Pricing.Cost(model, entry.Usage)
	if err != nil {
		return 0, err
	}
	return micros, nil
}

func (s *Store) entryCurrency(entry provider.RecordEntry) string {
	if rate, ok := s.cfg.Pricing[s.pricingModel(entry)]; ok {
		return rate.Currency
	}
	return ""
}

// incrRedis 通过 Lua 脚本原子完成幂等检查与两级累计：
// 同一 EntryID 重放时整体跳过（token、费用、调用次数都只计一次）。
func (s *Store) incrRedis(ctx context.Context, entryID string, entry provider.RecordEntry, costMicros int64) error {
	tokens := int64(entry.Usage.TotalTokens)
	// 负增量会倒扣累计值并破坏脚本的逆向回滚，直接拒绝而非静默错账。
	if tokens < 0 || costMicros < 0 {
		return fmt.Errorf("billingstore: refuse negative usage increment (tokens=%d, cost=%d)", tokens, costMicros)
	}
	now := time.Now()
	keys := []string{
		s.entrySetKey(entry.UserID),
		s.usageKey(entry.UserID, QuotaPeriodTotal, now),
		s.usageKey(entry.UserID, QuotaPeriodDaily, now),
	}
	args := []any{
		entryID,
		tokens,
		costMicros,
		int64(dailyKeyTTL / time.Second),
	}
	if err := usageIncrScript.Run(ctx, s.cfg.Redis, keys, args...).Err(); err != nil {
		return fmt.Errorf("billingstore: usage incr script: %w", err)
	}
	return nil
}

// usageKey 返回累计 HASH 的 key：总量 {prefix}:{uid}:total，当日 {prefix}:{uid}:{yyyymmdd}。
func (s *Store) usageKey(userID string, period QuotaPeriod, now time.Time) string {
	prefix := s.userKeyPrefix(userID)
	if period == QuotaPeriodDaily {
		return fmt.Sprintf("%s:%s:%s", prefix, userID, now.Format("20060102"))
	}
	return fmt.Sprintf("%s:%s:total", prefix, userID)
}

func (s *Store) entrySetKey(userID string) string {
	return fmt.Sprintf("%s:%s:entries", s.userKeyPrefix(userID), userID)
}

func (s *Store) userKeyPrefix(userID string) string {
	if !s.cfg.RedisCluster {
		return s.cfg.KeyPrefix
	}
	digest := sha256.Sum256([]byte(userID))
	return s.cfg.KeyPrefix + ":{" + hex.EncodeToString(digest[:8]) + "}"
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
		// EntryID 唯一索引 + DoNothing：重复写入（重放/重试）被静默忽略，保证幂等。
		err := s.cfg.DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "entry_id"}},
			DoNothing: true,
		}).CreateInBatches(batch, defaultFlushBatch).Error
		if err != nil {
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

func redisInt(value any) (int64, error) {
	if value == nil {
		return 0, nil
	}
	str, ok := value.(string)
	if !ok {
		return 0, fmt.Errorf("unexpected Redis value type %T", value)
	}
	n, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", str, err)
	}
	return n, nil
}
