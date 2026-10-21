package main

import (
	"context"
	"embed"
	"log"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"ssh-tunnel/internal/config"
	"ssh-tunnel/internal/dock"
	"ssh-tunnel/internal/tray"
	"ssh-tunnel/internal/tunnel"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

// 全局引用，供托盘回调使用
var (
	appInstance  *App
	wailsCtx     context.Context
	trayInstance *tray.Tray
	// quitting 标记用户主动退出。OnBeforeClose 在「最小化到托盘」模式下会
	// 拦截关闭请求，若不区分主动退出，QuitApp 会被自己拦住：
	// 窗口隐藏、托盘已销毁、进程却还在跑（僵尸进程）。
	quitting atomic.Bool
)

func main() {
	// 初始化配置存储
	store, err := config.NewStore()
	if err != nil {
		log.Fatalf("初始化配置失败: %v", err)
	}

	// 隧道管理器，状态变更同步到 store + 托盘 + 前端
	manager := tunnel.NewManager(store, func(id string, status config.Status, lastErr string) {
		store.UpdateRuntimeStatus(id, status, lastErr)
		// 推送事件到前端
		if wailsCtx != nil {
			wailsRuntime.EventsEmit(wailsCtx, "tunnel:status", map[string]string{
				"id":        id,
				"status":    string(status),
				"lastError": lastErr,
			})
		}
		// 更新托盘状态
		if trayInstance != nil {
			trayInstance.SetTunnelState(id, string(status), lastErr)
		}
	})

	appInstance = NewApp(store, manager)
	appInstance.setRefreshTray(func() {
		trayInstance.SetItems(toTrayItems(store.ListTunnels(), manager))
	})
	// 完全退出：停隧道 + 退托盘 + 退 Wails
	appInstance.setQuitApp(func() {
		// 先置位再走关闭流程，否则 OnBeforeClose 会把这次退出当成「关窗口」拦下来
		quitting.Store(true)
		manager.StopAll()
		if trayInstance != nil {
			trayInstance.Quit()
		}
		if wailsCtx != nil {
			wailsRuntime.Quit(wailsCtx)
		}
	})

	// 托盘注册（非阻塞，使用外部 runloop 模式与 Wails 共享 NSApp）
	trayInstance = tray.New(tray.Callbacks{
		OnToggleTunnel: func(id string) {
			if manager.IsRunning(id) {
				_ = manager.Stop(id)
			} else {
				_ = manager.Start(id)
			}
		},
		OnStartAll: func() {
			manager.StartAll()
		},
		OnStopAll: func() {
			manager.StopAll()
		},
		OnShowWindow: func() {
			if wailsCtx != nil {
				wailsRuntime.WindowShow(wailsCtx)
			}
		},
		OnQuit: func() {
			appInstance.quitApp()
		},
	})
	if !disableTray() {
		trayInstance.Init()
		// 在主线程创建 NSStatusItem（必须在 wails.Run 阻塞主线程之前）
		trayInstance.Start()
	}

	// 初始托盘菜单（Start 后会应用）
	trayInstance.SetItems(toTrayItems(store.ListTunnels(), manager))

	// 主线程运行 Wails（macOS 要求 NSWindow 在主线程创建）
	err = wails.Run(&options.App{
		Title:  "SSH Tunnel",
		Width:  960,
		Height: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 245, G: 245, B: 245, A: 1},
		OnStartup: func(ctx context.Context) {
			wailsCtx = ctx
			appInstance.startup(ctx)
		},
		OnDomReady: func(ctx context.Context) {
			// 延迟执行：等窗口完全显示后再隐藏，否则 Hide 调用无效
			settings := store.GetSettings()
			if settings.HideFromDock || settings.StartMinimized {
				go func() {
					time.Sleep(300 * time.Millisecond)
					if settings.HideFromDock {
						dock.Hide()
					}
					if settings.StartMinimized {
						wailsRuntime.Hide(ctx)
					}
				}()
			}
		},
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			// 主动退出（菜单/托盘/设置页的「退出程序」）时不再拦截
			if quitting.Load() {
				return false
			}
			// 关闭窗口时隐藏到托盘而非退出
			settings := store.GetSettings()
			if settings.MinimizeToTray && trayInstance != nil {
				wailsRuntime.Hide(ctx)
				return true
			}
			manager.StopAll()
			return false
		},
		Bind: []interface{}{
			appInstance,
		},
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title: "SSH Tunnel " + Version,
				Message: "SSH 端口转发可视化管理工具\n\n" +
					"作者: Midaug <days0814@gmail.com>\n" +
					"GitHub: https://github.com/midaug/ssh-tunnel\n" +
					"授权: MIT License",
				Icon: appIcon,
			},
		},
	})
	if err != nil {
		log.Fatalf("wails: %v", err)
	}
	manager.StopAll()
}

// disableTray 通过环境变量 SSH_TUNNEL_NO_TRAY=1 禁用托盘（用于开发/无桌面环境）
func disableTray() bool {
	return os.Getenv("SSH_TUNNEL_NO_TRAY") == "1"
}

func toTrayItems(tunnels []config.Tunnel, m *tunnel.Manager) []tray.TunnelItem {
	_ = runtime.GOOS // keep import
	items := make([]tray.TunnelItem, 0, len(tunnels))
	for _, t := range tunnels {
		s, e := m.Status(t.ID)
		items = append(items, tray.TunnelItem{
			ID:     t.ID,
			Name:   t.Name,
			Status: string(s),
			Error:  e,
		})
	}
	return items
}
