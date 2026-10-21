package tunnel

import (
	"fmt"
	"io"
	"net"
	"sync"

	"ssh-tunnel/internal/config"
	"golang.org/x/crypto/ssh"
)

// startOneForward 根据类型启动单个转发
func (t *Tunnel) startOneForward(client *ssh.Client, f config.Forward) error {
	switch f.Type {
	case config.ForwardLocal:
		return t.startLocal(client, f)
	case config.ForwardRemote:
		return t.startRemote(client, f)
	case config.ForwardDynamic:
		return t.startDynamic(client, f)
	default:
		return fmt.Errorf("未知转发类型: %s", f.Type)
	}
}

// startLocal 本地转发 -L listen:targetHost:targetPort
// 在本地监听 listen，每个连接通过 ssh 拨号到 target
func (t *Tunnel) startLocal(client *ssh.Client, f config.Forward) error {
	listen := normalizeAddr(f.Listen)
	if listen == "" {
		return fmt.Errorf("监听地址为空")
	}
	target := f.Target
	if target == "" {
		return fmt.Errorf("目标地址为空")
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("本地监听 %s 失败: %w", listen, err)
	}
	t.mu.Lock()
	t.listeners = append(t.listeners, ln)
	t.mu.Unlock()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go t.handleLocalConn(client, conn, target)
		}
	}()
	return nil
}

func (t *Tunnel) handleLocalConn(client *ssh.Client, local net.Conn, target string) {
	defer local.Close()
	remote, err := client.Dial("tcp", target)
	if err != nil {
		return
	}
	defer remote.Close()
	pipe(local, remote)
}

// pipe 双向转发，两个方向都结束后才返回。
// 单个方向读到 EOF（对端半关闭写端）时，把 EOF 传播到另一侧的写方向，
// 而不是直接整条断开；否则「先发请求、再半关闭、等服务端响应」的协议
// 会在响应回来之前就被掐断。
func pipe(a, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		closeWrite(b)
	}()
	wg.Wait()
}

// closeWrite 尽力半关闭连接的写方向。*net.TCPConn 与 ssh 的 net.Conn 包装
// 都实现了 CloseWrite；不支持时退化为整条关闭，避免调用方永久等待。
func closeWrite(c io.ReadWriteCloser) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		if err := cw.CloseWrite(); err == nil {
			return
		}
	}
	_ = c.Close()
}
