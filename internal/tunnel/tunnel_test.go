package tunnel

import (
	"net"
	"strconv"
	"testing"
	"time"

	"ssh-tunnel/internal/config"
)

// silentTCPServer 起一个只 accept、永不发送任何数据的服务端，
// 模拟「端口能连通但不是 SSH」的黑洞场景。
func silentTCPServer(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	conns := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(conns)
				return
			}
			conns <- c
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		for c := range conns {
			_ = c.Close()
		}
	})
	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	port, err = strconv.Atoi(p)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return h, port
}

// Stop 必须能打断卡在 SSH 握手阶段的连接，否则停止隧道、退出程序都会永久阻塞。
func TestStopInterruptsHandshake(t *testing.T) {
	host, port := silentTCPServer(t)
	tun := New(config.Tunnel{
		ID:            "test",
		Host:          host,
		Port:          port,
		User:          "u",
		AuthType:      config.AuthPassword,
		Password:      "p",
		AutoReconnect: false,
	}, nil)

	if err := tun.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// 等握手真的卡住（TCP 已连上、banner 未到）
	time.Sleep(300 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		tun.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() 未能打断握手，阻塞超过 3s")
	}

	if s, _, _ := tun.Status(); s != config.StatusStopped {
		t.Fatalf("status = %s, want stopped", s)
	}
	// 停止后应能再次启动（stopCh/doneCh 已被正确清理）
	if err := tun.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	tun.Stop()
}
