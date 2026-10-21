package conn

import (
	"testing"
)

// 这组基准用来守住两条性能底线：
//  1. 单条连接登记必须是常数时间，不能被满表判定拖慢；
//  2. 每秒一次的快照 + 采样必须远低于 1ms，否则 1Hz 推送会自己变成瓶颈。

func fillActive(n int) *Registry {
	r := NewRegistry()
	for i := 0; i < n; i++ {
		e := r.New("t1", "隧道", "L 127.0.0.1:8080", "TCP", "127.0.0.1:1", "example.com:80")
		r.Add(e)
	}
	return r
}

func fillMixed(active, closed int) *Registry {
	r := fillActive(active)
	for i := 0; i < closed; i++ {
		e := r.New("t1", "隧道", "L 127.0.0.1:8080", "TCP", "127.0.0.1:1", "example.com:80")
		r.Add(e)
		e.Close(ReasonNormal)
	}
	return r
}

// BenchmarkAdd 单条连接登记（未满表）
func BenchmarkAdd(b *testing.B) {
	r := NewRegistry()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := r.New("t1", "隧道", "L 127.0.0.1:8080", "TCP", "127.0.0.1:1", "x:1")
		r.Add(e)
		r.Remove(e.ID)
	}
}

// BenchmarkAddAtCap 满表时被拒绝的登记。这里曾经是 128µs/次（每次全表重算），
// 加限流后应该落在 1µs 量级。
func BenchmarkAddAtCap(b *testing.B) {
	r := fillActive(DefaultMaxActive)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := r.New("t1", "隧道", "L 127.0.0.1:8080", "TCP", "127.0.0.1:1", "x:1")
		r.Add(e)
	}
}

// BenchmarkSnapshot500 500 行的快照（含排序与分配）
func BenchmarkSnapshot500(b *testing.B) {
	r := fillMixed(400, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Snapshot()
	}
}

// BenchmarkSample500 500 行的速率采样
func BenchmarkSample500(b *testing.B) {
	r := fillMixed(400, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Sample()
	}
}
