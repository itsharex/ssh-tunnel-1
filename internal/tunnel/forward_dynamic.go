package tunnel

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
	"ssh-tunnel/internal/config"
	"ssh-tunnel/internal/conn"
)

// startDynamic 动态转发 -D listen
// 在本地监听 listen，同一端口同时支持 SOCKS5 与 HTTP 代理，
// 按目标地址通过 ssh 拨号。
func (t *Tunnel) startDynamic(client *ssh.Client, f config.Forward, meta connMeta) error {
	local := normalizeAddr(f.Listen)
	if local == "" {
		return fmt.Errorf("本地监听地址为空")
	}
	ln, err := net.Listen("tcp", local)
	if err != nil {
		return fmt.Errorf("本地监听 %s 失败: %w", local, err)
	}
	t.mu.Lock()
	t.listeners = append(t.listeners, ln)
	t.mu.Unlock()

	desc := "D " + local
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go t.handleDynamicConn(client, c, meta, desc)
		}
	}()
	return nil
}

// handleDynamicConn 窥探首字节区分协议：SOCKS5 首字节为 0x05，
// 否则按 HTTP 代理处理。
func (t *Tunnel) handleDynamicConn(client *ssh.Client, c net.Conn, meta connMeta, forward string) {
	defer c.Close()

	// 一条客户端 socket 对应一条记录。HTTP 代理 keep-alive 下同一 socket
	// 会依次访问多个目标，记录里的目标地址以最后一次为准，流量按 socket 累计。
	//
	// 协议先留空：此刻还不知道对面是 SOCKS5 还是 HTTP，标成 TCP 是错的。
	// 端口扫描这类连上就走的探测连接会停在「未识别」，界面上如实显示。
	e := t.newEntry(meta, forward, "", c.RemoteAddr(), true)
	e.AddCloser(c)

	br := bufio.NewReader(c)
	b, err := br.Peek(1)
	if err != nil {
		e.Close(conn.ReasonNormal)
		return
	}
	if b[0] == 0x05 {
		t.handleSOCKS5(client, c, br, e)
		return
	}
	t.handleHTTPProxy(client, c, br, e)
}

// handleSOCKS5 处理 SOCKS5 连接（读取走 br，写回走 conn）
func (t *Tunnel) handleSOCKS5(client *ssh.Client, c net.Conn, br *bufio.Reader, e *conn.Entry) {
	defer e.Close(conn.ReasonNormal)

	// SOCKS5 握手：版本 + 方法数 + 方法列表
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	if hdr[0] != 5 {
		return
	}
	nmethods := int(hdr[1])
	if _, err := io.ReadFull(br, make([]byte, nmethods)); err != nil {
		return
	}
	// 回复：不需要认证
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	// 读取请求
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	if req[0] != 5 || req[1] != 1 { // 只支持 CONNECT
		_, _ = c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch req[3] {
	case 1: // IPv4
		buf := make([]byte, 4)
		if _, err := io.ReadFull(br, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	case 3: // 域名
		l := make([]byte, 1)
		if _, err := io.ReadFull(br, l); err != nil {
			return
		}
		buf := make([]byte, int(l[0]))
		if _, err := io.ReadFull(br, buf); err != nil {
			return
		}
		host = string(buf)
	case 4: // IPv6
		buf := make([]byte, 16)
		if _, err := io.ReadFull(br, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	default:
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(br, portBuf); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBuf)
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))

	e.SetProto("SOCKS5")
	e.SetDst(target)

	remote, err := client.Dial("tcp", target)
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		e.Close(conn.ReasonDialFailed)
		return
	}
	defer remote.Close()
	e.SetUpstream(remote)

	// 成功回复
	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	// 读取侧走 br（数据可能已缓冲在 bufio 里，不能靠包装 socket 计数）
	pipeConn(e.Wrap(c), e.UpReader(br), remote)
}

// handleHTTPProxy 处理 HTTP 代理连接：
//   - CONNECT method 建立隧道（HTTPS 及任意 TCP）
//   - 普通请求按绝对 URI 解析后转发到源站，支持 keep-alive 复用
func (t *Tunnel) handleHTTPProxy(client *ssh.Client, c net.Conn, br *bufio.Reader, e *conn.Entry) {
	defer e.Close(conn.ReasonNormal)

	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}

		if req.Method == http.MethodConnect {
			target := req.Host
			if _, _, e2 := net.SplitHostPort(target); e2 != nil {
				target = net.JoinHostPort(target, "443")
			}
			_ = req.Body.Close()

			e.SetProto("HTTPS")
			e.SetDst(target)

			remote, err := client.Dial("tcp", target)
			if err != nil {
				_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
				e.Close(conn.ReasonDialFailed)
				return
			}
			e.SetUpstream(remote)
			_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
			pipeConn(e.Wrap(c), e.UpReader(br), remote)
			_ = remote.Close()
			return
		}

		// 普通 HTTP 请求：目标由绝对 URI 给出
		host := req.URL.Host
		if host == "" {
			host = req.Host
		}
		if host == "" {
			_ = req.Body.Close()
			_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
			return
		}
		if _, _, e2 := net.SplitHostPort(host); e2 != nil {
			host = net.JoinHostPort(host, "80")
		}

		e.SetProto("HTTP")
		e.SetDst(host)

		remote, err := client.Dial("tcp", host)
		if err != nil {
			_ = req.Body.Close()
			_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			e.Close(conn.ReasonDialFailed)
			return
		}
		// 登记当前上游连接：否则用户点「断开」时只关掉了客户端 socket，
		// 卡在 http.ReadResponse 上的 goroutine 会一直等一个可能永远不来的响应，
		// SSH 通道也跟着悬挂。SetUpstream 是替换语义，keep-alive 循环里不会堆积。
		e.SetUpstream(remote)
		// 转成 origin-form 发给源站，去掉逐跳的代理头
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		if err := req.Write(e.UpWriter(remote)); err != nil {
			_ = req.Body.Close()
			_ = remote.Close()
			return
		}
		// body 已被 req.Write 消费，显式关闭以释放 reader，避免 keep-alive 下一轮读错位
		_ = req.Body.Close()
		resp, err := http.ReadResponse(bufio.NewReader(remote), req)
		if err != nil {
			_ = remote.Close()
			return
		}
		werr := resp.Write(e.DownWriter(c))
		_ = resp.Body.Close()
		_ = remote.Close()
		if werr != nil || req.Close || resp.Close {
			return
		}
	}
}

// pipeConn 双向转发，两个方向都结束后才返回。
// 客户端方向从 clientReader 读取（含 bufio 已缓冲的数据），回写走 clientConn。
// 单个方向结束时传播半关闭，避免另一方向的数据被提前掐断。
func pipeConn(clientConn io.ReadWriteCloser, clientReader io.Reader, remote io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(remote, clientReader)
		closeWrite(remote)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, remote)
		closeWrite(clientConn)
	}()
	wg.Wait()
}
