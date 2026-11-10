# 平台消耗与系统计费设计

## 目标

平台消耗和系统计费是两套不同的数据域，必须分开采集、存储和展示：

- 平台消耗：供应商真实返回的账号状态、请求数、输入/输出 Token、成功率和离线账单。
- 系统计费：系统自己的调用事件、计费规则版本和内部账本。

平台账单金额不能直接当作系统收入或应收金额；系统没有接入调用事件时，系统计费页必须显示“计费源未接入”。

当前项目已有供应商客户端方法：

- POST /supplier-account/api/openapi/metrics/query
- POST /supplier-account/api/openapi/bill/query

平台同步闭环已接入：上传成功返回的供应商 `id` 会写入 `platform_links`，后台按连接每 2 分钟批量拉取 metrics；独立账单 worker 每 2 小时重查最近 3 日、每天重查最近 7 日，并支持主动历史补采；`usage_metrics` 仅保留兼容查询，不再作为账号真实消耗来源。

供应商账单接口是“指定日期范围汇总”，不是逐日明细。系统因此按单日请求写入 `platform_bill_daily`，严禁把多日汇总平均分摊成日数据。系统计费事件源仍未接入，系统计费接口明确返回“计费源未接入”。

## 分层模型

### 本地账号层

accounts 保存账号资料、套餐、代理、凭证状态和交付配置。balance 是录入元数据，不能当作实时额度或真实消耗。

### 平台观测层

建议新增以下表：

| 表 | 作用 |
| --- | --- |
| platform_links | 本地账号与平台账号关联，保存 upstream id、aid、alias、运行状态 |
| platform_metric_snapshots | 平台实时/窗口指标快照 |
| platform_bill_daily | 平台按业务日返回的离线账单，可被修订 |
| sync_runs / sync_cursors | 同步批次、分片、代次、游标和错误 |

平台 id 与 aid 必须分开保存。指标和账单使用上传成功返回的 id 查询。所有观测数据必须保存 fetched_at、窗口起止、平台时区、数据来源和 freshness_state。

### 系统计费层

建议新增以下表：

| 表 | 作用 |
| --- | --- |
| usage_events | 由 API 网关或受信任内部服务上报的调用事件，带唯一幂等键 |
| pricing_rules | 版本化计费规则，记录生效时间和请求/Token单价 |
| billing_ledger | 按事件和规则版本生成的不可变计费账本 |

系统账单只由 usage_events 生成，不能从平台账单复制。没有规则或事件缺少计费单位时，状态为 pending_pricing，不能默认 0。

## 关键字段设计

### platform_metric_snapshots

account_id、platform_connection_id、upstream_id、bucket_kind、bucket_key、window_start、observed_window_end、requests、prompt_tokens、completion_tokens、success_rate、status_raw、fetched_at、source_timezone、sync_generation、freshness_state。

平台 metrics 的 cost 已停用且恒为 0，不写入金额账本。requests、Token、success_rate 为空时保留为空，不转换成 0。

### platform_bill_daily

account_id、platform_connection_id、upstream_id、bill_date、request_count、prompt_tokens、completion_tokens、cache_read_input_tokens、cache_write_input_tokens、cost_fen、source_range_json、fetched_at、revision、finalized、sync_generation。

平台 cost 使用精确十进制解析后转换为整数分。同一账号同一天重查时覆盖当前记录，只有数值变化才递增 revision，禁止重复累加。当天数据显示“可修订”。

### usage_events / billing_ledger

usage_events 至少包含 idempotency_key、account_id、platform_link_id、consumer_id、source、occurred_at、request_count、input_tokens、output_tokens、billable_units、received_at。

billing_ledger 至少包含 usage_event_id、account_id、consumer_id、pricing_rule_id、period_date、request_amount_fen、input_token_amount_fen、output_token_amount_fen、total_amount_fen、state。相同事件和规则版本只允许一条账本记录。

标准计费公式：

total_amount_fen = 请求次数 × 请求单价 + 输入 Token × 输入单价 + 输出 Token × 输出单价。

## 同步流程

### 实时指标

每 2 分钟按平台连接生成同步批次，每批最多 100 个 upstream id。查询当天业务日窗口，按 connection + account + source + bucket 加锁，使用递增 sync_generation。旧响应不能覆盖新代次。成功后更新平台状态、最后观测时间和 freshness；单账号失败不能影响同批其他账号。

### 离线账单

每 2 小时重查最近 3 个业务日，每天重查最近 7 日以吸收延迟修订。主动补采起始日期不早于连接时区今天减 90 个日历日，包含两端时最多 91 个日期。本地历史查询不受此窗口限制。任务持久化到 sync_runs/bill_sync_items，按连接、日期和最多 100 个账号拆批。部分批次失败不能清空已有数据。

### 系统计费

内部服务上报带 Idempotency-Key 的 usage event，系统校验账号和平台关联，按 occurred_at 选择当时生效的 pricing rule，生成不可变账本。规则调整通过新版本或冲正记录完成，不能直接改历史结算金额。

## 页面设计

### 账号管理

账号列表继续展示本地身份，但增加真实观测列：平台账号 ID、alias、平台状态、最后观测、今日请求、输入 Token、输出 Token、成功率、今日平台账单、账单 revision、系统本账期金额和数据新鲜度。

无数据显示“未同步”“无平台数据”或“同步失败”，不能显示 0 造成误解。账号详情拆成：资料与凭证、平台实时、平台账单、系统计费、同步记录五个页签。

### 平台消耗（独立页面）

平台消耗只保留一个“平台离线账单”Table 页面，提供日期范围、账号/平台账号检索、同步状态和手动同步按钮。系统计费不作为本期页面能力。

离线账单 Table：账单日期、平台账号 ID、请求数、输入 Token、输出 Token、缓存 Token、平台消费（人民币）、revision、采集时间、状态。当天或未完结账单显示可修订。

账号管理页使用高密度暗色卡片展示平台观测摘要：平台账号/别名、平台状态、今日请求、输入/输出 Token、最近账单、观测时间和上号操作。卡片中的空值显示“未同步”，不把缺失数据渲染成 0。

金额列必须叫“平台消费”，不能叫收入、利润或系统应收。

### 系统计费（后续能力，当前不展示）

本期不提供系统计费页面，系统计费数据模型和接口暂不进入前端导航。

对账页单独展示：平台消费金额、系统计费金额、差额、未接入事件、平台延迟、规则不同和数据缺失等差异原因。

## API

平台观测：

- GET /api/v1/platform-usage/metrics
- GET /api/v1/platform-usage/bills
- POST /api/v1/platform-usage/sync
- GET /api/v1/accounts/{id}/platform-usage

系统计费：

- POST /api/v1/usage-events
- GET /api/v1/billing/ledger
- GET /api/v1/accounts/{id}/billing
- GET /api/v1/pricing-rules
- POST /api/v1/pricing-rules
- GET /api/v1/billing/reconciliation

usage-events 只能由受信任内部来源调用，浏览器不能直接伪造计费事件。

## 实施顺序

### P0 平台真实数据闭环

1. 新增 platform_links、platform_metric_snapshots、platform_bill_daily、sync_runs。
2. 上传成功后写入平台账号关联。
3. 实现 metrics/bill worker、分片、游标、代次和失败记录。
4. 新增独立“平台消耗”Table 页面。
5. 账号管理接入最新平台状态和指标快照。

### P1 系统计费闭环

1. 新增 usage_events、pricing_rules、billing_ledger。
2. 实现内部事件接收和幂等入账。
3. 新增系统计费和对账页面。
4. 增加规则版本、账期汇总、导出和冲正机制。

## 账单实现补充（2026-11-05）

账单页面已切换到独立组件，提供平台连接、多账号、日期快捷项、归档、关键词、修订次数和服务端分页筛选。合计覆盖全部匹配行；覆盖统计独立于关键词和修订过滤，明确提示缺失、无返回、失败、过期与历史待复核。完整接口见 [本地 API 契约](api-contract.md#账单实现契约2026-11-05)，实测与恢复见 [账单实施记录](bill-query-implementation-report.md)。

新增 response_verified、实际/请求窗口、业务时区、write_generation 和 last_changed_at；旧账单默认待复核。保留历史金额，不把缺少字段或缺少返回行当作零。上号路径复用同一响应校验和写入代次规则。

## 当前边界

供应商查询方法和平台消耗同步 worker 已接入：上传成功后建立 `platform_links`，后台按连接批量采集 metrics，并逐业务日采集账单。首次采集前、连接失败或数据过期时，页面仍必须显示未同步/同步失败/过期状态，不能显示 0 造成误解。系统调用事件来源尚未接入，因此系统计费页持续显示“计费源未接入”，绝不复制平台账单金额。
