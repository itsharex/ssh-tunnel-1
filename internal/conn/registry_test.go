package conn

import (
	"testing"
	"time"
)

func newTestEntry(r *Registry, id string) *Entry {
	e := r.New("t1", "隧道", "L 127.0.0.1:8080", "TCP", "127.0.0.1:50000", "example.com:80")
	e.ID = id
	return e
}

// TestRegistryKeepsClosedBriefly 已关闭连接在保留期内仍然可见，
// 超过保留期后消失。这是「断开不是立刻消失」策略的核心。
func TestRegistryKeepsClosedBriefly(t *testing.T) {
	r := NewRegistry()
	r.keepClosed = 60 * time.Millisecond

	e := newTestEntry(r, "c1")
	if !r.Add(e) {
		t.Fatal("Add 应当成功")
	}
	if got := r.ActiveCount(); got != 1 {
		t.Fatalf("活跃数应为 1，实际 %d", got)
	}

	e.Close(ReasonNormal)
	r.Sample()
	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("关闭后应仍在保留期内可见，实际 %d 条", len(snap))
	}
	if snap[0].State != StateClosed || snap[0].Reason != ReasonNormal {
		t.Fatalf("状态错误: %s/%s", snap[0].State, snap[0].Reason)
	}
	if got := r.ActiveCount(); got != 0 {
		t.Fatalf("活跃数应为 0，实际 %d", got)
	}

	time.Sleep(100 * time.Millisecond)
	r.Sample()
	if got := len(r.Snapshot()); got != 0 {
		t.Fatalf("超过保留期后应被清理，实际还剩 %d 条", got)
	}
}

// TestRegistryCapsClosedRecords 已关闭记录超过条数上限时，保留最新的。
func TestRegistryCapsClosedRecords(t *testing.T) {
	r := NewRegistry()
	r.keepClosed = time.Hour // 只让数量上限起作用
	r.maxClosed = 3

	for i := 0; i < 5; i++ {
		e := newTestEntry(r, "c"+string(rune('0'+i)))
		r.Add(e)
		e.Close(ReasonNormal)
		time.Sleep(2 * time.Millisecond) // 保证关闭时间有序
	}
	r.Sample()

	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("应只保留 3 条，实际 %d 条", len(snap))
	}
	// 最新的 c4/c3/c2 应当留下，最旧的 c0/c1 被淘汰
	kept := map[string]bool{}
	for _, s := range snap {
		kept[s.ID] = true
	}
	for _, id := range []string{"c2", "c3", "c4"} {
		if !kept[id] {
			t.Errorf("%s 应当被保留", id)
		}
	}
	for _, id := range []string{"c0", "c1"} {
		if kept[id] {
			t.Errorf("%s 应当已被淘汰", id)
		}
	}
}

// TestRegistryActiveCapDoesNotEvictActive 达到登记上限时只是不再登记新连接，
// 绝不能踢掉已有活跃连接——那等于把用户的业务连接掐断。
func TestRegistryActiveCapDoesNotEvictActive(t *testing.T) {
	r := NewRegistry()
	r.maxActive = 2

	a := newTestEntry(r, "a")
	b := newTestEntry(r, "b")
	c := newTestEntry(r, "c")

	if !r.Add(a) || !r.Add(b) {
		t.Fatal("前两条应当登记成功")
	}
	if r.Add(c) {
		t.Fatal("超出上限时 Add 应返回 false")
	}
	if got := r.ActiveCount(); got != 2 {
		t.Fatalf("活跃连接不应被淘汰，实际 %d 条", got)
	}
	if r.Kill("a") != true {
		t.Fatal("已有连接仍应可断开")
	}
}

// TestRegistryKillScopes 断开单个 / 按隧道 / 全部
func TestRegistryKillScopes(t *testing.T) {
	r := NewRegistry()

	a := r.New("t1", "隧道一", "L :1", "TCP", "127.0.0.1:1", "a:1")
	b := r.New("t1", "隧道一", "L :1", "TCP", "127.0.0.1:2", "b:1")
	c := r.New("t2", "隧道二", "L :2", "TCP", "127.0.0.1:3", "c:1")
	for _, e := range []*Entry{a, b, c} {
		r.Add(e)
	}

	if n := r.KillByTunnel("t1"); n != 2 {
		t.Fatalf("隧道一应断开 2 条，实际 %d", n)
	}
	if got := r.ActiveCount(); got != 1 {
		t.Fatalf("应剩 1 条活跃，实际 %d", got)
	}
	if n := r.KillAll(); n != 1 {
		t.Fatalf("应再断开 1 条，实际 %d", n)
	}
	if got := r.ActiveCount(); got != 0 {
		t.Fatalf("应无活跃连接，实际 %d", got)
	}
}

// TestRegistryCloseByTunnelMarksReason 隧道停止时原因应为 tunnel 而不是 user
func TestRegistryCloseByTunnelMarksReason(t *testing.T) {
	r := NewRegistry()
	e := r.New("t1", "隧道一", "L :1", "TCP", "127.0.0.1:1", "a:1")
	r.Add(e)

	if n := r.CloseByTunnel("t1"); n != 1 {
		t.Fatalf("应关闭 1 条，实际 %d", n)
	}
	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("应保留 1 条记录，实际 %d", len(snap))
	}
	if snap[0].Reason != ReasonTunnelStop {
		t.Fatalf("原因应为 %q，实际 %q", ReasonTunnelStop, snap[0].Reason)
	}
}

// TestEntryCounters 字节计数与速率采样
func TestEntryCounters(t *testing.T) {
	e := NewEntry("c1", "t1", "隧道", "L :1", "TCP", "127.0.0.1:1", "a:1")

	// 读记上行、写记下行
	rc := e.Wrap(&fakeRW{})
	_, _ = rc.Read(make([]byte, 100))
	_, _ = rc.Write(make([]byte, 40))

	if e.Up() != 100 || e.Down() != 40 {
		t.Fatalf("计数错误: up=%d down=%d", e.Up(), e.Down())
	}

	// 首轮只建立基准：没有上一样本就算不出速率，此时必须是 0 而不是累计值
	e.sample()
	if e.rateUp.Load() != 0 || e.rateDown.Load() != 0 {
		t.Fatalf("首轮速率应为 0，实际 %d/%d", e.rateUp.Load(), e.rateDown.Load())
	}

	// 第二轮才有增量
	_, _ = rc.Read(make([]byte, 30))
	_, _ = rc.Write(make([]byte, 10))
	e.sample()
	if e.rateUp.Load() != 30 || e.rateDown.Load() != 10 {
		t.Fatalf("速率错误: %d/%d，期望 30/10", e.rateUp.Load(), e.rateDown.Load())
	}

	// 没有新流量时速率应归零
	e.sample()
	if e.rateUp.Load() != 0 || e.rateDown.Load() != 0 {
		t.Fatalf("空闲速率应为 0，实际 %d/%d", e.rateUp.Load(), e.rateDown.Load())
	}
}

// TestSampleResetsAfterLongGap 监控页关过一段时间再打开时，
// 期间累计的字节不能被当成 1 秒的速率——那会在界面上蹦出一个虚高值。
func TestSampleResetsAfterLongGap(t *testing.T) {
	e := NewEntry("c1", "t1", "隧道", "L :1", "TCP", "127.0.0.1:1", "a:1")
	rc := e.Wrap(&fakeRW{})

	e.sample() // 建立基准
	_, _ = rc.Read(make([]byte, 1<<20))
	// 把上次采样时刻拨回一分钟前，模拟「页面关着、没人采样」
	e.lastAt.Store(time.Now().Add(-time.Minute).UnixNano())

	e.sample()
	if e.rateUp.Load() != 0 {
		t.Fatalf("停看后首轮速率应为 0，实际 %d", e.rateUp.Load())
	}
	if e.Up() != 1<<20 {
		t.Fatalf("累计字节不应被重置，实际 %d", e.Up())
	}

	// 下一次采样恢复正常
	_, _ = rc.Read(make([]byte, 50))
	e.sample()
	if e.rateUp.Load() != 50 {
		t.Fatalf("恢复后速率应为 50，实际 %d", e.rateUp.Load())
	}
}

// TestCloseReleasesConnReferences 正常关闭也要丢掉对两端连接的引用，
// 否则已关闭的记录会一直挂着两个已经关掉的 socket 对象。
func TestCloseReleasesConnReferences(t *testing.T) {
	r := NewRegistry()
	e := r.New("t1", "隧道", "L :1", "TCP", "127.0.0.1:1", "a:1")
	r.Add(e)

	a, b := &fakeCloser{}, &fakeCloser{}
	e.AddCloser(a, b)
	e.SetUpstream(&fakeCloser{})
	e.Close(ReasonNormal)

	e.mu.Lock()
	n, up := len(e.closers), e.upstream
	e.mu.Unlock()
	if n != 0 || up != nil {
		t.Fatalf("关闭后不应再持有连接引用: closers=%d upstream=%v", n, up)
	}
}

// TestClosedRecordsCollectedWithoutSample 从不打开监控页（Sample 永不被调用）时，
// 已关闭记录也不该一路堆到登记上限。
func TestClosedRecordsCollectedWithoutSample(t *testing.T) {
	r := NewRegistry()
	r.keepClosed = time.Nanosecond
	r.maxClosed = 10

	for i := 0; i < 200; i++ {
		e := r.New("t1", "隧道", "L :1", "TCP", "127.0.0.1:1", "a:1")
		r.Add(e)
		e.Close(ReasonNormal)
	}

	// 注意：全程没有调用 Sample()
	if got := r.Len(); got > 100 {
		t.Fatalf("未调用 Sample 时已关闭记录也应被摊薄回收，实际残留 %d 条", got)
	}
}

// fakeRW 只报告长度、不真正搬运数据
type fakeRW struct{}

func (f *fakeRW) Read(p []byte) (int, error)  { return len(p), nil }
func (f *fakeRW) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeRW) Close() error                { return nil }

// fakeCloser 记录自己是否被关闭
type fakeCloser struct {
	closed  bool
	onClose func()
}

func (f *fakeCloser) Close() error {
	f.closed = true
	if f.onClose != nil {
		f.onClose()
	}
	return nil
}

// TestSetUpstreamReplacesInsteadOfAppending 动态转发的 HTTP keep-alive 下，
// 同一个客户端 socket 每来一条请求都会新建一个上游连接。若用 AddCloser 追加，
// closers 会随请求数无界增长——一条跑了几万次请求的长连接就挂着几万个
// 已经关闭的连接对象，直到保留期结束才释放。
func TestSetUpstreamReplacesInsteadOfAppending(t *testing.T) {
	e := NewEntry("c1", "t1", "隧道", "D 127.0.0.1:1080", "HTTP", "127.0.0.1:1", "a:1")

	var closed int
	for i := 0; i < 5000; i++ {
		e.SetUpstream(&fakeCloser{onClose: func() { closed++ }})
	}

	e.mu.Lock()
	n := len(e.closers)
	e.mu.Unlock()
	if n != 0 {
		t.Fatalf("closers 不应堆积，实际 %d 个", n)
	}

	if !e.Kill() {
		t.Fatal("Kill 应返回 true")
	}
	if closed != 1 {
		t.Fatalf("只应关闭最新的上游连接，实际关闭 %d 个", closed)
	}
}

// TestSetUpstreamAfterKillClosesImmediately 断开之后才登记的上游连接必须立刻关掉，
// 否则那条 SSH 通道会一直挂着没人管（拨号完成得比用户点断开更晚的情况）。
func TestSetUpstreamAfterKillClosesImmediately(t *testing.T) {
	e := NewEntry("c1", "t1", "隧道", "D 127.0.0.1:1080", "HTTP", "127.0.0.1:1", "a:1")
	e.Kill()

	c := &fakeCloser{}
	e.SetUpstream(c)
	if !c.closed {
		t.Fatal("已断开的连接不应再接收新的上游连接")
	}
}
