# 供应商同步健康 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 供应商请求只走显式网络，指标同步持续失败时退避，仪表盘显示同步健康与原因。

**Architecture:** `internal/supplier` 读取 `CC_MANAGER_SUPPLIER_PROXY` 并返回可分类的指标错误；`internal/app/worker.go` 在每个连接同步前根据 `sync_runs` 计算退避并记录 `error_code`（迁移 v9）；`dashboard` 汇总健康字段；`DashboardView` 渲染。

**Tech Stack:** Go 1.22、modernc sqlite、Vue 3 + TS、Vitest。

**Spec:** `docs/superpowers/specs/2026-11-10-supplier-sync-health-design.md`

## Global Constraints

- 新错误码先登记 `internal/errs/registry.go`，再 `make generate`。
- 不提交；每个任务以测试通过为完成。命令同前一计划（Go 在仓库根，前端在 `web/`）。

## Review Focus

1. `CC_MANAGER_SUPPLIER_PROXY` 非法时，上号、账单、指标都要明确报错而不是悄悄直连（Task 1 测试）。
2. 退避只算该连接 `updated_at` 之后的记录，修改连接后立即恢复（Task 2 测试）。
3. 退避跳过时不得写 `sync_runs`、不得打 WARN（Task 2 测试）。
4. 旧记录没有 `error_code` 也要显示中文原因（Task 3 测试）。
5. 账单同步记录不能再污染“最近同步”（Task 3 测试）。

---

### Task 1: 供应商网络显式化 + 指标错误分类

**Files:** Modify `internal/supplier/client.go`；Modify `internal/errs/registry.go`（新码 `PLATFORM_QUERY_DENIED`，status 0，category upstream，action contact_admin）；Test `internal/supplier/client_test.go`。

**Interfaces (Produces):**
```go
type MetricsHTTPError struct{ Status int; Summary string } // implements errs.Upstream
func (e *MetricsHTTPError) Error() string // "supplier metrics HTTP %d: %s"（保持旧文本，兼容日志检索）
```
`New` 中：`transport.Proxy = nil`；若 `CC_MANAGER_SUPPLIER_PROXY` 非空则 `url.Parse` 校验 scheme ∈ http/https/socks5/socks5h 且有 host，设 `http.ProxyURL(u)`，否则返回 `errors.New("supplier proxy is invalid")`。

- [ ] 写失败测试：`TestClientIgnoresEnvironmentProxy`（`t.Setenv("HTTPS_PROXY","http://127.0.0.1:1")`，构造 client，取 `c.http.Transport.(*http.Transport).Proxy`，对任意 https 请求返回 nil URL）；`TestClientUsesExplicitSupplierProxy`（设 `CC_MANAGER_SUPPLIER_PROXY=http://10.0.0.9:3128` → Proxy 返回该 URL；设 `ftp://x` → `New` 报错）；`TestQueryMetricsClassifiesHTTPErrors`（模拟服务返回 404 `{"detail":"账号不存在或无权访问"}` → `errors.As(*MetricsHTTPError)`，`UpstreamCode()=="PLATFORM_QUERY_DENIED"`，`Error()` 含 `HTTP 404`；403→`PLATFORM_AUTH_FAILED`；503→`PLATFORM_UNAVAILABLE`）。
- [ ] 运行确认失败：`go test ./internal/supplier/`。
- [ ] 实现；`make generate`。
- [ ] `go test ./internal/supplier/ ./internal/errs/` 通过。

### Task 2: 指标同步退避 + error_code 入库

**Files:** Modify `internal/app/migrations.go`（v9 `ALTER TABLE sync_runs ADD COLUMN error_code TEXT NOT NULL DEFAULT ''`）、`internal/app/worker.go`；Test `internal/app/metrics_backoff_test.go`（新建）。

**Interfaces (Produces):**
```go
func metricsBackoffDelay(consecutive int) time.Duration // 0→0, n≥1→min(2·2^(n-1),60) 分钟
func (a *App) metricsBackoff(ctx context.Context, connectionID string, now time.Time) (consecutive int, next time.Time, skip bool, err error)
func metricsErrorCode(err error) string // Upstream→UpstreamCode()；其他非 nil→"PLATFORM_UNAVAILABLE"；nil→""
```
`syncAllPlatformUsage` 循环里先调 `metricsBackoff`，`skip` 则 `continue`；`syncPlatformConnection` 的 `finish` 增加 `error_code` 写入（`metricsErrorCode(queryError)`；连接不可用等本地错误分别写 `CONNECTION_DISABLED`/`ENCRYPTION_KEY_REQUIRED`/`CONNECTION_UNREADABLE`/`CONNECTION_INVALID`）。

- [ ] 写失败测试：`TestMetricsBackoffDelay`（0→0，1→2m，2→4m，3→8m，6→60m，20→60m）；`TestMetricsBackoffCountsSinceLastSuccessAndConnectionEdit`（插入 succeeded→3 次 partial，期望 consecutive=3、next=最后一次+8m；把连接 `updated_at` 设为最后一次之后 → consecutive=0、skip=false）；`TestSyncSkipsConnectionInBackoffWithoutWritingRuns`（deliveryFixture，插一条已上号 link 和 5 条刚发生的 partial → 调 `syncAllPlatformUsage` 后 `sync_runs` 行数不变）；`TestMetricsRunStoresErrorCode`（deliveryFixture 的模拟供应商对指标路径返回 404 → 新运行 `error_code=='PLATFORM_QUERY_DENIED'`）；`TestMigrationV9AddsErrorCode`（列存在）。
- [ ] 运行确认失败。
- [ ] 实现。
- [ ] `go test -race ./internal/app/` 通过。

### Task 3: 仪表盘同步健康

**Files:** Modify `internal/app/app.go`（`dashboard`）、`web/src/types/api.ts`、`web/src/views/DashboardView.vue`；Create `web/src/model/sync-health.ts`；Test `internal/app/app_test.go` 或新 `dashboard_sync_test.go`、`web/tests/sync-health.test.ts`。

**Interfaces (Produces):**
```go
// dashboard platform 新字段
"sync_status": "ok"|"failing"|"never", "error_code", "error_message", "error_hint", "consecutive_failures": int, "next_attempt_at": string
```
```ts
export interface SyncHealthView { tone: 'success' | 'danger' | 'info'; label: string; detail?: string; hint?: string }
export function describeSyncHealth(p: DashboardPlatform, now?: Date): SyncHealthView
```
`detail` = `${error_message} · 连续失败 ${n} 次 · 下次重试 HH:MM`（next 为空时省略最后一段）。

- [ ] 写失败测试：Go `TestDashboardReportsFailingMetricsSync`（插 2 条 partial(error_code PLATFORM_QUERY_DENIED) 和 1 条更新的 bill 成功运行 → `sync_status=failing`、`error_message` 为注册表文案、`consecutive_failures=2`、`last_sync_state=partial`，bill 运行不影响）；`TestDashboardFallsBackForLegacyRuns`（error_code 空、message 含 `HTTP 404` → `PLATFORM_QUERY_DENIED`）。TS：`describeSyncHealth` 三态与 detail 格式。
- [ ] 运行确认失败。
- [ ] 实现（`describeSyncHealth` 用 `ErrorMessages` 兜底）。
- [ ] Go 与 vitest、lint、build 通过。

### Task 4: 回归、审查与现场验证

- [ ] `go test -race ./...`、`make check-generated`、`npm test`、`npm run lint`、`npm run build`、`npx playwright test`。
- [ ] 独立审查（fresh reviewer），修复 Critical/Important。
- [ ] 重建二进制，重启 8080（不带代理变量），验证迁移 v9、仪表盘显示“同步异常”与原因，下一轮同步按退避跳过。
