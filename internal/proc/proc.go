// Package proc 按本地端口反查占用它的进程。
//
// 只用于「客户端在本机」的连接（-L / -D）：这类连接是用户自己的程序
// （浏览器、IDE、psql…）连到我们监听的端口，所以对端地址里的那个本地端口
// 就是发起方的 socket 本地端口，可以反查到 PID。
//
// -R 远程转发的发起方在 SSH 服务器那一侧，本机没有对应进程，不查。
package proc

import (
	"sync"
	"sync/atomic"
	"time"
)

// Owner 是某个本地端口对应的进程
type Owner struct {
	PID  int
	Name string
}

// Table 缓存「本地端口 → 进程」映射。
//
// 查询一律走异步刷新：macOS 上一次全量扫描要遍历所有进程的所有 fd，
// 耗时可达几十毫秒，绝不能放在 accept 路径上同步执行——否则连接一多，
// 光查进程就能把转发拖慢。
type Table struct {
	mu sync.RWMutex
	m  map[uint16]Owner
	at time.Time

	busy atomic.Bool
}

// NewTable 创建空表
func NewTable() *Table { return &Table{} }

// Lookup 查询端口对应的进程
func (t *Table) Lookup(port uint16) (Owner, bool) {
	t.mu.RLock()
	o, ok := t.m[port]
	t.mu.RUnlock()
	return o, ok
}

// Refresh 同步刷新（供测试与首次加载使用）
func (t *Table) Refresh() error {
	m, err := localOwners()
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.m = m
	t.at = time.Now()
	t.mu.Unlock()
	return nil
}

// RefreshAsync 后台刷新。距上次刷新不足 minInterval、或已有刷新在跑时直接返回。
func (t *Table) RefreshAsync(minInterval time.Duration) {
	t.mu.RLock()
	fresh := time.Since(t.at) < minInterval
	t.mu.RUnlock()
	if fresh {
		return
	}
	if !t.busy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer t.busy.Store(false)
		m, err := localOwners()
		if err != nil {
			return
		}
		t.mu.Lock()
		t.m = m
		t.at = time.Now()
		t.mu.Unlock()
	}()
}

// Size 返回当前缓存的端口数
func (t *Table) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.m)
}
