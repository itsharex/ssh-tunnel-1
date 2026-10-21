package tray

import (
	"sync"

	"fyne.io/systray"
)

// iconBytes / iconGrayBytes 由平台特定的文件 embed 注入
// macOS/Linux 用 PNG，Windows 用 ICO

// Callbacks 托盘菜单触发的回调
type Callbacks struct {
	OnToggleTunnel func(tunnelID string)
	OnStartAll     func()
	OnStopAll      func()
	OnShowWindow   func()
	OnQuit         func()
}

// TunnelItem 托盘显示的隧道条目
type TunnelItem struct {
	ID     string
	Name   string
	Status string // running | connecting | error | stopped
	Error  string
}

// Tray 系统托盘
type Tray struct {
	mu        sync.Mutex
	cbs       Callbacks
	items     []TunnelItem
	menuItems map[string]*systray.MenuItem
	ready     bool
	startFn   func()
	endFn     func()
	// done 是「当前这一代菜单」的关闭信号。每次 rebuild 都会关闭旧的、
	// 换一个新的，用来回收上一代菜单项上的点击监听 goroutine。
	done chan struct{}
}

// New 创建托盘实例。调用 Init 注册到 systray（不阻塞）。
func New(cbs Callbacks) *Tray {
	return &Tray{
		cbs:       cbs,
		menuItems: make(map[string]*systray.MenuItem),
	}
}

// Init 注册托盘回调，获取 start/end 函数。必须在 Wails 启动前调用。
func (t *Tray) Init() {
	start, end := systray.RunWithExternalLoop(t.onReady, t.onExit)
	t.mu.Lock()
	t.startFn = start
	t.endFn = end
	t.mu.Unlock()
}

// Start 在主线程上启动托盘（创建 NSStatusItem）。
func (t *Tray) Start() {
	t.mu.Lock()
	start := t.startFn
	t.mu.Unlock()
	if start != nil {
		start()
	}
}

// Quit 退出托盘
func (t *Tray) Quit() {
	systray.Quit()
}

// SetItems 更新隧道列表，重建菜单。
func (t *Tray) SetItems(items []TunnelItem) {
	t.mu.Lock()
	t.items = items
	ready := t.ready
	t.mu.Unlock()
	if ready {
		t.rebuild()
		t.refreshIcon()
	}
}

// SetTunnelState 更新某个隧道的启停状态显示
func (t *Tray) SetTunnelState(id, status, lastErr string) {
	t.mu.Lock()
	for i := range t.items {
		if t.items[i].ID == id {
			t.items[i].Status = status
			t.items[i].Error = lastErr
		}
	}
	mi, ok := t.menuItems[id]
	if !ok {
		t.mu.Unlock()
		t.refreshIcon()
		return
	}
	label := ""
	for _, it := range t.items {
		if it.ID == id {
			label = formatLabel(it)
			break
		}
	}
	t.mu.Unlock()
	if label != "" {
		mi.SetTitle(label)
		switch status {
		case "running":
			mi.Check()
		default:
			mi.Uncheck()
		}
	}
	t.refreshIcon()
}

func (t *Tray) onReady() {
	systray.SetTooltip("SSH Tunnel Manager")
	t.mu.Lock()
	t.ready = true
	t.mu.Unlock()
	t.rebuild()
	t.refreshIcon()
}

func (t *Tray) onExit() {
	// nothing
}

// statusMark 返回状态对应的前缀标记（精致的几何符号，非厚重 emoji）
func statusMark(status string) string {
	switch status {
	case "running":
		return "●" // 实心圆
	case "connecting":
		return "◐" // 半圆
	case "error":
		return "⚠" // 警告
	default:
		return "○" // 空心圆
	}
}

// formatLabel 拼接菜单项标签
func formatLabel(it TunnelItem) string {
	return statusMark(it.Status) + " " + it.Name
}

// anyActive 是否有隧道正在运行或连接中
func (t *Tray) anyActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, it := range t.items {
		if it.Status == "running" || it.Status == "connecting" {
			return true
		}
	}
	return false
}

// refreshIcon 根据整体状态切换图标颜色
func (t *Tray) refreshIcon() {
	if !t.anyActive() {
		systray.SetIcon(iconGrayBytes)
	} else {
		systray.SetIcon(iconBytes)
	}
}

// rebuild 重建整个菜单。必须在 systray ready 后调用。
func (t *Tray) rebuild() {
	t.mu.Lock()
	// ResetMenu 会丢弃旧的 MenuItem，但它们的 ClickedCh 不会关闭，
	// 不主动发信号回收就会每重建一次泄漏一批阻塞的 goroutine。
	if t.done != nil {
		close(t.done)
	}
	t.done = make(chan struct{})
	done := t.done
	t.menuItems = make(map[string]*systray.MenuItem)
	items := make([]TunnelItem, len(t.items))
	copy(items, t.items)
	cbs := t.cbs
	t.mu.Unlock()

	systray.ResetMenu()

	if len(items) == 0 {
		mi := systray.AddMenuItem("暂无隧道", "")
		mi.Disable()
		systray.AddSeparator()
	} else {
		for _, it := range items {
			it := it
			label := formatLabel(it)
			mi := systray.AddMenuItemCheckbox(label, label, it.Status == "running")
			t.mu.Lock()
			t.menuItems[it.ID] = mi
			t.mu.Unlock()
			go t.watchToggle(mi, it.ID, cbs, done)
		}
		systray.AddSeparator()

		// 全部启用 / 全部关闭
		startAll := systray.AddMenuItem("全部启用", "启动所有隧道")
		go watchClick(startAll, done, func() {
			if cbs.OnStartAll != nil {
				cbs.OnStartAll()
			}
		})
		stopAll := systray.AddMenuItem("全部关闭", "停止所有隧道")
		go watchClick(stopAll, done, func() {
			if cbs.OnStopAll != nil {
				cbs.OnStopAll()
			}
		})
		systray.AddSeparator()
	}

	show := systray.AddMenuItem("显示窗口", "显示主窗口")
	go watchClick(show, done, func() {
		if cbs.OnShowWindow != nil {
			cbs.OnShowWindow()
		}
	})

	quit := systray.AddMenuItem("退出", "退出程序")
	go watchClick(quit, done, func() {
		if cbs.OnQuit != nil {
			cbs.OnQuit()
		}
		systray.Quit()
	})
}

// watchClick 监听菜单项点击，直到该代菜单被重建（done 关闭）为止。
func watchClick(mi *systray.MenuItem, done <-chan struct{}, fn func()) {
	for {
		select {
		case <-done:
			return
		case _, ok := <-mi.ClickedCh:
			if !ok {
				return
			}
			fn()
		}
	}
}

func (t *Tray) watchToggle(mi *systray.MenuItem, id string, cbs Callbacks, done <-chan struct{}) {
	watchClick(mi, done, func() {
		if cbs.OnToggleTunnel != nil {
			cbs.OnToggleTunnel(id)
		}
	})
}
