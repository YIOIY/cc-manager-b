# 修改平台资料 + 仪表盘改版 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 仪表盘改成"账号舰队"视角（需要处理清单 + 7 天消费 + 一行同步状态），并提供从账号卡片修改平台资料的手动操作。

**Architecture:** 仪表盘：后端新增 `GET /dashboard/attention` 和 `GET /dashboard` 的消费字段，前端重写 `DashboardView.vue` 为四块。平台资料：`WebClient` 增加 `UpdateAccountProfile`，服务端 `POST /accounts/{id}/platform-profile` 用快照拼整体载荷、成功后刷新快照并回写本地；前端卡片按钮 + 两步弹窗。先做仪表盘（任务 1–4），再做平台资料（任务 5–8）。

**Tech Stack:** Go 1.26 + SQLite，Vue 3 + TypeScript + Element Plus，Vitest，`go test -race`。

**Spec:** `docs/superpowers/specs/2026-11-12-platform-edit-and-dashboard-design.md`

## Global Constraints

- 平台写接口只有 `PUT /supplier-account/api/supplier/accounts/{id}`，载荷字段与类型照设计 §协议：除 `account_type`（数字 2）外全是字符串；`enc_adc_config` 为加密的空串；`cpa_auth_json`、`proxy_type`、`proxy_region`、`adc_config`、`api_key`、`region`、`aws_*` 为空串；`aws_auth_type:"arn"`；不发 `cpa_tags`、`proxy_ids`。
- 不新增错误码；校验用 `errs.Fields`（`VALIDATION_FAILED`），快照变化用 `VERSION_CONFLICT`，平台错误用现有 `WEB_*`/`PLATFORM_*`。
- 新权限 `accounts.platform_edit`（"修改平台上的账号资料"），加入权限目录"账号管理"模块；管理员自动拥有；不授予运营角色（无迁移）；README 权限计数 32→33。
- 仪表盘"需要处理"只看已关联（`platform_links.ingestion_state='accepted'`）且未归档的账号；到期阈值 30 天；接近限额阈值 80%，≥95% 优先；列表上限 8 条。
- 消费按连接时区的本地日期分桶，时区空或无效用 UTC；`cost_by_day` 固定 7 天补 0；今天 `final=false`。
- 前端去掉对 `today_requests`、`observed_recently`、`ready`、任务饼图的展示；接口字段保留。

## Review Focus

1. 没有配置网页通道（`platform_remote_accounts` 为空）时，仪表盘不能空白报错：顶行退回本地账号数并提示配置网页登录，需要处理列表为空态。→ Task 2 `TestDashboardAttentionWithoutWebChannel`，Task 4 前端空态测试。
2. 平台资料提交时平台返回 200 但 `msg` 不是"修改成功"（例如 `{"msg":"参数错误"}`）：必须当失败，本地不回写。→ Task 5 `TestUpdateAccountProfileRejectsNonSuccessMessage`。
3. 两个人同时改同一账号：第二个人的 `base` 已过期，必须 409，不能覆盖。→ Task 6 `TestPlatformProfileRejectsStaleBase`。
4. 提交成功但刷新快照失败（列表接口暂时 500）：返回 200 `verified:false`，本地已按提交值回写，审计仍记录。→ Task 6 `TestPlatformProfileUnverifiedWhenRefreshFails`。
5. 快照里平台值为空、用户没改该字段（例如 `recharge_time` 空）：载荷照发空串，不能因为"空值"报校验错。→ Task 6 `TestPlatformProfileKeepsEmptyUntouchedFields`。

---

### Task 1: 仪表盘消费字段与时区修正

**Files:**
- Modify: `internal/app/web_routes.go:274-303`（`webDashboard`）
- Test: `internal/app/web_routes_test.go`（追加）

**Interfaces:**
- Produces：`webDashboard` 的返回 map 新增 `week_model_cost_fen int64`、`cost_by_day []map{date string, cost_fen int64, final bool}`（7 条，按日期升序）、`cost_by_model []map{model_name, cost_fen}`（≤5，金额降序）、`cost_by_account []map{account_id, account, cost_fen}`（≤5）；`today_model_cost_fen` 改为连接时区的今天。
- 新函数 `func (a *App) spendSummary(ctx context.Context) (map[string]any, error)`：遍历 `platform_connections`（`enabled=1`），每个连接用自己的时区算 `today` 与 7 天窗口，对 `platform_bill_models` 按 `bill_date` 聚合后按日期字符串合并。
- `func connectionLocation(zone string) *time.Location`：空/无效 → `time.UTC`。

- [ ] **Step 1: 写 `TestDashboardSpendFields`**：用 `billsFixture` + 同步一轮账单（今天 2 行、昨天 2 行），再手插一条 8 天前的行；断言 `cost_by_day` 长度 7、今天 `final=false` 且金额等于今天两行之和、昨天 `final=true`、8 天前不计入 `week_model_cost_fen`；`cost_by_model[0]` 是金额最大的模型；`cost_by_account` 含账号邮箱；`today_model_cost_fen` 等于今天之和。再写 `TestConnectionLocationFallsBackToUTC`（`""`、`"Nowhere/Zone"` → UTC，`"Asia/Shanghai"` → 对应）。
- [ ] **Step 2: 运行 `go test ./internal/app -run 'DashboardSpendFields|ConnectionLocation' -v`**，期望：FAIL
- [ ] **Step 3: 实现 `spendSummary`、`connectionLocation`，接入 `webDashboard`**
- [ ] **Step 4: 运行 `go test -race ./internal/app -run 'Dashboard|WebBill'`**，期望：PASS
- [ ] **Step 5: Commit** `git commit -m "仪表盘：7 天消费聚合与连接时区的今日消费"`

### Task 2: 需要处理清单接口

**Files:**
- Create: `internal/app/dashboard_attention.go`
- Modify: `internal/app/router.go`（`need("GET /api/v1/dashboard/attention", PermDashboardView, a.dashboardAttention)`）
- Modify: `docs/api-contract.md`（附录加一行）
- Test: `internal/app/dashboard_attention_test.go`

**Interfaces:**
- `type attentionItem struct { Kind, Severity, AccountID, Account, Alias, Label, Detail, UpstreamID, ResetAt, ExpiresAt string; Value float64 }`，JSON 字段为蛇形小写同名（`kind, severity, account_id, account, alias, label, detail, upstream_id, reset_at, expires_at, value`）。
- `func (a *App) attentionItems(ctx context.Context, now time.Time) (items []attentionItem, counts map[string]int, err error)`：
  - 数据源：`platform_links l JOIN accounts a (archived=0) LEFT JOIN platform_remote_accounts r ON (r.connection_id,r.upstream_id)=(l.connection_id,l.upstream_id)`，`l.ingestion_state='accepted'`。
  - `near_limit`：`r.five_hour_used>=0.8`，`severity` = `"danger"`（≥0.95）/`"warn"`，`label` 如 `5 小时窗口 88%`，`detail` 为 `重置 HH:mm`（连接时区），`reset_at` 原始 RFC3339，`value` 百分比。
  - `banned`：`webFailureStatuses[r.ext_status]`，`severity:"danger"`，`label` = `runtimeLabel` 的中文由前端映射，这里给 `ext_status`，`detail` = `r.reason_text`（截 80 字）。
  - `expiring`：`plan_end_time`（优先 `r.plan_end_time`，否则 `a.plan_end_time`）距今 ≤ 30 天（含负数），且状态非失败类且未移除；`label` = `套餐 MM-DD 到期（N 天）` 或 `已到期 N 天`，`value` = 剩余天数，`severity` = `"danger"`（≤7 天或已到期）/`"warn"`。
  - `missing`：`r.missing_since<>''`，`severity:"warn"`。`profile_diff`：复用 `remoteProfileFields` 比较本地与快照 5 个字段，任一不同即一条，`severity:"info"`，`detail` 为字段名列表。
  - 排序：kind 顺序 near_limit(≥95 先) → banned → expiring（天数升序）→ missing → profile_diff；同 kind 内按 `value` 规则；截断 8 条；`counts` 为截断前各 kind 总数。
- `GET /api/v1/dashboard/attention` → `{ items, counts }`。

- [ ] **Step 1: 写 `TestDashboardAttentionOrdersAndCounts`**：用 `routesFixture` 的三个远端账号（one 5%、hot 84%、banned）再加：本地关联账号 `exp` 平台 `plan_end_time` = 今天+10 天、`gone` 远端标 `missing_since`、`diff` 本地 `plan_end_time` 与平台不同、一个 `five_hour=0.97` 的远端账号 `crit`、一个未关联的本地草稿 `draft`（`plan_end_time` 明天，不应出现）。断言顺序 `crit(near_limit,danger) → hot(near_limit,warn) → banned → exp(expiring) → gone(missing) → diff(profile_diff)`，`counts` 各为 1/1/…，`draft` 不在列表；再插 10 个 ≥80% 的账号，断言 `items` 长度 8 且 `counts.near_limit==12`。
- [ ] **Step 2: 写 `TestDashboardAttentionWithoutWebChannel`**：`deliveryFixture` + `linkFixtureAccount`（无远端行）→ 200，`items` 空、`counts` 全 0（本地 `plan_end_time` 为空时不算到期）；本地 `plan_end_time` 设为今天+3 天 → 出现一条 `expiring`（本地回退）。
- [ ] **Step 3: 运行 `go test ./internal/app -run DashboardAttention -v`**，期望：FAIL
- [ ] **Step 4: 实现并加路由、写契约文档**
- [ ] **Step 5: 运行 `go test -race ./internal/app -run 'Dashboard|Permission'`**，期望：PASS
- [ ] **Step 6: Commit** `git commit -m "仪表盘：需要处理清单接口"`

### Task 3: 前端模型与同步状态映射

**Files:**
- Modify: `web/src/types/api.ts`（`DashboardPlatform` 加 `week_model_cost_fen?`、`cost_by_day?`、`cost_by_model?`、`cost_by_account?`；新 `AttentionItem`、`AttentionResponse`）
- Modify: `web/src/model/sync-health.ts`（`describeMetricsHealth(p)`：`error_code==='PLATFORM_QUERY_DENIED'` → `{tone:'info', label:'平台未开通', detail:'OpenAPI 查询权限未开通，等供应商处理'}`，其他沿用 `describeSyncHealth`）
- Create: `web/src/model/dashboard.ts`（`attentionLabel(item)`：`near_limit`→`5 小时窗口 N%`，`banned`→`runtimeLabel(status)`，`expiring`→`套餐 MM-DD 到期（N 天）`/`已到期 N 天`，`missing`→`平台已移除`，`profile_diff`→`资料与平台不一致`；`accountQuery(item)` → `{ q: item.account }`；`spendBars(cost_by_day)` → 宽度百分比数组）
- Test: `web/tests/dashboard-model.test.ts`、`web/tests/sync-health.test.ts`（追加）

- [ ] **Step 1: 写测试**：`describeMetricsHealth` 对 `PLATFORM_QUERY_DENIED` 返回 info/`平台未开通`，对 `WEB_LOGIN_FAILED` 仍 danger；`attentionLabel` 各 kind；`spendBars([0,100,50])` → `[0,100,50]`，全 0 → 全 0 不除零。
- [ ] **Step 2: 运行 `cd web && npx vitest run tests/dashboard-model.test.ts tests/sync-health.test.ts`**，期望：FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行 `npx vitest run && npm run typecheck`**，期望：PASS
- [ ] **Step 5: Commit** `git commit -m "前端：仪表盘模型与指标同步状态映射"`

### Task 4: 仪表盘页面重写

**Files:**
- Create: `web/src/components/AttentionList.vue`（props `items`, `counts`, `loading`；行点击 `emit('open', item)`；空态文案"暂无需要处理的账号"；"查看全部"按钮 `emit('all')`）
- Create: `web/src/components/SpendPanel.vue`（props `platform: DashboardPlatform`；按天 7 根条（今天标"未出齐"）、按模型前 5 占比条、按账号前 5 金额；无账单时"暂无账单数据，配置网页登录后约 2 小时出现"）
- Modify: `web/src/views/DashboardView.vue`（顶行 4 个数字：平台账号（使用中/已封禁小字，无网页通道时退回本地总数并提示）、接近限额（点击 → `five_hour_min=80`）、今日消费（未出齐）、7 天消费；左列 `AttentionList`，右列 `SpendPanel`；底行一行：网页同步 + 指标同步（`describeMetricsHealth`）+ 代理启用数 + 任务（运行+排队>0 时显示详情并可点进任务中心）；删除库存概览、任务饼图、系统状态面板）
- Test: `web/tests/dashboard-view.test.ts`（挂载 `DashboardView`，mock `api` 返回 dashboard + attention：断言顶行数字、需要处理第一行文字、点击一行调用 `router.push` 带 `{name:'accounts', query:{q: email}}`、指标同步显示"平台未开通"、无网页通道时顶行显示本地总数与提示）

- [ ] **Step 1: 写 `dashboard-view.test.ts`**（参考 `account-cards-platform.test.ts` 的挂载与 stubs，`vi.mock('../src/api')`、stub `vue-router` 的 `useRouter`）
- [ ] **Step 2: 运行 `npx vitest run tests/dashboard-view.test.ts`**，期望：FAIL
- [ ] **Step 3: 实现组件与页面**
- [ ] **Step 4: 运行 `cd web && npx vitest run && npm run typecheck && npm run lint && npm run format:check && npm run build`**，期望：PASS
- [ ] **Step 5: 本地 8080 截图核对**（Playwright 登录后截图 `.local-run/web-channel-20261112/dashboard-new.png`，对照设计草图）
- [ ] **Step 6: Commit** `git commit -m "前端：仪表盘改版——需要处理清单、7 天消费与一行同步状态"`

### Task 5: WebClient 修改资料

**Files:**
- Modify: `internal/supplier/web.go`（登录时缓存 `public_key`；新增类型与方法）
- Test: `internal/supplier/web_test.go`（追加；`consoleStub` 增加 `accountUpdates []map[string]any` 记录 PUT 载荷与一个 `updateResponse` 可配置项）

**Interfaces:**
- `type WebProfileUpdate struct { Provider, Model, Email, Remark string; Plan, RegisterTime, PlanStart, PlanEnd, RegisterLocation, Balance, PaymentMethod, RegisterDevice, RechargeTime, WarrantyPeriod string }`
- `func (c *WebClient) UpdateAccountProfile(ctx context.Context, id int64, p WebProfileUpdate) error`：`PUT /supplier-account/api/supplier/accounts/{id}`，载荷按 Global Constraints；`enc_adc_config = EncryptLoginPassword(cachedPublicKey, "")`；401 重登一次后重试；200 且响应 `msg` 不含"成功" → `*WebHTTPError{Status:200, Summary: msg}`（码 `PLATFORM_RESPONSE_INVALID`）；非 2xx 走 `webErr`。
- `WebClient` 新增字段 `publicKey string`（登录响应 `public_key`，为空时退回 `pubkey` 接口的值）。

- [ ] **Step 1: 写 `TestUpdateAccountProfileSendsWholeRecord`**：桩记录 PUT 载荷；断言 JSON 键集合与设计 §协议一致（22 个键）、`account_type==2`（数字）、`cpa_balance=="100"`（字符串）、`cpa_warranty_period==""`、`cpa_auth_json==""`、`proxy_type==""`、`aws_auth_type=="arn"`、`enc_adc_config` 用桩私钥解密后明文为空串、不含 `cpa_tags`/`proxy_ids`；桩返回 `{"id":411177,"msg":"修改成功"}` → err nil。
- [ ] **Step 2: 写 `TestUpdateAccountProfileRejectsNonSuccessMessage`**（200 `{"msg":"参数错误"}` → `PLATFORM_RESPONSE_INVALID`；422 `{"detail":"x"}` → `PLATFORM_RESPONSE_INVALID`；401 一次后重登成功）。
- [ ] **Step 3: 运行 `go test ./internal/supplier -run UpdateAccountProfile -v`**，期望：FAIL
- [ ] **Step 4: 实现**
- [ ] **Step 5: 运行 `go test -race ./internal/supplier`**，期望：PASS
- [ ] **Step 6: Commit** `git commit -m "WebClient：修改平台账号资料"`

### Task 6: 服务端修改资料接口与权限

**Files:**
- Create: `internal/app/platform_profile.go`
- Modify: `internal/app/permissions.go`（`PermAccountsPlatformEdit Permission = "accounts.platform_edit"`，目录"账号管理"加 `{PermAccountsPlatformEdit, "修改平台上的账号资料", "通过供应商网页后台写入，仅资料字段"}`）
- Modify: `internal/app/router.go`（`need("POST /api/v1/accounts/{id}/platform-profile", PermAccountsPlatformEdit, a.platformProfileUpdate)`）
- Modify: `internal/app/web_routes.go`（`platformBlock` 加 `remark`、`web_channel: {configured, suspended}` 字段需要连接信息 → 改为在 `attachPlatformBlocks` 的查询里多取 `platform_connections.web_username<>''`、`web_suspended_at` 两列）
- Modify: `README.md`（32→33）、`docs/api-contract.md`
- Test: `internal/app/platform_profile_test.go`（`webConsoleStub` 增加 PUT 处理：记录载荷，按 `updateResponse` 回复，并把 `accounts` 里对应项的 `auth_config`/`remark` 按载荷更新，使后续列表反映改动）

**Interfaces:**
- `POST /api/v1/accounts/{id}/platform-profile` body `{changes: map[string]string, base: map[string]string}` → 200 `{ verified: bool, hint?: string, changed: []string, platform: <块> }`。
- 校验顺序：字段名合法 → 必填字段不可清空 → 日期格式 → `balance` 范围 → 账号已关联且快照存在、`missing_since==''`、`superseded_by==''`（否则 409 `VERSION_CONFLICT` 消息"平台记录已变化"）→ `base` 与快照一致（否则 409）→ 网页通道可用（`webClientFor` 错误透传 422/502）。
- 成功：`UpdateAccountProfile` → `ListAccounts` 找到该 `upstream_id` → `upsertRemoteAccount`（`verified=true`）；找不到或列表失败 → 用提交值更新快照的 9 个字段 + `remark`（`verified=false`，`hint`）→ 本地 `accounts`：`plan`（`validPlan` 才写）、三个日期、`register_location`，可选字段合并进 `metadata_json`，`version+1`，`updated_at` → 审计 `account.platform_profile_update`，detail = `strings.Join(changed, ",")`。
- `func buildProfileUpdate(snapshot remoteRow, changes map[string]string) supplier.WebProfileUpdate`（纯函数，可测）。

- [ ] **Step 1: 写测试**：`TestPlatformProfileUpdatesPlatformAndLocal`（改 `remark` 与 `plan_end_time`：桩收到整体载荷且其余字段等于快照值；响应 `verified=true`、`changed` 两项；快照与本地 `plan_end_time` 都更新；`≠本地` 消失即 `profile_diff` 为空；审计一条）、`TestPlatformProfileRejectsStaleBase`（`base` 与快照不同 → 409 `VERSION_CONFLICT`，桩未收到 PUT）、`TestPlatformProfileValidation`（未知字段、`plan` 清空、`balance:"150"`、日期 `2026-13-01` → 422 且 `fields` 逐项；桩未收到 PUT）、`TestPlatformProfileUnverifiedWhenRefreshFails`（桩 PUT 成功但之后列表返回 500 → 200 `verified=false`，本地已回写，审计一条）、`TestPlatformProfileKeepsEmptyUntouchedFields`（快照 `recharge_time` 空，只改 `remark` → 载荷 `cpa_recharge_time==""`，200）、`TestPlatformProfileBlockedStates`（`missing_since` 非空 → 409；网页通道未配置 → 422 `WEB_NOT_CONFIGURED`；无权限用户 → 403）、`TestPermissionCatalogueIncludesPlatformEdit`（目录含新权限，运营角色默认不含）。
- [ ] **Step 2: 运行 `go test ./internal/app -run 'PlatformProfile|PermissionCatalogueIncludes' -v`**，期望：FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行 `go test -race ./internal/app`**，期望：PASS
- [ ] **Step 5: Commit** `git commit -m "修改平台资料：接口、权限与本地回写"`

### Task 7: 前端修改资料弹窗

**Files:**
- Create: `web/src/components/PlatformProfileDialog.vue`（props `account: Account`；步骤一表单：10 个字段（套餐 select 用平台当前值 + 本地允许值合并去重、日期用 `el-date-picker` `YYYY-MM-DD`、注册地/支付方式/设备 `el-input`、余量 `el-input-number` 0–100、备注）+ "用本地资料填充"；步骤二对照表 `字段 | 平台当前 | 新值`，无改动禁用提交；提交 `changes`+`base`；成功 `emit('saved')`，409 显示"平台资料刚更新，请重新打开"）
- Modify: `web/src/components/AccountCards.vue`（按钮"修改平台资料"：`can('accounts.platform_edit') && card.platform && !card.platform.missing_since && card.platform.web_channel?.configured && !card.platform.web_channel?.suspended`；`emit('edit-platform', card)`）
- Modify: `web/src/views/ListView.vue`（挂载弹窗，`@edit-platform`，保存后 `page.load()`）
- Modify: `web/src/types/api.ts`（`PlatformInfo` 加 `remark`、`web_channel`）
- Test: `web/tests/platform-profile-dialog.test.ts`（填充、对照只列改动字段、提交体形状、409 文案）

- [ ] **Step 1: 写测试**，运行期望 FAIL
- [ ] **Step 2: 实现**
- [ ] **Step 3: 运行 `cd web && npx vitest run && npm run typecheck && npm run lint && npm run format:check && npm run build`**，期望：PASS
- [ ] **Step 4: Commit** `git commit -m "前端：修改平台资料弹窗"`

### Task 8: 文档与真实验收

**Files:**
- Modify: `docs/platform-contract.md`（§9 加写接口说明与"只此一个写接口、人工确认"）、`docs/operations.md`（§6.2 加一段）、`docs/superpowers/specs/2026-11-11-supplier-web-channel-design.md`（非目标处加注：2026-11-12 起开放资料修改这一个写操作）

- [ ] **Step 1: 写文档**
- [ ] **Step 2: 运行 `make test && make lint`**，期望：PASS
- [ ] **Step 3: 真实验收（本地 8080）**：重启到新二进制；仪表盘截图对照；在账号 411177（备注当前 "1"）上把备注改为 "1 cc-manager" 再改回 "1"，两次都断言平台列表随后返回的 `remark` 与提交一致、其余资料字段不变（抓改前/改后快照对比），审计两条；日志无密码。
- [ ] **Step 4: Commit** `git commit -m "文档：修改平台资料与仪表盘改版"`
