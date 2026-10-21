package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"ssh-tunnel/internal/autostart"
	"ssh-tunnel/internal/config"
	"ssh-tunnel/internal/conn"
	"ssh-tunnel/internal/dock"
	"ssh-tunnel/internal/tunnel"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Version 程序版本号。本地开发时为 "dev"；
// CI 发布时通过 -ldflags "-X main.Version=<tag>" 注入实际版本。
var Version = "dev"

// AppInfo 是「关于」页面展示的应用元信息
type AppInfo struct {
	Version   string `json:"version"`
	Author    string `json:"author"`
	Email     string `json:"email"`
	GitHubURL string `json:"githubUrl"`
	License   string `json:"license"`
}

// App 主应用，绑定到前端
type App struct {
	ctx         context.Context
	store       *config.Store
	manager     *tunnel.Manager
	refreshTray func()
	quitAppFn   func()

	connMu   sync.Mutex
	connStop chan struct{}
}

func NewApp(store *config.Store, manager *tunnel.Manager) *App {
	return &App{store: store, manager: manager}
}

// setRefreshTray 注入托盘刷新回调（包内使用，不导出给前端）
func (a *App) setRefreshTray(fn func()) { a.refreshTray = fn }

// setQuitApp 注入完全退出回调
func (a *App) setQuitApp(fn func()) { a.quitAppFn = fn }

func (a *App) refresh() {
	if a.refreshTray != nil {
		a.refreshTray()
	}
}

// quitApp 完全退出程序
func (a *App) quitApp() {
	// 先停掉推送协程，避免退出过程中还在序列化快照
	a.ConnWatch(false)
	if a.quitAppFn != nil {
		a.quitAppFn()
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// --- 隧道管理 ---

// TunnelList 返回所有隧道配置（含运行时状态）
func (a *App) TunnelList() []config.Tunnel {
	return a.store.ListTunnels()
}

// TunnelSave 新增或更新隧道
func (a *App) TunnelSave(t config.Tunnel) (config.Tunnel, error) {
	saved, err := a.store.SaveTunnel(t)
	if err != nil {
		return config.Tunnel{}, err
	}
	// 若隧道正在运行，重启以应用新配置
	if a.manager.IsRunning(saved.ID) {
		_ = a.manager.Restart(saved.ID)
	} else {
		a.manager.ReloadConfig(saved.ID)
	}
	a.refresh()
	return saved, nil
}

// TunnelDelete 删除隧道（运行中则先停止）
func (a *App) TunnelDelete(id string) error {
	_ = a.manager.Stop(id)
	err := a.store.DeleteTunnel(id)
	a.refresh()
	return err
}

// TunnelStart 启动隧道
func (a *App) TunnelStart(id string) error {
	return a.manager.Start(id)
}

// TunnelStop 停止隧道
func (a *App) TunnelStop(id string) error {
	return a.manager.Stop(id)
}

// TunnelRestart 重启隧道
func (a *App) TunnelRestart(id string) error {
	return a.manager.Restart(id)
}

// TunnelStartAll 启动所有隧道
func (a *App) TunnelStartAll() {
	a.manager.StartAll()
}

// TunnelStopAll 停止所有隧道
func (a *App) TunnelStopAll() {
	a.manager.StopAll()
}

// --- 连接监控 ---

// connPushInterval 连接列表推送间隔。1Hz 是刻意选的：
// 再快前端也来不及渲染，而且每秒一次的全表序列化（500 行约 107µs）已经够用。
const connPushInterval = time.Second

// ConnList 返回当前所有连接快照
func (a *App) ConnList() []conn.Snapshot {
	return a.manager.Conns().Snapshot()
}

// ConnWatch 开关连接列表推送。
// 前端进入连接页时开启、离开时关闭——用户不看的时候没必要每秒序列化整张表
// 再推过 WebView 桥，那是这个功能唯一有实际成本的地方。
func (a *App) ConnWatch(on bool) {
	a.connMu.Lock()
	defer a.connMu.Unlock()

	if !on {
		if a.connStop != nil {
			close(a.connStop)
			a.connStop = nil
		}
		return
	}
	if a.connStop != nil {
		return // 已经在推送
	}
	stop := make(chan struct{})
	a.connStop = stop
	go a.connPump(stop)
}

// connPump 每秒采样一次（算速率、清理过期记录、补进程信息）并推给前端
func (a *App) connPump(stop <-chan struct{}) {
	reg := a.manager.Conns()
	ticker := time.NewTicker(connPushInterval)
	defer ticker.Stop()
	for {
		reg.Sample()
		if ctx := a.ctx; ctx != nil {
			wailsRuntime.EventsEmit(ctx, "conn:update", reg.Snapshot())
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// ConnKill 断开单条连接
func (a *App) ConnKill(id string) bool {
	return a.manager.Conns().Kill(id)
}

// ConnKillAll 断开所有活跃连接，返回断开数量
func (a *App) ConnKillAll() int {
	return a.manager.Conns().KillAll()
}

// ConnKillByTunnel 断开某条隧道的所有连接，返回断开数量
func (a *App) ConnKillByTunnel(tunnelID string) int {
	return a.manager.Conns().KillByTunnel(tunnelID)
}

// --- 配置导入导出 ---

// ExportConfig 导出配置为 JSON 字符串
func (a *App) ExportConfig() (string, error) {
	return a.store.ExportConfig()
}

// ImportConfig 导入配置 JSON，mode: "merge" | "replace"
func (a *App) ImportConfig(jsonStr string, mode string) (int, error) {
	if mode == "" {
		mode = "merge"
	}
	// replace 会整体替换隧道列表，先停止并清理所有运行时实例，
	// 否则旧配置的隧道会在后台继续运行却无法从 UI 控制
	if mode == "replace" {
		a.manager.Reset()
	}
	n, err := a.store.ImportConfig(jsonStr, mode)
	a.refresh()
	return n, err
}

// ParseSSHCommand 解析 ssh 命令行为隧道配置
func (a *App) ParseSSHCommand(cmdline string) (config.Tunnel, error) {
	return config.ParseSSHCommand(cmdline)
}

// ToSSHCommand 把隧道配置导出为 ssh 命令行
func (a *App) ToSSHCommand(t config.Tunnel) string {
	return config.ToSSHCommand(t)
}

// ToSSHCommandByID 按 ID 导出某条隧道的 ssh 命令
func (a *App) ToSSHCommandByID(id string) (string, error) {
	t, err := a.store.GetTunnel(id)
	if err != nil {
		return "", err
	}
	return config.ToSSHCommand(t), nil
}

// --- 测试连接 ---

// TestConnection 仅测试 SSH 连通性，不启动转发
func (a *App) TestConnection(t config.Tunnel) error {
	return tunnel.TestConnection(t)
}

// --- 设置 ---

// GetSettings 返回全局设置
func (a *App) GetSettings() config.Settings {
	return a.store.GetSettings()
}

// SetSettings 更新全局设置（同时同步开机自启、Dock 显示）
func (a *App) SetSettings(s config.Settings) error {
	if s.Autostart {
		// 取当前可执行文件路径写入自启项，否则各平台会写入空路径导致无法启动
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取程序路径失败: %w", err)
		}
		if err := autostart.Enable(exe); err != nil {
			return fmt.Errorf("启用开机自启失败: %w", err)
		}
	} else {
		if err := autostart.Disable(); err != nil {
			return fmt.Errorf("关闭开机自启失败: %w", err)
		}
	}
	// 实时切换 Dock 显示
	if s.HideFromDock {
		dock.Hide()
	} else {
		dock.Show()
	}
	return a.store.SetSettings(s)
}

// GetAutostart 返回系统层面开机自启是否启用
func (a *App) GetAutostart() bool {
	return autostart.Enabled()
}

// QuitApp 完全退出程序（停所有隧道 + 退托盘 + 退 Wails）
func (a *App) QuitApp() {
	a.quitApp()
}

// --- 工具 ---

// ConfigPath 返回配置文件路径
func (a *App) ConfigPath() string {
	return a.store.Path()
}

// OpenInFolder 在文件管理器中显示文件
func (a *App) OpenInFolder(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer", "/select,", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

// GetAppInfo 返回「关于」页面所需的版本号、作者、GitHub 地址、授权协议等元信息
func (a *App) GetAppInfo() AppInfo {
	return AppInfo{
		Version:   Version,
		Author:    "Midaug",
		Email:     "days0814@gmail.com",
		GitHubURL: "https://github.com/midaug/ssh-tunnel",
		License:   "MIT License",
	}
}
