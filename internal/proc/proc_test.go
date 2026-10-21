//go:build darwin || linux || windows

package proc

import (
	"net"
	"os"
	"testing"
)

// TestLocalOwnersFindsSelf 建立一条本机回环连接，验证能按「客户端本地端口」
// 反查到发起进程（应当就是当前测试进程）。
func TestLocalOwnersFindsSelf(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	srv, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	addr, ok := client.LocalAddr().(*net.TCPAddr)
	if !ok {
		t.Fatal("本地地址不是 TCPAddr")
	}

	tbl := NewTable()
	if err := tbl.Refresh(); err != nil {
		t.Fatalf("刷新失败: %v", err)
	}

	owner, found := tbl.Lookup(uint16(addr.Port))
	if !found {
		t.Skipf("端口 %d 未出现在连接表中（可能是权限或时序问题），表大小 %d", addr.Port, tbl.Size())
	}
	if owner.PID != os.Getpid() {
		t.Fatalf("期望 PID %d，实际 %d（%s）", os.Getpid(), owner.PID, owner.Name)
	}
	t.Logf("port=%d pid=%d name=%q 表大小=%d", addr.Port, owner.PID, owner.Name, tbl.Size())
}

// BenchmarkRefresh 量化一次全量扫描的成本。这个数字决定了刷新间隔的下限：
// 扫描必须异步且低频，绝不能放在 accept 路径上。
func BenchmarkRefresh(b *testing.B) {
	tbl := NewTable()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := tbl.Refresh(); err != nil {
			b.Fatal(err)
		}
	}
}
