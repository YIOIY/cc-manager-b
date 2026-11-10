# cc-manager 优化项清单（2026-11-08）

> **执行状态（2026-11-08 完成四轮落地）**：下表标注每一项的处理结果；原始分析保留在后文供对照。验证方式：`make test`（go vet + go test -race + 前端单测）、`make lint`（vue-tsc / eslint / prettier）、`make build`，以及在临时端口上用浏览器逐页检查。

| 项 | 状态 | 说明 |
| --- | --- | --- |
| P0-1 仓库卫生 | ✅ 已做 | `git init`、`.gitignore`/`.dockerignore`/`.editorconfig`；Dockerfile 只复制 `cmd internal go.mod go.sum`。`.local-run/config.json` **未移动**（本机仍有进程在用），已被 gitignore；残留 `local-dev.db-wal`、`smoke2.db-wal`、`deploy/artifacts/*.tar.gz`、部署验收记录保留在磁盘但不入库，删除与脱敏由使用者决定 |
| P0-2 前端构建产物 | ✅ 已做 | 删除 `src/**/*.js`、`tsbuildinfo`；`tsconfig.app.json` `noEmit`；build 改 `vue-tsc --noEmit && vite build` |
| P0-3 超时/优雅关闭 | ✅ 已做 | `http.Server` 超时；`signal.NotifyContext` → `Shutdown` → `App.Close()`（cancel worker ctx、`wg.Wait`、WAL checkpoint）；worker 循环 `select ctx.Done` |
| P0-4 tzdata | ✅ 已做 | `import _ "time/tzdata"` + 镜像 `apk add tzdata ca-certificates` |
| P0-5 限流/会话/请求体 | ✅ 已做 | 登录按 IP+用户名指数退避（429）；会话挂到 App、上限 10000、匿名 30 分钟、每分钟清理；`decodeJSON` 统一 MaxBytesReader |
| P0-6 Scan 丢行 | ✅ 已做 | 所有 Scan 错误返回 500 + 日志；新库列 `NOT NULL DEFAULT`；迁移 v3 回填旧库 NULL；补 `rows.Err()`；有回归测试 |
| P0-7 错误吞没/零日志 | ✅ 已做 | `dbError` 用 sqlite 错误码 2067/1555 判 UNIQUE → 409，其余 500；`serverError` 带 request_id 的 slog；供应商拒绝原因脱敏存入 `result_json.message` |
| B1 PRAGMA DSN | ✅ 已做 | `_pragma=busy_timeout/journal_mode/foreign_keys/synchronous&_txlock=immediate` |
| B2 查询 context | ✅ 已做 | handler、worker（`runOne`/指标同步）、账单创建/调度/执行、账单查询全部传 ctx；仅无请求上下文的小工具函数（`finishJob`、`preflight`、`billRunDetail`）保留无 ctx 调用 |
| B3 原子写入 | ✅ 已做 | 创建账号、导入提交单事务；worker 失败路径统一回写 deliveries；重启把 `running` 交付标 `unknown` |
| B4 密码哈希/密钥轮换/清理 | ✅ 已做 | argon2id 哈希 + `hash-password`；janitor 清理；密文 `k1:<kid>:…` + `CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS` + `rotate-key [--check]`（单事务、无法解密整体回滚、含导入草稿），有端到端测试 |
| B5 反向代理 | ✅ 已做 | `CC_MANAGER_TRUST_PROXY=1` 时读 `X-Forwarded-Proto/Host/For`、`X-Real-IP` |
| B6 安全头/SPA/静态 | ✅ 已做 | 中间件加 nosniff/X-Frame-Options/Referrer/Permissions-Policy，index.html 加 CSP；SPA 回退；`/assets` immutable；`CC_MANAGER_WEB_DIR`。未用 `go:embed`（保持前后端可独立构建） |
| B7 索引/查询 | ✅ 已做 | 迁移 v3 增加 20 余个索引；账号列表改为单次 CTE 关联；jobs `ORDER BY created_at,rowid` |
| B8 乐观锁 | ✅ 已做 | PATCH 账号、PUT 凭证、PUT 平台连接接受 `expected_version` |
| B9 路由/拆分 | ✅ 已做 | 路由 `router.go` + `protect`；`runBillJob` 拆为 `billRun.{claim,prepare,process,reserve,fetch,persist}` 与 `billRetryDelay`。未引入 repository/service 层（当前规模下不值得） |
| B10 重复代码/魔数 | ✅ 已做 | 套餐、元数据键、代理协议、账号身份、日期校验收敛到 `validation.go`；超时/TTL/上限改为常量 |
| B11 死代码 | ✅ 已做 | 删除 `proxyFingerprint`、`sortStrings`、`mailParse`、`account.onboard`；`/audit-events` 补全为真实事件。第四轮已删除 `/usage`、`/billing/ledger`、`/sync/*`、`/platform-usage/sync` |
| B12 配置集中 | ◐ 部分 | `Config` 启动时解析校验（DB、密码、密钥、限额、代理信任）；供应商 TLS pin 仍在 `supplier.New` 内按调用解析（测试依赖此行为，解析开销可忽略） |
| B13 迁移版本化 | ✅ 已做 | `[]migration{version,name,up}`，每步独立事务；测试断言新库包含迁移列并可重复执行 |
| B14 可观测性 | ✅ 已做 | slog JSON 日志 + request_id 中间件；`/healthz` Ping DB、worker 心跳、版本；`-ldflags -X main.version`；第四轮新增 `GET /metrics`（Prometheus 文本） |
| F1 拆 App.vue | ✅ 已做 | `views/{Login,Dashboard,List,PlatformBills}View.vue`、`layouts/AppShell.vue`、`components/{ListFilters,AccountCards,ImportDialog,AccountFormDialog,ProxyFormDialog,ConnectionFormDialog,PageHead}.vue`、`composables/{useSession,usePolling,useListPage}.ts` |
| F2 vue-router | ✅ 已做 | history 路由，筛选条件写入 query，导航记住每页最近筛选，`document.title` 跟随 |
| F3 类型 | ✅ 已做 | `types/api.ts`；`api.ts` 泛型 + `ApiError`；组件 props/emits 类型化（`PlatformBills`/`ProxyNetworkPanel` 内部仍有少量 `any`，ESLint 为 warn） |
| F4 按需引入/locale | ✅ 已做 | `unplugin-vue-components` + `ElConfigProvider zh-cn`；manualChunks。JS 1.09MB → 532KB，CSS 430KB → 154KB |
| F5 package.json | ✅ 已做 | 版本范围、`private/engines`、工具移 devDependencies、`typecheck/lint/format` 脚本 |
| F6 401 处理 | ✅ 已做 | `api.ts` 捕获 401 → 清会话回登录页；登录页与工作台错误分离；fetch 30 秒超时 |
| F7 校验去重 | ✅ 已做 | `model/account-form.ts` 纯函数，`tests/account-form.test.mjs` 覆盖 |
| F8 格式化统一 | ✅ 已做 | `utils/format.ts`（BigInt 安全金额/计数、日期、百分比、套餐） |
| F9 CSS | ✅ 已做 | 全局 CSS 按「规则最后一个归属类所在组件」迁入 SFC：视图/列表/卡片用 `<style scoped>`（以 `:deep()` 覆盖 Element Plus 内部、以 `:global()` 保留以标签或 EP 类结尾的选择器原有优先级），对话框用普通 `<style>` 块；跨组件共用类留在 `styles/shared.css`，令牌/重置/登录/壳层留在 `base.css`；删除 53 条无引用规则。用「旧构建 vs 新构建」逐元素计算样式指纹（14 个页面/对话框状态）验证无视觉差异。第四轮把重复 ≥3 次的色值（188 处）抽为 18 个派生变量，近似色收敛 |
| F10 测试 | ✅ 已做 | Vitest 5（支持 Vite 8）+ Vue Test Utils + happy-dom；原有纯函数测试迁移，新增 `api.ts` 与 `AccountDeliveryDialog` 组件测试，共 19 个 |
| F11 轮询/分页 | ✅ 已做 | `usePolling`：setTimeout 链 + `visibilityState`；`/jobs`、`/audit-events` 服务端分页与筛选，前端 `useListPage` 的 `serverPaged` 模式；第四轮账号列表改为服务端分页与筛选（`onboarding_state` 在 SQL 派生）；代理/连接保持客户端筛选 |
| F12/F13 tsconfig/格式化/lint | ✅ 已做 | 三个 tsconfig、`@/` alias、Prettier、ESLint flat config、`.editorconfig` |
| D1 Makefile/CI | ✅ 已做 | `Makefile`（test/lint/build/release）、`.github/workflows/ci.yml` |
| R4-1 写请求限速 | ✅ 已做 | 登录后的 POST/PUT/PATCH/DELETE 按用户令牌桶限速（`CC_MANAGER_WRITE_RATE_PER_MINUTE`，默认 120、突发 30），`429 RATE_LIMITED` + `Retry-After`；读请求不限 |
| R4-2 长函数拆分 | ✅ 已做 | `onboardAccount` 222 行 → 5 步（`apiError`/`respondError`）；`createBillRun` 184 行 → 6 步（幂等回放共用 `replayBillRun`）；app/onboard/bill_sync 内无 >100 行函数；覆盖率 62.7% → 64.9% |
| R4-3 UsersView 拆分 | ✅ 已做 | 1329 行 → 439 行 + `components/users/{RoleCard,RoleEditorDialog,UserFormDialog}.vue` + `access.ts`（纯逻辑，10 个新测试，前端共 30 个） |
| R4-4 端到端测试 | ✅ 已做 | Playwright（`web/e2e`，5 条：登录失败/成功、只读角色可见性与 API 403、检测路径向导），`make web-e2e`，CI 独立 job |
| R4-5 依赖升级 | ✅ 已做 | Go：modernc sqlite 1.36 → 1.62 等；前端：Playwright 新增。eslint 10 / TypeScript 7 / @types/node 26 / unplugin 32 大版本未升（需单独验证 lint 规则与类型变化） |
| R4-6 git 基线 | ◐ 待办 | 仍 0 次提交，待使用者 review 后 `git add -A && git commit` 并推送触发 CI |
| D2 backup.sh | ✅ 已做 | 不再复制 env；`RETENTION_DAYS` 清理 |
| D3 部署口径 | ✅ 已做 | `cc-manager.env.example`；`install.sh` 用 `SERVER_NAME/APP_PORT/NGINX_PORT`；README 列全环境变量 |
| D4 Docker | ✅ 已做 | `HEALTHCHECK`、`env_file`、`restart`、`read_only`、`cap_drop`、`no-new-privileges` |
| D5 systemd | ✅ 已做 | 补齐硬化项，`Restart=always`，`TimeoutStopSec=40` 配合优雅关闭 |
| D6 nginx | ✅ 已做 | HSTS/Permissions-Policy，按路径缓存头，限速 key 回退 |
| D7 install.sh | ✅ 已做 | 可重入、`ln -sfn` + 原子 `mv -T` |
| D8 artifacts | ◐ 部分 | 已 gitignore；磁盘上的旧 tar.gz 未删除 |
| D9 toolchain | ✅ 已做 | `go.mod` 声明 `toolchain go1.26.0`（已删除对 `.tools/` Windows 工具链的依赖） |
| P2 文档 | ✅ 已做 | README 重写、`docs/operations.md`、`CHANGELOG.md`、导航补全、实施计划勾选、architecture 标注实际差异 |
| P2 前端小项 | ✅ 已做 | `randomId()` 回退、`Object.hasOwn`、连接表单保存后清空 token、审计列空值显示 `—`、`Math.max` 展开改循环 |
| P2 后端测试 | ✅ 已做 | 在第一轮基础上补 `syncPlatformConnection` 成功路径、导入原子性、`decimalFen` 边界、密钥环/轮换、服务端分页测试 |

**剩余建议**：账号列表若超过数千条再做服务端筛选；按需引入 repository 层；颜色硬编码可逐步替换为 `:root` 变量。

---

基于对 `cmd/`、`internal/`（约 7200 行 Go）、`web/src/`（Vue 3 + TS）、`deploy/`、`docs/` 的完整阅读整理。关键结论均已在源码中逐条核对。

**一句话结论**：业务核心（账单同步的 generation/lease/幂等、交付状态机、代理链路探测、CSRF/HttpOnly 会话）设计细致且有测试，但"外壳"严重落后——无版本控制、无 CI、HTTP 无超时与优雅关闭、错误被吞没且无日志、前端 57KB 单文件且被压缩成超长行、构建产物与真实密钥散落在项目根目录。**在接入真实凭证或继续加功能之前，应先做下面 P0 的清理和加固。**

---

## P0 · 立即处理（安全 / 一跑就炸 / 阻塞后续一切重构）

### 1. 仓库卫生：没有 git、没有 .gitignore、密钥和 1.6GB 残留躺在根目录

- 根目录无 `.git`、无 `.gitignore`、无 `.dockerignore`。
- `.local-run/`（1.2GB）含 `config.json`（真实管理员密码、32 字节主加密密钥、供应商 TLS pin）、`key.pem` 私钥、多份完整项目副本、448 张验收截图、Chrome profile；`.tools/`（378MB）为 Windows Go 工具链；`.omx/` 为 AI 工具会话状态。
- 根目录散落 `cc-manager.db`、`local-dev.db-wal`、`smoke2.db-wal`（后两者没有对应 `.db`，说明进程曾被非正常杀死，见 P0-3）。数据库内含真实 Gmail 账号与真实供应商 IP 的连接记录。
- `deploy/Dockerfile` 第 12 行 `COPY . .` 会把上述全部内容送进构建上下文。
- `deploy/production/部署验收记录-20261105.md` 含服务器公网 IP、Cloudflare Tunnel UUID、DNS 记录 ID。

**做法**：`git init`；添加 `.gitignore` / `.dockerignore`（模板见文末）；把 `.local-run/config.json` 迁到 `~/.config/cc-manager/`；Dockerfile 改为只 `COPY cmd internal go.mod go.sum ./`；部署记录脱敏或移出仓库；若将来公开仓库，先轮换加密密钥与管理员密码。

### 2. 前端构建产物污染源码目录（可能导致改 .ts 不生效）

- `web/tsconfig.json` 无 `noEmit`，`npm run build` 的 `vue-tsc -b` 在 `src/` 生成 14 个 `.js` / `.vue.js`（`App.vue.js` 278KB，内含 2049 处 `__VLS_` 类型桩）。
- `src/api.js` 与 `src/api.ts` 并存，Vite 裸导入 `./api` 的默认解析顺序 `.mjs,.js,.mts,.ts` 会**先命中过期的 .js**。

**做法**：`tsconfig.json` 加 `"noEmit": true`，build 改为 `vue-tsc --noEmit && vite build`（或按 Vite 官方模板拆 `tsconfig.app.json` / `tsconfig.node.json`）；删除 `src/**/*.js`、`src/**/*.vue.js`、`tsconfig.tsbuildinfo`。

### 3. HTTP Server 无超时、无优雅关闭、worker 不可停止

- `cmd/cc-manager/main.go:20` 直接 `http.ListenAndServe`，无 `ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout/MaxHeaderBytes`，无信号处理；全仓库 `grep Shutdown` 零命中。
- `internal/app/worker.go:22-72` 起 4 个后台 goroutine，退出条件是 `DB.Ping()!=nil`，无 context / WaitGroup。systemd stop 时正在进行的供应商请求和事务被硬杀。
- `docs/architecture.md` 4.2 承诺的"平滑退出时停止接收任务"未实现。

**做法**：`http.Server{ReadHeaderTimeout:5s, ReadTimeout:15s, WriteTimeout:90s, IdleTimeout:120s, MaxHeaderBytes:1<<20}`；`signal.NotifyContext` → `srv.Shutdown(ctx)`；`App.Close()`：cancel worker ctx、`wg.Wait()`、`PRAGMA wal_checkpoint(TRUNCATE)`、`db.Close()`；worker 循环改 `select{case <-ctx.Done()...}`。

### 4. Docker 镜像缺 tzdata，容器内账单同步必然失败

- `deploy/Dockerfile` 基础镜像 `alpine:3.22` 无 `/usr/share/zoneinfo`；`bill_sync.go:98`、`bills_query.go:168`、`worker.go:288` 调用 `time.LoadLocation`，代码未 `import _ "time/tzdata"`。生产是 Ubuntu 裸机所以验收没暴露。

**做法**：`main.go` 加 `import _ "time/tzdata"`，或 Dockerfile `RUN apk add --no-cache tzdata ca-certificates`。

### 5. 登录无限流、匿名会话可无限撑大内存、请求体无大小限制

- `app.go:148-166` `/auth/login` 只做常量时间比较，无失败计数/退避/IP 限流，失败不写审计不打日志。
- `security.go:81-93` 任何无 cookie 的 `GET /auth/session` 都会向全局 map 写一条 12h 的会话，过期条目只在被查到时删除，无后台清理。
- `http.MaxBytesReader` 全仓库只在 `bill_sync.go` 用了 1 次；login、accounts POST/PATCH、credentials、preflight、proxies、platform-connections、onboard、importCommit、account_delivery 全部不限 body。
- nginx 层虽有登录限速，但应用层自身无防护。

**做法**：按 IP+用户名的指数退避；抽公共 `decodeJSON(w, r, &v, limit)`（可复用 `bill_sync.go:41` 的模式）；sessions 挂到 `App` 上 + 定时 sweeper + 总量上限，匿名会话缩短到 30 分钟。

### 6. `rows.Scan(...) == nil` 静默丢行，NULL 列让账号"消失"

- 模式出现在 `app.go:200/313/370/617/644/675/747/861`、`usage.go:61/115`。
- 建表（`app.go:47-61`）大量列无 `NOT NULL DEFAULT ''`，只要 `accounts.plan` 等列为 NULL，该账号在 `GET /accounts` 里直接不见，`app.go:544` 详情会误报 404，无任何错误。
- 所有 `for rows.Next()` 后都没检查 `rows.Err()`。

**做法**：Scan 用 `sql.NullString` / `COALESCE`；错误一律 return 500 + 日志；schema 补默认值；补 `rows.Err()`。

### 7. 数据库错误整体吞没、错误码张冠李戴、全程零日志

- `app.go:457` 任何 INSERT 错误 → `409 ACCOUNT_EXISTS`；`app.go:283` → `409 CONNECTION_EXISTS`；`app.go:718` → `400 PROXY_INVALID`；`819/837` 同理。磁盘满、锁超时都被伪装成业务错误。
- `bill_sync.go:33-40 billFail`、`security.go:124 fail` 返回 500 但**不打印 err**；整个 `internal/app` 只有 `worker.go` 三处 `log.Printf`。
- `worker.go:187-196` 供应商拒绝原因被丢弃，`result_json` 只存 `{"code":"PLATFORM_REJECTED"}`。

**做法**：用 `errors.As` 取 `*sqlite.Error` 判断 `Code()==2067`（UNIQUE）再返回 409，其余 500；`fail/billFail` 加 request-id 并 `slog.Error`；`result_json` 存脱敏后的上游消息。

---

## P1 · 近期处理（可维护性 / 正确性 / 发布流程）

### 后端

| # | 问题 | 位置 | 建议 |
|---|---|---|---|
| B1 | SQLite PRAGMA 只在首个连接生效，`busy_timeout`/`foreign_keys` 是每连接属性，重建连接后丢失；未设 `synchronous` | `app.go:43-46` | DSN 用 `?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate` |
| B2 | 所有查询不带 context；`MaxOpenConns(1)` 下 `readBills` 一个事务内 4 条全扫查询会阻塞 worker 和其他请求 | 全文，`bills_query.go:101-163` | handler 传 `r.Context()`，worker 传带超时 ctx，长读查询不开写事务 |
| B3 | 非原子写入：创建账号两条 INSERT 无事务；`importCommit` 逐行 INSERT 中途失败留半截；deliveries 置 `running` 后失败分支不回写 | `app.go:456-472`、`advanced.go:176-236`、`worker.go:108-170` | 全部包进 `BeginTx`；失败路径统一 `deliveries → failed` |
| B4 | 密码明文常驻内存无哈希；密文无 key-id 前缀无法轮换；`imports.rows_json`（含凭证密文）、`proxy_checks`、`sync_runs`（每连接每 2 分钟 1 条）只增不删 | `app.go:74-96`、`crypto/box.go`、`advanced.go:125` | `CC_MANAGER_ADMIN_PASSWORD_HASH`（argon2id）；密文格式 `v1:<kid>:<nonce‖ct>` + 多 key 解密 + re-encrypt 子命令；后台定期清理过期行 |
| B5 | `Secure: r.TLS != nil`、`sameOrigin` 比较 `r.Host`，在反向代理/容器后 Secure 永远 false | `security.go:35-45, 68` | `CC_MANAGER_TRUST_PROXY=1` 时按 `X-Forwarded-Proto/Host` 判断 |
| B6 | 无安全响应头、`http.FileServer` 会列目录、SPA 深链刷新 404、`web/dist` 用 CWD 相对路径 | `app.go:111-119` | 中间件统一加 `nosniff/X-Frame-Options/Referrer-Policy/CSP`、API `Cache-Control: no-store`；自定义静态 handler 回退 `index.html`；`//go:embed` 前端 |
| B7 | 缺索引：worker 每秒全表扫 `jobs(state,created_at)`；`sync_runs` 每秒相关子查询 UPDATE 且无索引；`GET /accounts` 读取全部历史 jobs，每账号 9 个子查询 | `worker.go:76`、`bill_sync.go:353`、`account_delivery.go:92`、`app.go:347-357` | 补索引；最新任务改 `MAX(rowid) GROUP BY account_id`；`ORDER BY created_at, rowid`（`now()` 只有秒精度） |
| B8 | 乐观锁不完整：PATCH 和平台连接 PUT 不接受 `expected_version`，`proxy_network.go:132` 已有正确实现但未推广 | `app.go:490-521, 760-827` | 统一接受 `expected_version` |
| B9 | 路由是 200 行 if 链 + 手工 split；`runBillJob` 单函数 250 行 3 个事务 4 层嵌套；无 repository/service 层 | `app.go:142-343`、`bill_sync.go:345-598` | Go 1.22+ 的 `mux.HandleFunc("PATCH /api/v1/accounts/{id}")` + `authorize` 中间件；拆 `router.go / handlers_*.go`；`runBillJob` 拆为 claim/load/reserve/fetch/persist |
| B10 | 重复代码：套餐白名单 4 处、元数据 key 列表 6 处、代理协议白名单 4 处、邮箱校验 3 套（`onboard.go:342` 自写版本更弱）；`write()` 与 `fail()` 两套错误输出；十余个魔数散落（12h、35s、100、10000、90d、120s、3、6h/30h、5min、2h/24h、30min） | 多处 | 收敛到 `validation.go` 的常量与函数；统一 `Config` 结构体 |
| B11 | 死代码/半成品：`proxyFingerprint` 无调用、`sortStrings` 仅包装、`account.onboard` job kind 从未产生、`/sync/*` 返回 503、`/audit-events` 永远 `[]`、`usage_metrics` 表只读不写、`internal/httpapi` 与 `internal/store` 是空目录 | `onboard.go:275`、`bill_routes.go:366`、`app.go:334-340` | 删除或补全 |
| B12 | 配置散落 `os.Getenv`：`CC_MANAGER_BILL_MAX_ITEMS` 每次请求读、`TLS_PINS` 每次 `supplier.New` 重新解析；README 提到 `.local-run/config.json` 但代码从不读取 | `app.go:74-104`、`bill_sync.go:221`、`supplier/client.go:65-94` | 启动时一次性解析为 `Config` 并校验 |
| B13 | 迁移半版本化：`migrateProxyNetwork` 直接 `db.Exec` 不进 `schema_migrations` 无事务；新库 `CREATE TABLE` 与旧库 `ADD COLUMN` 两条路径无测试保证 schema 一致 | `proxy_network.go:36`、`migrations.go` | 统一为有序 `[]migration{version, up}`；加测试断言新库与升级库的 `sqlite_master` 一致 |
| B14 | 可观测性：无请求日志、无 request-id、`/healthz` 不 Ping DB 不反映 worker、无 `/metrics`、无版本号 | `app.go:116`、`main.go` | `log/slog` + 日志中间件；`/healthz` 做 `PingContext` + 最近 worker tick；ldflags 注入 `version`；expvar 或 Prometheus |

### 前端

| # | 问题 | 位置 | 建议 |
|---|---|---|---|
| F1 | `App.vue` 57KB 单文件，227 行但最长行 2310 字符，`<script setup>` 72 行内塞 25 个 ref、12 个 computed、30 个函数、7 个页面、3 套表单、JSON 导入、轮询；模板 `page===` 出现 48 次；与 `docs/frontend-design.md` §15"不要写巨型组件"背离 | `src/App.vue` | 先 Prettier 展开；拆 `views/{Login,Dashboard,List,Accounts}View.vue`、`layouts/AppShell.vue`、`components/{AccountForm,ProxyForm,ConnectionForm,Import}Dialog.vue`；composables `useListPage / usePolling / useSession / useJsonImport` |
| F2 | `vue-router` 已安装（5.4.0）零引用；页面状态是 `page` 字符串，刷新回 dashboard，`watch(page)` 兼任路由离开钩子 | `App.vue` L19/L172/L225 | `createRouter(createWebHistory())` + `beforeEach` 会话守卫；筛选条件放 query string |
| F3 | API 层无类型：`api<T=any>`、`session:any`、全文 30 处 `any`；只有账单域定义了 4 个接口，Account/Proxy/Connection/Job/Dashboard 全无类型；emits 一处无类型其他有类型 | `src/api.ts`、`App.vue` L166-170、`AccountDeliveryDialog.vue` L33-34 | 新建 `src/types/api.ts`；`api.ts` 按域拆分；长期用 tygo 从 Go struct 生成 |
| F4 | Element Plus 全量引入（dist 1.09MB JS + 430KB CSS），未传 `locale` 导致日期选择器/分页文案英文 | `src/main.ts` | `unplugin-vue-components` 按需引入；`use(ElementPlus, {locale: zhCn})`；`manualChunks` 分块 |
| F5 | `package.json` 8 个依赖全 `latest`、无 `name/version/private/engines`、构建工具放 `dependencies`、无 lint；lock 已锁在 TS 6.0.3 / Vite 8.3.5 / vue-router 5.4.0 等前沿版本 | `web/package.json` | 改 `^` 范围写入当前版本；工具移 `devDependencies`；加 `private: true`、`engines.node >=24`、`typecheck/lint` 脚本 |
| F6 | 无 401 处理：会话 12h 过期后界面仍显示已登录；`logout()` 把 `page='dashboard'` 触发 `loadDash()` 未登录也发请求；登录页与工作台共用 `error` ref | `src/api.ts`、`App.vue` L202/L225 | `api.ts` 捕获 401 → 清会话跳登录；`loadDash` 加 auth 守卫 |
| F7 | 校验逻辑 4 处重复（必填 8 项 + 凭证 12 项在 `entryMissingFields`、`buildCredential`、`save` 各写一遍）；邮箱正则 2 份；`defaultConnectionId` 逻辑 4 处；`<el-form>` 未传 `:model/:rules`，错误只汇总到一条 alert | `App.vue` L180/L208/L209/L115/L206/L212 | 抽 `src/model/account-form.ts` 纯函数并用现有 node --test 覆盖；模板改字段级校验 |
| F8 | 格式化重复且不一致：日期格式化 4 份、金额格式化 3 份用 `Number` 而账单页用 BigInt 安全的 `formatBillMoney` | `App.vue` L41/L80/L190/L192、`ProxyNetworkPanel.vue` L68、`PlatformBills.vue` L68 | 统一 `src/utils/format.ts` |
| F9 | CSS：5 个全局文件 65KB，单行最长 7089 字符；5 组选择器跨 `account-delivery.css` / `workspace.css` 重复；`:root` 定义 10 个变量但 565 处硬编码色值；204 处覆盖 `.el-*` 内部类、16 处 `!important` | `src/*.css` | 保留 `tokens/base/element-overrides` 三个全局文件，其余随组件迁入 `<style scoped>`（`PlatformBills.vue` 已有先例）；stylelint `no-duplicate-selectors` |
| F10 | 测试只有 3 个文件 7 个 test，覆盖 3 个纯函数；`list-model.ts` 的 `filterRows`（最复杂的纯逻辑）、`api.ts`、所有 `.vue` 无测试；`tsconfig.include` 不含 `tests/` | `web/tests/` | 迁移到 Vitest + `@vue/test-utils` + `happy-dom`；优先补 `filterRows`、表单校验、`api.ts`（mock fetch 验 CSRF 头）、`AccountDeliveryDialog` preflight |
| F11 | 列表全量拉取 + 客户端分页 + 3 秒 `setInterval` 轮询：jobs 页无条件重拉全量；accounts 页有一个 submitting 就每 3 秒拉 3 个接口；页面隐藏不停；后端 `/jobs`、`/audit-events`、`/accounts` 无分页参数 | `App.vue` L205/L225、`list-model.ts` | `visibilityState` 判断 + `setTimeout` 链式；accounts 轮询只刷 `/accounts`；与后端协商 `page/page_size/q` |
| F12 | `tsconfig.json` 单行且薄弱：无 `noEmit/lib/types/noUnusedLocals/paths`（`api.ts` 未使用的 `Ref` 导入正是没被发现的例子）；`env.d.ts` 缺 `vite/client` 引用 | `web/tsconfig.json`、`env.d.ts` | 按 Vite 模板拆三个 tsconfig；加 `@/` alias |
| F13 | 无 ESLint / Prettier / .editorconfig，`tsconfig.json`、`vite.config.ts`、`main.ts`、`index.html` 全部单行 | `web/` | `eslint-plugin-vue` + `@vue/eslint-config-typescript` + Prettier（printWidth 120）；**先一次性格式化再做任何重构，否则 diff 无法审阅** |

### 工程化 / 部署

| # | 问题 | 位置 | 建议 |
|---|---|---|---|
| D1 | 无 CI、无 Makefile/Taskfile，验证命令只存在于 `.local-run/*.log` 和部署记录文字里；`implementation-plan.md` 3.1 的"建立 CI"仍为 `[ ]` | 根目录 | `Makefile`：`web-build / build / test(go vet + go test -race + npm test) / release(-trimpath -ldflags "-s -w -X main.version=...")`；GitHub Actions 跑同一套 |
| D2 | `backup.sh` 把 `cc-manager.env`（含主密钥）和数据库放进同一备份目录，与 `architecture.md` 4.2/9.3"密钥不能与数据库同备份"自相矛盾；无保留策略 | `deploy/production/backup.sh:24` | 密钥独立备份或 age/gpg 加密；`find -mtime +30 -delete`。（sqlite 部分用了 Python `backup()` API + 两次 `integrity_check`，做得对） |
| D3 | `.env.example`（Docker 口径 `:8080`、`/data`）与 `install.sh`（硬编码 `:18080`、`:8088`、`cc.happyaether.com`）、`cc-manager.service`（`StateDirectory`）三者不一致；`CC_MANAGER_SUPPLIER_TLS_PINS / SUPPLIER_INSECURE_TLS / BILL_MAX_ITEMS` 在代码中存在但任何文档未列出 | `deploy/` | 提供 `deploy/production/cc-manager.env.example`；域名端口改参数；补全环境变量清单 |
| D4 | Docker 无 `HEALTHCHECK`；compose 无 `healthcheck / restart`，密钥通过 `environment:` 插值 `docker inspect` 可见明文 | `deploy/Dockerfile`、`docker-compose.yml` | `HEALTHCHECK CMD wget -qO- http://127.0.0.1:8080/healthz`；`env_file:` 或 `secrets:`；`restart: unless-stopped`、`read_only: true`、`cap_drop: [ALL]` |
| D5 | systemd 已有 `NoNewPrivileges/ProtectSystem=strict/ProtectHome/PrivateTmp/UMask=0077`，缺 `PrivateDevices / RestrictAddressFamilies / SystemCallFilter=@system-service / MemoryDenyWriteExecute / CapabilityBoundingSet= / RestrictNamespaces / LimitNOFILE`；`Restart=on-failure` 建议 `always` | `deploy/production/cc-manager.service` | 补齐 |
| D6 | nginx 缺 `HSTS / CSP / Permissions-Policy`；`Cache-Control: no-store always` 对带 hash 的 `/assets/*` 也生效；`limit_req_zone $http_cf_connecting_ip` 在绕过 Cloudflare 直连时为空失效 | `deploy/production/cc-manager.nginx.conf` | 分 location 设置缓存头；限速 key 回退 `$binary_remote_addr` |
| D7 | `install.sh` 不可重入：已有 env 直接 `exit 1`，`ln -s` 在 `current` 已存在时报错，只能首次安装不能升级 | `deploy/production/install.sh:17,21` | `ln -sfn`；env 已存在则跳过 |
| D8 | `deploy/artifacts/` 放 5MB 二进制 tar.gz | `deploy/artifacts/` | 移到 Release / 对象存储，仓库只留 `SHA256SUMS` |
| D9 | `.tools/go` 只有 Windows 二进制，CI/Linux 无法复用 | `.tools/` | 用 `go.mod` 的 `toolchain` 指令 |

---

## P2 · 文档与细节

- `docs/implementation-plan.md` 第 3 行仍写"本次交付是方案文档，不包含已运行的前后端"，3.1～3.7 全部复选框为 `[ ]`，而根 README 已列大段"已实现范围"——两种时态并存。`docs/architecture.md` 4.1 列出的 `internal/{config,domain,service,repository,...}` 目录和 4.3 的 `security.keyring_file`、`worker.max_concurrency` 配置项与实际实现毫无对应。建议勾选已完成项、在 architecture 标注"实际实现与此不同"。
- README 第 9 行"请勿公网部署"与部署记录已上线 `https://cc.happyaether.com/` 矛盾；"本地运行"段写死 `.local-run/dev.db`、"不要重新生成配置里的加密密钥"是个人机器状态而非项目说明。
- `docs/README.md` 导航漏掉 `platform-usage-billing-design.md`。
- 缺 CHANGELOG、CONTRIBUTING、独立运维手册（安装/升级/备份恢复/密钥轮换/日志位置）；恢复步骤散落在部署记录和 `bill-query-implementation-report.md`，后者依赖 `.local-run/bill-acceptance/recover.py` 和用户绝对路径。
- 前端小项：`crypto.randomUUID()` 在非安全上下文（局域网 HTTP）为 undefined 会 TypeError（5 处）；`style.css` 第 1 行 `@import` Google Fonts 在大陆网络阻塞首屏且泄露访客 IP；`account-workflow.ts:13` 用 `in` 判断可命中原型链（`Object.hasOwn`）；`cellText` 空值统一显示"未同步"对审计日志不准确；connections 表单保存成功后 `edit.value`（含 token）未清空；`fetch` 无 AbortSignal；`Math.max(...arr)` 大数组展开。
- 后端测试空白：`internal/crypto` 零测试；`security.go` 的 Origin 拒绝/CSRF 失败/会话过期只有间接覆盖；`syncPlatformConnection` 成功路径、dashboard、PATCH/archive/restore、credentials PUT、platform-connections 的 in-use 守卫、`preflight` 各错误码、`importCommit` 中途失败、`scheduleBills`、`supplier.QueryMetrics` 解码、`decimalFen` 边界均无测试。全局 `sessions` 变量 + `t.Setenv` 使测试无法 `t.Parallel()`。

---

## 建议的落地顺序

1. **半天，零行为改动**：`git init` → `.gitignore`/`.dockerignore` → 删除 `.js` 产物 + `noEmit` → Prettier 一次性展开前端 → `package.json` 规范化 → 迁走 `.local-run/config.json`。做完这一步所有后续 diff 才可审。
2. **一天**：P0-3 超时与优雅关闭、P0-4 tzdata、P0-5 限流与 body 限制、P0-6/7 Scan 与错误日志。这四项是接入真实凭证前的硬门槛。
3. **一到两天**：Makefile + CI；B1 PRAGMA DSN、B3 事务、B7 索引。
4. **逐步**：前端 F3 类型层 → F2 vue-router → F1 拆组件（CSS 随组件 scoped）→ F4 按需引入 → F10 Vitest；后端 B9 路由重构 → B4 密码哈希与密钥轮换 → B14 可观测性。
5. 文档收口（P2）。

---

## 附：建议的 .gitignore

```gitignore
# 本地运行与验收产物
.local-run/
.tools/
.omx/
web/.omx/

# SQLite
*.db
*.db-shm
*.db-wal
*.sqlite

# 构建产物
/cc-manager
/cc-manager.next
*.exe
web/dist/
web/node_modules/
web/tsconfig.tsbuildinfo
web/src/**/*.js
deploy/artifacts/*.tar.gz

# 密钥与环境
*.pem
*.key
*.env
!deploy/.env.example
deploy/production/部署验收记录-*.md

.DS_Store
```

`.dockerignore` 可复用上述内容并追加 `docs/`、`deploy/`、`README.md`。
