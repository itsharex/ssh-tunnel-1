// Package conn 记录运行中的转发连接，供界面展示流量、速率并支持手动断开。
//
// 一条 Entry 对应一个「客户端侧 socket」。三个转发方向的客户端侧分别是：
//
//	-L 本地转发：本机监听 accept 到的连接（发起方是本机进程）
//	-D 动态转发：同上
//	-R 远程转发：SSH 服务器侧转过来的通道（发起方是远端/局域网的外部主机）
//
// 计数口径统一为：up = 客户端 → 目标，down = 目标 → 客户端。
// 实现上在数据真正流经的那一端包装读写，而不是在 socket 上统一包装——
// 动态转发里 bufio 会先把头读进缓冲区再原样写出，统一包装会导致请求头被计两次。
package conn

import (
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// State 连接状态
type State string

const (
	StateActive State = "active"
	StateClosed State = "closed"
)

// Reason 连接关闭原因
type Reason string

const (
	ReasonNormal     Reason = "normal" // 连接正常结束（对端关闭 / 半关闭后收尾）
	ReasonUser       Reason = "user"   // 用户在界面上断开
	ReasonDialFailed Reason = "dial"   // 拨号到目标失败
	ReasonTunnelStop Reason = "tunnel" // 隧道停止
)

// Entry 一条连接记录。
//
// ID/TunnelID/Tunnel/Forward/Src/Started 创建后只读，无需加锁；
// Proto/Dst 在动态转发里要等协议头解析完才知道，用原子值承载；
// PID/Proc 由进程查询异步补齐；状态与 closers 由 mu 保护。
type Entry struct {
	ID          string
	seq         uint64 // 创建序号，仅用于稳定排序
	TunnelID    string
	Tunnel      string
	Forward     string // 形如 "L 127.0.0.1:8080"
	Src         string // 客户端地址
	Started     time.Time
	LocalClient bool   // 客户端是否在本机（决定要不要查进程）
	LocalPort   uint16 // 客户端 socket 的本地端口，用于反查进程

	proto atomic.Value // string
	dst   atomic.Value // string

	up   atomic.Int64 // 客户端 → 目标
	down atomic.Int64 // 目标 → 客户端

	lastUp   atomic.Int64
	lastDown atomic.Int64
	rateUp   atomic.Int64
	rateDown atomic.Int64
	lastAt   atomic.Int64 // 上一样本时刻（unix 纳秒），用于识别「停看过久」

	pid  atomic.Int32
	proc atomic.Value // string

	mu       sync.Mutex
	state    State
	reason   Reason
	closedAt time.Time
	closers  []io.Closer
	upstream io.Closer // 当前上游连接，动态转发下会被替换，只保留最新一个

	// reg 反向引用登记表，仅用于关闭时摊薄清理成本，可为 nil。
	// 在登记之后才被其它 goroutine 看到，写入早于发布，无需加锁。
	reg *Registry
}

// NewEntry 创建一条连接记录
func NewEntry(id, tunnelID, tunnel, forward, proto, src, dst string) *Entry {
	e := &Entry{
		ID:       id,
		TunnelID: tunnelID,
		Tunnel:   tunnel,
		Forward:  forward,
		Src:      src,
		Started:  time.Now(),
		state:    StateActive,
	}
	e.proto.Store(proto)
	e.dst.Store(dst)
	return e
}

// SetProto 更新协议名（动态转发解析出协议头后调用）
func (e *Entry) SetProto(p string) {
	if e != nil {
		e.proto.Store(p)
	}
}

// SetDst 更新目标地址（动态转发解析出目标后调用；
// HTTP 代理 keep-alive 下同一 socket 会换目标，以最后一次为准）
func (e *Entry) SetDst(d string) {
	if e != nil {
		e.dst.Store(d)
	}
}

// Proto 返回协议名
func (e *Entry) Proto() string {
	if e == nil {
		return ""
	}
	if v, ok := e.proto.Load().(string); ok {
		return v
	}
	return ""
}

// Dst 返回目标地址
func (e *Entry) Dst() string {
	if e == nil {
		return ""
	}
	if v, ok := e.dst.Load().(string); ok {
		return v
	}
	return ""
}

// AddCloser 登记一个「断开这条连接」时需要关闭的对象。
// 通常把客户端侧与目标侧的原始连接都登记进来，Kill 时一起关掉，
// 这样 pipe 里的两个 io.Copy 会立刻返回，不会残留半开连接。
func (e *Entry) AddCloser(cs ...io.Closer) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.state == StateClosed {
		e.mu.Unlock()
		for _, c := range cs {
			_ = c.Close()
		}
		return
	}
	e.closers = append(e.closers, cs...)
	e.mu.Unlock()
}

// SetUpstream 登记「当前上游连接」，重复调用时替换而不是追加。
//
// 动态转发的 HTTP keep-alive 下，同一个客户端 socket 上每来一条请求都会新建
// 一个上游连接。若用 AddCloser 追加，closers 会随请求数无界增长——一条跑了几万
// 次请求的长连接就会在记录里挂着几万个已经关闭的连接对象，直到保留期结束才释放。
// 断开时只需要掐断「正在传输的那一条」，所以这里只保留最新一个。
func (e *Entry) SetUpstream(c io.Closer) {
	if e == nil || c == nil {
		return
	}
	e.mu.Lock()
	if e.state == StateClosed {
		e.mu.Unlock()
		_ = c.Close()
		return
	}
	e.upstream = c
	e.mu.Unlock()
}

// Up 返回累计上行字节数
func (e *Entry) Up() int64 { return e.up.Load() }

// Down 返回累计下行字节数
func (e *Entry) Down() int64 { return e.down.Load() }

// SetPID 由进程查询异步回填进程信息
func (e *Entry) SetPID(pid int, name string) {
	e.pid.Store(int32(pid))
	e.proc.Store(name)
}

// Status 返回状态、原因与关闭时间
func (e *Entry) Status() (State, Reason, time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state, e.reason, e.closedAt
}

// Close 仅标记关闭，不主动关闭连接。由转发路径在 pipe 返回后调用。
// 已经被关闭过（例如用户手动断开）时不覆盖原有原因。
//
// 关闭时顺手丢掉对两端连接的引用：转发路径的 defer 已经把 socket 关掉了，
// 这里再留着只是让一批已关闭的对象陪跑到清理为止。登记表也借此机会
// 摊薄地清理过期记录——用户可能从不打开监控页，Sample 一直不会被调用。
func (e *Entry) Close(r Reason) {
	if e == nil {
		return
	}
	e.mu.Lock()
	changed := e.state != StateClosed
	if changed {
		e.state = StateClosed
		e.reason = r
		e.closedAt = time.Now()
		e.closers = nil
		e.upstream = nil
	}
	e.mu.Unlock()
	if changed {
		e.notifyClosed()
	}
}

// notifyClosed 告知登记表有记录进入已关闭状态。
func (e *Entry) notifyClosed() {
	if e.reg != nil {
		e.reg.noteClosed()
	}
}

// Kill 主动断开：标记为用户断开并关闭所有已登记的连接。
// 返回 false 表示这条连接已经是关闭状态。
func (e *Entry) Kill() bool { return e.KillWith(ReasonUser) }

// KillWith 主动断开并指定关闭原因（隧道停止时用 ReasonTunnelStop）。
func (e *Entry) KillWith(r Reason) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if e.state == StateClosed {
		e.mu.Unlock()
		return false
	}
	e.state = StateClosed
	e.reason = r
	e.closedAt = time.Now()
	cs := e.closers
	e.closers = nil
	if e.upstream != nil {
		cs = append(cs, e.upstream)
		e.upstream = nil
	}
	e.mu.Unlock()

	for _, c := range cs {
		_ = c.Close()
	}
	e.notifyClosed()
	return true
}

// maxSampleGap 允许的最大采样间隔。超过它说明监控页关过一段时间
// （采样只发生在页面打开时），期间的累计字节不能算进「当前速率」。
const maxSampleGap = 3 * time.Second

// sample 计算自上一样本以来的字节增量，写入速率字段。
func (e *Entry) sample() { e.sampleAt(time.Now()) }

// sampleAt 同 sample，但由调用方统一提供当前时刻——
// 一次采样要处理成百上千条记录，每条都调一次 time.Now() 纯属浪费
// （500 条实测多花约 17µs）。
//
// 首轮与「停看过久」这两种情况只重建基准、速率归零：没有上一样本就算不出速率，
// 把累计总量当成 1 秒的量会蹦出一个吓人的虚高值（重开监控页时最容易看到）。
func (e *Entry) sampleAt(now time.Time) {
	u := e.up.Load()
	d := e.down.Load()

	prev := e.lastAt.Swap(now.UnixNano())
	if prev == 0 || now.Sub(time.Unix(0, prev)) > maxSampleGap {
		e.lastUp.Store(u)
		e.lastDown.Store(d)
		e.rateUp.Store(0)
		e.rateDown.Store(0)
		return
	}

	e.rateUp.Store(u - e.lastUp.Swap(u))
	e.rateDown.Store(d - e.lastDown.Swap(d))
}

// countedConn 包装连接：读记上行、写记下行。
type countedConn struct {
	inner io.ReadWriteCloser
	e     *Entry
}

func (c *countedConn) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	if n > 0 {
		c.e.up.Add(int64(n))
	}
	return n, err
}

func (c *countedConn) Write(p []byte) (int, error) {
	n, err := c.inner.Write(p)
	if n > 0 {
		c.e.down.Add(int64(n))
	}
	return n, err
}

func (c *countedConn) Close() error { return c.inner.Close() }

// CloseWrite 透传底层半关闭能力，保持 pipe 的 EOF 传播语义不变。
// 底层不支持时退化为整条关闭——与 forward 包里 closeWrite 的兜底一致。
func (c *countedConn) CloseWrite() error {
	if cw, ok := c.inner.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.inner.Close()
}

// Wrap 把连接包成计数连接。e 为 nil 时原样返回，方便在未启用监控时零成本降级。
func (e *Entry) Wrap(c io.ReadWriteCloser) io.ReadWriteCloser {
	if e == nil {
		return c
	}
	return &countedConn{inner: c, e: e}
}

// countingReader 只计上行
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n.Add(int64(n))
	}
	return n, err
}

// countingWriter 只计一个方向
type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.n.Add(int64(n))
	}
	return n, err
}

// UpReader 返回计入上行字节的 Reader。用于 bufio 已经把数据缓冲在内存里的场景：
// 此时不能靠包装 socket 计数，只能在真正消费缓冲区的地方计数。
func (e *Entry) UpReader(r io.Reader) io.Reader {
	if e == nil {
		return r
	}
	return countingReader{r: r, n: &e.up}
}

// UpWriter 返回计入上行字节的 Writer
func (e *Entry) UpWriter(w io.Writer) io.Writer {
	if e == nil {
		return w
	}
	return countingWriter{w: w, n: &e.up}
}

// DownWriter 返回计入下行字节的 Writer
func (e *Entry) DownWriter(w io.Writer) io.Writer {
	if e == nil {
		return w
	}
	return countingWriter{w: w, n: &e.down}
}

// PortOf 从网络地址里取端口，取不到返回 0
func PortOf(addr net.Addr) uint16 {
	if addr == nil {
		return 0
	}
	if ta, ok := addr.(*net.TCPAddr); ok && ta != nil {
		return uint16(ta.Port)
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return 0
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(p)
}
