# 项目长期记忆

> 本文件是项目的「长期事实」存储：架构约定、踩过的坑、决策记录。
> 逐日的流水记录写在同目录的 `YYYY-MM-DD.md`，本文件只保留仍然有效、跨会话需要知道的内容。
> 维护方式见仓库根目录 `AGENTS.md` 的「记忆协议」一节。

## 项目定位

SSH Tunnel —— 跨平台 SSH 端口转发管理工具。Wails v2（Go + WebView）外壳，前端 Vue 3 + TS + Pinia，
托盘常驻，支持 `-L` / `-R` / `-D` 三种转发、代理中转（HTTP/HTTPS/SOCKS5）、自动重连、ssh 命令导入导出。

## 命令

```bash
# Go 侧（本机 proxy.golang.org 不通，必须带镜像）
GOPROXY=https://goproxy.cn,direct go vet ./...
GOPROXY=https://goproxy.cn,direct go test -race -count=1 ./...
GOPROXY=https://goproxy.cn,direct go build -o /tmp/x .     # 含 cgo/dock，可验证全量编译

# 前端（build 内含 vue-tsc --noEmit 类型检查）
cd frontend && npm run build

# 开发 / 打包（需 wails CLI v2.12.0，与 go.mod 对齐）
wails dev
wails build -clean -ldflags "-X main.Version=v1.0.0"
```

`frontend/dist`、`build/bin` 已在 `.gitignore` 中，属构建产物。

## 跨平台（硬性要求）

**本项目必须同时支持 macOS / Windows / Linux，任何改动都要三平台验证通过才算完成**（用户明确要求，2026-10-21）。
完整命令见 `AGENTS.md` 3.2 节，要点：

- 纯 Go 包用 `GOOS/GOARCH` 交叉 `go vet`（会连测试一起编译）；
- 根包 Windows 用 `CGO_ENABLED=0` 交叉编译（Wails/systray 在 Windows 全是纯 Go，已验证 amd64/386）；
- macOS 双架构需 `CGO_ENABLED=1`（amd64 交叉要 `CC=clang`）；
- `go test` 不能跨平台执行：Linux 用「交叉编译测试二进制 + 容器跑」（静态链接，容器内不用装 Go）；Windows 本机无法执行时至少保证 `go vet` + `go test -c` 通过；
- CI `.github/workflows/test.yml` 已在 ubuntu/macos/windows 三平台跑 vet + test + build 兜底。

## 架构要点

- **状态流**：`tunnel.Manager` → 每个 `Tunnel` 的 `run()` goroutine → `setStatus` → 回调同时更新 `Store.UpdateRuntimeStatus`（仅内存，不落盘）、托盘、前端事件 `tunnel:status`。
- **配置落盘**：只有 `SaveTunnel` / `DeleteTunnel` / `SetSettings` / `ImportConfig` 会写盘，走 `Store.persist()`（`writeMu` 串行 + 临时文件 rename 原子替换）。
- **停止语义**：`Tunnel.Stop()` 阻塞直到 `run()` 退出；`run()` 内部一律使用启动时捕获的局部 `stopCh`，不再读 `t.stopCh` 字段。
- **托盘**：systray 用 `RunWithExternalLoop`，必须在 `wails.Run` 之前 `Init()` + `Start()`；菜单重建用「代际 done 通道」回收旧监听 goroutine。

## 踩过的坑（改代码前务必看）

1. **Wails 退出**：darwin 的 `Frontend.Quit()` 会先问 `OnBeforeClose`，返回 `true` 就跳过真正退出。主动退出必须先置 `quitting` 标志，否则窗口隐藏、托盘销毁、进程变僵尸。
2. **AppKit 主线程**：`NSApp setActivationPolicy` 等调用必须在主线程，`internal/dock` 已用 `dispatch_async(dispatch_get_main_queue())` 包住；systray 的 `SetTitle`/`SetIcon` 内部是 `waitUntilDone:YES`，不要在主线程里等隧道 goroutine。
3. **SSH 握手超时**：`ssh.ClientConfig.Timeout` 只在 `ssh.Dial` 内生效。用 `ssh.NewClientConn` 必须自己 `conn.SetDeadline`，握手成功后要清掉 deadline，否则长连接会在超时点被断开。
4. **半关闭**：转发管道必须双向等待并传播 `CloseWrite`，只等一个方向会截断响应。
5. **keepalive**：`client.SendRequest(..., true, nil)` 在链路静默死亡时不会返回错误而是永久阻塞，必须自己加超时后 `client.Close()`。
6. **路由复用**：`/edit/:id` 与 `/edit` 是同一条路由记录，组件会复用，表单初始化必须用 `watch(route.params.id)` 而不是只靠 `onMounted`。
7. **Windows 文件替换**：`os.Rename` 覆盖已存在文件是支持的（`MOVEFILE_REPLACE_EXISTING`），但目标被杀毒/索引服务独占时会 access denied —— `store.persist` 已加 5 次短重试（`renameFile`）。

## 约定

- 代码注释、错误信息、提交信息都用中文；提交前缀沿用 `feat:` / `fix:` / `chore:`（CI 的 changelog 按此前缀分类）。
- 新增导出函数写清楚「为什么」，尤其是绕坑的写法，别只写「做什么」。
- 改动涉及并发/生命周期时，优先补 `_test.go` 回归用例（现有：`tunnel_test.go`、`forward_test.go`、`store_test.go`、`ssh_export_test.go`、`parser_test.go`、`proxy_test.go`）。
