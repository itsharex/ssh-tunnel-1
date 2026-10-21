package tunnel

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"ssh-tunnel/internal/config"
	"ssh-tunnel/internal/conn"
)

// startDynamicTunnel 起一条只做 -D 动态转发的隧道，返回登记表与监听地址
func startDynamicTunnel(t *testing.T) (*conn.Registry, string) {
	t.Helper()
	host, sshPort := startTestSSHD(t)
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))

	cfg := config.Tunnel{
		ID:       "t-dyn",
		Name:     "动态测试",
		Host:     host,
		Port:     sshPort,
		User:     "tester",
		AuthType: config.AuthPassword,
		Password: "x",
		Forwards: []config.Forward{{
			Type:   config.ForwardDynamic,
			Listen: listen,
		}},
	}

	reg := conn.NewRegistry()
	tn := New(cfg, nil)
	tn.reg = reg
	if err := tn.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tn.Stop)
	return reg, listen
}

func dynWrite(t *testing.T, c net.Conn, p []byte) {
	t.Helper()
	if _, err := c.Write(p); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
}

func dynRead(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("读取 %d 字节失败: %v", n, err)
	}
	return buf
}

// waitActive 等登记表里出现 n 条活跃记录，只返回活跃的那些。
//
// 只取活跃记录是有意的：探测端口可用性的那次 dial 也会被登记成一条
// （随后立刻关闭），不该干扰「一个 socket 一条记录」的断言。
func waitActive(t *testing.T, reg *conn.Registry, n int) []conn.Snapshot {
	t.Helper()
	activeOnly := func() []conn.Snapshot {
		var out []conn.Snapshot
		for _, s := range reg.Snapshot() {
			if s.State == conn.StateActive {
				out = append(out, s)
			}
		}
		return out
	}
	for i := 0; i < 100; i++ {
		reg.Sample()
		if got := activeOnly(); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	reg.Sample()
	return activeOnly()
}

// TestDynamicSOCKS5 SOCKS5 CONNECT 全流程 + 记录里的协议/目标/计数
func TestDynamicSOCKS5(t *testing.T) {
	reg, listen := startDynamicTunnel(t)
	echoAddr := startEchoServer(t)

	echoHost, echoPortStr, err := net.SplitHostPort(echoAddr)
	if err != nil {
		t.Fatal(err)
	}
	echoPort, _ := strconv.Atoi(echoPortStr)
	ip := net.ParseIP(echoHost).To4()
	if ip == nil {
		t.Fatalf("回显服务地址不是 IPv4: %s", echoAddr)
	}

	c := waitPort(t, listen)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	// 方法协商：只提供「无认证」
	dynWrite(t, c, []byte{0x05, 0x01, 0x00})
	if got := dynRead(t, c, 2); !bytes.Equal(got, []byte{0x05, 0x00}) {
		t.Fatalf("方法协商失败: %v", got)
	}

	// CONNECT 请求（IPv4）
	req := append([]byte{0x05, 0x01, 0x00, 0x01}, ip...)
	req = append(req, byte(echoPort>>8), byte(echoPort))
	dynWrite(t, c, req)
	reply := dynRead(t, c, 10)
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("CONNECT 失败: %v", reply)
	}

	payload := []byte("socks-echo-payload")
	dynWrite(t, c, payload)
	if got := dynRead(t, c, len(payload)); !bytes.Equal(got, payload) {
		t.Fatalf("回显不一致: %q", got)
	}

	snap := waitActive(t, reg, 1)
	if len(snap) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d 条", len(snap))
	}
	s := snap[0]
	if s.Proto != "SOCKS5" {
		t.Errorf("协议应为 SOCKS5，实际 %q", s.Proto)
	}
	if s.Dst != echoAddr {
		t.Errorf("目标应为 %q，实际 %q", echoAddr, s.Dst)
	}
	if s.Up < int64(len(payload)) || s.Down < int64(len(payload)) {
		t.Errorf("流量计数偏小: up=%d down=%d，期望各 >= %d", s.Up, s.Down, len(payload))
	}
	if s.Forward != "D "+listen {
		t.Errorf("转发描述错误: %q", s.Forward)
	}
}

// TestDynamicHTTPProxyKeepAlive 同一个客户端 socket 上连续多次普通 HTTP 请求。
// 这条路径曾经有个隐患：上游连接没登记进记录，用户点「断开」时只关了客户端 socket，
// 卡在等响应的 goroutine 会一直挂着。同时验证「一个 socket 一条记录」的口径。
func TestDynamicHTTPProxyKeepAlive(t *testing.T) {
	reg, listen := startDynamicTunnel(t)
	// 等监听器就绪。这次 dial 会被登记成一条随后关闭的记录，
	// 断言只看活跃记录，不受影响。
	waitPort(t, listen).Close()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = io.WriteString(w, "ok:"+r.URL.Path)
	}))
	defer srv.Close()

	proxyURL, err := url.Parse("http://" + listen)
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		DisableKeepAlives:   false,
		MaxIdleConnsPerHost: 1,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	// 串行发多次，复用同一个到代理的连接
	for i := 0; i < 3; i++ {
		path := "/p" + strconv.Itoa(i)
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("第 %d 次请求失败: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != "ok:"+path {
			t.Fatalf("第 %d 次响应错误: %q", i, body)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("源站应收到 3 次请求，实际 %d", got)
	}

	snap := waitActive(t, reg, 1)
	if len(snap) != 1 {
		t.Fatalf("一个客户端 socket 应只有 1 条记录，实际 %d 条", len(snap))
	}
	s := snap[0]
	if s.Proto != "HTTP" {
		t.Errorf("协议应为 HTTP，实际 %q", s.Proto)
	}
	if s.Dst != srv.Listener.Addr().String() {
		t.Errorf("目标应为 %q，实际 %q", srv.Listener.Addr().String(), s.Dst)
	}
	if s.Up == 0 || s.Down == 0 {
		t.Errorf("流量计数为 0: up=%d down=%d", s.Up, s.Down)
	}
}

// TestDynamicProbeLeavesUnidentifiedRecord 连上就断的探测连接（端口扫描之类）
// 会留下一条记录，但协议必须是「未识别」而不是被错误地标成 TCP——
// 它根本没来得及说自己是什么协议。
func TestDynamicProbeLeavesUnidentifiedRecord(t *testing.T) {
	reg, listen := startDynamicTunnel(t)

	c := waitPort(t, listen)
	_ = c.Close()

	var snap []conn.Snapshot
	for i := 0; i < 100; i++ {
		reg.Sample()
		snap = reg.Snapshot()
		if len(snap) == 1 && snap[0].State == conn.StateClosed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(snap) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d 条", len(snap))
	}
	if snap[0].Proto != "" {
		t.Errorf("未识别协议的探测连接不应标成 %q", snap[0].Proto)
	}
	if snap[0].Dst != "" {
		t.Errorf("未解析出目标时目标应为空，实际 %q", snap[0].Dst)
	}
}

// TestDynamicHTTPConnect HTTPS 走 CONNECT 隧道
func TestDynamicHTTPConnect(t *testing.T) {
	reg, listen := startDynamicTunnel(t)
	echoAddr := startEchoServer(t)

	c := waitPort(t, listen)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	dynWrite(t, c, []byte("CONNECT "+echoAddr+" HTTP/1.1\r\nHost: "+echoAddr+"\r\n\r\n"))
	// 读到空行为止（200 Connection established）
	br := make([]byte, 0, 128)
	one := make([]byte, 1)
	for {
		if _, err := io.ReadFull(c, one); err != nil {
			t.Fatalf("读取 CONNECT 响应失败: %v", err)
		}
		br = append(br, one[0])
		if len(br) >= 4 && string(br[len(br)-4:]) == "\r\n\r\n" {
			break
		}
		if len(br) > 512 {
			t.Fatalf("CONNECT 响应异常: %q", br)
		}
	}
	if !bytes.Contains(br, []byte("200")) {
		t.Fatalf("CONNECT 未成功: %q", br)
	}

	payload := []byte("connect-echo")
	dynWrite(t, c, payload)
	if got := dynRead(t, c, len(payload)); !bytes.Equal(got, payload) {
		t.Fatalf("回显不一致: %q", got)
	}

	snap := waitActive(t, reg, 1)
	if len(snap) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d 条", len(snap))
	}
	if snap[0].Proto != "HTTPS" {
		t.Errorf("协议应为 HTTPS，实际 %q", snap[0].Proto)
	}
	if snap[0].Dst != echoAddr {
		t.Errorf("目标应为 %q，实际 %q", echoAddr, snap[0].Dst)
	}
}
