# 供应商网页只读通道设计（2026-11-11）

## 意图

供应商平台（`https://120.233.254.152:8000`，供应商 ID 228）的 OpenAPI 查询接口对当前令牌一律返回 404，系统因此拿不到平台侧的账号状态和账单。网页后台用同一套接口的另一组路径（`/supplier-account/api/supplier/*`、`/bill/*`），用后台账号密码登录后能读到这些数据。本设计把这些**只读**接口接进系统，让账号状态、封禁原因、每个账号的平台用量（5 小时/7 天窗口占比、累计 Token、累计花费）、每日按模型账单自动进来，并把平台上有、本地没有的账号自动建成本地账号。用户要求本地账号卡片能复用平台账号卡片的全部信息（见附录"平台账号卡片"）。用户确认系统为个人使用，接入完全合规。

依据：2026-11-11 两份 HAR 的分析结论（见 `.claude` 记忆 `supplier-web-api-2026-11-11` 与本文附录）。

## 目标

1. 系统自动登录网页后台，不需要人工维护会话。
2. 每个关联账号显示平台侧状态（含锁定、封禁/验证失败原因）和平台用量块（5 小时窗口已用与重置时间、7 天窗口已用与重置时间、累计输入/输出 Token、累计花费），变化在 10 分钟内反映。
3. 平台上存在、本地不存在的账号自动建成本地账号并关联，标注来源。
4. 每日账单按账号、按天、按模型进入系统，账单页面可查、可汇总，当天数据标为未出齐。
5. 网页通道的同步健康（失败原因、退避、下次重试）像现有指标同步一样可见。

## 非目标

- 不接按时间窗口的请求数/成功率指标：`/metrics/query` 网页端同样 404，`record-log-*` 对该供应商恒为 0。保留现有 OpenAPI 指标同步，供应商开通权限后自动生效。账号列表里的累计用量不是这类指标的替代，但能回答"这个账号用了多少、离限额多远"。
- 不改变现有 OpenAPI 账单同步与 `platform_bill_daily` 的语义；网页账单单独存表。
- 不做平台侧写操作（编辑账号、改代理、设额度、封禁、批量、`/admin/*`）。这是硬性约束，不是暂缓项。
  - 2026-11-12 更新：经用户确认，开放**资料修改**这一个写操作（`PUT /supplier/accounts/{id}`，人工在弹窗里对照确认后提交），见 `2026-11-12-platform-edit-and-dashboard-design.md`；其余写接口仍不做。

## 反目标（做成这样算失败）

- 把网页账单的金额写进 `platform_bill_daily` 并把请求数、Token 填 0，让报表把"没有数据"显示成"用量为零"。
- 同步失败后每 2 分钟重试并反复登录，把后台账号刷到锁定。
- 把后台密码或会话令牌写进日志、审计事件或 API 响应。
- 自动建账号时把平台上已有的本地账号重复建一份。

## 约束

- 后台登录：`POST /supplier/pubkey {username}` 得 RSA 公钥 PEM；`POST /supplier/login {username, enc_password}` 得 `{token, name, expires_in(秒，当前 18000)}`。`enc_password` = base64(UTF-8 JSON `{"__enc__":"hybrid-aesgcm","k":b64(RSA-OAEP-SHA256(随机 32 字节 AES 密钥)),"iv":b64(12 字节),"ct":b64(AES-256-GCM(密码) ‖ 16 字节 tag)}`)，MGF1 亦为 SHA-256。无验证码。
- 读取接口统一 `Authorization: Bearer <token>`，基础路径 `/supplier-account/api`。
- 平台证书 CN 为 `aiexpert.tencent.com` 且已过期，现有 `CC_MANAGER_SUPPLIER_TLS_PINS` 固定证书机制必须复用；供应商客户端不读环境代理，只用 `CC_MANAGER_SUPPLIER_PROXY`。
- 账单接口 `GET /bill/query?account_ids=<逗号分隔交付 ID>&start_ds=YYYYMMDD&end_ds=YYYYMMDD` 一次可查多账号、7 天（含当天），返回 `list[{account_id, ds, model_name, bill_model_name, cost}]`（元，两位小数）及 `range`。只有金额，没有请求数和 Token。
- 账号列表 `GET /supplier/accounts?status=all&page=&page_size=&keyword=&provider=&model=&account_type=` 返回 `counts`（各状态数量）、`total`、`list[]`，每项含：
  - 标识与状态：`id`（交付 ID）、`a1_id`/`a1_status`（平台 1.0 账号 ID 与状态）、`a2_id`/`a2_status`、`ext_status`、`status`、`is_locked`、`proxy_type`、`account_alias`、`project_id`（账号邮箱）、`provider`、`model`、`models[{name, usable}]`、`usage_limit_type/period/value`、`remark`、`created_at`/`updated_at`（`YYYY-MM-DD HH:MM`，平台时区）。
  - `auth_config`：JSON **字符串**，键为 `account`（邮箱）、`password`（平台已脱敏，形如 `jonas****e3785`，**不可用**）、`plan`、`plan_start_time`、`plan_end_time`、`register_time`、`register_location`（如 `US`）、`balance`（如 `"100"`，页面显示"套餐余量 100%"）、`payment_method`（如 `google_pay`、`apple_pay`）、`register_device`、`recharge_time`、`warranty_period`（数字）、`tags`（字符串数组）。字段可缺失。
  - `extra`：JSON **字符串**，当前只有 `cpa_usage`：`{"cost": 35209959, "utilization": {"five_hour_used_percent": 0.05, "five_hour_reset_at": 1794415800, "week_used_percent": 0.01, "week_reset_at": 1794978000}, "input_tokens": 411638, "output_tokens": 82648}`。`cost` 单位为**百万分之一元**（35209959 → ¥35.21，与账单合计一致）；`*_used_percent` 为 0–1 的小数（0.07 → 7%）；`*_reset_at` 为 Unix 秒。页面显示的美元按汇率 7.2 换算，是平台展示口径。
  - 列表里**没有** OpenAPI 文档的 `requests`/`success_rate`。
- 原因接口 `GET /supplier/accounts/{id}/check-fail-reason`、`GET /supplier/accounts/{id}/claude-status-reason`：存在但响应格式未抓到，实现时按"任意 JSON，取其中可读文本"处理并保留原文。
- 已知状态值（来自 `counts` 的键）：all, arrearage, ban, banned, bound, bound_failed, check_fail, cooking, cooking_failed, fault, init, login_failed, offline, online, pending_activation, re_login, re_login_failed, ready, store, suspended, sync_failed, using, wait_check。未知值按原文保留并显示"未知"。
- 现有代码：平台连接表 `platform_connections`（含加密的 OpenAPI `token_secret`）；账号关联表 `platform_links(account_id, connection_id, upstream_id=交付 ID, upstream_aid, alias, runtime_status, runtime_status_raw, last_observed_at, ingestion_state)`；同步记录 `sync_runs(kind, state, error_code, ...)`；指标同步每 2 分钟、按连接退避（`metricsBackoff`，2 分钟翻倍到 60 分钟）；账单同步有自己的运行/条目/重试体系（`bill_sync.go`），只针对 OpenAPI 账单；凭证用 `a.keys.Seal/Open`，AAD 为记录 ID。

## 方案比较

1. **挂在现有平台连接上作为第二通道（选定）**：同一条 `platform_connections` 记录增加网页登录账号与密码，账号关联、同步记录、退避、账单页面全部复用。一个供应商仍只有一条连接。代价：连接表多两列，连接表单多两项。
2. 单独的"网页连接"实体：表、路由、页面都要再来一套，而且每个账号要同时挂在两条连接上，账单和状态要按两种连接汇总，复杂且容易混。排除。
3. 一次性导出脚本（用 `claude-export` CSV 手工导入）：不自动、不进系统，不满足目标。排除。

## 用户已定的决定

- 平台上有、本地没有的账号：**自动建成本地账号**，标注来源为平台同步。
- 同一邮箱在平台上换了新交付 ID（如被封后重新投放）：**本地账号自动跟到最新的那条**。

## 设计

### 1. 连接与凭证

- `platform_connections` 新增 `web_username TEXT NOT NULL DEFAULT ''`、`web_password_secret TEXT NOT NULL DEFAULT ''`（迁移 v10）。密码用 `a.keys.Seal(password, id+":web")` 加密；读取时 `Open`。两者都为空表示网页通道未启用，所有网页同步跳过该连接。
- 连接 API：创建/更新接受 `web_username`、`web_password`（明文，只在请求中出现）；读取时返回 `web_username` 和 `web_configured: bool`，永不返回密码。更新时 `web_password` 为空字符串表示不变；传 `web_username: ""` 且 `web_password: ""`（或新增字段 `web_clear: true`）表示清除。实现者二选一并在 API 契约文档写明。
- 修改网页凭证会重置该连接的网页退避计数（与编辑连接重置指标退避的做法一致）。
- **登录失败保护（实现者裁定）**：连续 3 次 `WEB_LOGIN_FAILED`（跨同步轮次计数，任一成功清零）后把该连接的网页通道标为"已暂停"：`platform_connections` 新增 `web_suspended_at TEXT NOT NULL DEFAULT ''`、`web_suspended_reason TEXT NOT NULL DEFAULT ''`（迁移 v10）；暂停期间所有网页同步跳过该连接、不再尝试登录；仪表盘网页同步状态显示"已暂停：登录连续失败，请检查后台密码"，连接表单显示同样提示。恢复路径：重新保存网页凭证，或"测试登录"成功，二者都清空暂停字段并重置退避。平台的锁定阈值未知，3 次是保守取值。
- **客户端生命周期（实现者裁定）**：每条连接在进程内只保留一个 `WebClient`（`App` 持有 map，按连接 ID），令牌只在内存；凭证变更或连接删除时丢弃该客户端；进程重启后首次同步重新登录一次。令牌不落库。
- 新增 `POST /api/v1/platform-connections/{id}/web-login-test`（权限同连接更新）：用当前保存的凭证登录一次，返回 `{ok, supplier_name, expires_in}` 或错误码；不触发同步。连接表单提供"测试登录"按钮。
- 审计事件：`connection.web_credentials_set`、`connection.web_credentials_cleared`，不记录值。

### 2. 网页客户端（`internal/supplier/web.go`）

- `WebClient` 由 `supplier.New` 相同的传输层构造（复用证书固定、代理规则、30 秒超时），持有 `username`、`password`，内存中缓存 `token` 与 `expiresAt`。
- `Login(ctx)`：请求公钥 → 组装 `enc_password`（Go 标准库 `crypto/rsa` OAEP-SHA256、`crypto/aes`+`cipher.NewGCM`）→ 登录 → 缓存令牌，过期时间取 `now + expires_in - 60s`。
- 每个读请求先确保有未过期令牌；收到 401 时重新登录一次再重试，第二次仍 401 则返回 `WEB_LOGIN_FAILED`。同一客户端内登录串行，避免并发重复登录。
- 接口方法：`ListAccounts(ctx) ([]WebAccount, WebCounts, error)`（按 `page_size=100` 翻页到 `total`）、`AccountReason(ctx, id, kind)`（kind ∈ check-fail, claude-status；返回原文 JSON 与提取的文本）、`QueryModelBills(ctx, ids []int64, start, end string) ([]WebBillRow, Range, error)`（`account_ids` 每批最多 50 个）。
- 错误类型 `*WebHTTPError{Status, Summary}` 实现 `errs.Upstream`，映射：401 → `WEB_LOGIN_FAILED`，403 → `WEB_FORBIDDEN`，404 → `WEB_ENDPOINT_MISSING`，429 → `WEB_RATE_LIMITED`，5xx → `PLATFORM_UNAVAILABLE`（已有），网络错误沿用现有网络错误码。新错误码按现有流程注册到 `internal/errs/registry.go` 并 `make generate`。
- 日志与错误信息经 `errs.Sanitize`；令牌、密码、`enc_password` 不进入任何错误文本。

### 3. 账号同步（`sync_runs.kind = "web-accounts"`）

- 周期：每 10 分钟每连接一次（新常量 `webAccountsInterval`；用量块是用户要盯的数据，平台自己也标注"定时同步"），共用现有 worker 循环的启动方式，退避沿用 `metricsBackoff` 的算法但按 kind 区分（把 `metricsBackoff` 参数化为 kind，不复制代码）。
- 新表 `platform_remote_accounts`（每连接每交付 ID 一行，迁移 v10）：
  - 标识与状态：`connection_id, upstream_id (交付 ID), upstream_aid, account_email, alias, provider, model, models_json, ext_status, a1_status, a2_status, is_locked, proxy_type, usage_limit_json, remark, remote_created_at, remote_updated_at, first_seen_at, last_seen_at, missing_since (NULL 表示本次仍在列表中), reason_kind, reason_text, reason_raw, reason_fetched_at, account_id (本地账号 ID，可空), auto_created INTEGER`。
  - 资料（来自 `auth_config`，密码不存）：`plan, plan_start_time, plan_end_time, register_time, register_location, balance, payment_method, register_device, recharge_time, warranty_period, tags_json`。
  - 用量（来自 `extra.cpa_usage`，缺失为 NULL）：`usage_cost_micro INTEGER, usage_input_tokens INTEGER, usage_output_tokens INTEGER, five_hour_used REAL, five_hour_reset_at TEXT(RFC3339), week_used REAL, week_reset_at TEXT, usage_observed_at TEXT`。
  - `raw_json`：该条列表项原文去掉 `auth_config.password`、`api_key`、`api_secret`、`account_md5` 后的 JSON，供诊断。
  - 主键 `(connection_id, upstream_id)`。它是平台侧的原样快照，账号卡片和对账都从它读。
- 新表 `platform_usage_snapshots`（迁移 v10）：`id, connection_id, upstream_id, account_id, observed_at, usage_cost_micro, usage_input_tokens, usage_output_tokens, five_hour_used, five_hour_reset_at, week_used, week_reset_at`，索引 `(account_id, observed_at)`、`(observed_at)`。**只在用量任一字段与上一条快照不同时追加**；保留 90 天，由现有 janitor 清理。它让"今天用了多少""过去一周的 5 小时占比走势"可查，不要求本期做图。
- 每次同步：
  1. 拉全量列表，逐条 upsert；本次未出现的行设置 `missing_since`（首次缺失时间），再次出现则清空。
  2. **关联**：先按邮箱把本轮远端记录分组（`account_email` 与 `accounts.account` 比较时不区分大小写、去空白）。每个邮箱选出**当前交付**：本轮仍在列表中（非缺失）的记录里，优先 `ext_status` 不属于失败类（见第 5 步的失败类集合）的，再按 `remote_created_at` 最新的；全部失败类时取最新的一条。然后：
     - 本地已有该邮箱的账号且其 `platform_links.upstream_id` 等于当前交付：只更新。
     - 本地已有账号但关联指向别的交付：把 `platform_links` 改指向当前交付（`upstream_id`、`upstream_aid`、`alias` 一并更新，`ingestion_state='accepted'`），写审计事件 `account.platform_relink`（摘要含旧、新交付 ID），旧交付记录保留在快照表并标 `superseded_by = 新交付 ID`。`platform_bill_models` 里旧交付的行 `account_id` 不变，账单仍按本地账号归并。
     - 本地没有该邮箱的账号：进入第 3 步自动建账号，并关联当前交付。
     - 同一邮箱的其余记录只保留在快照表（`superseded_by` 指向当前交付），对账计数记为"历史交付"而不是"重复"。
     - 没有邮箱的远端记录仍按 `(connection_id, upstream_id)` 匹配已有关联，匹配不到则只保留在快照表。
  3. **自动建账号**：仍找不到本地账号时，新建 `accounts` 行：`account`=邮箱，`plan`/`register_location`/`register_time`/`plan_start_time`/`plan_end_time` 取自 `auth_config`（缺失留空），可选资料 `balance`/`payment_method`/`register_device`/`recharge_time`/`warranty_period`/`tags` 写入 `metadata_json`（沿用现有可选字段的键名，见 `web/src/types/api.ts` 的 `balance`、`payment_method` 等），并加 `{"source":"platform_sync","platform_connection_id":…,"platform_upstream_id":…,"imported_at":…}`；`credentials_json='{}'`，不写 `account_secrets`（平台返回的密码已脱敏，拿不到）；然后建 `platform_links`。邮箱为空的远端记录不建账号，只保留在快照表并在对账计数里标"无法匹配"。
  3a. **自动建账号的校验（实现者裁定）**：`plan` 不在本地允许值（`allowedPlans`，当前含 `claude/cc-max-20x`）内时本地 `plan` 存空字符串（卡片显示"未设置"，平台原值仍在 `platform.profile.plan` 里显示）；日期字段不是 `YYYY-MM-DD` 的存空；`register_location` 不是 `US` 的照存（本地只在提交上号时校验）。自动建的账号在对账接口里带 `profile_incomplete: true` 及缺失字段名。
  3b. **已存在的本地账号**不被平台资料覆盖（本地资料以本地为准），但平台资料和本地不一致时在对账接口里标 `profile_diff: true` 并列出不一致的字段名，由人判断。
  4. **状态与用量回写**：更新 `platform_links.runtime_status = ext_status`、`runtime_status_raw = ext_status`、`alias`、`upstream_aid = a1_id`、`last_observed_at`；用量字段写入 `platform_remote_accounts`，有变化时追加 `platform_usage_snapshots`。
  4b. **两个通道同时有状态时（实现者裁定）**：账号列表/详情的 `platform_status` 改为"取观察时间更新的那个"——比较 `platform_links.last_observed_at`（网页同步写）与最新 `platform_metric_snapshots.fetched_at`（OpenAPI 指标同步写），新者胜；`source_channel` 相应为 `web` 或 `openapi`。两者都写 `platform_links.runtime_status` 的现状不变，因为它们描述的是同一个平台账号的同一状态。
  5. **原因**：`ext_status` 或 `a1_status` 属于 {ban, banned, check_fail, login_failed, bound_failed, cooking_failed, re_login_failed, sync_failed, fault, suspended, arrearage} 的记录，若上次抓原因距今超过 6 小时或状态变化，则调用两个原因接口（各一次），写入 `reason_*`。单条原因失败不影响整轮，计入 `failed`。
  6. `sync_runs` 记 `processed`（更新条数）、`failed`、`error_code`、`error_message`；全部成功为 `succeeded`，部分为 `partial`，登录或列表失败为 `failed`。
- 自动建账号产生审计事件 `account.platform_import`（主体为新账号 ID，摘要含交付 ID，不含邮箱以外的资料）。
- `platform_remote_accounts` 增加列 `superseded_by TEXT NOT NULL DEFAULT ''`（指向同邮箱的当前交付 ID，空表示它自己就是当前交付）。对账接口的 `summary` 用 `superseded` 替代原先的 `duplicates`。

### 4. 账单同步（`sync_runs.kind = "web-bills"`）

- 新表 `platform_bill_models`（迁移 v10）：`id, connection_id, upstream_id, account_id (可空), bill_date (YYYY-MM-DD), model_name, bill_model_name, cost_fen INTEGER, source_range_json, fetched_at, generation INTEGER, final INTEGER`，`UNIQUE(connection_id, upstream_id, bill_date, model_name)`，索引 `(connection_id, bill_date)`、`(account_id, bill_date)`。金额按现有 `decimalFen` 精确转分。
- 周期：每 2 小时每连接一次（`webBillsInterval`），同样走退避。窗口为连接时区下"今天往前 6 天到今天"共 7 天。
- 一次运行：取 `platform_remote_accounts` 中 `missing_since IS NULL` 的全部交付 ID，每 50 个一批调用 `QueryModelBills`，校验返回的 `range` 与请求一致（不一致记 `WEB_BILL_RANGE_MISMATCH`，该批丢弃）。
- 写入规则：同一 `(connection, upstream, date, model)` 以新值覆盖（`generation` 递增）；**窗口内本次返回中缺失的组合删除**（平台已没有该行，通常是重算），但只限本次成功返回的批次与日期范围。当天（连接时区）写 `final=0`，其余 `final=1`。`account_id` 取同步时 `platform_links` 的映射。
- 退避与失败码同账号同步；账单接口 401 走重新登录。

### 5. 对外 API

- `GET /api/v1/platform-connections/{id}/remote-accounts?status=&q=&page=&page_size=`（权限 `PermAccountsView`）：从快照表读，返回每条远端记录及其本地账号 ID、是否自动创建；附 `summary{total, linked, auto_created, unmatched(无邮箱), duplicates, missing(已从平台消失)}` 与 `counts_by_status`。
- `GET /api/v1/accounts/{id}` 与列表响应新增 `platform` 块：`{status, status_raw, locked, reason_kind, reason_text, reason_fetched_at, observed_at, aid, upstream_id, alias, provider, model, models_usable: bool, remote_created_at, source_channel: "web"|"openapi", profile: {plan, plan_start_time, plan_end_time, register_time, register_location, balance, payment_method, register_device, recharge_time, warranty_period, tags}, usage: {cost_fen, cost_yuan: "35.21", input_tokens, output_tokens, five_hour_used_percent: 7.0, five_hour_reset_at, week_used_percent: 1.0, week_reset_at, observed_at} | null}`。`cost_fen` 由 `cost_micro` 四舍五入到分；百分比在后端乘 100 并保留两位。现有 `platform_status` 字段保留（值来自 `platform_links.runtime_status`，网页同步会更新它）。
- `GET /api/v1/accounts/{id}/platform-usage-history?days=7`（权限 `PermAccountsUsage`）：返回 `platform_usage_snapshots` 的时间序列，供后续画图；本期前端只在卡片里显示最新值。
- `GET /api/v1/bills/models?connection_id=&from=&to=&account_ids=&page=&page_size=`（权限同现有账单查询）：返回明细行、按账号与按模型的合计、`coverage{days_requested, days_present, today_final: false}`。`POST /api/v1/bills/models/sync`（权限同账单同步）立即触发一次该连接的网页账单同步（受退避与"正在运行"约束，返回 run ID 或 `SYNC_IN_BACKOFF`）。
- 仪表盘 `platform` 块新增 `web_sync_health`（结构同现有 `metricsSyncHealth`，kind 为 web-accounts 与 web-bills 中更差的一个）、`remote_counts_by_status`、`near_limit_accounts`（5 小时 ≥80%）、`today_model_cost_fen`（网页账单当天合计）。账号列表接口新增过滤 `five_hour_min`（0–100）与排序 `five_hour_desc`。
- 新错误码：`WEB_LOGIN_FAILED`、`WEB_FORBIDDEN`、`WEB_ENDPOINT_MISSING`、`WEB_RATE_LIMITED`、`WEB_BILL_RANGE_MISMATCH`、`WEB_NOT_CONFIGURED`、`WEB_SUSPENDED`、`SYNC_IN_BACKOFF`，类别与动作提示按现有注册表体系。
- 现有 `metricsBackoff`、`metricsSyncHealth` 与 janitor 中写死的 `kind='platform-usage'` 改为按 kind 参数化，三种 kind（platform-usage、web-accounts、web-bills）共用。

### 6. 界面

- 连接表单：新增"网页登录账号""网页登录密码"（密码框，编辑时留空表示不变）与"测试登录"按钮，成功显示供应商名称。
- 账号卡片：复用平台卡片的信息布局（附录）。顶部标签：provider、套餐、平台状态（映射表见约束；锁定显示锁图标）、右上角平台创建时间。资料两列：账号别名、平台账号 ID（可复制）、账号、套餐、注册时间、套餐开始/结束时间、注册地、套餐余量（`balance` + "%"）、支付方式、注册设备、质保期/充值日期（有则显示）。本地资料与平台资料不一致的字段加小标记。状态属于失败类时显示原因文本（截断，悬停全文）与观察时间。来源为平台同步的账号显示"来自平台"标记。
- 账号卡片的"平台用量（定时同步）"块：两列显示 5 小时窗口已用（百分比，≥80% 橙色、≥95% 红色）与重置时间（本地时区，`YYYY-MM-DD HH:mm:ss`）、7 天窗口已用与重置时间、累计输入 Token、累计输出 Token（千分位）、累计花费 `¥35.21`（美元换算不做，汇率不是本系统的事实）；右上角显示观察时间，超过 30 分钟未更新显示"数据可能过期"。没有用量数据时整块显示"平台未返回用量"。
- 仪表盘："同步异常"块扩展为两行（指标同步 / 网页同步），各自显示状态、原因、下次重试；新增"平台账号状态"小卡（各状态数量，取 `counts`）；新增"接近限额"数字：5 小时窗口 ≥80% 的账号数，点击跳到账号列表并按该条件筛选（列表接口加 `five_hour_min` 过滤）。今日消费改为显示网页账单当日合计，并注明"约 2 小时延迟，未出齐"。
- 账单页面：新增"按模型消费"标签页，筛选连接、日期（默认最近 7 天）、账号；表格列：日期、账号、模型、金额；底部按账号合计、按模型合计；当天行标"未出齐"。提供"立即同步"按钮。
- 新增"平台账号对账"页面或账号页内的抽屉（实现者二选一，倾向账号页顶部一个入口按钮打开抽屉）：列出远端账号、状态、本地关联、自动创建标记、已消失标记。

### 7. 频率与保护

- 每连接每 10 分钟 1–2 次列表请求（按页，`page_size=100`）+ 少量原因请求；每 2 小时一次账单（8 个账号即 1 次请求）。登录每 5 小时一次。
- 任何同步失败按 kind 退避（2→60 分钟），登录失败也退避，不在同一轮内重试登录超过一次。
- 两种网页同步在同一连接上互斥（同一时刻只跑一个），与现有指标同步并行无妨。

### 8. 测试要求

- 加密函数：用生成的 RSA 密钥对做往返测试（解密出原密码，JSON 字段齐全，base64 合法）。
- 网页客户端：`httptest` 模拟 pubkey/login/accounts/bill/reason 接口，覆盖登录缓存、401 重登一次、翻页、批次拆分、range 不一致。
- 账号同步：已有关联只更新状态；邮箱匹配建关联；无匹配自动建账号且不重复，可选资料进 `metadata_json`；同邮箱出现新交付（旧的 `ban`、新的 `using`）时关联改指新交付并记审计，旧记录标 `superseded_by`；同邮箱两条都失败类时取最新；消失账号打 `missing_since`；原因只在失败状态下抓取；`auth_config`/`extra` 为空串、非 JSON、缺键时不报错；`cost` 微元转分四舍五入；用量不变时不追加快照，变化时追加；`raw_json` 不含密码、密钥、md5。
- 账单同步：覆盖写、删除窗口内消失的行、当天 `final=0`、金额分值精确。
- API 与前端：对账接口分页与汇总；账单模型接口合计；前端 Vitest 覆盖状态映射与"未出齐"标记；连接表单密码不回显。
- 所有新错误码经 AST 守卫测试与 `make check-generated`。

## 留给实现者

- `auth_config`/`extra` 以实际响应为准，解析要容错（字符串 JSON、空字符串、非对象都不能报错）；平台以后在 `extra` 加新键时忽略。
- 原因接口的响应结构未知：保留原文 `reason_raw`，`reason_text` 取第一个字符串型字段（优先键名含 reason/message/detail/msg）。
- 账单明细页的具体布局与配色沿用现有 `PlatformBills.vue` 的样式。
- 对账入口是独立页面还是抽屉。

## 附录：平台账号卡片（用户要求复用的样式，2026-11-11 截图）

```
[claude] [claude/cc-max-20x] [使用中]                         创建时间 2026-11-11 19:36
账号别名：claude-益充汇-20261111002        平台账号ID：20204 📋
账号：j***@gmail.com                         套餐：claude/cc-max-20x
注册时间：2026-11-03                          套餐开始时间：2026-11-11
套餐结束时间：2026-12-10                      注册地：美国
套餐余量：100%                                支付方式：GooglePay
注册设备：Mac                                 密码：jonas****e3785（本系统不显示平台的脱敏密码）
auth_json：已配置（不回显）
┌ 账号用量（定时同步） ─────────────────────────────────────────┐
│ 5 小时窗口已用：7.00%        5 小时窗口重置时间：2026-11-12 00:50:00 │
│ 7 天窗口已用：1.00%          7 天窗口重置时间：2026-11-18 13:00:00  │
│ 累计输入 Token：411,638      累计输出 Token：82,648                │
│ 累计花费：¥35.21                                                  │
└──────────────────────────────────────────────────────────────────┘
```

字段来源：标签与别名、ID、创建时间来自列表项本身；资料来自 `auth_config`；用量来自 `extra.cpa_usage`。`register_location` 的 `US` 显示为"美国"，`google_pay`/`apple_pay` 显示为 GooglePay/ApplePay，映射表放前端，未知值按原文显示。

## 附录：HAR 验证要点（2026-11-11）

- 登录无验证码；令牌 48 字符，`expires_in=18000`。
- 账号列表 8 条，其中 384186、385753 为 `ban`；本地仅 3 条。所有 8 条都带 `extra.cpa_usage`；411605 的 5 小时窗口已用 84%，412206 为 67%。
- `/bill/query` 一次返回 2 个账号、7 天、按模型的明细，`range` 原样回显；395826 在 11-07～11-11 消费约 ¥8885。
- `record-log-aggregate/list` 对 1.0/2.0、指定账号、7 天全部为 0 或空。
- `/metrics/query` 网页端 404。
