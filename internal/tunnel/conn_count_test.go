package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh-tunnel/internal/config"
	"ssh-tunnel/internal/conn"
)

// countEstablished 用 lsof 数出指定监听端口上真实处于 ESTABLISHED 的 socket 数，
// 作为独立于我们自己的计数的「外部真相」。
func countEstablished(t *testing.T, port int) (int, bool) {
	t.Helper()
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:ESTABLISHED").Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return 0, false // lsof 不存在
		}
		// lsof 无匹配时返回 1，视为 0
		return 0, true
	}
	n := 0
	// lsof 会把连接的两端都列出来（客户端一行、服务端一行），
	// 只数「本地端是这个端口」的那些行，否则会翻倍。
	needle := ":" + strconv.Itoa(port) + "->"
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "ESTABLISHED") && strings.Contains(line, needle) {
			n++
		}
	}
	return n, true
}

func activeSnapshots(reg *conn.Registry) []conn.Snapshot {
	var out []conn.Snapshot
	for _, s := range reg.Snapshot() {
		if s.State == conn.StateActive {
			out = append(out, s)
		}
	}
	return out
}

// TestConnectionCountMatchesOS 验证「界面上的活跃连接数」等于「系统里真实的 socket 数」。
// 用 keep-alive 的长连接模拟浏览器打开一个页面后保持多条并发连接的情形。
func TestConnectionCountMatchesOS(t *testing.T) {
	host, sshPort := startTestSSHD(t)

	// 目标服务：一个正常的 HTTP 服务，默认 keep-alive
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpLn.Close()
	go func() { _ = (&http.Server{Handler: mux}).Serve(httpLn) }()

	listenPort := freePort(t)
	listen := "127.0.0.1:" + strconv.Itoa(listenPort)

	cfg := config.Tunnel{
		ID:       "t-count",
		Name:     "计数验证",
		Host:     host,
		Port:     sshPort,
		User:     "tester",
		AuthType: config.AuthPassword,
		Password: "x",
		Forwards: []config.Forward{{
			Type:   config.ForwardLocal,
			Listen: listen,
			Target: httpLn.Addr().String(),
		}},
	}

	reg := conn.NewRegistry()
	tn := New(cfg, nil)
	tn.reg = reg
	if err := tn.Start(); err != nil {
		t.Fatal(err)
	}
	defer tn.Stop()

	// 模拟浏览器：并发 7 条 keep-alive 长连接，每条取一个资源后保持不关
	const want = 7
	waitPort(t, listen).Close() // 等监听就绪
	conns := make([]net.Conn, 0, want)
	for i := 0; i < want; i++ {
		c, err := net.Dial("tcp", listen)
		if err != nil {
			t.Fatalf("第 %d 条连接失败: %v", i, err)
		}
		fmt.Fprintf(c, "GET /r%d HTTP/1.1\r\nHost: localhost\r\n\r\n", i)
		br := bufio.NewReader(c)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("第 %d 条读取响应失败: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		conns = append(conns, c)
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()

	// 等记录就位
	var active []conn.Snapshot
	for i := 0; i < 100; i++ {
		active = activeSnapshots(reg)
		if len(active) == want {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	osCount, ok := countEstablished(t, listenPort)
	t.Logf("界面活跃连接数 = %d，lsof ESTABLISHED = %d（%v）", len(active), osCount, ok)

	if len(active) != want {
		t.Fatalf("应记录 %d 条连接，实际 %d 条", want, len(active))
	}
	if ok && osCount != len(active) {
		t.Fatalf("与系统实际 socket 数不一致：界面 %d，lsof %d", len(active), osCount)
	}

	// 关掉 3 条，界面上的活跃数应当同步下降，不留僵尸
	for i := 0; i < 3; i++ {
		_ = conns[i].Close()
	}
	for i := 0; i < 150; i++ {
		if len(activeSnapshots(reg)) == want-3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	after := activeSnapshots(reg)
	osAfter, ok2 := countEstablished(t, listenPort)
	t.Logf("关闭 3 条后：界面活跃 = %d，lsof = %d（%v）", len(after), osAfter, ok2)
	if len(after) != want-3 {
		t.Fatalf("关闭 3 条后应剩 %d 条活跃，实际 %d 条", want-3, len(after))
	}
	if ok2 && osAfter != len(after) {
		t.Fatalf("关闭后与系统实际 socket 数不一致：界面 %d，lsof %d", len(after), osAfter)
	}
}
