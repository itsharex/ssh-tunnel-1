package tunnel

import (
	"io"
	"net"
	"testing"
	"time"
)

// pipe 必须在单向 EOF 时把半关闭传播到对端，而不是提前整条断开。
// 回归用例：客户端写完请求后半关闭写端，服务端读到 EOF 后才回响应。
func TestPipeHalfClose(t *testing.T) {
	newPair := func(t *testing.T) (net.Conn, net.Conn) {
		t.Helper()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer ln.Close()
		accepted := make(chan net.Conn, 1)
		go func() {
			c, err := ln.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- c
		}()
		dialed, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		peer, ok := <-accepted
		if !ok {
			t.Fatalf("accept failed")
		}
		return dialed, peer
	}

	client, pipeA := newPair(t)  // 客户端 <-> pipe 的一端
	pipeB, server := newPair(t)  // pipe 的另一端 <-> 服务端
	defer client.Close()
	defer server.Close()

	// 服务端：读到 EOF 后再回写响应
	go func() {
		_, _ = io.Copy(io.Discard, server)
		_, _ = server.Write([]byte("response"))
		_ = server.Close()
	}()

	go pipe(pipeA, pipeB)

	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}

	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "response" {
		t.Fatalf("got %q, want %q", string(got), "response")
	}
}
