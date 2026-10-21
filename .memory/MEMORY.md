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
- **连接监控口径**：`up` = 客户端→目标，`down` = 目标→客户端；速率是**每秒字节数**，
  `sampleAt` 按实际采样间隔换算（`delta * 1e9 / elapsed`），不是「每次采样的增量」——
  推送循环间隔恒大于 1s，ticker 漏拍时直接拿增量当速率会翻倍虚高。
- **`-D` 的 HTTP 代理不复用上游**：`handleHTTPProxy` 的 keep-alive 循环里每条请求都
  `client.Dial` 一次（多付 SSH 通道建立 + 源站 TCP 握手各 1 RTT）。只有明文 HTTP 受影响，
  HTTPS 走 CONNECT 是裸管道、本来就只有一条上游。2026-10-21 评估后决定暂不修（要处理
  「复用连接已失效 → 重放」的正确性），细节见当日日志。

## 踩过的坑（改代码前务必看）

1. **Wails 退出**：darwin 的 `Frontend.Quit()` 会先问 `OnBeforeClose`，返回 `true` 就跳过真正退出。主动退出必须先置 `quitting` 标志，否则窗口隐藏、托盘销毁、进程变僵尸。
2. **AppKit 主线程**：`NSApp setActivationPolicy` 等调用必须在主线程，`internal/dock` 已用 `dispatch_async(dispatch_get_main_queue())` 包住；systray 的 `SetTitle`/`SetIcon` 内部是 `waitUntilDone:YES`，不要在主线程里等隧道 goroutine。
3. **SSH 握手超时**：`ssh.ClientConfig.Timeout` 只在 `ssh.Dial` 内生效。用 `ssh.NewClientConn` 必须自己 `conn.SetDeadline`，握手成功后要清掉 deadline，否则长连接会在超时点被断开。
4. **半关闭**：转发管道必须双向等待并传播 `CloseWrite`，只等一个方向会截断响应。
5. **keepalive**：`client.SendRequest(..., true, nil)` 在链路静默死亡时不会返回错误而是永久阻塞，必须自己加超时后 `client.Close()`。
6. **路由复用**：`/edit/:id` 与 `/edit` 是同一条路由记录，组件会复用，表单初始化必须用 `watch(route.params.id)` 而不是只靠 `onMounted`。
7. **Windows 文件替换**：`os.Rename` 覆盖已存在文件是支持的（`MOVEFILE_REPLACE_EXISTING`），但目标被杀毒/索引服务独占时会 access denied —— `store.persist` 已加 5 次短重试（`renameFile`）。
8. **固定表头 + 独立滚动区**：`.thead` 和 `.tbody` 共用一套 grid 列宽时，经典滚动条会占掉
   tbody 的内容宽度，表头不让位就会逐列错开（1fr 列先吃，定宽列整体左移）。
   用 `offsetWidth - clientWidth` 量出宽度补到表头 `padding-right`，并挂 `ResizeObserver`。
9. **速率断言不要依赖采样间隔**：`UpRate` 是「每秒字节数」，两次 `Sample()` 之间的耗时
   直接决定数值大小（间隔 1s 才等于增量）。断言「速率 >= N 字节」会假失败：
   - 间隔 > 1s 时算出的速率**小于**增量；
   - 间隔恰好 0 时（Windows 墙上时钟粒度约 15.6ms，密集采样很容易撞上）速率**直接归零**。
   要断言就用 `sampleAt` 显式给时刻，或只断言 `> 0`。集成测试里要测速率，
   必须先 `time.Sleep` 出一个可测的间隔（见 `conn_tracking_test.go` 的 100ms）。

## 发布

```bash
git push origin main                        # 先推 commit，否则 tag 指向的提交不在远端
git tag -a v0.0.2 -m "Release v0.0.2"       # annotated，与 v0.0.1 一致
git push origin v0.0.2                      # 只有推 tag 才会触发 Release
```

- `release.yml` 两个触发：`push: tags: v*` 走完整发布；`workflow_dispatch` 只构建、版本号是 `dev`。
- 重发同一版本必须先删 GitHub Release → `git push origin :v0.0.2` → 重打，直接 force-push tag 会撞「Release already exists」。
- **NSIS 安装包缺口已闭合**（v0.0.1 的 assets 里没有 `*-installer.exe`）。
  根因在 wails 源码 `pkg/commands/build/nsis_installer.go`：`makensis` 不在 PATH 时它只
  `Println("Warning: Cannot create installer: makensis not found")` 然后 `return nil`，
  构建照常成功。所以 `Rename NSIS installer` 的 `nullglob` 兜底也永远走空、job 全绿。
  修法（已落地在 `release.yml`）：Windows job 里 `choco install nsis -y` 并把
  `C:\Program Files (x86)\NSIS` 加进 `GITHUB_PATH`（同一步要用全路径自检，`GITHUB_PATH`
  只对后续步骤生效）；同时把 rename 步骤的「警告并继续」改成 `exit 1`。
  v0.0.2 第二次发布已产出 `ssh-tunnel-v0.0.2-windows-amd64-installer.exe`（6.51 MB）。

## Windows CI：速率断言在 0 秒间隔上失败（已修）

**现象**：`Test` 工作流在 windows-latest 上挂，run `39578629433`（commit `fa0f881`）。
`Vet` 通过、`Test (race)` 被 skip、`go test -count=1 ./...` 退出码 1；macOS / Linux / 前端全绿。

**根因**（真实日志，别再猜）：

```
--- FAIL: TestConnectionTracking (0.08s)
conn_tracking_test.go:287: 活跃连接速率异常: upRate=0，期望 >= 4096
```

`upRate` 是**恰好 0**，不是偏小。`sampleAt` 里归零只有三条路径：首轮基准、
`elapsed > maxSampleGap(3s)`、`elapsed <= 0`。测试总共 0.08 秒 → 只能是 `elapsed <= 0`。

**为什么 elapsed 会是 0**：Windows 的墙上时钟（`time.Now().UnixNano()`）粒度约 **15.6ms**，
而测试里两次 `reg.Sample()` 之间只隔一次 4KB 回环收发（几百微秒），很容易落在**同一个 tick**。
改动前那句是 `e.rateUp.Store(u - e.lastUp.Swap(u))`，直接存增量、根本不看 elapsed，所以
0 间隔也能过；加了 `elapsed <= 0` 的除零保护把它丢进归零分支后就挂了。
生产侧只有 `app.go:170` 一处调用、1Hz ticker，**永远不会出现 0 间隔**，所以修的是测试不是代码。

**修法**（`internal/tunnel/conn_tracking_test.go`）：两次采样之间 `time.Sleep(100 * time.Millisecond)`
模拟 1Hz 节奏，让间隔在各平台都可测；断言改成 `UpRate > 0` + `Up >= len(big)`，
不再钉具体数值——精确换算由 `internal/conn` 的 `TestRateNormalizedByElapsed` 用显式时刻覆盖。
另加 `TestSampleSameInstantYieldsZeroRate` 把「0 间隔必须归零、不能除零崩」这个契约钉住。

**排查经验**：
- `actions/runs/{id}/jobs` 的 `steps[].started_at/completed_at` **匿名可读**，能直接拿到每步耗时。
  这次就是靠「失败步骤 4.0s == 上次成功步骤 4.0s」先排除了「慢机器等满超时」这条错路。
- job logs 接口匿名 403 `Must have admin rights`；`check-runs/{id}/annotations` 匿名可读
  （但只有一句退出码）；网页 `.../job/{id}` 匿名 200 但日志登录后才异步加载。
- **`test.yml` 已加 `Surface test failures` 步骤**：输出 tee 到 `test-output.txt`，
  失败时把 `--- FAIL` / `panic:` / `_test.go:行号` 提取成一条 annotation。
  annotation 匿名可读，以后 CI 挂了直接 `curl .../check-runs/{id}/annotations` 就能看到
  是哪个测试、哪一行。**这是本项目排查 CI 失败的标准入口。**

**副产品**：这次日志里 `conn_tracking_test.go:240: 进程识别成功: pid=9072 name="tunnel.test.exe"`
说明 `internal/proc` 的 Windows 实现（`GetExtendedTcpTable` + `QueryFullProcessImageNameW`）
**在运行时确实可用**，不再只有编译和 vet 证据。

## 约定

- 代码注释、错误信息、提交信息都用中文；提交前缀沿用 `feat:` / `fix:` / `chore:`（CI 的 changelog 按此前缀分类）。
- 新增导出函数写清楚「为什么」，尤其是绕坑的写法，别只写「做什么」。
- 改动涉及并发/生命周期时，优先补 `_test.go` 回归用例（现有：`tunnel_test.go`、`forward_test.go`、`store_test.go`、`ssh_export_test.go`、`parser_test.go`、`proxy_test.go`）。
