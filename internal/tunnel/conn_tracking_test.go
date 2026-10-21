package tunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"ssh-tunnel/internal/config"
	"ssh-tunnel/internal/conn"
)

// startTestSSHD 起一个最小 SSH 服务端，只实现 direct-tcpip（对应 -L 转发）。
// 返回监听主机与端口。
func startTestSSHD(tb testing.TB) (string, int) {
	tb.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		tb.Fatal(err)
	}
	srvCfg := &ssh.ServerConfig{NoClientAuth: true}
	srvCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveTestSSHConn(c, srvCfg)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func serveTestSSHConn(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)

	for nc := range chans {
		if nc.ChannelType() != "direct-tcpip" {
			_ = nc.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		var p struct {
			Host       string
			Port       uint32
			OriginHost string
			OriginPort uint32
		}
		if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, "bad payload")
			continue
		}
		go func(nc ssh.NewChannel, host string, port uint32) {
			target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
			if err != nil {
				_ = nc.Reject(ssh.ConnectionFailed, err.Error())
				return
			}
			ch, reqs2, err := nc.Accept()
			if err != nil {
				_ = target.Close()
				return
			}
			go ssh.DiscardRequests(reqs2)
			pipe(ch, target)
			_ = target.Close()
		}(nc, p.Host, p.Port)
	}
}

// freePort 取一个当前空闲的本地端口
func freePort(tb testing.TB) int {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return p
}

// startEchoServer 起一个原样回显的 TCP 服务，便于校验双向流量对称
func startEchoServer(tb testing.TB) string {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(c, c)
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// waitPort 等待本地端口可连接
func waitPort(tb testing.TB, addr string) net.Conn {
	tb.Helper()
	var lastErr error
	for i := 0; i < 100; i++ {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			return c
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	tb.Fatalf("端口 %s 一直不可连接: %v", addr, lastErr)
	return nil
}

// TestConnectionTracking 覆盖「登记 → 计数 → 速率 → 主动断开」整条链路
func TestConnectionTracking(t *testing.T) {
	host, sshPort := startTestSSHD(t)
	echoAddr := startEchoServer(t)
	listenPort := freePort(t)
	listen := "127.0.0.1:" + strconv.Itoa(listenPort)

	cfg := config.Tunnel{
		ID:       "t-track",
		Name:     "测试隧道",
		Host:     host,
		Port:     sshPort,
		User:     "tester",
		AuthType: config.AuthPassword,
		Password: "x",
		Forwards: []config.Forward{{
			Type:   config.ForwardLocal,
			Listen: listen,
			Target: echoAddr,
		}},
	}

	reg := conn.NewRegistry()
	tn := New(cfg, nil)
	tn.reg = reg
	if err := tn.Start(); err != nil {
		t.Fatal(err)
	}
	defer tn.Stop()

	c := waitPort(t, listen)
	defer c.Close()

	payload := []byte("hello-ssh-tunnel")
	if _, err := c.Write(payload); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	echoed := make([]byte, len(payload))
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(c, echoed); err != nil {
		t.Fatalf("读回显失败: %v", err)
	}
	if string(echoed) != string(payload) {
		t.Fatalf("回显不一致: %q", echoed)
	}

	// 等记录出现且字节数到位
	var snap []conn.Snapshot
	for i := 0; i < 100; i++ {
		reg.Sample()
		snap = reg.Snapshot()
		if len(snap) == 1 && snap[0].Up >= int64(len(payload)) && snap[0].Down >= int64(len(payload)) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(snap) != 1 {
		t.Fatalf("期望 1 条连接记录，实际 %d 条", len(snap))
	}

	got := snap[0]
	if got.State != conn.StateActive {
		t.Errorf("状态应为 active，实际 %q", got.State)
	}
	if got.Proto != "TCP" {
		t.Errorf("协议应为 TCP，实际 %q", got.Proto)
	}
	if got.Tunnel != "测试隧道" || got.TunnelID != "t-track" {
		t.Errorf("隧道信息错误: %q / %q", got.Tunnel, got.TunnelID)
	}
	if got.Forward != "L "+listen {
		t.Errorf("转发描述错误: %q", got.Forward)
	}
	if got.Dst != echoAddr {
		t.Errorf("目标地址错误: %q，期望 %q", got.Dst, echoAddr)
	}
	if got.Src == "" {
		t.Error("来源地址为空")
	}
	if got.Up != int64(len(payload)) || got.Down != int64(len(payload)) {
		t.Errorf("流量计数错误: up=%d down=%d，期望各 %d", got.Up, got.Down, len(payload))
	}

	// 进程信息是异步补的，给它一点时间（各平台扫描一次几十微秒到几毫秒）
	for i := 0; i < 60 && snap[0].PID == 0; i++ {
		time.Sleep(50 * time.Millisecond)
		reg.Sample()
		snap = reg.Snapshot()
	}
	if snap[0].PID == 0 {
		t.Logf("未查到进程信息（部分平台/权限下正常），跳过 PID 断言")
	} else if snap[0].PID != os.Getpid() {
		t.Errorf("PID 应为当前测试进程 %d，实际 %d (%s)", os.Getpid(), snap[0].PID, snap[0].Proc)
	} else {
		t.Logf("进程识别成功: pid=%d name=%q", snap[0].PID, snap[0].Proc)
	}

	// 主动断开
	if !reg.Kill(got.ID) {
		t.Fatal("Kill 应当返回 true")
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 16)); err == nil {
		t.Fatal("断开后连接仍然可读，说明没有真正关闭")
	}

	reg.Sample()
	after := reg.Snapshot()
	if len(after) != 1 {
		t.Fatalf("已关闭记录应在保留期内继续可见，实际 %d 条", len(after))
	}
	if after[0].State != conn.StateClosed {
		t.Errorf("状态应为 closed，实际 %q", after[0].State)
	}
	if after[0].Reason != conn.ReasonUser {
		t.Errorf("关闭原因应为 user，实际 %q", after[0].Reason)
	}
	if after[0].UpRate != 0 || after[0].DownRate != 0 {
		t.Errorf("已关闭连接不应再有速率: %d/%d", after[0].UpRate, after[0].DownRate)
	}

	// 速率：先采样建立基准，再写入，再采样一次才有增量。
	// 首轮采样只记录基准值（没有上一样本算不出速率），这一点和 1Hz 推送一致。
	c2 := waitPort(t, listen)
	defer c2.Close()
	before := reg.Len()
	for i := 0; i < 100 && reg.Len() <= before; i++ {
		time.Sleep(10 * time.Millisecond) // 等新连接登记完成
	}
	reg.Sample()

	big := make([]byte, 4096)
	if _, err := c2.Write(big); err != nil {
		t.Fatalf("第二次写入失败: %v", err)
	}
	if _, err := io.ReadFull(c2, big); err != nil {
		t.Fatalf("第二次读回显失败: %v", err)
	}
	reg.Sample()
	for _, s := range reg.Snapshot() {
		if s.State == conn.StateActive && s.UpRate < int64(len(big)) {
			t.Errorf("活跃连接速率异常: upRate=%d，期望 >= %d", s.UpRate, len(big))
		}
	}
}

// TestTunnelStopClosesConnections 验证隧道停止时会把本隧道的连接标记为
// 「隧道停止」并关闭，不残留僵尸记录
func TestTunnelStopClosesConnections(t *testing.T) {
	host, sshPort := startTestSSHD(t)
	echoAddr := startEchoServer(t)
	listenPort := freePort(t)
	listen := "127.0.0.1:" + strconv.Itoa(listenPort)

	cfg := config.Tunnel{
		ID:       "t-stop",
		Name:     "停止测试",
		Host:     host,
		Port:     sshPort,
		User:     "tester",
		AuthType: config.AuthPassword,
		Password: "x",
		Forwards: []config.Forward{{
			Type:   config.ForwardLocal,
			Listen: listen,
			Target: echoAddr,
		}},
	}

	reg := conn.NewRegistry()
	tn := New(cfg, nil)
	tn.reg = reg
	if err := tn.Start(); err != nil {
		t.Fatal(err)
	}

	c := waitPort(t, listen)
	defer c.Close()
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	for i := 0; i < 100 && reg.Len() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if reg.Len() != 1 {
		t.Fatalf("期望 1 条连接，实际 %d", reg.Len())
	}

	tn.Stop()

	reg.Sample()
	snap := reg.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("期望保留 1 条已关闭记录，实际 %d", len(snap))
	}
	if snap[0].State != conn.StateClosed {
		t.Errorf("隧道停止后连接状态应为 closed，实际 %q", snap[0].State)
	}
	if snap[0].Reason != conn.ReasonTunnelStop {
		t.Errorf("关闭原因应为 tunnel，实际 %q", snap[0].Reason)
	}
}
