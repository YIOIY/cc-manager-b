# 供应商网页只读通道 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让系统用供应商网页后台的只读接口自动拿到平台账号状态、原因、每账号用量和按模型账单，并把平台上多出来的账号自动建成本地账号。

**Architecture:** 现有 `platform_connections` 记录增加网页登录凭证，`internal/supplier` 新增 `WebClient`（登录 + 三个读接口），`internal/app` 新增两个同步种类 `web-accounts`（10 分钟）和 `web-bills`（2 小时），写入新表 `platform_remote_accounts`、`platform_usage_snapshots`、`platform_bill_models`，并回写 `platform_links`。账号列表/详情、仪表盘、账单页面读这些表；现有 OpenAPI 同步不动。

**Tech Stack:** Go 1.26 标准库（`crypto/rsa` OAEP、`crypto/aes` GCM、`net/http`、`database/sql` + SQLite），Vue 3 + TypeScript + Element Plus，Vitest，`go test -race`。

**Spec:** `docs/superpowers/specs/2026-11-11-supplier-web-channel-design.md`

## Global Constraints

- 只调用读取接口：`POST /supplier/pubkey`、`POST /supplier/login`、`GET /supplier/accounts`、`GET /supplier/accounts/{id}/check-fail-reason`、`GET /supplier/accounts/{id}/claude-status-reason`、`GET /bill/query`。基础路径 `/supplier-account/api`，鉴权 `Authorization: Bearer <token>`。任何写接口（PUT/POST 到 accounts、proxy、admin）一律不实现。
- 密码、`enc_password`、会话令牌不得出现在日志、审计、错误文本、API 响应、`raw_json` 中。`raw_json` 还要去掉 `api_key`、`api_secret`、`account_md5`、`auth_config.password`。
- 新错误码必须注册到 `internal/errs/registry.go` 并运行 `make generate`（AST 守卫测试与 `make check-generated` 必须通过）：`WEB_LOGIN_FAILED`、`WEB_FORBIDDEN`、`WEB_ENDPOINT_MISSING`、`WEB_RATE_LIMITED`、`WEB_BILL_RANGE_MISMATCH`、`WEB_NOT_CONFIGURED`、`WEB_SUSPENDED`、`SYNC_IN_BACKOFF`。
- 周期常量：`webAccountsInterval = 10 * time.Minute`，`webBillsInterval = 2 * time.Hour`，`webLoginFailureLimit = 3`，`webReasonRefresh = 6 * time.Hour`，`webUsageRetention = 90 * 24 * time.Hour`，账单窗口 7 天（今天往前 6 天），`account_ids` 每批 ≤ 50，列表 `page_size=100`。
- 退避沿用 2 分钟翻倍到 60 分钟的曲线，按 kind 独立计数；登录连续失败 3 次暂停通道。
- 单位换算：`cost`（百万分之一元）→ 分 = `round(cost/10000)`；`*_used_percent`（0–1）→ 百分比保留两位；`*_reset_at`（Unix 秒）→ RFC3339 UTC 存储，前端按本地时区显示。
- 供应商客户端继续复用 `supplier.New` 的传输规则：不读 `HTTP(S)_PROXY`、只用 `CC_MANAGER_SUPPLIER_PROXY`、`CC_MANAGER_SUPPLIER_TLS_PINS` 固定证书、30 秒超时。
- 文案全部中文；前端状态映射复用 `web/src/account-workflow.ts` 的 `runtimeLabel`，未知值显示"未知"并保留原值。

## Review Focus

1. 平台返回的列表 `total=0`/空 `list`（后台临时故障或筛选异常）：不能把本地所有关联标成"已消失"，也不能把这一轮当成功。→ Task 6 测试 `TestWebAccountsSyncEmptyListIsFailure`。
2. 翻到第二页时令牌刚好过期（401）：重登一次后继续，整轮成功；第二次仍 401 才失败。→ Task 3 测试 `TestWebClientReloginOnce`。
3. `extra` 缺少 `cpa_usage` 或 `auth_config` 为空串/非 JSON：账号仍被同步，用量为 NULL，卡片显示"平台未返回用量"。→ Task 3 测试 `TestParseWebAccountTolerates`，Task 6 测试 `TestWebAccountsSyncWithoutUsage`。
4. 平台邮箱大小写或首尾空格与本地不同：仍能匹配，不重复建账号。→ Task 6 测试 `TestWebAccountsSyncMatchesEmailCaseInsensitive`。
5. `/bill/query` 返回了请求日期范围之外的行，或 `range` 与请求不一致：范围外的行忽略，`range` 不一致整批丢弃并记 `WEB_BILL_RANGE_MISMATCH`。→ Task 3 测试 `TestQueryModelBillsRange`，Task 7 测试 `TestWebBillsSyncIgnoresRowsOutsideWindow`。

---

### Task 1: 迁移 v10、新表与错误码

**Files:**
- Modify: `internal/app/migrations.go`（`migrations` 列表加 `{10, "web-channel", migrateWebChannelV10}`）
- Modify: `internal/app/app.go:60-80`（fresh-database schema 常量里加同样的表和列）
- Modify: `internal/errs/registry.go`（8 个新码）
- Generate: `web/src/generated/error-codes.ts`、`docs/errors.md`
- Test: `internal/app/migrations_test.go`（沿用现有"迁移后 schema 与 fresh schema 一致"的测试）

**Interfaces:**
- Produces 表：`platform_remote_accounts`、`platform_usage_snapshots`、`platform_bill_models`；`platform_connections` 新列 `web_username TEXT NOT NULL DEFAULT ''`、`web_password_secret TEXT NOT NULL DEFAULT ''`、`web_suspended_at TEXT NOT NULL DEFAULT ''`、`web_suspended_reason TEXT NOT NULL DEFAULT ''`。
- 列定义照设计文档 §3、§4；文本列一律 `TEXT NOT NULL DEFAULT ''`（`missing_since`、`superseded_by`、`account_id`、`reason_*` 用空串表示"无"，不用 NULL），用量数值列（`usage_cost_micro`、`usage_input_tokens`、`usage_output_tokens`、`five_hour_used`、`week_used`）可空，NULL 表示平台未返回；`platform_remote_accounts` 主键 `(connection_id, upstream_id)`，另加 `superseded_by TEXT NOT NULL DEFAULT ''`、`auto_created INTEGER NOT NULL DEFAULT 0`；`platform_usage_snapshots` 索引 `(account_id, observed_at)`、`(observed_at)`；`platform_bill_models` 唯一 `(connection_id, upstream_id, bill_date, model_name)`，索引 `(connection_id, bill_date)`、`(account_id, bill_date)`。
- 错误码定义（Category/Action 用现有常量）：
  - `WEB_LOGIN_FAILED` Upstream, Action ContactAdmin, Message "网页后台登录失败", Hint "请检查平台连接里的网页登录账号和密码"
  - `WEB_FORBIDDEN` Upstream, Action ContactAdmin, Message "网页后台拒绝读取", Hint "请确认该后台账号有查看权限"
  - `WEB_ENDPOINT_MISSING` Upstream, Action ContactAdmin, Message "网页后台接口不存在", Hint "平台可能已改版，请联系维护者"
  - `WEB_RATE_LIMITED` Upstream, Retryable, Action Wait, Message "网页后台限流", Hint "稍后自动重试"
  - `WEB_BILL_RANGE_MISMATCH` Upstream, Retryable, Action Retry, Message "平台返回的账单范围与请求不符", Hint "稍后自动重试"
  - `WEB_NOT_CONFIGURED` Validation, Status 422, Action FixInput, Message "该平台连接未配置网页登录", Hint "请在平台连接里填写网页登录账号和密码"
  - `WEB_SUSPENDED` Upstream, Action FixInput, Message "网页通道已暂停：登录连续失败", Hint "请检查后台密码后重新保存，或点"测试登录""
  - `SYNC_IN_BACKOFF` Conflict, Status 409, Action Wait, Message "同步正在退避等待中", Hint "请稍后再试"

- [ ] **Step 1: 在 `migrations_test.go` 的 schema 一致性测试里确认它会覆盖新表**（读测试，确认它比较所有表的 `sqlite_master`；若只比较固定表名列表，把三张新表加进去）
- [ ] **Step 2: 运行 `go test ./internal/app -run 'Migration|Schema' -v`**，期望：PASS（基线）
- [ ] **Step 3: 写迁移 `migrateWebChannelV10(tx *sql.Tx) error`**：对 `platform_connections` 用 `addColumns`，三张表用 `CREATE TABLE IF NOT EXISTS` + 索引；同步把 fresh schema 常量改成相同定义
- [ ] **Step 4: 在 `registry.go` 注册 8 个错误码，并加 `Code*` 常量**（`CodeWebLoginFailed`、`CodeWebForbidden`、`CodeWebEndpointMissing`、`CodeWebRateLimited`、`CodeWebBillRangeMismatch`、`CodeWebNotConfigured`、`CodeWebSuspended`、`CodeSyncInBackoff`）
- [ ] **Step 5: 运行 `make generate && make check-generated && go test ./internal/errs/... ./internal/app -run 'Migration|Schema|Registry|Generated'`**，期望：全部 PASS
- [ ] **Step 6: Commit** `git commit -m "网页通道：迁移 v10 新表、连接凭证列与错误码"`

### Task 2: 登录密码加密

**Files:**
- Create: `internal/supplier/webcrypto.go`
- Test: `internal/supplier/webcrypto_test.go`

**Interfaces:**
- Produces: `func EncryptLoginPassword(publicKeyPEM, password string) (string, error)`。返回 base64(UTF-8 JSON `{"__enc__":"hybrid-aesgcm","k":...,"iv":...,"ct":...}`)：`k` = base64(RSA-OAEP-SHA256(32 字节随机 AES 密钥))，`iv` = base64(12 字节随机)，`ct` = base64(AES-256-GCM 密文 ‖ 16 字节 tag)。PEM 解析用 `x509.ParsePKIXPublicKey`；PEM 非 RSA 公钥时返回错误。

- [ ] **Step 1: 写测试 `TestEncryptLoginPasswordRoundTrip`**：生成 2048 位 RSA 密钥，`EncryptLoginPassword(pem, "p@ss 密码")`，base64 解码 → JSON → 断言 `__enc__=="hybrid-aesgcm"`，用私钥 OAEP-SHA256 解出 32 字节 AES 密钥，GCM 解密 `ct` 得到原密码；再断言两次加密结果不同（随机 IV）。写 `TestEncryptLoginPasswordRejectsNonRSA`：传 ECDSA 公钥 PEM 期望 error。
- [ ] **Step 2: 运行 `go test ./internal/supplier -run EncryptLoginPassword -v`**，期望：FAIL（函数不存在）
- [ ] **Step 3: 实现 `EncryptLoginPassword`**
- [ ] **Step 4: 运行同一测试**，期望：PASS
- [ ] **Step 5: Commit** `git commit -m "网页通道：登录密码混合加密"`

### Task 3: 网页客户端

**Files:**
- Modify: `internal/supplier/client.go`（把 `New` 里构造 `*http.Transport` 的部分抽成 `func newSupplierTransport(host string) (*http.Transport, error)`，`New` 改为调用它；行为不变）
- Create: `internal/supplier/web.go`
- Test: `internal/supplier/web_test.go`

**Interfaces:**
- Produces（全部在 package `supplier`）：
  - `type WebClient struct{ ... }`；`func NewWebClient(baseURL, username, password string) (*WebClient, error)`（校验同 `New`：HTTPS、非空用户名密码）
  - `type WebSession struct{ SupplierName string; ExpiresIn int }`；`func (c *WebClient) Login(ctx context.Context) (WebSession, error)`
  - `type WebProfile struct{ Plan, PlanStart, PlanEnd, RegisterTime, RegisterLocation, Balance, PaymentMethod, RegisterDevice, RechargeTime, WarrantyPeriod string; Tags []string }`
  - `type WebUsage struct{ CostMicro, InputTokens, OutputTokens int64; FiveHourUsed, WeekUsed float64; FiveHourResetAt, WeekResetAt time.Time }`
  - `type WebAccount struct{ ID, AID, A2ID int64; Email, Alias, Provider, Model, ExtStatus, A1Status, A2Status, ProxyType, Remark, UsageLimitJSON, CreatedAt, UpdatedAt string; ModelsUsable, Locked bool; Profile WebProfile; Usage *WebUsage; RawJSON string }`
  - `type WebCounts map[string]int`；`func (c *WebClient) ListAccounts(ctx context.Context) ([]WebAccount, WebCounts, int, error)`（第三个返回值为平台 `total`；按 `page_size=100` 翻页直到取满 `total`）
  - `type WebReason struct{ Text, Raw string }`；`func (c *WebClient) AccountReason(ctx context.Context, id int64, kind string) (WebReason, error)`，`kind` ∈ `"check-fail"`、`"claude-status"`；`Text` 取第一个字符串值，优先键名含 `reason`/`message`/`detail`/`msg`
  - `type WebBillRow struct{ AccountID int64; Date, Model, BillModel string; CostFen int64 }`（`Date` 为 `YYYY-MM-DD`）；`func (c *WebClient) QueryModelBills(ctx context.Context, ids []int64, start, end string) ([]WebBillRow, error)`，`start`/`end` 为 `YYYY-MM-DD`，发送时转 `YYYYMMDD`；`ids` 超过 50 返回错误；响应 `range` 与请求不等返回 `*WebHTTPError{Status: 200, Summary: "range mismatch"}` 且 `UpstreamCode()=="WEB_BILL_RANGE_MISMATCH"`；`ds` 不在范围内的行丢弃
  - `type WebHTTPError struct{ Status int; Summary string; RangeMismatch bool }` 实现 `errs.Upstream`：401 → `WEB_LOGIN_FAILED`，403 → `WEB_FORBIDDEN`，404 → `WEB_ENDPOINT_MISSING`，429 → `WEB_RATE_LIMITED`，5xx → `PLATFORM_UNAVAILABLE`，RangeMismatch → `WEB_BILL_RANGE_MISMATCH`，其他 → `PLATFORM_RESPONSE_INVALID`
  - `func parseWebAccount(raw json.RawMessage) (WebAccount, error)`（包内，供测试）
- 内部行为：令牌与 `expiresAt = now + expires_in - 60s` 缓存在客户端，`sync.Mutex` 串行化登录；每个读请求先 `ensureToken`；收到 401 → 清令牌、重登一次、重试一次。

- [ ] **Step 1: 写 `TestParseWebAccountTolerates`**：用 HAR 里 428802 那条（邮箱改为 `fixture@example.test`）作为输入，断言 `ID==428802`、`AID==20204`、`Email`、`Alias`、`ExtStatus=="using"`、`Profile.Plan=="claude/cc-max-20x"`、`Profile.Balance=="100"`、`Usage.CostMicro==35209959`、`Usage.InputTokens==411638`、`Usage.FiveHourUsed==0.05`、`Usage.FiveHourResetAt.Unix()==1794415800`；再断言 `RawJSON` 不含 `"password"`、`api_key`、`account_md5` 的值。三个变体：`extra=""`、`extra="not json"`、`extra='{"cpa_usage":{}}'` → `Usage==nil` 且无 error；`auth_config=""` → `Profile` 零值且无 error；`warranty_period` 为数字 1 → `"1"`。
- [ ] **Step 2: 写 `TestWebClientLoginAndList`**：`httptest.NewTLSServer`（`t.Setenv("CC_MANAGER_SUPPLIER_INSECURE_TLS","1")`，参考 `network_test.go:62`）实现 pubkey（返回测试 RSA 公钥）、login（用私钥解 `enc_password`，等于 `"secret"` 才返回 `{"token":"tok1","name":"益充汇","expires_in":18000}`，否则 401）、accounts（校验 `Authorization: Bearer tok1`，`page=1` 返回 100 条、`page=2` 返回 8 条，`total=108`）。断言 `ListAccounts` 返回 108 条、`counts["using"]`、`total==108`，且 login 只被调用 1 次（两页共用令牌）。
- [ ] **Step 3: 写 `TestWebClientReloginOnce`**：服务端第一次 accounts 返回 200，第二次（page=2）返回 401，重登后用新令牌 `tok2` 返回 200 → `ListAccounts` 成功且 login 调用 2 次；另一个用例所有请求都 401 → 返回错误，`errors.As` 到 `*WebHTTPError` 且 `UpstreamCode()=="WEB_LOGIN_FAILED"`，login 调用恰好 2 次。
- [ ] **Step 4: 写 `TestQueryModelBillsRange`**：请求 `ids=[428802,428242]`、`2026-11-05`..`2026-11-11`，服务端断言 query `account_ids=428802,428242&start_ds=20261105&end_ds=20261111`，返回 HAR 里的 4 行 + 一行 `ds=20261104` → 结果恰好 4 行，`CostFen` 为 2761/760/92/7804，`Date=="2026-11-11"`；`range` 返回 `{start_ds:20261101,end_ds:20261111}` → 错误码 `WEB_BILL_RANGE_MISMATCH`；51 个 id → 本地错误。再写 `TestAccountReason`：返回 `{"reason":"账号被封禁","code":7}` → `Text=="账号被封禁"`。
- [ ] **Step 5: 运行 `go test ./internal/supplier -run 'WebClient|ParseWebAccount|QueryModelBills|AccountReason' -v`**，期望：FAIL（未定义）
- [ ] **Step 6: 抽出 `newSupplierTransport`，实现 `web.go`**
- [ ] **Step 7: 运行 `go test -race ./internal/supplier`**，期望：全部 PASS（含原有 TLS/代理测试）
- [ ] **Step 8: Commit** `git commit -m "网页通道：WebClient 登录、账号列表、原因与按模型账单"`

### Task 4: 退避、健康与清理按 kind 参数化

**Files:**
- Modify: `internal/app/worker.go:408-456`（`metricsBackoff` → `syncBackoff`）
- Modify: `internal/app/app.go:1142-1215`（`metricsSyncHealth` → `syncHealth`）
- Modify: `internal/app/janitor.go:60-90`
- Test: `internal/app/metrics_backoff_test.go`、`internal/app/dashboard_sync_test.go`

**Interfaces:**
- Produces：`func (a *App) syncBackoff(ctx context.Context, connectionID, kind string, at time.Time) (consecutive int, next time.Time, skip bool, err error)`；`func (a *App) syncHealth(ctx context.Context, kind string) (map[string]any, error)`。保留 `metricsBackoff(ctx, id, at)` 与 `metricsSyncHealth(ctx)` 作为 `kind="platform-usage"` 的薄包装，现有调用与测试不改。
- 清理：`sync_runs` 的 `web-accounts`、`web-bills` 与 `platform-usage` 同一保留期；`platform_usage_snapshots` 删除 `observed_at < now-webUsageRetention`。
- 退避计数的"编辑重置"对网页 kind 改为看 `platform_connections.updated_at`（与现在一致；保存网页凭证会更新 `updated_at`）。

- [ ] **Step 1: 写 `TestSyncBackoffIsPerKind`**（`metrics_backoff_test.go`）：插入 3 条 `web-accounts` partial 运行，断言 `syncBackoff(..., "web-accounts", now)` 得 `n==3, skip`，而 `syncBackoff(..., "platform-usage", now)` 得 `n==0`。写 `TestSyncHealthPerKind`（`dashboard_sync_test.go`）：同样数据下 `syncHealth(ctx,"web-accounts")["sync_status"]=="failing"`，`metricsSyncHealth(ctx)["sync_status"]=="never"`。写 `TestCleanupPrunesUsageSnapshots`：插入一条 91 天前和一条 1 天前的 `platform_usage_snapshots`，`cleanup` 后只剩 1 条。
- [ ] **Step 2: 运行 `go test ./internal/app -run 'SyncBackoff|SyncHealthPerKind|CleanupPrunesUsage' -v`**，期望：FAIL
- [ ] **Step 3: 重构为 `syncBackoff`/`syncHealth`，补 janitor 语句**
- [ ] **Step 4: 运行 `go test -race ./internal/app -run 'Backoff|Health|Cleanup|Dashboard'`**，期望：PASS
- [ ] **Step 5: Commit** `git commit -m "同步退避与健康按 kind 参数化，清理用量快照"`

### Task 5: 平台连接的网页凭证、暂停状态与测试登录

**Files:**
- Modify: `internal/app/app.go:891-1075`（list/create/update/delete 连接）
- Create: `internal/app/web_channel.go`（客户端缓存、暂停、测试登录）
- Modify: `internal/app/router.go`（新增 `need("POST /api/v1/platform-connections/{id}/web-login-test", PermConnectionsUpdate, a.webLoginTest)`）
- Modify: `internal/app/lookups.go:34`（lookup 返回加 `web_configured`）
- Test: `internal/app/web_channel_test.go`

**Interfaces:**
- API：创建/更新接受 `web_username`、`web_password`（更新时 `web_password` 空 = 不变）；`web_clear: true` 清空两者与暂停字段。列表与 lookup 返回 `web_username`、`web_configured`（用户名与密文都非空）、`web_suspended_at`、`web_suspended_reason`；永不返回密码。保存了新凭证或清除时写审计 `connection.web_credentials_set` / `connection.web_credentials_cleared`，并清空 `web_suspended_*`、丢弃缓存客户端。
- `POST /api/v1/platform-connections/{id}/web-login-test` → 200 `{ "ok": true, "supplier_name": "...", "expires_in": 18000 }`；未配置 → 422 `WEB_NOT_CONFIGURED`；登录失败 → 502 `WEB_LOGIN_FAILED`。成功时清空暂停字段。
- Produces（package app）：
  - `type webConnection struct{ ID, BaseURL, Timezone, Username string; Enabled, Configured bool; SuspendedAt, SuspendedReason string }`
  - `func (a *App) loadWebConnection(ctx context.Context, id string) (webConnection, string /*password*/, error)`
  - `func (a *App) webClientFor(ctx context.Context, id string) (*supplier.WebClient, webConnection, error)`：`App` 增加 `webClients map[string]*supplier.WebClient` + `webMu sync.Mutex`；未配置返回 `errs.New(errs.CodeWebNotConfigured)`；暂停返回 `errs.New(errs.CodeWebSuspended)`
  - `func (a *App) dropWebClient(id string)`
  - `func (a *App) noteWebLogin(ctx context.Context, id string, err error) (suspended bool)`：`err` 的 `UpstreamCode()=="WEB_LOGIN_FAILED"` 时把 `sync_runs` 里该连接最近 N 条（任意 web kind）`error_code=='WEB_LOGIN_FAILED'` 的连续计数（从最近一次成功或成功登录测试后算起）+1；达到 3 写 `web_suspended_at/reason="登录连续失败 3 次"`，写审计 `connection.web_suspended`；`err==nil` 不做事（成功的 run 由调用方记）。
  - `const webLoginFailureLimit = 3`

- [ ] **Step 1: 写 `TestConnectionWebCredentialsRoundTrip`**：POST 连接带 `web_username/web_password` → 列表 `web_configured==true` 且响应体不含密码；PATCH 不带 `web_password` → 密文不变（直接读表比较）；PATCH `web_clear:true` → 两列为空、`web_configured==false`；审计表有 `connection.web_credentials_set` 与 `..._cleared`。
- [ ] **Step 2: 写 `TestWebLoginTestEndpoint`**：用 Task 3 的 httptest 登录桩作为 `base_url`；未配置 → 422 `WEB_NOT_CONFIGURED`；密码错 → 502 `WEB_LOGIN_FAILED`；正确 → 200 `ok:true, supplier_name`，且之前人为写入的 `web_suspended_at` 被清空。
- [ ] **Step 3: 写 `TestNoteWebLoginSuspendsAfterThree`**：插入 2 条 `web-accounts` failed 运行（`error_code='WEB_LOGIN_FAILED'`），`noteWebLogin` 第 3 次返回 `suspended==true`，表里 `web_suspended_at` 非空；`webClientFor` 返回错误码 `WEB_SUSPENDED`；保存新凭证后再次 `webClientFor` 不再报暂停。
- [ ] **Step 4: 运行 `go test ./internal/app -run 'ConnectionWeb|WebLoginTest|NoteWebLogin' -v`**，期望：FAIL
- [ ] **Step 5: 实现 `web_channel.go`、改连接 handlers、lookup、路由**（密码密文 AAD 用 `id+":web"`）
- [ ] **Step 6: 运行 `go test -race ./internal/app -run 'Connection|Web|Lookup|Permission'`**，期望：PASS（含权限目录测试，若它枚举路由则补新路由）
- [ ] **Step 7: Commit** `git commit -m "平台连接：网页登录凭证、暂停保护与测试登录"`

### Task 6: 账号同步（web-accounts）

**Files:**
- Create: `internal/app/web_accounts_sync.go`
- Test: `internal/app/web_accounts_sync_test.go`（fixture：复制 `deliveryFixture` 的建库方式，加一个 `webFixture(t, handler http.Handler) (*App, connID string)` 把 httptest TLS 服务器 URL 存为连接 `base_url` 并写入网页凭证）

**Interfaces:**
- Produces：
  - `func (a *App) syncAllWebAccounts(ctx context.Context) error`（遍历 `enabled=1 AND web_username<>'' AND web_password_secret<>'' AND web_suspended_at=''` 的连接，`syncBackoff(..., "web-accounts", now)` 跳过退避中的）
  - `func (a *App) syncWebAccounts(ctx context.Context, connectionID string) error`：写 `sync_runs(kind='web-accounts')`，步骤照设计 §3（分组选当前交付 → 关联/改关联/自动建 → 状态与用量回写 → 原因 → 完成）。`total==0` 或 `list` 为空：run 记 `failed`、`error_code='PLATFORM_RESPONSE_INVALID'`、不改任何关联、不标缺失。
  - `var webFailureStatuses = map[string]bool{"ban": true, "banned": true, "check_fail": true, "login_failed": true, "bound_failed": true, "cooking_failed": true, "re_login_failed": true, "sync_failed": true, "fault": true, "suspended": true, "arrearage": true}`
  - `func normalizeEmail(s string) string`（trim + lower）
  - `func chooseCurrentDelivery(rows []supplier.WebAccount) supplier.WebAccount`（非失败类优先，再按 `CreatedAt` 字符串最新）
  - `func (a *App) autoCreateAccount(tx *sql.Tx, conn string, r supplier.WebAccount, at string) (accountID string, err error)`：`plan` 不在 `allowedPlans` 存 `''`；日期非 `validDate` 存 `''`；`metadata_json` 写 `optionalMetadataKeys` 中有值的键（`tags` 为数组，`warranty_period` 原样字符串）+ `source/platform_connection_id/platform_upstream_id/imported_at`；审计 `account.platform_import`（detail = 交付 ID）
  - 改关联时审计 `account.platform_relink`（detail = `old->new`）
  - 用量写入：`platform_remote_accounts` 更新；与上一条 `platform_usage_snapshots` 任一字段不同（或没有）时插入新快照
  - `const webReasonRefresh = 6 * time.Hour`
- 事务边界：每条远端记录一个事务（关联/建号/回写一起成功或失败）；原因请求在事务外。

- [ ] **Step 1: 写 `TestWebAccountsSyncAutoCreatesAndLinks`**：远端 3 条（A 邮箱=本地已有账号 `legacy@example.test`、B 新邮箱、C 新邮箱无 `auth_config`），本地只有 A。同步后：A 有 `platform_links`（upstream_id=A 的 id、aid、alias、runtime_status）；B 自动创建（`accounts.plan=="claude/cc-max-20x"`、`metadata_json` 含 `"source":"platform_sync"` 与 `"balance":"100"`，无 `account_secrets`）；C 自动创建但 `plan==''`；`platform_remote_accounts` 3 行且 `auto_created` 分别 0/1/1；审计 2 条 `account.platform_import`；`sync_runs` 一条 `succeeded`，`processed==3`。再跑一次同步：账号数不变，快照表行数不变（用量未变）。
- [ ] **Step 2: 写 `TestWebAccountsSyncMatchesEmailCaseInsensitive`**：本地 `Mixed.Case@Example.test `，远端 `mixed.case@example.test` → 关联到同一账号，不新建。
- [ ] **Step 3: 写 `TestWebAccountsSyncRelinksToNewestDelivery`**：本地账号关联旧交付 1001（远端 `ext_status=ban`），远端新增交付 1002 同邮箱 `using`、`created_at` 更新 → `platform_links.upstream_id=="1002"`、`runtime_status=="using"`；远端 1001 行 `superseded_by=="1002"`；审计 `account.platform_relink` detail `1001->1002`。变体：两条都 `ban` → 取最新的。
- [ ] **Step 4: 写 `TestWebAccountsSyncMissingAndReasons`**：第一轮远端有 X、Y；第二轮只有 X，Y 的 `ext_status` 变 `ban`（在第一轮里就是 ban）→ 第一轮 Y 的 `reason_text` 来自桩的 `claude-status-reason`，`reason_fetched_at` 非空，X 没有请求原因（桩计数 0）；第二轮 Y 行 `missing_since` 非空，X 为空；第三轮 Y 回来 → `missing_since` 清空。桩的原因接口在 6 小时内不被重复调用（第二轮计数不增）。
- [ ] **Step 5: 写 `TestWebAccountsSyncEmptyListIsFailure`** 与 `TestWebAccountsSyncWithoutUsage`（`extra` 为空 → `platform_remote_accounts.usage_cost_micro IS NULL`，快照表 0 行，run 仍 `succeeded`）与 `TestWebAccountsSyncUsageSnapshotOnChange`（两轮用量不同 → 2 条快照；第三轮相同 → 仍 2 条；`usage_cost_micro==35209959` 且 API 层 `cost_fen==3521`）。
- [ ] **Step 6: 写 `TestWebAccountsSyncLoginFailureSuspends`**：桩登录一直 401 → 连续 3 次 `syncWebAccounts` 后连接 `web_suspended_at` 非空，第 4 次 `syncAllWebAccounts` 不再请求桩（计数不变）。
- [ ] **Step 7: 运行 `go test ./internal/app -run WebAccountsSync -v`**，期望：FAIL
- [ ] **Step 8: 实现 `web_accounts_sync.go`**
- [ ] **Step 9: 运行 `go test -race ./internal/app -run 'WebAccountsSync|Account|Delivery'`**，期望：PASS（现有账号/交付测试不受影响）
- [ ] **Step 10: Commit** `git commit -m "网页通道：账号状态、用量与自动建号同步"`

### Task 7: 账单同步（web-bills）

**Files:**
- Create: `internal/app/web_bills_sync.go`
- Test: `internal/app/web_bills_sync_test.go`

**Interfaces:**
- Produces：`func (a *App) syncAllWebBills(ctx context.Context) error`、`func (a *App) syncWebBills(ctx context.Context, connectionID string) error`、`func webBillWindow(now time.Time, loc *time.Location) (start, end string)`（`end` = 今天，`start` = 今天 −6 天，`YYYY-MM-DD`）。
- 行为照设计 §4：取 `platform_remote_accounts` 里 `missing_since=''`（存空串表示 NULL，与 `superseded_by` 一致）的全部交付 ID，每 50 个一批；成功批次：窗口内该批交付 ID 的现有行若本次未返回则删除，返回的行 upsert（`generation` 递增、`fetched_at`、`final = date<today`、`account_id` 取当前 `platform_links`）；`range` 不一致的批次丢弃计 `failed`。`sync_runs(kind='web-bills')`。
- 同一连接的两种网页同步互斥：`App` 增加 `webSyncMu map[string]*sync.Mutex`（按连接），`syncWebAccounts`/`syncWebBills` 进入时 `TryLock`，拿不到直接返回 nil（下一轮再来）。

- [ ] **Step 1: 写 `TestWebBillWindow`**：`Asia/Shanghai` 2026-11-11 01:00 本地 → `2026-11-05`..`2026-11-11`。
- [ ] **Step 2: 写 `TestWebBillsSyncUpsertsDeletesAndFinal`**：远端 2 个交付、桩返回 HAR 的 4 行（日期改为"今天"与"昨天"各 2 行）→ 4 行写入，今天的 `final=0`、昨天的 `final=1`，`cost_fen` 精确；第二轮桩少返回一行 → 该行被删除、其余 `generation+1`；桩 `range` 错 → 第三轮不改表、run `partial`、`error_code=='WEB_BILL_RANGE_MISMATCH'`。
- [ ] **Step 3: 写 `TestWebBillsSyncIgnoresRowsOutsideWindow`**（桩多返回一行 8 天前 → 不入库）与 `TestWebBillsSyncBatchesOf50`（远端 120 个交付 → 桩收到 3 次请求，id 数 50/50/20）。
- [ ] **Step 4: 运行 `go test ./internal/app -run WebBill -v`**，期望：FAIL
- [ ] **Step 5: 实现 `web_bills_sync.go` 与互斥锁**
- [ ] **Step 6: 运行 `go test -race ./internal/app -run 'WebBill|WebAccountsSync'`**，期望：PASS
- [ ] **Step 7: Commit** `git commit -m "网页通道：按模型账单同步"`

### Task 8: 后台循环接入

**Files:**
- Modify: `internal/app/worker.go:20-73`（常量与两个新 loop）
- Modify: `internal/app/app.go`（`ticks` 结构加 `webAccounts`、`webBills`；`/healthz` 的 workers 输出加 `web_accounts_last_tick`、`web_bills_last_tick`）
- Test: `internal/app/app_lifecycle_test.go`（现有 healthz 测试加断言两个新字段存在）

- [ ] **Step 1: 在 healthz 测试里加断言 `workers.web_accounts_last_tick`、`workers.web_bills_last_tick` 键存在**，运行期望 FAIL
- [ ] **Step 2: 加常量 `webAccountsInterval`、`webBillsInterval`，`a.wg.Add(6)`，两个 loop 分别调 `syncAllWebAccounts`、`syncAllWebBills`（`safeTick("web-accounts"...)`、`safeTick("web-bills"...)`），tick 记录**
- [ ] **Step 3: 运行 `go test -race ./internal/app -run 'Health|Lifecycle'`**，期望：PASS
- [ ] **Step 4: Commit** `git commit -m "网页通道：后台同步循环与健康心跳"`

### Task 9: 对外 API

**Files:**
- Modify: `internal/app/app.go:275-300`（`accountListBase`：`link` CTE 改为按 `last_observed_at` 选；加 `remote` CTE 关联 `platform_remote_accounts`；`platform_status` 改为"观察时间新者胜"；输出列加 `five_hour_used`）
- Modify: `internal/app/app.go:360-400`（行解码加 `platform` 块）、`accountGet`（同样加 `platform` 块）、`accountListFilter`（`five_hour_min`、`sort=five_hour_desc`）
- Create: `internal/app/web_routes.go`（`remoteAccounts`、`billModels`、`billModelsSync`、`accountUsageHistory`）
- Modify: `internal/app/router.go`、`internal/app/app.go:201-260`（dashboard）
- Modify: `docs/api-contract.md`（新增/变更的接口与字段）
- Test: `internal/app/web_routes_test.go`、`internal/app/dashboard_sync_test.go`

**Interfaces（响应结构照设计 §5）:**
- `GET /api/v1/accounts` 与 `GET /api/v1/accounts/{id}`：新增 `platform` 对象；无远端记录时 `platform: null`；`usage` 无数据时 `usage: null`。`five_hour_used_percent`/`week_used_percent` 为 0–100 两位小数，`cost_fen` 整数，`cost_yuan` 字符串两位小数。
- 列表过滤 `five_hour_min=<0-100>`（非法 → `queryInvalid("five_hour_min", ...)`），排序 `sort=five_hour_desc`。
- `GET /api/v1/platform-connections/{id}/remote-accounts`（`PermAccountsView`；query `status`、`q`、`page`、`page_size`）→ `{ list: [...], summary: {total, linked, auto_created, unmatched, superseded, missing}, counts_by_status: {...} }`，每项 `{upstream_id, aid, email, alias, ext_status, locked, account_id, auto_created, superseded_by, missing_since, reason_text, profile_incomplete, profile_diff: []string, observed_at}`。
- `GET /api/v1/bills/models?connection_id=&from=&to=&account_ids=&page=&page_size=`（`PermBillsView`）→ `{ list: [{bill_date, account_id, account, alias, upstream_id, model_name, cost_fen, final}], by_account: [{account_id, account, cost_fen}], by_model: [{model_name, cost_fen}], total_cost_fen, coverage: {days_requested, days_present, today_final: false}, page, page_size, total }`；日期校验复用 `supplier.ValidateBillDates(..., remote=false)`。
- `POST /api/v1/bills/models/sync` body `{connection_id}`（`PermBillsSync`）→ 202 `{run_id}`；退避中 → 409 `SYNC_IN_BACKOFF` 带 `details.next_attempt_at`；未配置 → 422 `WEB_NOT_CONFIGURED`；同步在 goroutine 里跑（复用 `syncWebBills`）。
- `GET /api/v1/accounts/{id}/platform-usage-history?days=7`（`PermAccountsUsage`，`days` 1–90）→ `{ list: [{observed_at, cost_fen, input_tokens, output_tokens, five_hour_used_percent, week_used_percent}] }`。
- 仪表盘 `platform` 块新增：`web_sync_health`（`syncHealth` 取 web-accounts 与 web-bills 中 `consecutive_failures` 更大者；都 ok 取较新的）、`remote_counts_by_status`（最近一次成功 web-accounts run 的 counts，存在 `sync_runs.request_json` 里）、`near_limit_accounts`（`five_hour_used>=0.8` 且 `missing_since=''` 的远端记录数）、`today_model_cost_fen`。

- [ ] **Step 1: 写 `TestAccountListExposesPlatformBlock`**：用 Task 6 fixture 同步后请求列表 → `platform.status=="using"`、`platform.usage.cost_fen==3521`、`platform.usage.five_hour_used_percent==5`、`platform.profile.balance=="100"`；`five_hour_min=80` 只返回 ≥80 的；`sort=five_hour_desc` 顺序正确；详情接口同样有 `platform`。
- [ ] **Step 2: 写 `TestRemoteAccountsEndpoint`**（summary 各计数、分页、`status=ban` 过滤、`profile_diff` 在本地 `plan_end_time` 与远端不同时含 `"plan_end_time"`）、`TestBillModelsEndpoint`（合计、`coverage.days_present`、分页、非法日期 422）、`TestBillModelsSyncEndpoint`（202 后 run 出现；退避中 409 且 `details.next_attempt_at`）、`TestUsageHistoryEndpoint`、`TestDashboardWebFields`（`near_limit_accounts==1` 当一条远端 0.84）。
- [ ] **Step 3: 运行 `go test ./internal/app -run 'PlatformBlock|RemoteAccounts|BillModels|UsageHistory|DashboardWeb' -v`**，期望：FAIL
- [ ] **Step 4: 实现路由与 handlers，改 `accountListBase` 与解码，改 dashboard；更新 `docs/api-contract.md`**
- [ ] **Step 5: 运行 `go test -race ./internal/app` 全量**，期望：PASS（注意现有 `accountListBase` 相关测试：`platform_status` 来源变化后它们仍应通过，因为只有网页通道写 `last_observed_at` 更新的数据）
- [ ] **Step 6: Commit** `git commit -m "网页通道：账号 platform 块、对账、按模型账单与仪表盘接口"`

### Task 10: 前端：类型、API 与连接表单

**Files:**
- Modify: `web/src/types/api.ts`（`Account.platform?: PlatformInfo | null`；新 `PlatformInfo`、`PlatformProfile`、`PlatformUsage`、`RemoteAccount`、`RemoteAccountsResponse`、`BillModelRow`、`BillModelsResponse`；`PlatformConnection` 加 `web_username`、`web_configured`、`web_suspended_at`、`web_suspended_reason`；`DashboardPlatform` 加 `web_sync_health?: DashboardPlatform`、`remote_counts_by_status?: Record<string, number>`、`near_limit_accounts?: number`、`today_model_cost_fen?: number`）
- Create: `web/src/model/platform-card.ts`
- Modify: `web/src/components/ConnectionFormDialog.vue`
- Test: `web/tests/platform-card.test.ts`

**Interfaces:**
- `platform-card.ts` 导出：`usageTone(percent: number | null | undefined): 'ok' | 'warn' | 'danger' | 'none'`（≥95 danger，≥80 warn，数字 ok，否则 none）；`formatResetAt(iso: string | null | undefined): string`（本地时区 `YYYY-MM-DD HH:mm:ss`，空 → `'—'`）；`usageStale(observedAt: string, now?: Date): boolean`（>30 分钟）；`locationLabel(code: string): string`（`US`→`美国`，其他原文）；`paymentLabel(v: string): string`（`google_pay`→`GooglePay`，`apple_pay`→`ApplePay`，其他原文）；`formatTokens(n?: number | null): string`（千分位，空 → `'—'`）。
- 连接表单：新增"网页登录账号"（`web_username`）、"网页登录密码"（`web_password`，password 框，编辑时占位"留空则保留"）、"清除网页登录"开关（编辑时才显示，提交 `web_clear:true`）、"测试登录"按钮（调用 `POST /platform-connections/{id}/web-login-test`，成功显示 `供应商：<name>`，失败用 `ErrorNotice`；新建未保存时禁用并提示先保存）。编辑时若 `web_suspended_at` 非空，表单顶部显示警告 `网页通道已暂停：<reason>`。

- [ ] **Step 1: 写 `platform-card.test.ts`**：`usageTone(84)==='warn'`、`usageTone(95)==='danger'`、`usageTone(null)==='none'`；`formatResetAt('2026-11-11T16:50:00Z')` 在 `TZ=Asia/Shanghai` 下为 `2026-11-12 00:50:00`（测试里固定时区：`process.env.TZ`，或改为接受 `timeZone` 参数并在测试传 `Asia/Shanghai`）；`usageStale` 31 分钟 true、29 分钟 false；`locationLabel('US')==='美国'`、`paymentLabel('google_pay')==='GooglePay'`、`formatTokens(411638)==='411,638'`。
- [ ] **Step 2: 运行 `cd web && npx vitest run tests/platform-card.test.ts`**，期望：FAIL
- [ ] **Step 3: 实现 `platform-card.ts`，改类型，改连接表单**
- [ ] **Step 4: 运行 `cd web && npx vitest run && npm run typecheck && npm run lint`**，期望：PASS
- [ ] **Step 5: Commit** `git commit -m "前端：网页通道类型、工具函数与连接表单"`

### Task 11: 前端：账号卡片、仪表盘、账单页与对账抽屉

**Files:**
- Create: `web/src/components/PlatformUsageBlock.vue`（props: `usage: PlatformUsage | null`, `observedAt: string`）
- Modify: `web/src/components/AccountCards.vue`（关联状态下按设计附录布局：标签行、两列资料、`PlatformUsageBlock`、原因与"来自平台"标记；未关联账号保持现状）
- Create: `web/src/components/RemoteAccountsDrawer.vue`（props: `connectionId`；表格列：交付 ID、平台账号 ID、邮箱、别名、状态、本地账号、自动创建、已消失、历史交付、资料不一致、原因；顶部 summary 芯片；状态筛选与搜索）
- Modify: `web/src/views/ListView.vue`（工具栏加"平台对账"按钮，需先选平台连接；加"接近限额"筛选项 → `five_hour_min=80`）
- Create: `web/src/components/BillModelsPanel.vue`（连接、日期（默认最近 7 天）、账号筛选；表格：日期、账号、模型、金额、"未出齐"标记；底部按账号/按模型合计；"立即同步"按钮 → 202 后提示"已开始，约 1 分钟后刷新"，409 显示下次时间）
- Modify: `web/src/views/PlatformBillsView.vue`（`el-tabs`：`离线账单`（现有 `PlatformBills`）、`按模型消费`（`BillModelsPanel`））
- Modify: `web/src/views/DashboardView.vue` 与 `web/src/model/sync-health.ts`（第二行"网页同步"用 `describeSyncHealth(dash.platform.web_sync_health)`；新卡"平台账号状态"列出 `remote_counts_by_status` 中非零项；"接近限额"数字点击跳 `{ name: 'accounts', query: { five_hour_min: 80 } }`；今日消费改用 `today_model_cost_fen` 并注明"约 2 小时延迟，未出齐"）
- Test: `web/tests/sync-health.test.ts`（加 web 行用例）、`web/tests/account-cards-platform.test.ts`（挂载 `AccountCards`，给一条带 `platform.usage` 的账号，断言渲染出 `7.00%`、`411,638`、`¥35.21`、`美国`、`GooglePay`；`usage: null` 时渲染"平台未返回用量"）

- [ ] **Step 1: 写上面两个测试**，运行 `npx vitest run tests/account-cards-platform.test.ts tests/sync-health.test.ts`，期望：FAIL
- [ ] **Step 2: 实现组件与页面改动**（样式沿用 `AccountCards.vue`/`PlatformBills.vue` 现有 class；颜色：warn 用现有橙色变量，danger 用现有红色变量）
- [ ] **Step 3: 运行 `cd web && npx vitest run && npm run typecheck && npm run lint && npm run format:check`**，期望：PASS
- [ ] **Step 4: Commit** `git commit -m "前端：平台账号卡片、用量块、对账抽屉、按模型账单与仪表盘"`

### Task 12: 文档、端到端与真实只读验收

**Files:**
- Modify: `docs/platform-contract.md`（新节"9. 网页后台只读接口"：登录方式、三个读接口、字段单位、不实现的写接口清单）
- Modify: `docs/operations.md`（§2 配置项不变；§6 常见异常加 `WEB_LOGIN_FAILED`/`WEB_SUSPENDED` 处理；说明网页凭证加密存储与暂停规则）
- Modify: `README.md`（功能列表加一行）
- Modify: `web/e2e/`（若现有 e2e 覆盖连接表单，补"网页登录"字段的可见性用例；否则跳过）
- Ledger: `.superpowers/sdd/2026-11-11-supplier-web-channel/progress.md`

- [ ] **Step 1: 写文档**
- [ ] **Step 2: 运行 `make test && make lint`**，期望：PASS
- [ ] **Step 3: 本地真实只读验收**：用 `.local-run` 的 8080 开发服务（`env -u HTTPS_PROXY -u HTTP_PROXY`，带现有 `CC_MANAGER_SUPPLIER_TLS_PINS`），在连接 `tx` 上通过界面填入网页登录账号密码 → 点"测试登录"显示"益充汇" → 等待或用 `POST /api/v1/bills/models/sync` 与一次手动 `syncWebAccounts`（临时 `go run` 或等 10 分钟）→ 核对：账号列表出现 8 个账号（5 个标"来自平台"）、384186/385753 显示封禁与原因、428802 卡片用量块与平台截图一致（允许数值随时间变化）、按模型账单页显示 395826 的 11-07..11-11 明细、仪表盘"接近限额"≥1。记录到 ledger（不写邮箱与密码）。
- [ ] **Step 4: 确认服务器日志与 `sync_runs.error_message` 不含密码或令牌**：`grep -c` 密码与 `enc_password` 在 `.local-run/server.log` 为 0。
- [ ] **Step 5: Commit** `git commit -m "文档：供应商网页只读通道"`，然后 `./scripts/release.sh` 不在本任务内，由用户决定是否发布。
