package tunnel

import (
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
	"ssh-tunnel/internal/config"
	"ssh-tunnel/internal/conn"
)

// connMeta 登记连接所需的隧道元信息。
// 单独抽出来是为了不把整份配置（含密码、密钥口令）传进每条连接的 goroutine。
type connMeta struct {
	id   string
	name string
}

// displayName 隧道展示名，未填名字时退化为 user@host
func displayName(cfg config.Tunnel) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	return cfg.User + "@" + cfg.Host
}

// newEntry 创建并登记一条连接记录。
// reg 未注入时返回 nil——Entry 的方法全部 nil 安全，调用方不需要写分支。
func (t *Tunnel) newEntry(meta connMeta, forward, proto string, clientAddr net.Addr, localClient bool) *conn.Entry {
	if t.reg == nil {
		return nil
	}
	src := ""
	if clientAddr != nil {
		src = clientAddr.String()
	}
	e := t.reg.New(meta.id, meta.name, forward, proto, src, "")
	e.LocalClient = localClient
	if localClient {
		e.LocalPort = conn.PortOf(clientAddr)
	}
	t.reg.Add(e)
	return e
}

// startOneForward 根据类型启动单个转发
func (t *Tunnel) startOneForward(client *ssh.Client, f config.Forward, meta connMeta) error {
	switch f.Type {
	case config.ForwardLocal:
		return t.startLocal(client, f, meta)
	case config.ForwardRemote:
		return t.startRemote(client, f, meta)
	case config.ForwardDynamic:
		return t.startDynamic(client, f, meta)
	default:
		return fmt.Errorf("未知转发类型: %s", f.Type)
	}
}

// startLocal 本地转发 -L listen:targetHost:targetPort
// 在本地监听 listen，每个连接通过 ssh 拨号到 target
func (t *Tunnel) startLocal(client *ssh.Client, f config.Forward, meta connMeta) error {
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

	desc := "L " + listen
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go t.handleLocalConn(client, c, target, meta, desc)
		}
	}()
	return nil
}

func (t *Tunnel) handleLocalConn(client *ssh.Client, local net.Conn, target string, meta connMeta, forward string) {
	defer local.Close()

	// 客户端在本机（-L 的发起方是本机进程），可以按本地端口反查进程
	e := t.newEntry(meta, forward, "TCP", local.RemoteAddr(), true)
	e.SetDst(target)

	remote, err := client.Dial("tcp", target)
	if err != nil {
		// 拨号失败也保留一条记录：这类「连上了但没通」恰恰是最需要被看到的
		e.Close(conn.ReasonDialFailed)
		return
	}
	defer remote.Close()

	e.AddCloser(local, remote)
	pipe(e.Wrap(local), remote)
	e.Close(conn.ReasonNormal)
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
