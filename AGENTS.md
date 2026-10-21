# AGENTS.md

本文件是本仓库对**所有 AI 编码工具**（Claude Code、Codex、Cursor、Copilot、Windsurf、Zed、Gemini CLI、Aider……）的统一约定。
任何工具在动手改代码之前，请先读完本文件，尤其是最后的 [记忆协议](#记忆协议) 一节。

---

## 1. 项目概览

**SSH Tunnel** —— 跨平台的 SSH 端口转发可视化管理工具。

用图形界面管理多条 SSH 隧道：`-L` 本地转发、`-R` 远程转发、`-D` 动态代理（同一端口同时兼容 SOCKS5 与 HTTP 代理），
支持经 HTTP / HTTPS / SOCKS5 代理连接 SSH 服务器、断线自动重连、ssh 命令行导入导出、系统托盘常驻。

| 项 | 值 |
| --- | --- |
| 形态 | 桌面应用（macOS / Windows / Linux） |
| 外壳 | Wails v2.12（Go + 系统 WebView） |
| 后端 | Go 1.25，`golang.org/x/crypto/ssh` |
| 前端 | Vue 3 + TypeScript + Pinia + Vue Router，Vite 3 |
| 托盘 | `fyne.io/systray` |
| 配置 | JSON，存于用户配置目录（macOS: `~/Library/Application Support/ssh-tunnel/config.json`） |

## 2. 目录结构

```
.
├── main.go                  # 入口：Wails 配置、托盘回调、退出流程
├── app.go                   # 绑定给前端的 App 方法（Tunnel*/Settings/Import…）
├── internal/
│   ├── config/              # 数据模型、JSON 持久化、ssh 命令解析/导出
│   ├── tunnel/              # 隧道生命周期、重连、代理拨号、三种转发实现
│   ├── tray/                # 系统托盘菜单与图标
│   ├── dock/                # macOS Dock 显隐（cgo）
│   └── autostart/           # 开机自启（LaunchAgent / 注册表 / .desktop）
├── frontend/src/
│   ├── App.vue              # 布局 + 侧边栏 + 事件订阅
│   ├── views/               # Dashboard / TunnelEdit / ImportView / Settings
│   ├── stores/tunnel.ts     # Pinia store
│   └── wailsjs/             # 由 wails 自动生成的绑定，不要手改
├── build/                   # 图标、Info.plist、NSIS 脚本等打包资源
└── .memory/                 # ★ 项目记忆（见第 7 节）
```

## 3. 常用命令与跨平台验证

### 3.1 常用命令

```bash
# ---- Go 后端 ----
# 注意：本机 proxy.golang.org 不通，务必带镜像，否则依赖拉不下来
export GOPROXY=https://goproxy.cn,direct

go vet ./...                      # 静态检查
go test -race -count=1 ./...      # 全部测试（含竞态检测）
go build -o /tmp/st .             # 全量编译（会走 cgo，能验证 dock 等平台代码）

# ---- 前端 ----
cd frontend && npm run build      # 内含 vue-tsc --noEmit 类型检查 + vite 打包
cd frontend && npm run dev        # 单独调前端（后端接口会缺失，通常用 wails dev）

# ---- 应用 ----
wails dev                         # 热重载开发
wails build -clean                # 打包到 build/bin/
wails build -clean -ldflags "-X main.Version=v1.2.3"   # 注入版本号（关于页展示）
```

> 改了 Go 代码至少要跑 `go vet` + `go test -race ./...` + `go build`；
> 改了 `frontend/src` 至少跑一次 `npm run build`（类型错误只在 vue-tsc 阶段暴露）。

### 3.2 跨平台验证（硬性要求）

**本项目要同时支持 macOS / Windows / Linux，任何改动都必须在三个平台上验证通过，只在本机跑通不算完成。**

本机能做的三平台验证（无需真机）：

```bash
export GOPROXY=https://goproxy.cn,direct

# ① 纯 Go 包：直接交叉 vet（会连同 _test.go 一起编译）
for t in darwin/arm64 darwin/amd64 windows/amd64 windows/386 linux/amd64 linux/arm64; do
  GOOS=${t%/*} GOARCH=${t#*/} go vet ./internal/...
done

# ② macOS 双架构全量编译（cgo 需显式开）
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o /tmp/st-arm64 .
CGO_ENABLED=1 CC=clang GOOS=darwin GOARCH=amd64 go build -o /tmp/st-amd64 .

# ③ Windows 交叉编译（纯 Go，CGO 关掉）
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /tmp/st.exe .
CGO_ENABLED=0 GOOS=windows GOARCH=386   go build -o /tmp/st386.exe .

# ④ Linux 交叉编译（根包在无 cgo 时也能编过；带 cgo 的完整构建需 gtk/webkit 头文件）
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/st-linux .
```

**真正跑测试**：`go test` 不能跨平台执行，所以

- 本机（macOS）跑 `go test -race ./...`；
- Linux 可以用容器跑：交叉编译 `-c` 出测试二进制，挂进任意 linux 镜像执行（静态链接，无需容器内装 Go）；
- Windows 本机无法执行时，至少保证 `GOOS=windows go vet ./internal/...` 和 `go test -c` 通过，并把验证结果写进 `.memory/` 当天日志；
- **CI 已经兜底**：`.github/workflows/test.yml` 在 ubuntu / macos / windows 三平台跑 vet + test + build，PR 上必须全绿。

平台差异注意点：

| 点 | 说明 |
| --- | --- |
| 文件原子写 | `os.Rename` 覆盖已存在文件在 Windows 上可用（Go 用 `MOVEFILE_REPLACE_EXISTING`），但目标被其它进程独占打开时会失败 |
| 半关闭 | `*net.TCPConn.CloseWrite` 三平台都支持；ssh 的 conn 包装也支持 |
| cgo | 只有 `internal/dock` 是 cgo（仅 darwin）；Linux 根包需要 gtk/webkit2gtk 头文件 |
| 构建标签 | 新增平台相关文件必须带 `//go:build`，并同步检查 `dock_other.go` 这类兜底实现 |
| 路径 | 一律用 `filepath`，不要手拼 `/` 或 `\` |

## 4. 架构速览

```
前端 Vue ──绑定调用──> app.go ──> tunnel.Manager ──> 每个 Tunnel 的 run() goroutine
   ▲                                                        │
   └────── 事件 "tunnel:status" ◄── setStatus 回调 ◄─────────┘
                                     │
                          Store.UpdateRuntimeStatus（仅内存）
                          tray.SetTunnelState（托盘图标/菜单）
```

- **一条隧道一个 goroutine**：`run()` 内部是 `连接 → 起转发 → 等断线 → 退避重连` 的循环，退出时关闭 `doneCh`。
- **状态三处同步**：`Store`（给前端列表用）、`Tray`（托盘菜单）、Wails 事件（推给前端）。改状态时三者要一起改。
- **配置只在增删改时落盘**，运行时状态（running/error/startedAt）不写盘，重启后一律重置为 stopped。
- **前端绑定方法在 `app.go`**，返回值/参数用 `config` 包的结构体，Wails 会自动生成 `frontend/wailsjs/` 下的 TS 类型。

## 5. 编码约定

- **语言**：注释、错误信息、日志、提交信息全部用中文。
- **错误处理**：Go 侧错误用 `fmt.Errorf("做某事失败: %w", err)` 包装，不要吞错；面向用户的错误要能看懂（不要直接把 `dial tcp ...` 甩给用户）。
- **注释**：解释「为什么」而不是「做什么」。凡是绕坑的写法（超时、锁、主线程调度），必须写清踩过什么坑。
- **并发**：共享状态用 `sync.Mutex` 保护并在注释里说明保护范围；跨 goroutine 传递信号优先用 channel；不要让持锁路径回调外部函数（会死锁）。
- **前端**：`<script setup>` + TS；调后端统一走 `frontend/src/stores/tunnel.ts` 或 `wailsjs/go/main/App`；破坏性操作必须二次确认。
- **不要手改** `frontend/wailsjs/`（构建时自动生成）、`frontend/dist/`、`build/bin/`。

## 6. 已知陷阱（改相关代码前必读）

1. **退出流程**：Wails darwin 的 `Frontend.Quit()` 会先调用 `OnBeforeClose`，返回 `true` 就**不会真正退出**。
   主动退出必须先置 `quitting` 标志，否则窗口隐藏、托盘已销毁、进程变僵尸。
2. **AppKit 主线程**：`NSApp setActivationPolicy` 等必须在主线程，`internal/dock` 已用 `dispatch_async(dispatch_get_main_queue())` 包住。
   systray 的 `SetTitle`/`SetIcon` 内部是 `waitUntilDone:YES`，**不要在主线程里等隧道 goroutine**，否则死锁。
3. **SSH 握手超时**：`ssh.ClientConfig.Timeout` 只在 `ssh.Dial` 内部生效。用 `ssh.NewClientConn` 必须自己 `conn.SetDeadline`，
   且握手成功后要清掉 deadline，否则长连接会在超时点被断开。
4. **半关闭**：转发管道必须双向等待并传播 `CloseWrite`。只等一个方向会截断「先发请求再半关闭」类协议的响应。
5. **keepalive**：`client.SendRequest(..., wantReply=true, nil)` 在链路静默死亡时**不会返回错误而是永久阻塞**，必须自己加超时后 `client.Close()`。
6. **路由复用**：`/edit/:id` 与 `/edit` 是同一条路由记录，组件会被复用，表单初始化必须用 `watch(route.params.id)`，不能只靠 `onMounted`。
7. **配置文件**：写盘走 `Store.persist()`（临时文件 + rename 原子替换）；损坏的配置会被备份为 `config.json.corrupt-<时间戳>` 并以默认配置启动。

## 7. 记忆协议

**所有 AI 工具的记忆都读写仓库根目录的 `.memory/` 目录。** 不要用工具自己的私有目录
（`.workbuddy-ai/`、`.cursor/`、`.claude/`、`.codex/` 等）保存项目记忆——那些已被 `.gitignore` 忽略，换工具就丢。

### 7.1 目录结构

```
.memory/
├── MEMORY.md            # 长期事实：架构约定、踩坑记录、决策。跨会话长期有效
└── YYYY-MM-DD.md        # 每日工作日志：当天做了什么、为什么。只追加不修改
```

### 7.2 会话开始时：先读

1. **必读** `.memory/MEMORY.md`——项目的长期约束都在这里，不读会重复踩坑。
2. **按需读** 最近 1–3 天的 `.memory/YYYY-MM-DD.md`（按文件名倒序），了解最近改动脉络。
3. 与当前任务无关的历史不必通读；需要跨项目、跨会话回溯时再找用户确认。

### 7.3 完成实质工作后：写入

| 内容 | 写到哪 | 方式 |
| --- | --- | --- |
| 当天做了什么、为什么这么改 | `.memory/YYYY-MM-DD.md` | **追加**（文件不存在则先创建） |
| 长期有效的结论：架构决策、约定、踩坑、用户偏好 | `.memory/MEMORY.md` | **就地更新**，合并同类项，保持精简 |
| 临时信息：命令输出、搜索结果、一次性报错、随手路径 | —— | **不要写** |

**算「实质工作」的**：新增/修改功能、修 bug、重构、定技术方案、写文档或报告、确立项目约定。
**不算的**：打招呼、简单查询、纯问答、只读文件看看。

### 7.4 日志格式

```markdown
# 2026-10-21

## 修复隧道停止卡死

- 现象：对端只 accept 不发 SSH banner 时，点「停止」永不返回。
- 原因：`connect()` 用 `ssh.NewClientConn` 却没设 deadline。
- 改动：`internal/tunnel/tunnel.go` 加 15s 握手 deadline + stopCh 关闭底层连接。
- 验证：新增 `TestStopInterruptsHandshake`，修复前 5s 超时，修复后 0.3s 通过。
```

要点：写清「现象 → 原因 → 改动 → 怎么验证的」。四行能说清就别写四段。

### 7.5 硬性规则

- `YYYY-MM-DD.md` **只追加**，不改写历史；要修正结论就写在新的一段里并注明「更正」。
- `MEMORY.md` 保持精简（目标 < 3000 字），过时内容直接删掉，不要堆成流水账。
- **不写密钥、密码、token、内网 IP、真实主机名**等敏感信息。
- 超过 30 天的日志，把仍有价值的结论提炼进 `MEMORY.md` 后，可以删除旧日志文件。
- 记忆是辅助材料，**不能替代**给用户的正式回答或代码改动本身。

### 7.6 其他工具的接入

`AGENTS.md` 是跨工具标准，Codex、Cursor、Copilot、Windsurf、Zed、Gemini CLI 等会直接读取。
若某个工具只认自己的文件名，在仓库根目录建一个软链即可（**不要复制内容**，避免两份文件不同步）：

```bash
ln -s AGENTS.md CLAUDE.md            # Claude Code
mkdir -p .cursor/rules && ln -s ../../AGENTS.md .cursor/rules/agents.md   # Cursor
```

## 8. 提交与发布

- 提交信息：`feat: xxx` / `fix: xxx` / `chore: xxx`（CI 的 Release 变更日志按此分类，前缀写错会漏进「其它」）。
- 发布：打 `v*` tag 触发 `.github/workflows/release.yml`，构建 linux-amd64、windows-amd64/386、darwin-amd64/arm64/universal 并发布 Release。
- 版本号通过 `-ldflags "-X main.Version=<tag>"` 注入 `app.go` 的 `Version`，本地构建默认 `dev`。

## 9. 不要做的事

- 不要提交 `frontend/dist/`、`build/bin/`、`node_modules/`、`.DS_Store`。
- 不要手改 `frontend/wailsjs/`。
- 不要为了「跑通」而降低安全性（例如把 `HostKeyCallback` 之外的校验也一并去掉、把密码明文写进日志）。
- 不要在没有验证的情况下声称修好了——至少跑一遍第 3.1 节的命令，并在 `.memory/` 里留下验证方式。
- **不要只在 macOS 上跑通就说「改好了」**——必须按 3.2 做三平台验证，Windows / Linux 的结论要明确写清是「编译通过」还是「测试通过」。
