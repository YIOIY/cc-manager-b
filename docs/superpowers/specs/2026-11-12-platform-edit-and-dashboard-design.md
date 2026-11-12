# 修改平台资料 + 仪表盘改版 设计（2026-11-12）

两件独立的事放在一份文档里，因为它们都建立在 2026-11-11 上线的网页只读通道之上（见 `2026-11-11-supplier-web-channel-design.md`），而且用户要求一起出方案。它们可以分开实施和发布；计划按两个部分编排。

## 第一部分：修改平台资料

### 意图

网页通道现在把平台上的账号资料（套餐、日期、注册地、余量、支付方式、设备、备注等）同步回本地，并在卡片上标出"≠本地"。用户希望能从系统里把资料改回平台，而不是再去后台改。用户明确只要"资料修改"，不要密码/凭证、代理、封禁、用量上限。

### 协议（已从平台前端代码核实，2026-11-12）

`PUT /supplier-account/api/supplier/accounts/{id}`，`Authorization: Bearer <网页会话令牌>`。Claude（CPA）类账号的请求体是**整体覆盖**：

```
provider, model, adc_config:"", api_key:"", region:"", aws_region:"", aws_project_id:"", aws_auth_type:"arn",
account_id:<邮箱>, proxy_type:"", proxy_region:"", account_type:2, remark,
cpa_plan, cpa_register_time, cpa_plan_start_time, cpa_plan_end_time, cpa_register_location,
cpa_balance, cpa_payment_method, cpa_register_device, cpa_recharge_time, cpa_warranty_period,
cpa_auth_json:"", enc_adc_config:<hybrid-aesgcm(密码)>
```

- `enc_adc_config` 是用登录时拿到的公钥、以登录密码同样的混合加密方式加密的**密码字段**；平台弹窗说明"留空则不修改"，并把空字符串加密后发送（实测密文恰为 16 字节 GCM 标签，明文为空）。资料修改固定发加密的空串，密码不变。
- `cpa_auth_json:""` 同理表示凭证不变。
- 平台自己的弹窗对 CPA 账号固定发 `proxy_type:""`、`proxy_region:""`，代理不经此接口；照发空串。
- `cpa_tags` 在编辑时不发送（前端只在新建时带）；标签不改。
- 成功响应 `{"id": <交付 ID>, "msg": "修改成功"}`；其余按状态码处理。
- 公钥：登录响应里的 `public_key`，`WebClient` 需要把它和令牌一起缓存。
- **线上类型（来自 2026-11-12 抓到的真实 PUT）**：除 `account_type`（数字 2）外全部字段都是字符串；`cpa_balance` 是 `"100"` 这样的字符串，`cpa_warranty_period` 发空串或 `"1"`（列表里是数字，提交时是字符串），日期一律 `YYYY-MM-DD`，`cpa_recharge_time` 同格式或空串。模拟服务的测试要按这个样本逐字段断言类型。

### 目标 / 非目标 / 反目标

- 目标：从账号卡片发起，改平台上的 9 个资料字段（套餐、注册时间、套餐开始/结束、注册地、余量、支付方式、注册设备、充值日期、质保期）和备注，提交前看到对照，成功后本地与平台一致。
- 非目标：密码、凭证、邮箱、代理、标签、封禁、用量上限、批量；不做自动同步。
- 反目标：用空值覆盖平台已有的值；在平台记录已被移除或正在改关联时发请求；没有确认就发；把密码或公钥写进日志。

### 设计

1. **客户端**：`WebClient` 增加 `UpdateAccountProfile(ctx, id int64, p WebProfileUpdate) error`，内部用缓存公钥加密空串作 `enc_adc_config`，401 重登一次（公钥随之更新），响应 `msg` 不含"成功"且状态 200 时视为 `PLATFORM_RESPONSE_INVALID`。`WebProfileUpdate` 含上面全部请求字段的值（由服务端拼好），客户端不做默认值。
2. **服务端** `POST /api/v1/accounts/{id}/platform-profile`，权限 `accounts.platform_edit`（新增，加入权限目录"账号管理"模块；管理员自动拥有，运营角色不默认授予，README 权限计数 32→33）。请求体 `{ "changes": {字段: 新值}, "base": {字段: 弹窗打开时看到的平台值} }`（`base` 只需包含 `changes` 里的字段），字段限定为 `plan, register_time, plan_start_time, plan_end_time, register_location, balance, payment_method, register_device, recharge_time, warranty_period, remark`。
   - 校验：账号有 `platform_links` 且对应 `platform_remote_accounts` 行存在、`missing_since=''`、`superseded_by=''`；对 `changes` 里的每个字段，快照当前值必须等于 `base` 里的值，否则 409 `VERSION_CONFLICT`（"平台资料已更新，请重新打开"）——不用 `last_seen_at`，因为每次同步都会刷新它；日期字段 `YYYY-MM-DD`；`plan`、`register_time`、`plan_start_time`、`plan_end_time`、`register_location` 不能改成空（平台要求必填）；`balance`、`payment_method`、`register_device`、`recharge_time`、`warranty_period`、`remark` 可以明确清空（用户在对照步骤看到"旧值 → （空）"并确认）；`balance` 非空时为 0–100 的整数文本。
   - 载荷：以快照当前值为底（`plan…warranty_period`、`remark`、`provider`、`model`、`account_email`），覆盖 `changes`，固定字段照协议。
   - 执行：`webClientFor` 取客户端 → `UpdateAccountProfile` → 成功后立刻 `ListAccounts` 一次只取该交付 ID 的行？（列表接口没有按 ID 过滤，用 `keyword=<邮箱>`）→ 用返回值 `upsertRemoteAccount` 刷新快照 → 把同样的 9 个字段写回本地 `accounts`（`plan` 不在本地允许值时本地留空并在响应里标 `local_plan_skipped: true`；可选字段进 `metadata_json`）→ 审计 `account.platform_profile_update`，detail 为改动字段名列表 → 返回刷新后的 `platform` 块。
   - 失败：平台返回错误 → 透传为 `WEB_*`/`PLATFORM_*` 码，本地不改；刷新失败 → 返回 202 `PLATFORM_PROFILE_UNVERIFIED`（新码，conflict 类）提示"已提交，下次同步核对"。
3. **前端**：卡片按钮"修改平台资料"（`can('accounts.platform_edit')` 且 `platform` 存在且未移除、未被取代、网页通道已配置且未暂停）。弹窗 `PlatformProfileDialog.vue`：表单预填平台当前值；"用本地资料填充"按钮把本地有值的字段填入；提交前第二步显示"字段：平台当前值 → 新值"对照，无改动时禁用提交；成功后提示并刷新列表。`expected_observed_at` 不一致时提示"平台资料刚更新，请重新打开"。
4. **错误码**：不新增。平台已接受但刷新快照失败时返回 200 `{verified:false, hint:"已提交，下次同步核对"}`；字段校验问题用现有 `errs.Fields`（`VALIDATION_FAILED`）；快照过期用现有 `VERSION_CONFLICT`（409）。
5. **验证**：本地模拟服务断言收到的 PUT 载荷字段完整、`enc_adc_config` 解密为空串、`cpa_auth_json=""`；真实后台验收只改一个账号的"备注"再改回。

### 已定的决定

- 成功后回写本地（否则"≠本地"永远在）。
- 入口在卡片，不在对账抽屉。
- 邮箱、凭证、代理不可编辑；标签不动。

## 第二部分：仪表盘改版

### 意图与现状

现在的仪表盘有一半数字来自已失效的 OpenAPI 指标（请求数 0、平台观测 0），"可交付账号/账号完成度"按本地凭证计算，对平台同步来的账号没有意义；任务状态占四分之一版面但只有 1 个历史任务；"指标同步异常"的红色大块是已知的平台侧问题却最醒目；真正要看的数据（消费、接近限额、封禁）挤在角落且不能下钻。用户选定的视角：**账号还够不够用、有没有要换的**（限额、封禁、到期为主，消费为辅）。

### 目标 / 非目标

- 目标：打开即看到账号舰队的状态和需要处理的事；每条都能点到对应账号；消费有 7 天趋势；同步状态降级为一行；没有失效数据。
- 非目标：图表库（用现有 CSS 条形即可）、自定义布局、时间范围选择（固定今天/7 天）。
- 反目标：把 OpenAPI 的 404 当成需要用户处理的异常天天标红；为了填版面保留无意义的卡片。

### 版面（1440 宽；窄屏按现有断点折成一列）

```
顶行 4 个数字
 平台账号 8（使用中 6 · 已封禁 2） | 接近限额 2 | 今日消费 ¥14,882.78（未出齐） | 7 天消费 ¥49,506.82
左列（宽）                                   右列
 需要处理（按紧急度排序，最多 8 条，"查看全部"进账号页）   消费（最近 7 天）
   ca***  5h 窗口 88%  重置 00:50  [卡片]                 按天 7 根条（今天标"未出齐"）
   cj***  5h 窗口 79%  …                                  按模型前 5（占比条）
   gr***  已封禁 · 账号被暂停（封号）…                    按账号前 5（金额）
   xx***  套餐 12-04 到期（22 天）
   yy***  平台已移除 / 资料不一致
底行一行：网页同步 正常 12:07 · OpenAPI 指标 平台未开通（灰）· 代理 2 启用 · 任务 0 运行（有任务时才显示详情）
```

### 数据与规则

- 新接口 `GET /api/v1/dashboard/attention`（权限 `dashboard.view`）返回 `{ items: [...], counts: {near_limit, banned, expiring, missing, profile_diff} }`，`items` 每条 `{kind, severity, account_id, account, alias, label, detail, value, reset_at, expires_at, upstream_id}`，排序：`near_limit`（五小时 ≥95% 在前，再按百分比降序）→ `banned`/失败类状态 → `expiring`（按剩余天数升序，已到期的为负数排最前）→ `missing` → `profile_diff`，上限 8 条，`counts` 为全量数。**只看已关联且未归档的账号**（舰队视角；从未上号的本地草稿不算）；到期用平台快照的 `plan_end_time`，没有时用本地，范围是"≤ 30 天内到期或已到期且仍在使用（状态非失败类、未移除）"，已到期显示"已到期 N 天"。
- 现有 `GET /dashboard` 的 `platform` 块增加 `week_model_cost_fen`、`cost_by_day: [{date, cost_fen, final}]`（连接时区最近 7 天，缺的天补 0）、`cost_by_model: [{model_name, cost_fen}]` 前 5、`cost_by_account: [{account_id, account, cost_fen}]` 前 5；`today_model_cost_fen` 改为按连接时区的今天计算（修掉上一轮遗留的"午夜后显示昨天"）。连接时区为空或无效时用 UTC。多个连接时按各自时区的本地日期字符串合并。
- 去掉前端对 `today_requests`、`observed_recently`、`ready`/`readiness` 的展示；接口字段保留，不破坏兼容。
- 同步行：网页同步用现有 `web_sync_health`；OpenAPI 指标同步：`error_code == PLATFORM_QUERY_DENIED` 时显示灰色"平台未开通查询权限"，不显示连续失败次数；其他失败仍标红。
- 任务：`running+queued > 0` 时在底行显示"任务 N 运行 / M 排队"并可点进任务中心，否则只显示"任务 0"。
- 顶行"平台账号"用 `remote_counts_by_status`（只算未移除）；没有网页通道时退回本地账号总数并提示"配置网页登录后显示平台状态"。

### 前端

- `DashboardView.vue` 重写为上述四块；新组件 `AttentionList.vue`、`SpendPanel.vue`；`sync-health.ts` 增加 `describeMetricsHealth`（把 `PLATFORM_QUERY_DENIED` 映射为 `{tone:'info', label:'平台未开通'}`）。
- 点击"需要处理"一行：`router.push({name:'accounts', query:{q:<邮箱>}})`；点击"接近限额"数字：`five_hour_min=80`。
- 测试：Vitest 覆盖排序与阈值（`attention` 的排序在后端，前端只测渲染与跳转参数）、`describeMetricsHealth`、空数据态（无网页通道、无账单）。

### 留给实现者

- 条形图的具体样式沿用 `AccountCards.vue` 的 bar 样式。
- "需要处理"为空时的文案。
- 两部分可以分两次发布；先做哪部分由计划决定（建议先仪表盘：纯读、风险低；再平台资料修改）。
