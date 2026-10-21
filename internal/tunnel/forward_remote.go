package tunnel

import (
	"fmt"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
	"ssh-tunnel/internal/config"
	"ssh-tunnel/internal/conn"
)

// startRemote 远程转发 -R listen:targetHost:targetPort
// 在远端（SSH 服务器侧）监听 listen，每个连接转发到本地 target
func (t *Tunnel) startRemote(client *ssh.Client, f config.Forward, meta connMeta) error {
	listen := normalizeAddr(f.Listen)
	if listen == "" {
		return fmt.Errorf("远端监听地址为空")
	}
	target := f.Target
	if target == "" {
		return fmt.Errorf("本地目标地址为空")
	}
	// 规范化 target 为 host:port。只把「纯端口」当作简写补上 localhost，
	// 其余（如裸 IPv6 "::1"）直接报错，避免拼出 localhost:::1 这种非法地址。
	if _, _, err := net.SplitHostPort(target); err != nil {
		if _, perr := strconv.Atoi(target); perr != nil {
			return fmt.Errorf("目标地址 %q 缺少端口，应为 host:port", target)
		}
		target = net.JoinHostPort("localhost", target)
	}

	ln, err := client.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("远端监听 %s 失败: %w", listen, err)
	}
	t.mu.Lock()
	t.listeners = append(t.listeners, ln)
	t.mu.Unlock()

	desc := "R " + listen
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go t.handleRemoteConn(c, target, meta, desc)
		}
	}()
	return nil
}

func (t *Tunnel) handleRemoteConn(remote net.Conn, target string, meta connMeta, forward string) {
	defer remote.Close()

	// 客户端在 SSH 服务器那一侧（可能是局域网/公网来的外部主机），
	// 本机没有对应进程，只记录来源 IP，不查进程。
	e := t.newEntry(meta, forward, "TCP", remote.RemoteAddr(), false)
	e.SetDst(target)

	local, err := net.Dial("tcp", target)
	if err != nil {
		e.Close(conn.ReasonDialFailed)
		return
	}
	defer local.Close()

	e.AddCloser(remote, local)
	// remote 是客户端侧：读记上行（外部 → 本地服务），写记下行
	pipe(e.Wrap(remote), local)
	e.Close(conn.ReasonNormal)
}
