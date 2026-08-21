package provider

import (
	"maps"
	"slices"
	"sync/atomic"
)

// ============================================================
// 价格表热替换
// ============================================================

// pricingSnapshot 是一代价格数据，构造后只读。
type pricingSnapshot struct {
	table   PricingTable
	version string
}

// PricingRegistry 承载可原子替换的价格表。
//
// 直接持有 PricingTable 时，运行中改价只能靠调用方自己保证"整表替换"，
// 一旦有人在线修改条目就会与计价路径的读发生 data race。
// PricingRegistry 把这个约束变成 API 保证：
//
//   - 构造与替换时整表做防御性拷贝，调用方之后再改自己那份表也影响不到已生效的价格；
//   - 构造与替换时强制走 PricingTable.Validate，非法费率不会进入生效状态；
//   - 计价读取走原子指针，无锁、不阻塞替换。
//
// 每一代价格带一个 Version 字符串，账务落库时一并记录，便于对账时还原
// 当时的计价口径。
//
// 所有方法可并发调用；查询方法对 nil 接收者安全（等价于空价格表）。
type PricingRegistry struct {
	snapshot atomic.Pointer[pricingSnapshot]
}

// NewPricingRegistry 用一代价格数据构造 registry。
//
// table 会被整表拷贝并校验，非法费率返回 ErrInvalidPricing 聚合的问题清单。
// table 为空表示暂无配价，此时 Cost 一律返回 ErrModelNotPriced，不会静默按零计费。
// version 是这一代价格的标识（如 "2026-08-21" 或配置版本号），由调用方定义。
func NewPricingRegistry(table PricingTable, version string) (*PricingRegistry, error) {
	snapshot, err := newPricingSnapshot(table, version)
	if err != nil {
		return nil, err
	}
	r := &PricingRegistry{}
	r.snapshot.Store(snapshot)
	return r, nil
}

// Replace 用新一代价格数据整体替换当前生效的价格表。
//
// 校验通过后一次原子切换，替换前后不存在"半新半旧"的中间状态：
// 每一次 Cost 调用要么整体按旧价、要么整体按新价。
// 校验失败时返回错误且不改变当前生效的价格。
func (r *PricingRegistry) Replace(table PricingTable, version string) error {
	if r == nil {
		return ErrNilPricingRegistry
	}
	snapshot, err := newPricingSnapshot(table, version)
	if err != nil {
		return err
	}
	r.snapshot.Store(snapshot)
	return nil
}

// Cost 按当前生效的价格表计算一次调用的费用（微元），语义与
// PricingTable.Cost 完全一致，包括 ErrModelNotPriced 与 ErrInvalidPricing 的判定。
func (r *PricingRegistry) Cost(model string, usage Usage) (micros int64, currency string, err error) {
	return r.load().table.Cost(model, usage)
}

// Rate 返回某个模型当前生效的费率，模型未配价时第二个返回值为 false。
func (r *PricingRegistry) Rate(model string) (ModelRate, bool) {
	rate, ok := r.load().table[model]
	return rate, ok
}

// Version 返回当前生效价格的版本标识。
func (r *PricingRegistry) Version() string {
	return r.load().version
}

// Models 返回当前已配价的模型名，按字典序排列。
func (r *PricingRegistry) Models() []string {
	snapshot := r.load()
	if len(snapshot.table) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(snapshot.table))
}

// Snapshot 返回当前生效价格的拷贝与版本标识，两者来自同一代数据。
//
// 每次调用都整表拷贝，用于导出、对外展示或喂给按 PricingTable 取价的组件；
// 计价请直接用 Cost，不要在热路径上反复取快照。
func (r *PricingRegistry) Snapshot() (table PricingTable, version string) {
	snapshot := r.load()
	if len(snapshot.table) == 0 {
		return nil, snapshot.version
	}
	return maps.Clone(snapshot.table), snapshot.version
}

func (r *PricingRegistry) load() *pricingSnapshot {
	if r == nil {
		return &pricingSnapshot{}
	}
	if snapshot := r.snapshot.Load(); snapshot != nil {
		return snapshot
	}
	return &pricingSnapshot{}
}

func newPricingSnapshot(table PricingTable, version string) (*pricingSnapshot, error) {
	if err := table.Validate(); err != nil {
		return nil, err
	}
	snapshot := &pricingSnapshot{version: version}
	if len(table) > 0 {
		snapshot.table = maps.Clone(table)
	}
	return snapshot, nil
}
