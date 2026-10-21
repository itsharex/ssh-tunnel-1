<div align="center">

# SSH Tunnel

**一个跨平台的 SSH 端口转发管理工具**

![version](https://img.shields.io/badge/version-v0.0.2-blue)
![platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux-lightgrey)

基于 Wails v2 + Vue 3 构建，用图形界面管理多条 SSH 隧道，支持系统托盘常驻、自动重连、实时连接监控、命令行导入导出。

[功能特性](#功能特性) · [界面预览](#界面预览) · [快速开始](#快速开始) · [构建](#构建) · [技术栈](#技术栈) · [更新日志](#更新日志)

</div>

---

## 功能特性

### 隧道管理

- **多隧道同时管理** — 每条隧道独立启停，状态实时同步
- **三种端口转发模式**
  - `-L` 本地转发（Local Forward）
  - `-R` 远程转发（Remote Forward）
  - `-D` 动态代理（Dynamic Forward）—— 同一监听端口同时兼容 SOCKS5 与 HTTP 代理，按首字节自动识别协议；HTTP 模式支持 `CONNECT` 隧道（HTTPS / 任意 TCP）与普通请求转发，并复用 keep-alive 连接
- **代理中转** — 到 SSH 服务器的连接可经由 HTTP / HTTPS / SOCKS5 代理建立，适用于处于内网或需跳板访问 SSH 的场景；支持代理认证，连接测试同样走代理
- **自动重连** — 断线后自动重试，可配置退避策略（初始 / 最大退避间隔）
- **连接测试** — 保存前先测试 SSH 连通性，不实际启动转发
- **一键全部启用 / 全部关闭**

### 连接监控

把「现在有哪些连接、连到哪、跑了多少流量」直接摆到界面上，不用再开 `lsof` 或抓包。

- **实时连接列表** — 每条连接显示进程名与 PID、来源地址、目标地址、所属隧道、协议（TCP / SOCKS5 / HTTP / HTTPS）
- **流量与速率** — 累计上下行字节数 + 1 秒粒度的实时速率，合计值在顶部汇总
- **进程反查** — 自动识别发起连接的本机进程（浏览器、IDE、数据库客户端…）
  - macOS：`libproc` 遍历进程 fd
  - Linux：`/proc/net/tcp` + `/proc/<pid>/fd` 反查 socket inode
  - Windows：`GetExtendedTcpTable` 一次系统调用拿到全部连接及所属 PID
  - `-R` 远程转发的发起方在 SSH 服务器那一侧，本机没有对应进程，只显示来源 IP
- **主动断开** — 单条断开、全部断开；隧道停止时会一并清理该隧道的连接，不留僵尸记录
- **关闭后短暂保留** — 已结束的连接保留 5 秒（最多 200 条），可以看到断开原因：手动断开 / 隧道停止 / 目标不可达
- **零后台开销** — 只在打开连接监控页面时以 1Hz 采样并推送；离开页面即停止，不看就不花 CPU
- **不干扰转发** — 登记表上限 4096 条，达到上限只是新连接不再显示，转发本身不受任何影响

### 系统托盘

- **状态指示** — 每条隧道用符号标识当前状态：
  - `●` 运行中
  - `◐` 连接中
  - `⚠` 错误
  - `○` 已停止
- **双色图标** — 有隧道运行时图标为蓝色，全部停止时自动变灰，一眼可知总体状态
- **托盘操作** — 直接在托盘菜单中启停隧道、全部启用/关闭、显示窗口、退出程序

### 导入 / 导出

- **从 SSH 命令导入** — 粘贴 `ssh -L 8080:localhost:80 -i ~/.ssh/id_rsa user@host`，自动解析为隧道配置
- **导出为 SSH 命令** — 将任意隧道一键导出为等效的 ssh 命令行，方便复制到终端使用
- **JSON 导入导出** — 支持合并 / 覆盖两种模式，方便备份和迁移

### 平台适配

| 功能 | macOS | Windows |
|------|:-----:|:-------:|
| 系统托盘 | ✅ | ✅ |
| 连接监控（含进程反查） | ✅ | ✅ |
| 开机自启 | ✅ LaunchAgent | ✅ 注册表 |
| 隐藏 Dock 图标 | ✅ 菜单栏应用模式 | — |
| 启动时不显示窗口 | ✅ | ✅ |
| 关闭窗口最小化到托盘 | ✅ | ✅ |
| 原生图标 | ✅ | ✅ ICO |

> Linux 同样支持构建与连接监控（`/proc` 反查进程），但不提供 Dock / 托盘的系统级集成。

## 界面预览

### 隧道列表

主界面展示所有隧道配置，含运行状态、转发规则、错误信息，可一键启停。

![隧道列表界面](images/隧道列表界面.png)

### 连接监控

实时展示所有进行中的连接：进程、来源、目标、流量与速率。绿色圆点为活动中，灰色为刚结束（保留 5 秒），红色表示连接目标失败。

![连接监控界面](images/连接监控界面.png)

### 新建 / 编辑隧道

配置 SSH 连接信息、认证方式（密钥 / 密码）、代理、端口转发规则及高级选项。

![编辑隧道界面](images/编辑隧道界面.png)

### 导入 / 导出

支持从 SSH 命令导入、JSON 导入导出、导出为 SSH 命令三种方式。

![导入导出界面](images/导入导出界面.png)

### 设置

开机自启、窗口行为、Dock 显示等全局配置。

![设置界面](images/设置界面.png)

### 系统托盘

托盘菜单可直接操作隧道，图标颜色反映整体运行状态。

![任务栏控制](images/任务栏控制.png)

## 快速开始

### 环境要求

- **Go** 1.21+
- **Node.js** 16+
- **[Wails CLI v2](https://wails.io/docs/gettingstarted/installation)**

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

<details>
<summary>平台额外依赖</summary>

- **macOS**：Xcode Command Line Tools
  ```bash
  xcode-select --install
  ```
- **Windows**：[WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)（Win10/11 通常已内置）+ C 编译器（TDM-GCC / MSYS2）

</details>

### 本地开发

```bash
# 克隆仓库
git clone https://github.com/midaug/ssh-tunnel.git
cd ssh-tunnel

# 安装前端依赖
cd frontend && npm install && cd ..

# 热重载开发模式
wails dev
```

开发模式下前端改动即时热更新，同时可在浏览器访问 `http://localhost:34115` 调试。

## 构建

```bash
# 构建当前平台
wails build -clean

# 交叉编译 Windows（需在对应平台或配置交叉编译工具链）
wails build -platform windows/amd64 -clean

# 交叉编译 macOS
wails build -platform darwin/universal -clean
```

构建产物输出到 `build/bin/` 目录。

## 技术栈

| 层 | 技术 |
|----|------|
| 框架 | [Wails v2](https://wails.io) — Go + WebView 桌面框架 |
| 后端 | Go，[golang.org/x/crypto/ssh](https://pkg.go.dev/golang.org/x/crypto/ssh)，[golang.org/x/net/proxy](https://pkg.go.dev/golang.org/x/net/proxy) |
| 前端 | Vue 3 + TypeScript + Pinia + Vue Router |
| 系统托盘 | [fyne.io/systray](https://github.com/fyne-io/systray) |
| macOS Dock | cgo 调用 `NSApp.setActivationPolicy` |
| 进程反查 | macOS `libproc` / Linux `procfs` / Windows `iphlpapi` |
| 开机自启 | macOS LaunchAgent / Windows 注册表 / Linux .desktop |

## 项目结构

```
ssh-tunnel/
├── main.go                     # 应用入口，Wails 配置，托盘回调
├── app.go                      # 前端绑定方法
├── wails.json                  # Wails 项目配置
├── internal/
│   ├── config/                 # 配置持久化、SSH 命令解析与导出
│   │   ├── model.go            # 数据模型
│   │   ├── store.go            # JSON 配置读写
│   │   ├── parser.go           # ssh 命令 → 隧道配置
│   │   └── ssh_export.go       # 隧道配置 → ssh 命令
│   ├── tunnel/                 # SSH 隧道运行时管理
│   │   ├── manager.go          # 多隧道管理器
│   │   ├── tunnel.go           # 单隧道生命周期、自动重连
│   │   ├── proxy.go            # 经 HTTP/HTTPS/SOCKS5 代理拨号 SSH 服务器
│   │   ├── forward_local.go    # -L 本地转发
│   │   ├── forward_remote.go   # -R 远程转发
│   │   └── forward_dynamic.go  # -D 动态转发（SOCKS5 + HTTP 代理）
│   ├── conn/                   # 连接登记表：快照、速率采样、主动断开
│   ├── proc/                   # 按本地端口反查进程（三平台各自实现）
│   ├── tray/                   # 系统托盘（跨平台图标）
│   ├── dock/                   # macOS Dock 显示控制
│   └── autostart/              # 开机自启（macOS/Linux/Windows）
├── frontend/
│   └── src/
│       ├── App.vue             # 主布局 + 侧边栏
│       ├── views/              # 隧道列表 / 连接监控 / 编辑 / 导入导出 / 设置
│       ├── components/         # 应用内对话框等公共组件
│       ├── lib/                # 对话框封装等前端工具
│       ├── stores/             # Pinia 状态管理
│       └── style.css           # 全局样式
├── build/
│   ├── appicon.png             # 应用图标（1024×1024）
│   ├── darwin/                 # macOS Info.plist
│   └── windows/                # Windows ICO + manifest
└── images/                     # README 截图
```

## 配置文件

配置自动持久化到用户配置目录：

| 平台 | 路径 |
|------|------|
| macOS | `~/Library/Application Support/ssh-tunnel/config.json` |
| Windows | `%AppData%\ssh-tunnel\config.json` |
| Linux | `~/.config/ssh-tunnel/config.json` |

可在「设置」页面查看完整路径并直接打开所在文件夹。

## 系统要求

| 平台 | 最低版本 | 备注 |
|------|---------|------|
| macOS | 10.13 High Sierra | — |
| Windows | 10 / Server 2016 | 需 WebView2 Runtime；Win7/8 不受支持 |

> ⚠️ **关于 Windows 7**：Wails v2 依赖 WebView2（Edge 内核），且 Go 1.21+ 已移除 Win7 支持，因此无法在 Win7 上运行。

## 更新日志

### v0.0.2

**新增**

- **连接监控** — 实时查看所有进行中的连接、访问目标、流量与速率，支持单条 / 全部断开
- **进程反查** — 自动识别发起连接的本机进程（macOS `libproc` / Linux `procfs` / Windows `iphlpapi`）
- **三平台 CI** — 每次提交自动在 macOS / Linux / Windows 上跑测试

**修复**

- **转发正确性** — 半关闭未传播导致响应被截断；`-R` 目标缺端口时拼出非法地址
- **连接稳定性** — SSH 握手无法中断、keepalive 超时不生效、`stopCh` 数据竞争
- **配置安全** — 写盘改为原子替换并可从损坏中恢复；修正 IPv6 导出括号与含空格密钥路径
- **界面** — macOS 上 `confirm()` / `alert()` 被 WebView 静默吞掉（表现为「删除点了没反应」）、复制按钮失效、编辑页覆盖旧隧道、`-D` 行删除按钮错位；删除隧道补上二次确认
- **托盘与退出** — 托盘菜单重建时的 goroutine 泄漏、Dock 切换的线程问题、主动退出被「最小化到托盘」拦截
- **细节** — 已关闭连接及时回收、重开监控页时速率虚高、`-D` 探测连接被误标为 TCP

**性能**

- 连接快照与满表登记路径显著提速，空闲时不再扫描进程表

### v0.0.1

首个可用版本：多隧道管理、三种转发模式、系统托盘、导入导出。

## License

[MIT](LICENSE)
