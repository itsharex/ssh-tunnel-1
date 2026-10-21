package conn

import (
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ssh-tunnel/internal/proc"
)

// 默认上限。
//
// maxActive 只是「登记」上限，不是「允许」上限：达到上限后新连接仍然照常转发，
// 只是不再出现在列表里。绝不能为了腾位置去踢掉活跃连接——那等于把用户的
// 业务连接掐断，比少显示几行严重得多。
const (
	DefaultMaxActive  = 4096
	DefaultMaxClosed  = 200
	DefaultKeepClosed = 5 * time.Second
)

// Snapshot 是推给前端的一行连接数据
type Snapshot struct {
	ID       string `json:"id"`
	TunnelID string `json:"tunnelId"`
	Tunnel   string `json:"tunnel"`
	Forward  string `json:"forward"`
	Proto    string `json:"proto"`
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	PID      int    `json:"pid"`
	Proc     string `json:"proc"`
	State    State  `json:"state"`
	Reason   Reason `json:"reason"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	UpRate   int64  `json:"upRate"`
	DownRate int64  `json:"downRate"`
	Started  int64  `json:"started"`  // unix 毫秒
	ClosedAt int64  `json:"closedAt"` // unix 毫秒，0 表示未关闭
}

// Registry 保存所有连接记录。并发安全。
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	seq     atomic.Uint64
	proc    *proc.Table

	maxActive  int
	maxClosed  int
	keepClosed time.Duration

	// 满表判定结果缓存。见 Add 里的说明。
	atCap     bool
	lastCapGc time.Time

	// closedSinceGc 自上次清理以来关闭的记录数，用于摊薄清理成本。
	closedSinceGc atomic.Int64
}

// capCheckInterval 满表时重新统计活跃数的间隔。
// 4096 条时一次全表统计要 128µs（正常登记只要 267ns），
// 不能每来一个连接就算一遍，限流到 200ms 一次。
const capCheckInterval = 200 * time.Millisecond

// NewRegistry 创建注册表
func NewRegistry() *Registry {
	return &Registry{
		entries:    make(map[string]*Entry, 64),
		proc:       proc.NewTable(),
		maxActive:  DefaultMaxActive,
		maxClosed:  DefaultMaxClosed,
		keepClosed: DefaultKeepClosed,
	}
}

// ProcTable 返回进程查询表（调试与测试用）
func (r *Registry) ProcTable() *proc.Table { return r.proc }

// New 生成一条尚未登记的连接记录。
//
// 之所以拆成「先创建、后登记」，是因为动态转发要先把 SOCKS5 / HTTP 头解析完
// 才知道目标地址，但字节计数必须从第一个字节就开始，所以 Entry 要先存在。
func (r *Registry) New(tunnelID, tunnel, forward, proto, src, dst string) *Entry {
	seq := r.seq.Add(1)
	e := NewEntry("c"+strconv.FormatUint(seq, 36), tunnelID, tunnel, forward, proto, src, dst)
	e.seq = seq
	e.reg = r
	return e
}

// noteClosed 记录一次「有连接关闭」，积压够多时真正跑一次清理。
//
// 清理原本只发生在 Sample（仅在监控页打开时 1Hz 调用）和 Add 达上限时，
// 于是从不打开监控页的用户，已关闭记录会一路堆到 4096 的登记上限才被回收。
// 有界、也会自愈，但没必要让它们白占着。这里按 maxClosed 摊薄：
// 平均每关闭 maxClosed 条才做一次 O(n) 清理，成本可以忽略。
func (r *Registry) noteClosed() {
	r.mu.RLock()
	limit := int64(r.maxClosed)
	r.mu.RUnlock()
	if limit <= 0 {
		limit = DefaultMaxClosed
	}
	if r.closedSinceGc.Add(1) < limit {
		return
	}
	r.closedSinceGc.Store(0)

	r.mu.Lock()
	r.gcLocked(time.Now())
	r.mu.Unlock()
}

// Add 登记一条连接。返回 false 表示已达登记上限，该连接不会被展示
// （但转发不受影响）。
func (r *Registry) Add(e *Entry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 快路径 O(1)：只有在条数接近上限时才做一次清理 + 精确统计。
	//
	// 满表后不能每次 Add 都重算：4096 条时一次精确统计要 128µs，
	// 比正常登记（267ns）慢近 500 倍，连接突发时会把写锁长时间占住。
	// 这里把重算限流到 200ms 一次——期间新连接不登记但仍然照常转发，
	// 界面上最多晚 200ms 出现（或干脆不出现），比拖慢转发重要得多。
	if len(r.entries) >= r.maxActive {
		now := time.Now()
		if now.Sub(r.lastCapGc) >= capCheckInterval {
			r.lastCapGc = now
			r.gcLocked(now)
			r.atCap = r.countActiveLocked() >= r.maxActive
		}
		if r.atCap {
			return false
		}
	}
	e.reg = r // 兼容直接用 NewEntry 创建的记录，保证关闭时也能触发摊薄清理
	r.entries[e.ID] = e
	return true
}

// Remove 直接丢弃一条记录（用于连接建立失败、从未真正转发的情况）
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.entries, id)
	r.mu.Unlock()
}

// Sample 计算所有连接的速率、清理过期记录，并异步补齐进程信息。
// 由上层以 1Hz 调用。
func (r *Registry) Sample() {
	r.mu.RLock()
	list := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		list = append(list, e)
	}
	r.mu.RUnlock()

	// 一次遍历同时做两件事：算速率、挑出「还缺进程信息」的连接。
	// 分开遍历会让每秒多一次全表扫描，虽然只有几微秒，但没必要。
	// 时钟只取一次，所有记录共用同一时刻。
	now := time.Now()
	var need []*Entry
	for _, e := range list {
		e.sampleAt(now)
		// 只有客户端在本机的连接才可能查到进程；已查到的不再重复查
		if e.LocalClient && e.LocalPort != 0 && e.pid.Load() == 0 {
			if st, _, _ := e.Status(); st == StateActive {
				need = append(need, e)
			}
		}
	}

	r.mu.Lock()
	r.gcLocked(now)
	r.mu.Unlock()

	// 没有任何连接需要反查进程时，连进程表都不刷新。
	// macOS 一次全量扫描约 3ms，空闲时每秒白烧这笔开销没有意义。
	if len(need) == 0 {
		return
	}
	// 刷新走后台：扫描成本被摊薄到可以忽略，进程名最多晚 procRefreshInterval 出现。
	r.proc.RefreshAsync(procRefreshInterval)
	for _, e := range need {
		if o, ok := r.proc.Lookup(e.LocalPort); ok {
			e.SetPID(o.PID, o.Name)
		}
	}
}

// procRefreshInterval 进程表刷新间隔。进程信息晚一两秒出现完全可以接受，
// 换来的是一次扫描的成本被摊薄到可以忽略。
const procRefreshInterval = 2 * time.Second

// Snapshot 返回当前全部连接（新的在前）
func (r *Registry) Snapshot() []Snapshot {
	r.mu.RLock()
	list := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		list = append(list, e)
	}
	r.mu.RUnlock()

	// 先给 Entry 排序再转 Snapshot：同一毫秒内用创建序号兜底，保证顺序稳定不抖动。
	// 不能先转 Snapshot 再排——那要靠 ID 反查 seq，等于每行建一个 string 键的
	// 哈希项，500 行每次多分配几十 KB、多花几十微秒，纯属浪费。
	sort.Slice(list, func(i, j int) bool {
		if !list[i].Started.Equal(list[j].Started) {
			return list[i].Started.After(list[j].Started)
		}
		return list[i].seq > list[j].seq
	})

	out := make([]Snapshot, len(list))
	for i, e := range list {
		out[i] = e.snapshot()
	}
	return out
}

// Kill 断开指定连接，返回是否真的断开了
func (r *Registry) Kill(id string) bool {
	r.mu.RLock()
	e, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	return e.Kill()
}

// KillByTunnel 断开某条隧道下的所有活跃连接，返回断开数量
func (r *Registry) KillByTunnel(tunnelID string) int {
	return r.eachActive(func(e *Entry) bool {
		return e.TunnelID == tunnelID
	}, (*Entry).Kill)
}

// KillAll 断开所有活跃连接，返回断开数量
func (r *Registry) KillAll() int {
	return r.eachActive(func(*Entry) bool { return true }, (*Entry).Kill)
}

// CloseByTunnel 把某条隧道的连接标记为「隧道停止」并关闭，
// 用于隧道断开时兜底清理，避免残留半开连接和僵尸记录。
func (r *Registry) CloseByTunnel(tunnelID string) int {
	return r.eachActive(func(e *Entry) bool {
		return e.TunnelID == tunnelID
	}, func(e *Entry) bool {
		return e.KillWith(ReasonTunnelStop)
	})
}

// eachActive 对满足条件的活跃连接执行 fn，返回 fn 返回 true 的次数
func (r *Registry) eachActive(match func(*Entry) bool, fn func(*Entry) bool) int {
	r.mu.RLock()
	list := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		list = append(list, e)
	}
	r.mu.RUnlock()

	n := 0
	for _, e := range list {
		if !match(e) {
			continue
		}
		if fn(e) {
			n++
		}
	}
	return n
}

// ActiveCount 返回活跃连接数
func (r *Registry) ActiveCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.countActiveLocked()
}

// Len 返回记录总数（含已关闭）
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

func (r *Registry) countActiveLocked() int {
	n := 0
	for _, e := range r.entries {
		if st, _, _ := e.Status(); st == StateActive {
			n++
		}
	}
	return n
}

// gcLocked 清理已关闭记录：超过保留时长，或条数超过 maxClosed（保留最新的）。
// 时间与数量双上限——纯时间保留在 -D 高并发下会堆积，纯数量保留在空闲时
// 又会把「刚断开、用户还想看一眼」的记录挤掉。
func (r *Registry) gcLocked(now time.Time) {
	type item struct {
		e  *Entry
		at time.Time
	}
	var closed []item
	for _, e := range r.entries {
		if st, _, at := e.Status(); st == StateClosed {
			closed = append(closed, item{e, at})
		}
	}
	if len(closed) == 0 {
		return
	}
	sort.Slice(closed, func(i, j int) bool { return closed[i].at.Before(closed[j].at) })

	drop := len(closed) - r.maxClosed
	for i, it := range closed {
		if i < drop || now.Sub(it.at) > r.keepClosed {
			delete(r.entries, it.e.ID)
		}
	}
}

func (e *Entry) snapshot() Snapshot {
	st, reason, at := e.Status()
	s := Snapshot{
		ID:       e.ID,
		TunnelID: e.TunnelID,
		Tunnel:   e.Tunnel,
		Forward:  e.Forward,
		Proto:    e.Proto(),
		Src:      e.Src,
		Dst:      e.Dst(),
		PID:      int(e.pid.Load()),
		State:    st,
		Reason:   reason,
		Up:       e.up.Load(),
		Down:     e.down.Load(),
		Started:  e.Started.UnixMilli(),
	}
	if v, ok := e.proc.Load().(string); ok {
		s.Proc = v
	}
	// 已关闭的连接不再展示速率：保留期里的幽灵行显示一个跳动数字会误导用户
	if st == StateActive {
		s.UpRate = e.rateUp.Load()
		s.DownRate = e.rateDown.Load()
	}
	if !at.IsZero() {
		s.ClosedAt = at.UnixMilli()
	}
	return s
}
