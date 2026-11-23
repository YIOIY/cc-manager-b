# 本地前后端 API 契约

> v1.0 设计稿，2026-11-04。所有 `/api/v1` 路径都是本项目拟实现的接口，不是供应商平台现有接口。平台事实见 [平台对接说明](platform-contract.md)。历史设计示例不代表全部实现；2026-11-05 已落地的账单接口以第 7 节“账单实现契约”为准，验证记录见 bill-query-implementation-report.md。

## 1. 通用协议

### 1.1 鉴权、版本与数据类型

- 同源 JSON API，前缀 `/api/v1`；浏览器不直接请求供应商平台。
- 使用服务端会话 Cookie，不使用浏览器长期 Bearer Token。变更请求携带 `X-CSRF-Token`。
- `GET /auth/session` 返回会话状态和CSRF令牌；未登录时也可建立仅用于登录CSRF校验的短期匿名会话。
- 服务端使用Origin校验、SameSite和CSRF验证；登录、登出也包含CSRF防护。
- 本地 `id` 为UUID字符串；平台 `upstream_id`、`upstream_aid` 使用十进制字符串输出。
- 时间戳为RFC3339（默认UTC）；业务日期为 `YYYY-MM-DD`，不能让浏览器转成UTC后改变日期。
- 请求数、Token和金额分等int64计数以十进制字符串输出，缺数据为 `null`。小范围页码、版本、进度计数可为JSON number。
- 百分比使用JSON number，例如 `success_rate_percent:99.8`；未知为null，不能把0和未知混淆。
- 所有资源变更携带 `expected_version`；创建和不修改实体的读取不需要。
- 本文首版所有业务接口均要求管理员登录。operator/viewer为P1预留，后端届时独立执行权限控制。

### 1.2 成功、分页和错误

成功示例：

```json
{
  "data": { "id": "7f4a5aef-4176-45df-8441-13036c369720", "version": 1 },
  "request_id": "req-example-001"
}
```

列表的 `data` 内包含 `items` 和 `pagination`，后者包含 `page`、`page_size`、`total`。默认每页20，最大100；排序字段白名单，不接受任意SQL列或表达式。

错误示例：

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "category": "validation",
    "message": "有 2 项资料不符合要求",
    "hint": "请修改标出的字段后重新提交",
    "retryable": false,
    "action": "fix_input",
    "fields": [
      { "field": "auth_json.refresh_token", "code": "REQUIRED", "message": "缺少刷新令牌" },
      { "field": "plan_end_time", "code": "OUT_OF_RANGE", "message": "套餐结束日期须至少晚于开始日期一天" }
    ],
    "details": { "resource": "account" }
  },
  "request_id": "req-example-002"
}
```

所有错误来自 `internal/errs` 的中央注册表；完整的 code 清单、分类、HTTP 状态、可重试标记、建议动作与文案见 [errors.md](errors.md)（由 `make generate` 生成）。`category` 取值：auth、permission、validation、not_found、conflict、rate_limit、upstream、config、storage、internal。`action` 取值：none、retry、wait、login、fix_input、contact_admin、check_platform、reauthorize。`fields[].code` 取值：REQUIRED、INVALID_FORMAT、OUT_OF_RANGE、UNSUPPORTED_VALUE、MISMATCH；`fields[].field` 为请求体 JSON 路径。校验采用全部收集：一次请求的所有字段问题在同一个响应里返回。`details` 仅允许白名单键（resource、permission、secret、retry_after_seconds、upstream_status、job_id、stage、field）。成功与失败的响应体都带 `request_id`，与响应头 `X-Request-ID` 及服务日志一致。429 同时携带 `Retry-After` 头与 `details.retry_after_seconds`。

不得返回带秘密的 `rejected_value`、原始平台响应或完整上传载荷；上游文本经统一脱敏（折叠空白、截断、隐藏令牌状片段）后才可出现在 message 中。

| HTTP | 分类 | 说明 |
| --- | --- | --- |
| 400 | validation | REQUEST_MALFORMED 等请求格式错误 |
| 401 | auth | AUTH_REQUIRED / AUTH_INVALID / USER_DISABLED |
| 403 | auth / permission | ORIGIN_FAILED / CSRF_FAILED / PERMISSION_DENIED（带 details.permission） |
| 404 | not_found | NOT_FOUND（带 details.resource）等 |
| 409 | conflict | VERSION_CONFLICT / ACCOUNT_EXISTS / IDEMPOTENCY_CONFLICT 等 |
| 413 | validation | PAYLOAD_TOO_LARGE |
| 422 | validation | VALIDATION_FAILED（含 fields）/ QUERY_INVALID / PREFLIGHT_BLOCKED 等 |
| 429 | rate_limit | RATE_LIMITED / AUTH_RATE_LIMITED / OAUTH_BUSY，读取 Retry-After |
| 502/504 | upstream | PLATFORM_* / OAUTH_EXCHANGE_FAILED / BILL_* 上游错误 |
| 503 | config / storage | ENCRYPTION_KEY_REQUIRED / SECRET_UNREADABLE / STORAGE_UNAVAILABLE |
| 500 | internal | INTERNAL_ERROR，带 request_id 供排障 |

异步任务中的错误保存在任务结果，不因后台上游出错而把已创建任务的202响应改写成成功上传。

### 1.3 幂等

- 创建上传、批量上传、导入提交等写操作必须携带 `Idempotency-Key`，建议UUID。
- 同一次操作的客户端网络重试复用同键；用户明确重新执行一个允许重试的操作才生成新键。
- 同键同规范化载荷返回同一资源/任务；同键异参返回409。
- 本地记录默认保存至少7天；交付唯一约束和任务记录保留时间更长，因此幂等记录过期不允许重复上传已交付账号。
- 未知结果的上传不开放通用重试，见任务接口。

## 2. 本地会话与能力

| 方法与路径 | 说明 |
| --- | --- |
| `GET /auth/session` | 返回authenticated、用户摘要、CSRF令牌和可用能力；不返回秘密 |
| `POST /auth/login` | username/password验证，建立会话并轮换ID；返回会话摘要 |
| `POST /auth/logout` | 撤销当前会话 |
| `GET /capabilities` | 返回本地能力开关、允许套餐、已知字段规范、枚举、限制与平台待确认项 |
| `GET /dashboard` | 本地库存和数据聚合，返回各数据源新鲜度 |

`GET /capabilities` 为历史设计，当前未实现。2026-11-09 已增加 Claude OAuth 手动回填入口（实验性），实际接口见 [授权录入说明](claude-oauth-onboarding.md)。自动刷新、远端更新和远端删除仍未实现。平台权限状态分 `configured / verified / unknown / rejected`，配置了套餐不等于实际验证通过。

首次管理员初始化使用部署侧一次性初始化过程，不开放无人值守的公网注册接口；具体初始化命令在实现后另写运行文档。

## 3. 账号接口

### 3.1 路由

| 方法与路径 | 说明 |
| --- | --- |
| `GET /accounts` | 分页、筛选和脱敏摘要 |
| `POST /accounts` | 新建本地草稿；最小必填 `account`（实现侧当前仍要求合法 `plan`）；日期等可稍后补齐。上号/`POST /accounts/onboard` 则要求完整资料与三个日期 |
| `GET /accounts/{id}` | 基础资料、凭证元信息、代理摘要、交付关系、状态和最新用量 |
| `PATCH /accounts/{id}` | 更新本地基本资料/密码/绑定代理，不同步远端 |
| `POST /accounts/{id}/archive` | 本地归档；未结束上传任务存在时拒绝 |
| `DELETE /accounts/{id}` | 删除本地账号，连同密码、凭证、任务与交付记录；需 `accounts.delete`。平台已明确接收（含 `accepted`，以及 `unknown` 但已写入 `upstream_id` 的本地关联失败）返回 409 `ACCOUNT_DELIVERED`；上号排队/进行中返回 409 `ACCOUNT_IN_PROGRESS`。无平台 ID 的结果待确认允许删除本地记录（操作前须先核实平台） |
| `POST /accounts/{id}/restore` | 恢复本地归档记录 |
| `GET /accounts/{id}/credentials/metadata` | 凭证字段存在性、缺项、格式状态、有效期、版本；不返回Token |
| `PUT /accounts/{id}/credentials` | 明确完整替换凭证对象，返回脱敏元信息 |
| `PATCH /accounts/{id}/credentials` | 合并更新指定字段，保留未提及字段及未知字段 |
| `POST /accounts/{id}/credentials/proxy-resolution` | 对导入代理与绑定代理冲突作版本化决议，原子更新凭证与绑定 |
| `POST /accounts/{id}/preflight` | 针对选定平台连接检查当前配置，返回阻断项和警告 |
| `POST /accounts/{id}/submit` | 锁定交付快照，创建上传任务，202 |
| `GET /accounts/{id}/deliveries` | 本地交付记录与快照元信息，不返回原载荷 |
| `POST /accounts/{id}/platform-links` | 人工核对后关联已有平台ID；用于历史账号或上传结果未知 |

首版不提供远端删除/换代理/刷新凭证的伪接口。`PATCH /accounts/{id}`可以变更本地配置，但必须返回 `remote_effect:"none"` 和对应 `out_of_sync`。

### 3.2 列表筛选

支持 `q`（账号/名称）、`plan`、`local_state`、`readiness`、`runtime_status`、`credential_state`、`proxy_id`、`proxy_health`、`has_delivery`、`out_of_sync`、`tag`、`archived`、`page`、`page_size`、`sort`。

账号搜索通过请求体查询变体或受控查询参数传输；为避免邮箱进入浏览器历史和访问日志，前端优先在 `POST /accounts/search` 发送敏感搜索词。该接口与GET列表共享筛选和分页返回类型；GET不携带邮箱搜索内容。

### 3.3 创建与局部更新字段

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `account` | string | 创建最小必填，不含空白；标识匹配按总体方案 |
| `password` | string，write-only | 创建时可不填；PATCH省略保持，传非空字符串替换；显式空字符串返回422 |
| `plan` | string或null | 配置允许的套餐；草稿建议填写（当前实现创建时非空）；`onboard`/预检/上号必填 |
| `register_location` | string或null | 当前仅 `US`；草稿可空，`onboard`/上号必填且须为 `US` |
| `register_time` | date或null | 草稿可缺；如提供须为 `YYYY-MM-DD`。`onboard`/预检/上号三者日期均必填，且 `注册 ≤ 开始 ≤ 结束` |
| `plan_start_time` / `plan_end_time` | date或null | 草稿可缺；如提供须合法且开始不晚于结束。`onboard`/预检/上号必填（同上顺序约束） |
| `proxy_id` | UUID或null | 选择本地代理；null明确解除本地绑定并使预检不通过 |
| `payment_method` / `register_device` | string或null | 依能力枚举；未核实值不可自动选择 |
| `balance` | number或null | 手工录入的初始剩余百分比0–100，单独记录来源和录入时间，不能当实时额度 |
| `recharge_time` | date或null | 可选 |
| `warranty_period` | number或null | 1/7/30 |
| `tags` | string[] | 必须存在于已知配置且互斥校验通过；本地后改标签不影响已交付标签 |
| `remark` | string | 有长度上限；审计不直接记录完整内容 |
| `expected_version` | number | PATCH必填 |

保存的返回信息至少包含：`id`、`version`、`local_state`、`readiness`、`password_configured`、`credential_metadata`、`proxy`、`platform_links`、`missing_fields`、`warnings`。不会返回密码原值。

账号有active交付后不允许直接改身份字段account；需新建本地记录或通过受审计纠错流程处理，不能让历史账单关联到另一账号。

### 3.4 凭证写入

完整替换请求结构：

```json
{
  "expected_version": 3,
  "expected_credential_version": 1,
  "auth_json": {
    "id_token": "",
    "access_token": "<真实值仅在用户输入时传入>",
    "refresh_token": "<真实值仅在用户输入时传入>",
    "last_refresh": "2026-11-04T06:00:00Z",
    "email": "account@example.com",
    "account_uuid": "<来自实际凭证>",
    "organization_uuid": "<来自实际凭证>",
    "organization_name": "<来自实际凭证>",
    "claude_device_ids": ["<来自实际凭证>"],
    "type": "<平台认可的实际值>",
    "expired": "2026-11-04T14:00:00Z",
    "proxy_url": "<待与选定代理明确核对>",
    "verification_status": "<平台认可的实际值>"
  }
}
```

以上是结构说明，不是有效上号凭证；占位值不可通过生产预检。当前凭证写入会校验 Claude 必填字段、RFC3339 时间、邮箱和代理协议；缺字段或格式错误直接拒绝。`auth_json`必须是对象；字符串形式由导入解析器显式解析，不能重复JSON编码。

PATCH对顶层字段逐项合并：未出现保持，null删除，字符串/数组/对象按字段替换；不做未定义的深合并。未知额外字段同样保留。修改必须带账号及凭证版本，避免两标签页交叉覆盖。

导入包含代理与现绑定不一致时返回 `PROXY_SOURCE_CONFLICT` 警告及脱敏比较。冲突未解决前不能提交平台，决议使用 `POST /accounts/{id}/credentials/proxy-resolution`，请求包含：

- `strategy`：`use_bound_proxy`（使用当前绑定代理）或 `adopt_import_proxy`（采用导入代理）。
- `proxy_id`、`expected_version`、`expected_credential_version`、`expected_proxy_version`。
- 采用导入代理时先在本地创建/复用对应代理；后端核对其规范化完整配置与加密凭证中的导入URL一致，不能仅凭前端声明采用。
- 采用绑定代理时，后端将该版本代理序列化为新的 `auth_json.proxy_url`。两种方式均原子更新账号/凭证版本、绑定和安全审计，保留其余未知字段。
- 返回新的版本和 `proxy_source_state:resolved`；决议依赖版本变化后重新校验。旧原始输入可在加密导入会话保留至TTL，不通过GET回传代理密码。

这里是用户明确选择后的已知字段更新，不是上传时静默覆盖导入出口。

### 3.5 预检

请求：`{ "platform_connection_id":"<uuid>", "expected_version":4 }`。

结果包含：

```json
{
  "data": {
    "account_id": "7f4a5aef-4176-45df-8441-13036c369720",
    "can_submit": false,
    "versions": { "account": 4, "credential": 2, "proxy": 1, "connection": 1 },
    "checks": [
      { "code": "AUTH_EXPIRED", "status": "error", "field": "auth_json.expired", "message": "凭证已过期" },
      { "code": "PLATFORM_AUTH_UNVERIFIED", "status": "warning", "message": "平台OpenAPI鉴权尚未实测" }
    ],
    "checked_at": "2026-11-04T06:10:00Z"
  },
  "request_id": "req-example-003"
}
```

预检本身不上传账号、不生成虚假凭证、不自动切换代理。代理检测需要独立检测任务；连接权限尚未实测时明确警告，不把“配置看起来正确”当作授权通过。

### 3.6 提交与关联

提交请求：

```json
{
  "platform_connection_id": "8ba11169-acaa-4d12-b422-949634a68fd3",
  "expected_version": 4,
  "expected_credential_version": 2,
  "expected_proxy_version": 1,
  "expected_connection_version": 1
}
```

必须带 `Idempotency-Key` 和CSRF。返回202：`data:{job_id,account_id,ingestion_state:"queued"}`。提交返回任务不包含凭证正文。Worker发现配置版本不同必须停止并要求重新预检，不能默默使用新配置。

人工关联 `POST /accounts/{id}/platform-links` 接收 `platform_connection_id`、`upstream_id`、`expected_version`、`reason`、`acknowledge_identity_check:true`。先显示用户确认的账号/平台/ID；响应只表示建立本地关联，不声称从上游验证了邮箱身份。已有不同关联返回409。

### 3.7 批量操作

| 方法与路径 | 约定 |
| --- | --- |
| `POST /accounts/batch-preflight` | 选择最多100条 `{id,expected_version}` 和平台连接，返回每条检查结果 |
| `POST /accounts/batch-submit` | 选择明确账号及各版本；带幂等键；创建父任务和逐账号子任务 |

父任务不是远端事务。每条子任务独立执行并展示成功/失败/结果未知；预检失败条目必须返回原因，不静默略过。父任务取消仅停止未发送子任务。

## 4. 导入接口

| 方法与路径 | 说明 |
| --- | --- |
| `POST /imports/preview` | JSON或文本内容的解析与脱敏预览；支持 `password` 和 `auth_json`，敏感值仅加密保存于临时导入会话，响应只返回是否已配置 |
| `POST /imports/{id}/commit` | 选择有效行及重复处理策略，幂等提交到本地库存，不上传平台 |
| `GET /imports/{id}` | 查询预览摘要或提交结果；不会返回原始秘密 |

`format`明确取 `credential`、`accounts`、`platform_payload`、`proxy_list`。单份凭证需要context提供目标account_id或用于新建的基础资料。JSON在UTF-8/BOM处理后解析，拒绝重复键、过深嵌套和畸形结构。

初始限制：普通JSON请求1MiB，导入请求5MiB，每批最多100行；限制可调整，但必须前后端一致。导入preview由请求体传入内容，不从用户提交的URL下载文件。

预览行返回 `row_index`、`masked_identity`、`action`、`issues`、`existing_id` 和字段来源摘要，不含密码、Token、带认证代理URL。提交策略为 `skip_existing` 或 `update_local_draft`；已交付记录不允许静默覆盖。超过TTL返回 `IMPORT_EXPIRED`，重新导入。

## 5. 代理池接口

| 方法与路径 | 说明 |
| --- | --- |
| `GET /proxies` | 分页、启用/健康/绑定数/地区筛选，返回脱敏地址和最近检测 |
| `POST /proxies` | 新建本地代理 |
| `GET /proxies/{id}` | 摘要、版本、最近健康、绑定数、期望地区和到期时间 |
| `PATCH /proxies/{id}` | 更新基本信息、启停和替换认证信息；带版本 |
| `POST /proxies/{id}/archive` | 无绑定后归档；不物理删除历史 |
| `POST /proxies/{id}/test` | 创建检测任务202，只访问配置允许的探测目标 |
| `POST /proxies/batch-test` | 最多100个代理，返回父任务及逐项结果 |
| `GET /proxies/{id}/checks` | 分页查看出口IP、延迟、探测阶段、错误码、时间 |
| `GET /proxies/{id}/accounts` | 查看本地关联账号及交付同步状态 |

代理写入字段：`name`、`scheme`、`host`、`port`、`username`、`password`、`enabled`、`expected_region`、`expires_at`、`remark`。用户名和密码只写；新建可空认证，PATCH省略保持原值，清空必须使用明确的 `clear_auth:true`，不能用掩码或空白意外覆盖。

健康结果：`state:unknown|healthy|unhealthy|stale`、`proxy_version`、`probe_generation`、`stage`、`exit_ip`、`latency_ms`、`checked_at`、`error_code`。启停是配置维度，`enabled:false`不等于网络不通。只有当前代理版本且在有效期内的检测可通过预检；旧配置迟到结果仅进入历史。

修改代理端点/认证后增加代理版本，所有引用该代理的已交付关系标记 `out_of_sync`；不得只在修改账号绑定时处理这个标记。

## 6. 任务接口

| 方法与路径 | 说明 |
| --- | --- |
| `GET /jobs` | 按kind、state、account_id、时间筛选分页 |
| `GET /jobs/{id}` | 状态、进度、子任务、时间、错误、allowed_actions |
| `GET /jobs/{id}/events` | 脱敏步骤事件，支持after游标 |
| `POST /jobs/{id}/cancel` | 仅后端声明cancellable的未发送任务；返回实际取消范围 |
| `POST /jobs/{id}/retry` | 仅明确安全可重试任务；返回新任务及原任务关联 |

任务种类：`account.submit`、`account.verify`、`proxy.test`、`sync.metrics`、`sync.bill`、`batch`。预检首版同步返回，不把普通表单检查都放进队列。

状态：`queued`、`running`、`waiting_external`、`succeeded`、`failed`、`cancelled`、`outcome_unknown`。

任务结果必须包含 `allowed_actions`，例如 `retry`、`cancel`、`associate_platform_id`、`open_platform`。前端不根据错误文案自行推断重试按钮。

上传任务收到平台接收响应后进入等待验证；出现online才可标最终验证成功，check_fail为明确验证失败。超时未知用attention_reason提示，保留最后状态与下一次检查时间。禁止将“平台接收”进度100%包装成“账号已在线”。

`account.submit`已经可能发送时的失败不得通过通用retry重放。只有适配器确认尚未发送或平台明确拒绝且未创建，才允许修正后重新提交。

## 7. 消耗、状态与同步

| 方法与路径 | 说明 |
| --- | --- |
| `GET /accounts/{id}/usage` | 按source/range读取本地保存的逐账号数据 |
| `POST /usage/query` | 多账号本地指标/账单查询，避免大量ID和搜索内容放URL |
| `POST /sync/metrics` | 创建受限的指标同步任务202 |
| `POST /sync/bills` | 创建受限的日账单补采/修订任务202 |

平台消耗闭环接口：

| `GET /platform-usage/metrics` | 读取供应商 metrics 快照（请求、Token、成功率、状态），不含金额 |
| `GET /platform-usage/bills` | 读取按业务日逐日采集的供应商账单，金额为平台消费（整数分） |
| `POST /platform-usage/sync` | 同步已建立平台关联的账号；可选 `connection_id` 分连接同步 |
| `GET /accounts/{id}/platform-usage` | 读取单账号平台指标和账单 |

### 账单实现契约（2026-11-05）

以下路径均带 `/api/v1` 前缀，已由当前 Go 服务实现，沿用登录、同源和 CSRF 校验。

| 方法与路径 | 实际行为 |
| --- | --- |
| `POST /platform-usage/bills/query` | 只读本地日账单；返回分页、全匹配合计与独立覆盖统计 |
| `GET /platform-usage/bills/accounts` | `connection_id` 必填；`include_archived=true` 可包含归档账号；包含有关联或有历史账单的账号 |
| `POST /platform-usage/bills/sync` | 创建或复用持久账单任务，返回 202：`run_id/state/reused` |
| `GET /platform-usage/sync-runs?kind=bill` | 最近 20 个账单任务，可按 `connection_id` 筛选 |
| `GET /platform-usage/sync-runs` | 保留原字段的最近 100 条同步记录，包含指标历史；可按连接或 `kind` 筛选 |
| `GET /platform-usage/sync-runs/{id}` | 账单任务日期、时区、账号数、进度、分类计数、HTTP 请求及重试次数 |
| `GET /platform-usage/sync-runs/{id}/items` | 任务项明细；支持 `state/page/page_size`；错误内容脱敏 |
| `POST /platform-usage/sync-runs/{id}/retry-failed` | 按业务日创建失败项的子任务，返回 `runs`；不额外采集成功项或 no_data 项 |

查询示例：

```json
{"connection_id":"本地连接ID","account_ids":[],"from":"2026-10-07","to":"2026-11-05","q":"","include_archived":false,"revision_min":null,"page":1,"page_size":20,"sort":"bill_date_desc"}
```

`connection_id/from/to` 必填；账号是本地 ID，空数组表示全部。支持 `bill_date_desc/bill_date_asc`，稳定次序包含账号和行 ID。`page_size` 允许 10、20、50、100。单次最多指定 10000 个账号。关键词匹配账号、关联别名、供应商 id 和平台 aid；修订次数可设非负下界。未知字段和多余 JSON 对象被拒绝。

返回 `data.items/pagination/totals/coverage/timezone`。`items` 的 `supplier_account_id` 是供应商上号返回的 id；`platform_account_id` 是 aid，未知时为 null。计数、Token、`cost_fen` 是十进制字符串，金额单位为分。所有本轮日账单保持 `finalized:false`；`revision` 初始 1，仅数值变化时递增。

`totals` 为当前筛选的**全部已保存行**合计，不受分页影响；包含 `currency:CNY`、`scope:all_matching_saved_rows`、`is_partial`。`coverage` 按连接、账号、日期和归档范围计算，不受关键词或修订次数改变；包含 `expected_items/verified_items/legacy_unverified_items/missing_items/no_data_items/failed_items/stale_items/latest_fetched_at/complete`。缺失、失败和 no_data 可能覆盖同一账号日，不能把这些数字直接相加。只有全部期望账号日都有新鲜、已校验的数据时才完整。

同步示例：

```json
{"connection_id":"本地连接ID","scope":"selected","account_ids":["本地账号ID"],"from":"2026-11-01","to":"2026-11-05","include_archived":false}
```

`scope` 必须显式为 `selected` 或 `all_linked`；后者不允许同时指定账号。后端固定任务创建时的关联账号、连接版本和时区。同步只允许连接 IANA 时区中今天及前 90 天（含下界），本地历史查询无此限制。默认限制 10000 个账号日，可用 `CC_MANAGER_BILL_MAX_ITEMS` 调整。按日拆分，每批最多 100 个供应商 ID；默认请求间隔 1000ms，可通过 `CC_MANAGER_BILL_INTERVAL_MS` 在 1～30000ms 调整。

前端提供 `Idempotency-Key`（最长 200 字符）；同键同规范化参数复用原任务，同键异参返回 409，活动同范围任务自动去重。网络失败后未修改范围的再次提交沿用原键。任务状态为 `queued/running/succeeded/partial/failed/interrupted`，明细另含 `updated/unchanged/no_data/superseded`。no_data 表示缺少返回行，不能解释为零用量；无失败但全部 no_data 的任务可以 succeeded，覆盖仍为不完整。

35 秒任务请求上下文、30 秒 HTTP 超时；临时网络错误、408、429 和 5xx 最多尝试 3 次，遵循 Retry-After 并封顶 30 秒。401/403 结束该连接任务剩余项。任务租约 120 秒，过期后可续跑；尝试次数持久化。按账号日写入代次阻止旧响应覆盖新值。格式/窗口/账号身份错误保留历史值；单行数值异常只影响对应账号。

原 `GET /platform-usage/bills` 的数组响应保留兼容，仍有 1000 行限制，新页面使用 POST 查询。原 `POST /platform-usage/sync` 保留指标采集，并为最近 3 日排队账单；返回已创建任务和逐连接错误，页面使用新的专用账单入口。

自动账单任务：每 2 小时重查最近 3 日，每天重查最近 7 日，默认不包含归档账号。`metrics/query-keys` 不接入。

### 原子上号

`POST /accounts/onboard` 接收账号基础字段（含 `plan`、`register_location`、`register_time`、`plan_start_time`、`plan_end_time`、`password`）、`auth_json`、`platform_connection_id` 和可选 `proxy_id`。日期为 `YYYY-MM-DD`，须满足 `注册 ≤ 开始 ≤ 结束`；与草稿 `POST /accounts` 不同，此处不允许缺日期。
服务会依次校验凭证、探测已选择的代理、调用供应商上号接口并读取 metrics 与 bill；全部步骤成功后才在一个事务中写入账号、密钥、交付、平台关联和快照。任一步失败都不会写入本地业务表。供应商已接收但同步失败时，响应会返回 `upstream_id` 并明确提示远端孤儿风险，便于人工处理。
| `GET /billing/ledger` | 系统计费账本；在受信任调用事件源接入前返回 `billing_status=计费源未接入`，不复制平台账单 |
| `GET /sync/status` | 各连接同步时间、失败分片、积压和下一次运行摘要 |

查询参数/字段：`source:metrics|bill`、`account_ids`（本地UUID）、`platform_connection_id`、`begin_time/end_time`或`start_date/end_date`、`granularity:window|day`。时间范围类型不能混用。

后端读取已保存数据可以超过上游可查窗口，但主动同步必须分别执行7天/90天边界。返回 `coverage` 描述是否完整、缺失日期和数据来源；不得“本地无数据”时自动发起不可见的大量远端请求。实时当日累计返回稳定业务日桶及实际观察窗口，前端不能把不同结束时刻的累计采样相加。

手动同步与周期同步在同一账号/连接/数据源/时间桶分区中去重和排序，旧generation不覆盖新结果；接口可返回已有排队任务，避免“刷新”反复生成同一采集。历史补采不改变当前运行状态。

返回示例：

```json
{
  "data": {
    "account_id": "7f4a5aef-4176-45df-8441-13036c369720",
    "source": "bill",
    "currency": "CNY",
    "totals": {
      "requests": "1234",
      "prompt_tokens": "100000",
      "completion_tokens": "50000",
      "cache_read_input_tokens": "3000",
      "cache_write_input_tokens": "1000",
      "cost_fen": "1234",
      "success_rate_percent": null
    },
    "coverage": { "complete": true, "missing_dates": [] },
    "freshness": {
      "state": "fresh",
      "fetched_at": "2026-11-04T06:00:00Z",
      "source_delay_hint_seconds": 7200,
      "finalized": false
    },
    "timezone": "Asia/Shanghai"
  },
  "request_id": "req-example-004"
}
```

`cost_fen:"1234"`显示¥12.34；不能当¥1234。metrics返回 `cost_fen:null`，而不是0；bill没有成功率时返回null。`finalized:false`表示仍可能因平台延迟发生修订，不能仅凭距当前2小时就声称上游账单绝不变化。

跨账号总数/金额只在相同source和不重叠窗口内聚合。缺项保留不完整标记，成功率权重未知时不返回伪精确总体值。

## 8. 平台连接、系统配置与审计

| 方法与路径 | 说明 |
| --- | --- |
| `GET /platform-connections` | 脱敏连接列表、Token是否配置、最近验证情况 |
| `POST /platform-connections` | 创建管理员指定的连接 |
| `PATCH /platform-connections/{id}` | 修改base_url、timezone、allowed_plans、enabled和Token；只写秘密 |
| `POST /platform-connections/{id}/test` | 对已关联账号做只读查询验证；无账号时仅校验配置并返回auth_state:unknown |
| `GET /settings/system` | 非敏感同步/保留策略摘要 |
| `PATCH /settings/system` | 版本化修改允许的本地策略；网络安全范围单独由管理员管理 |
| `GET /audit-events` | 按时间、动作、资源查询脱敏审计 |

平台写入 `openapi_token` 为write-only，GET只返回 `token_configured`；不返回掩码Token当作可回填值。不能把网页登录会话Token当OpenAPI Token自动导入。

base_url必须满足管理员允许的HTTPS origin策略；未知证书不能通过 `insecure_skip_verify` 字段绕过。自有CA通过部署配置提供，不允许普通账号操作接口任意修改信任策略。

连接没有平台关联、交付尝试和采集数据时允许修改origin、timezone；存在平台关联记录（包括人工关联的历史ID）、交付尝试（包括结果未知）或采集数据中的任一项后，两者不可变，修改返回409 `CONNECTION_IDENTITY_LOCKED`，必须新建连接。旧平台ID和账单不能自动迁移到不同origin/时区。

Token受控轮换、允许套餐和启停修改仍增加版本，已排队上传任务需重新确认版本；只读同步下次调度使用新配置。平台地址变更是否仍指同一实例需要另行审查，首版不提供绕过不可变约束的快捷开关。

审计事件包括账号创建/修改/归档、凭证替换、代理变更、平台配置、上传、人工关联、任务取消和重试。审计不含secret明文和完整自由文本备注。

## 9. 不开放的能力

- 未确认Claude授权适配器之前，不实现一个固定返回成功的“自动获取凭证”接口。
- 未确认远端更新接口之前，不提供伪装的 `PUT /remote-account` 或“远端换代理成功”。
- 不提供从浏览器localStorage读取平台代理Token的接口。
- 不提供任意URL下载/代理探测接口，不把管理后台变成通用网络转发器。
- 首版不提供原始凭证批量导出或无审计的秘密明文查询。

## 10. 契约实现要求

正式实现时以OpenAPI 3.1文件固定类型、枚举、nullable、writeOnly和错误schema，并由该契约生成前端DTO。本文路由与正式schema一起维护。

至少覆盖：UUID/上游ID区分、JSON未知字段保留、秘密不回读、日期跨时区、版本冲突、幂等键复用、上传不确定结果、批量部分失败、金额分与null语义。

## 已上号账号重新授权与同步

`POST /accounts/{id}/reauthorize` 要求现有账号凭证权限和资源写权限，支持可选 `expected_version`。省略或显式 `null` 的 `auth_json` 沿用密封凭证，可通过顶层 `claude_device_ids`（非空字符串数组，或逗号/换行分隔文本）补填设备标识；未提供且密封副本也没有时，服务端自动生成一个 UUID。非法元素类型返回 422。`proxy_id` 省略时沿用锁内读取的最新绑定，传 `""` 则解除池绑定并使用凭证中的代理地址。

提供新 `auth_json` 时，access/refresh token、刷新时间和有效期必须由本次授权提供，旧 token 不参与补齐。新旧邮箱均属于当前账号、且本次账号 UUID 没有与原值冲突时，允许从已保存凭证补齐缺失的账号 UUID、设备标识和代理地址。组织 UUID 必须由本次授权提供；只有组织 UUID 一致，才补齐缺失的组织名称。显式提供的非空字段优先，身份无法核对或原资料不可用时返回具体字段错误供补填。平台接收成功后才密封写回合并后的凭证；响应始终不包含凭证明文。

`POST /claude-oauth/sessions` 可额外传 `account_id`，在未选择代理池时使用该账号密封凭证中的代理进行 OAuth。生成链接和兑换时均校验账号写权限，旧凭证和代理认证信息不会回传前端。

OAuth 会话与账号写入的配合（详见 [授权录入说明](claude-oauth-onboarding.md)）：

- 兑换成功响应带 `oauth_session_id`；`POST /accounts/onboard`、`POST /accounts/{id}/reauthorize` 提交时应原样带上，服务端据此核对凭证来自该会话、出口代理未改变（否则 `409 OAUTH_PROXY_CHANGED`），并在提交完成后把结果缓存在会话内供重放。不带该字段时视为手动凭证，不做核对。
- 已绑定代理池记录的账号只能在原代理上发起 OAuth，换代理返回 `409 PROXY_BINDING_CHANGED`；改绑只能通过 `PATCH /accounts/{id}` 的 `proxy_id` 完成。账号存在活动 OAuth 会话期间，针对该账号的 `PATCH` / `reauthorize` / 归档等写操作返回 `409 ACCOUNT_IN_PROGRESS`，需先 `DELETE /claude-oauth/sessions/{id}` 释放。
- 管理会话内同时只允许一条活动的待授权链接；为另一账号或出口再次生成时返回 `409 OAUTH_SESSION_CONFLICT`，前端应先取消旧链接。
- `GET /oauth-settings`（需 `oauth.settings.view`）与 `PUT /oauth-settings`（需 `oauth.settings`）管理 OAuth 调度参数：全局并发 1–10、单出口并发 1–5、出口间隔 0–60 秒、探测缓存 0–30 秒、手动/批量预热范围（手动 0–3、批量 0–15 秒，上限 0 关闭）、UA 池（≤8 条、每条 ≤120 字符）；`PUT` 需带当前 `version`，冲突返回 `409 VERSION_CONFLICT`。

## 附录：2026-11-08 实现变更

- `POST /auth/login`：失败按来源 IP + 用户名指数退避，超限返回 `429 AUTH_RATE_LIMITED` 并带 `Retry-After`；请求体上限 16KB。
- 所有写接口：请求体上限 256KB（导入预览 5MB），超限返回 `413 PAYLOAD_TOO_LARGE`；JSON 不合法返回 `400 INVALID_JSON`。
- `PATCH /accounts/{id}`、`PUT /accounts/{id}/credentials`、`PUT /platform-connections/{id}`：可选 `expected_version`，不匹配返回 `409 VERSION_CONFLICT`；PATCH 同时校验套餐、日期格式/顺序与代理存在性。
- 唯一约束冲突返回 `409`（`ACCOUNT_EXISTS` / `CONNECTION_EXISTS` / `PROXY_EXISTS` / `ROLE_EXISTS` / `USER_EXISTS` / `IDEMPOTENCY_CONFLICT`）；数据库忙 / 锁定 / 磁盘满等暂时性故障返回 `503 STORAGE_UNAVAILABLE`，其余返回 `500 INTERNAL_ERROR`；响应体与响应头的 `request_id` 对应服务日志。
- `GET /jobs`：新增 `connection_id`、`code`、`message`（供应商拒绝原因，脱敏截断）；最多返回最近 2000 条。
- `GET /audit-events`：返回真实事件 `{id, action, resource_type, resource_id, actor, detail, created_at}`，最近 500 条。
- 账号列表 `submission_job` 新增 `message`。
- `GET /healthz`：`{ok, version, uptime_seconds, database, sessions, workers{...}}`，数据库不可用返回 503。
- 所有响应附带 `X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy`、`Permissions-Policy`；API 响应 `Cache-Control: no-store`。

## 附录：用户与权限（RBAC，2026-11-08）

- 权限名为 `<模块>.<动作>`，完整目录由 `GET /permissions` 返回：`[{key, label, permissions:[{key,label,hint}]}]`（需 `roles.view`）。无权限统一 `403 PERMISSION_DENIED`。
- `GET /auth/session` / `POST /auth/login` 返回 `user: {id, username, nickname, can_onboard, roles:[{id,name,builtin}], permissions:[...], is_admin}`。`is_admin` 表示持有内置管理员角色（超级权限）。`can_onboard=false` 时即使持有 `accounts.onboard` / `accounts.create` / `accounts.import`，上号相关写接口仍返回 `403 ONBOARD_DISABLED`。
- `POST /auth/password`（任何登录用户）：`{current_password, new_password}`。
- 角色：`GET /roles`（`roles.view`）→ `[{id,name,description,builtin,permissions,version,user_count,...}]`；`POST /roles` `{name, description, permissions[]}`；`PATCH /roles/{id}` `{name?, description?, permissions?, expected_version?}`；`DELETE /roles/{id}`。约束：权限名必须在目录内且不超过操作者自身权限（`VALIDATION_FAILED`）；管理员角色权限不可改、内置角色不可改名/删除（`ROLE_IMMUTABLE`）；仍被绑定的角色不可删（`ROLE_IN_USE`）；重名 `409 ROLE_EXISTS`。
- 用户：`GET /users`（`users.view`）；`POST /users` `{username, password, role_ids[]}`（`users.create`）；`PATCH /users/{id}` `{role_ids?, enabled?, can_onboard?, password?, nickname?, expected_version?}`（`users.update`，带 `password` 还需 `users.reset_password`）；`DELETE /users/{id}`（`users.delete`）。约束：至少一个角色；非管理员不能授予管理员角色或修改管理员用户；`SELF_DEMOTION` / `SELF_DISABLE` / `SELF_DELETE` / `LAST_ADMIN`。`can_onboard` 默认 `true`，关闭后禁止该用户新建/上号/导入/Claude OAuth。
- 审计新增 `role.create` / `role.update` / `role.delete`；`user.*` 事件的 detail 记录角色 id 列表。

## 附录：列表分页与写限速（2026-11-08）

- `GET /accounts` 支持服务端分页与筛选：`page`、`page_size`（10/20/50/100）、`q`（匹配账号、ID、套餐、注册地区、代理 ID、平台账号 ID/别名/状态）、`onboarding_state`（pending/submitting/linked/failed/unknown）、`plan`、`archived`（true/false）、`proxy`（present/absent）、`abnormal`（省略不过滤；`0`/`false` 排除异常账号；`1`/`true` 仅异常账号；账号列表页默认传 `0`）、`platform_status`（精确匹配）、`end_from`/`end_to`（套餐到期日）、`created_from`/`created_to`。异常账号指上号 `failed`/`unknown`、平台失败态（check_fail/login_failed/bound_failed/cooking_failed/re_login_failed/sync_failed/fault/suspended/arrearage；不含 ban/banned），或远端 `missing_since` 非空。带分页参数时返回 `{items, pagination}`；不带时沿用数组响应，最多 2000 条。非法筛选值返回 `422 FILTER_INVALID`。`onboarding_state` 在 SQL 中派生，规则与返回字段一致。
- 所有写请求（POST/PUT/PATCH/DELETE，登录除外）按用户令牌桶限速：默认 120/分钟、突发 30，超限返回 `429 RATE_LIMITED` 并带 `Retry-After`（秒）。读请求不受限。
- 已移除：`GET /usage`、`GET /billing/ledger`、`POST /sync/metrics`、`POST /sync/bills`、`POST /platform-usage/sync`。账单同步统一走 `POST /platform-usage/bills/sync`，运行记录走 `GET /platform-usage/sync-runs`。
- `GET /metrics`：Prometheus 文本格式，不鉴权，仅计数类指标；详见 operations.md 6.1。

## 附录：查找接口与代理编辑（2026-11-09）

- `GET /lookups/platform-connections` → `[{id,name,base_url,timezone,enabled}]`，需 `accounts.view` 或 `bills.view`；`GET /lookups/proxies` → `[{id,name,scheme,host,port,enabled}]`，需 `accounts.view` 或 `accounts.onboard`。均不含令牌、密码与检测记录，供下拉选择使用。
- `PATCH /proxies/{id}`（`proxies.update`）：`{name?, scheme?, host?, port?, username?, password?, clear_password?, enabled?, expected_version?}`，返回 `{id, version}`；校验同创建，版本不匹配 `409 VERSION_CONFLICT`。
- `DELETE /proxies/{id}`（`proxies.delete`）：仍被未归档账号绑定时 `409 PROXY_IN_USE`；成功时删除检测记录并把已归档账号的 `proxy_id` 置空，返回 `{id, deleted:true}`。
- `GET /proxies` 新增 `username`、`password_configured`。审计新增 `proxy.update`、`proxy.delete`。

## 附录：供应商网页只读通道（2026-11-12）

以下路径均带 `/api/v1` 前缀。数据来自服务端定时读取供应商网页后台（账号列表每 10 分钟、按模型账单每 2 小时），本地只保存快照，接口不直接访问平台。

| 方法与路径 | 实际行为 |
| --- | --- |
| `POST /platform-connections`、`PATCH /platform-connections/{id}` | 新增字段 `web_username`、`web_password`（明文只出现在请求里，服务端加密保存；更新时密码留空表示不变）、`web_clear: true`（清除网页登录并解除暂停）。列表与 `GET /lookups/platform-connections` 返回 `web_username`、`web_configured`、`web_suspended_at`、`web_suspended_reason`，永不返回密码 |
| `POST /platform-connections/{id}/web-login-test` | 用保存的凭证登录一次，返回 `{ok, supplier_name, expires_in, tested_at}`；未配置 422 `WEB_NOT_CONFIGURED`，失败 502 `WEB_LOGIN_FAILED`；成功会解除"登录连续失败"暂停 |
| `GET /platform-connections/{id}/remote-accounts` | 平台账号对账：`status`、`q`、`page`、`page_size`；每项为账号 `platform` 块加 `email`、`account_id`、`account`、`superseded_by`、`first_seen_at`、`profile_diff`（本地与平台不一致的字段名）、`profile_incomplete`；附 `summary{total, linked, auto_created, unmatched, superseded, missing}` 与 `counts_by_status`。非管理员只看到已关联到自己账号的远端记录（未匹配 / 无归属自动导入仅管理员可见）；列表、`summary`、`counts_by_status` 一并按归属收窄 |
| `GET /accounts`、`GET /accounts/{id}` | 新增 `platform` 对象（平台未列出该账号时为 `null`）：`status`、`status_raw`、`locked`、`reason_kind`、`reason_text`、`reason_fetched_at`、`observed_at`、`aid`、`upstream_id`、`alias`、`provider`、`model`、`models_usable`、`remote_created_at`、`missing_since`、`auto_created`、`source_channel`（`web`/`openapi`，按观察时间新者）、`profile{plan, plan_start_time, plan_end_time, register_time, register_location, balance, payment_method, register_device, recharge_time, warranty_period, tags}`、`usage{cost_fen, cost_yuan, input_tokens, output_tokens, five_hour_used_percent, five_hour_reset_at, week_used_percent, week_reset_at, observed_at}` 或 `null`。列表新增筛选 `five_hour_min`（0–100）与排序 `sort=five_hour_desc`；`platform_status` 改为取观察时间更新的通道 |
| `GET /accounts/{id}/platform-usage-history?days=7` | 用量快照时间序列（1–90 天，只在数值变化时记录）：`observed_at`、`cost_fen`、`input_tokens`、`output_tokens`、`five_hour_used_percent`、`week_used_percent` 等 |
| `GET /platform-usage/bills/models` | `connection_id`、`from`、`to` 必填，`account_ids` 逗号分隔可选，分页；返回按天按账号按模型的 `list`、`by_account`、`by_model`、`total_cost_fen`、`coverage{days_requested, days_present, today, today_final: false}`。当天的行 `final=false` |
| `POST /platform-usage/bills/models/sync` | `{connection_id}`；立即跑一次网页账单同步，202 `{run_id, state}`；退避中 409 `SYNC_IN_BACKOFF`（`details.next_attempt_at`）；未配置 422 `WEB_NOT_CONFIGURED`；已暂停 502 `WEB_SUSPENDED`。同一连接的账号同步正在进行时会等它结束再跑 |
| `POST /accounts/{id}/platform-profile` | 修改平台上的账号资料（网页通道唯一的写操作，需 `accounts.platform_edit`）：`{changes: {字段: 值}, base: {字段: 提交前看到的值}}`，可改字段 `plan`、`register_time`、`plan_start_time`、`plan_end_time`、`register_location`（不可清空）与 `balance`（0–100）、`payment_method`、`register_device`、`recharge_time`、`warranty_period`、`remark`（可清空）；日期 `YYYY-MM-DD`。校验失败 422 `VALIDATION_FAILED`（逐字段）；账号未关联、平台记录已移除/被替换、`base` 与当前快照不一致 → 409 `VERSION_CONFLICT`；未配置网页登录 422 `WEB_NOT_CONFIGURED`，暂停中 `WEB_SUSPENDED`，平台拒绝 502 `WEB_*`/`PLATFORM_*`。成功 200 `{verified, hint?, changed[], platform}`：平台整条记录被覆盖（密码、凭证、标签、代理不变），随后重读平台列表刷新快照（`verified=true`）；重读失败则用提交值更新快照并返回 `verified=false` 与 `hint`；本地账号同步写回（套餐、日期、注册地区入列，可选字段入 `metadata_json`，`version+1`）；审计 `account.platform_profile_update`，detail 为改动字段名 |
| `GET /accounts`、`GET /accounts/{id}`、`GET /platform-connections/{id}/remote-accounts` | `platform` 块新增 `remark`（平台备注）与 `web_channel{configured, suspended}`（该连接的网页通道是否可用于修改资料） |
| `GET/POST /platform-connections`、`PUT/PATCH /platform-connections/{id}` | 新增 `usd_cny_rate`：平台把美元原价折算成人民币的固定汇率（1 美元兑多少人民币），默认 7.2；请求里可选，必须是 1–20 的数字，否则 422 `VALIDATION_FAILED`（字段 `usd_cny_rate`）；修改时写审计 `connection.usd_rate_set`（detail 如 `7.2 → 7.3`）。账号的 `platform` 块同时带出所属连接的 `usd_cny_rate`，卡片用 `cost_fen ÷ usd_cny_rate` 显示美元 |
| `GET /dashboard/insights` | 仪表盘下半部分的聚合（`dashboard.view`）：`usd_cny_rate`（所有启用连接的平台汇率一致时为该值，否则 `null`）；`fleet{accounts, cost_fen_total, input_tokens, output_tokens, avg_five_hour_percent, avg_week_percent}`（已关联且平台仍在列的账号）；`quota[{account_id, account, alias, status, five_hour_percent, five_hour_reset_at, week_percent, week_reset_at, cost_fen}]`（按 5 小时占用降序，无用量的在后，最多 50 条）；`hourly[{hour, cost_fen}]`（最近 24 个整点小时，由用量快照的累计花费增量得出）；`composition{by_delivery, expiry{expired, within_7, within_30, later, unknown}}`；`recent_jobs[{id, account_id, account, connection, state, code, updated_at}]`（最近 6 条上号任务，只含结果码；没有 `jobs.view` 时为 `null`） |
| `GET /dashboard/attention` | 需要处理的已关联账号，最多 8 条：`items[{kind: near_limit|banned|expiring|missing|profile_diff, severity: danger|warn|info, account_id, account, alias, label, detail, upstream_id, reset_at, expires_at, value}]`（排序：接近限额（≥95% 在前）→ 失败状态 → 到期（已到期为负数在前）→ 平台已移除 → 资料不一致）与 `counts`（各类全量数）。只看已关联且未归档的账号；到期阈值 30 天 |
| `GET /dashboard` | `platform` 块另新增 `week_model_cost_fen`、`cost_by_day[{date, cost_fen, final}]`（连接时区最近 7 天，缺天补 0）、`cost_by_model[{model_name, cost_fen}]` 前 5、`cost_by_account[{account_id, account, cost_fen}]` 前 5；`today_model_cost_fen` 按连接时区的今天计算（时区空或无效时为 UTC） |
| `GET /dashboard` | `platform` 块新增 `web_sync_health`（两种网页同步中较差者，结构同指标同步健康）、`web_accounts_health`、`web_bills_health`、`remote_counts_by_status`、`near_limit_accounts`（5 小时窗口 ≥80%）、`today_model_cost_fen` |

同步记录 `sync_runs.kind` 新增 `web-accounts` 与 `web-bills`；审计事件新增 `connection.web_credentials_set`、`connection.web_credentials_cleared`、`connection.web_suspended`、`connection.web_login_test`、`account.platform_import`（自动建号，detail 为交付 ID）、`account.platform_relink`（同邮箱换交付，detail 为 `旧->新`）、`bills.web_sync`。网页通道只调用读取接口；平台侧的修改、改代理、封禁等操作不提供。

## 附录：用户之声（2026-11-13）

以下路径均带 `/api/v1` 前缀，都需要登录。提交和查看自己的反馈不需要权限；查看全部并处理需要 `feedback.manage`。

| 方法与路径 | 实际行为 |
| --- | --- |
| `POST /feedback` | `{kind: problem|suggestion|other, title?, body, page?, context?}`。`body` 必填、最多 2000 字；`title` 最多 80 字，留空取正文首行前 30 字。`page` 只保存路径（`?`、`#` 之后丢弃）；`context` 只保留 `error_code`、`request_id`、`user_agent`、`viewport` 四个键，每个值最多 300 字，其余键丢弃；服务端记录当前版本号。201 `{id}`；校验失败 422 `VALIDATION_FAILED`（逐字段）。审计 `feedback.create`。 |
| `GET /feedback` | 分页（`page`、`page_size`），可选 `status`（`open|in_progress|resolved|closed`）、`kind`、`q`（标题、正文、提交人）；非法的 `status`/`kind` 返回 422 `QUERY_INVALID`。有 `feedback.manage` 时返回全部，否则只返回本人的。`items[{id, username, kind, title, body, page, context, app_version, status, reply, handled_by, handled_at, version, created_at, updated_at, mine}]`（新的在前）、`pagination`、`counts`（各状态数量，遵循除 `status` 外的筛选）、`can_manage`。 |
| `PATCH /feedback/{id}` | 需 `feedback.manage`。`{status?, reply?, expected_version}`，至少给一项；`reply` 最多 1000 字。不存在 404；`expected_version` 不符 409 `VERSION_CONFLICT`。成功后记录处理人和时间，`version+1`；审计 `feedback.update`。 |
| `GET /feedback/summary` | `{open}`：待处理数量，范围同列表（管理者是全部，其他人是本人的）。 |

