package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"ssh-tunnel/internal/auth"
	"ssh-tunnel/internal/config"
	"golang.org/x/crypto/ssh"
)

// EventHandler 隧道状态变更回调（用于向 Wails 前端推送事件）
type EventHandler func(tunnelID string, status config.Status, lastError string)

// Tunnel 单条隧道的运行时实例
type Tunnel struct {
	cfg      config.Tunnel
	mu       sync.Mutex
	client   *ssh.Client
	stopCh   chan struct{}
	doneCh   chan struct{}
	listeners []net.Listener
	status   config.Status
	lastErr  string
	startedAt time.Time

	onStatus EventHandler
}

// New 用配置创建一个 Tunnel 实例（不启动）
func New(c config.Tunnel, h EventHandler) *Tunnel {
	return &Tunnel{cfg: c, onStatus: h}
}

// Cfg 返回当前配置快照
func (t *Tunnel) Cfg() config.Tunnel {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cfg
}

// Status 返回运行时状态
func (t *Tunnel) Status() (config.Status, string, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status, t.lastErr, t.startedAt
}

func (t *Tunnel) setStatus(s config.Status, err string) {
	t.mu.Lock()
	t.status = s
	// 与 Store.UpdateRuntimeStatus 保持一致：状态与错误信息总是成对更新，
	// 避免「UI 已清空错误、托盘还留着旧错误」的不一致。
	t.lastErr = err
	if s == config.StatusRunning || s == config.StatusConnecting {
		if t.startedAt.IsZero() {
			t.startedAt = time.Now()
		}
	} else {
		t.startedAt = time.Time{}
	}
	statusCopy := s
	errCopy := err
	h := t.onStatus
	id := t.cfg.ID
	t.mu.Unlock()
	if h != nil {
		h(id, statusCopy, errCopy)
	}
}

// Start 启动隧道。若已运行则直接返回。自动重连 goroutine 在断线后重试。
func (t *Tunnel) Start() error {
	t.mu.Lock()
	if t.status == config.StatusRunning || t.status == config.StatusConnecting {
		t.mu.Unlock()
		return nil
	}
	if t.stopCh != nil {
		t.mu.Unlock()
		return errors.New("隧道正在停止中")
	}
	t.stopCh = make(chan struct{})
	t.doneCh = make(chan struct{})
	t.mu.Unlock()

	go t.run()
	return nil
}

// Stop 停止隧道，阻塞直到 goroutine 退出
func (t *Tunnel) Stop() {
	t.mu.Lock()
	if t.stopCh == nil {
		t.mu.Unlock()
		return
	}
	select {
	case <-t.stopCh:
	default:
		close(t.stopCh)
	}
	done := t.doneCh
	t.mu.Unlock()
	if done != nil {
		<-done
	}
}

// run 主循环：连接 → 启动转发 → 监控断线 → 退避重连
func (t *Tunnel) run() {
	// stopCh 在本次 run 的生命周期内不会变化（Stop 只 close 不替换），
	// 捕获到局部变量后各 goroutine 都不必再读 t.stopCh 字段，避免数据竞争。
	stopCh := t.stopSignal()

	// errored 标记本次退出是否因终态错误（无自动重连时的连接失败 / 断开）。
	// 若是，保留 Error 状态，不 defer 覆盖为 Stopped，让用户能看到 ⚠。
	var errored bool
	defer func() {
		if !errored {
			t.setStatus(config.StatusStopped, "")
		}
		t.mu.Lock()
		if t.doneCh != nil {
			close(t.doneCh)
			t.doneCh = nil
			t.stopCh = nil
		}
		t.mu.Unlock()
	}()

	initCfg := t.Cfg()
	backoff := initCfg.ReconnectMinMS
	if backoff < 500 {
		backoff = 500
	}
	maxBackoff := initCfg.ReconnectMaxMS
	if maxBackoff < backoff {
		maxBackoff = 30000
	}

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		// 每轮迭代开始处加锁快照配置，循环体内一律用 cfg。
		// 写侧（Manager.Start/ReloadConfig）加锁整体替换 t.cfg，
		// 这样读写都在锁保护下，避免数据竞争，也让配置在下次重连时生效。
		cfg := t.Cfg()

		t.setStatus(config.StatusConnecting, "")
		client, err := t.connect(cfg, stopCh)
		if err != nil {
			// 若是停止导致的中断，不报错
			select {
			case <-stopCh:
				return
			default:
			}
			t.setStatus(config.StatusError, err.Error())
			if !cfg.AutoReconnect {
				errored = true
				return
			}
			if !t.sleep(time.Duration(backoff)*time.Millisecond, stopCh) {
				return
			}
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		t.mu.Lock()
		t.client = client
		t.mu.Unlock()

		// 启动所有转发监听器
		fwdErr := t.startForwards(client, cfg)
		if fwdErr != nil {
			t.setStatus(config.StatusError, fwdErr.Error())
			t.closeAll()
			_ = client.Close()
			if !cfg.AutoReconnect {
				errored = true
				return
			}
			if !t.sleep(time.Duration(backoff)*time.Millisecond, stopCh) {
				return
			}
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// 重连成功，重置退避
		backoff = cfg.ReconnectMinMS
		if backoff < 500 {
			backoff = 500
		}
		t.setStatus(config.StatusRunning, "")

		// keepalive：周期性发送 keepalive 请求，防止 NAT / 防火墙静默掐断空闲连接
		kaDone := make(chan struct{})
		if cfg.ServerAliveInterval > 0 {
			go t.keepalive(client, cfg.ServerAliveInterval, kaDone)
		}

		// 阻塞等待断线或停止
		waitErr := make(chan error, 1)
		go func() { waitErr <- client.Wait() }()
		select {
		case <-stopCh:
			close(kaDone)
			t.closeAll()
			_ = client.Close()
			return
		case <-waitErr:
			close(kaDone)
			t.closeAll()
			_ = client.Close()
			t.mu.Lock()
			t.client = nil
			t.mu.Unlock()
			if !cfg.AutoReconnect {
				t.setStatus(config.StatusError, "连接断开")
				errored = true
				return
			}
			t.setStatus(config.StatusConnecting, "连接断开，准备重连")
			if !t.sleep(time.Duration(backoff)*time.Millisecond, stopCh) {
				return
			}
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// handshakeTimeout SSH 握手超时。注意 ssh.ClientConfig.Timeout 只在 ssh.Dial
// 内部生效，本实现用 ssh.NewClientConn 自己握手，必须显式给连接设 deadline，
// 否则对端只 accept 却不发 banner（端口填错、防火墙黑洞）时会永久阻塞。
const handshakeTimeout = 15 * time.Second

// connect 建立 SSH 连接。stopCh 用于在 Stop 时立即中断拨号与握手。
// 配置由调用方以快照传入，避免与 Manager 的并发写 t.cfg 竞争。
func (t *Tunnel) connect(cfg config.Tunnel, stopCh <-chan struct{}) (*ssh.Client, error) {
	authMethods, err := auth.BuildAuthMethods(string(cfg.AuthType), cfg.Password, cfg.KeyPath, cfg.KeyPassphrase)
	if err != nil {
		return nil, fmt.Errorf("认证配置错误: %w", err)
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 简化：不校验 host key，管理工具常见做法
		Timeout:         handshakeTimeout,
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, port)

	// 拨号阶段：用 context 让 TCP 拨号可被 stopCh 中断
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dialDone := make(chan struct{})
	go func() {
		select {
		case <-stopCh:
			cancel()
		case <-dialDone:
		}
	}()

	conn, err := dialViaProxy(ctx, cfg.Proxy, addr)
	close(dialDone)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("已取消")
		}
		return nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
	}

	// 握手阶段：stopCh 触发时直接关闭底层连接打断 NewClientConn
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-stopCh:
			_ = conn.Close()
		case <-handshakeDone:
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	close(handshakeDone)
	if err != nil {
		_ = conn.Close()
		select {
		case <-stopCh:
			return nil, errors.New("已取消")
		default:
		}
		return nil, fmt.Errorf("握手 %s 失败: %w", addr, err)
	}
	// 握手完成，清掉 deadline，否则长连接会在 15s 后被判超时断开
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sc, chans, reqs), nil
}

// keepaliveTimeout 单次 keepalive 的等待上限。
// client.SendRequest(wantReply=true) 在链路「静默死亡」（NAT 表项过期、
// 中间设备丢包但不发 RST）时不会返回错误，而是一直阻塞；因此必须自己加超时，
// 超时即认定连接已死，关闭客户端让 client.Wait() 返回以触发重连。
const keepaliveTimeout = 10 * time.Second

// keepalive 周期性向 SSH 服务器发送 keepalive 请求，保持长连接活跃。
// 在 done 关闭、请求失败或超时时退出。
func (t *Tunnel) keepalive(client *ssh.Client, intervalSec int, done <-chan struct{}) {
	interval := time.Duration(intervalSec) * time.Second
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			res := make(chan error, 1)
			go func() {
				_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
				res <- err
			}()
			select {
			case <-done:
				return
			case err := <-res:
				if err != nil {
					return
				}
			case <-time.After(keepaliveTimeout):
				// 心跳无响应：主动断开，交由上层重连
				_ = client.Close()
				return
			}
		}
	}
}

// startForwards 启动所有转发监听器
func (t *Tunnel) startForwards(client *ssh.Client, cfg config.Tunnel) error {
	t.mu.Lock()
	t.listeners = t.listeners[:0]
	t.mu.Unlock()
	for _, f := range cfg.Forwards {
		if err := t.startOneForward(client, f); err != nil {
			return fmt.Errorf("转发 %s %s 启动失败: %w", f.Type, f.Listen, err)
		}
	}
	return nil
}

// closeAll 关闭所有监听器
func (t *Tunnel) closeAll() {
	t.mu.Lock()
	ls := t.listeners
	t.listeners = nil
	t.mu.Unlock()
	for _, l := range ls {
		_ = l.Close()
	}
}

// sleep 可被停止信号打断的 sleep
func (t *Tunnel) sleep(d time.Duration, stopCh <-chan struct{}) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-stopCh:
		return false
	case <-timer.C:
		return true
	}
}

// stopSignal 返回当前停止信号通道（加锁读取，避免与 run 退出时把字段置 nil 竞争）
func (t *Tunnel) stopSignal() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopCh
}

// normalizeAddr 规范化监听地址，缺少 host 时默认 127.0.0.1
func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		// 只有端口
		return "127.0.0.1:" + addr
	}
	return addr
}

// hostPort 拆出 host 和 port
func hostPort(addr string) (string, string) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", ""
	}
	return h, p
}
