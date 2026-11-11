# 供应商同步健康：网络显式化、失败退避、界面可见（简短设计）

日期：2026-11-10。状态：已确认（用户“按方案做”）。

## 背景与目的

排查发现：指标同步从 2026-11-06 起从未成功。根因在平台侧：OpenAPI 令牌对 `metrics/query`、`bill/query` 一律返回 404“账号不存在或无权访问”，连本令牌刚提交的 411605 也查不到，需供应商处理。本地有三个放大问题的缺陷：供应商请求隐式继承系统代理变量（本机代理失效时全部失败，连上号都会落入“结果待确认”）；失败后每 2 分钟固定重试、刷屏日志；界面只显示英文 `partial`，且混入账单同步记录，看不出原因。

## 目标 / 非目标 / 反目标

- 目标：供应商请求只走显式配置的网络；持续失败时退避；仪表盘清楚显示指标同步是否正常、为什么失败、下次何时重试、该找谁。
- 非目标：修复平台权限（平台侧）；改上号流程；新增“立即同步”按钮。
- 反目标：为了“看起来正常”把失败显示成零消费或隐藏失败。

## 方案取舍

1. **供应商网络**：(选定) 一律不读系统代理变量，默认直连，`CC_MANAGER_SUPPLIER_PROXY` 显式指定代理；(否) 共用代理池前置代理设置——那是为访问境外代理节点设计，供应商是国内地址，改检测设置会误伤上号；(否) 每个平台连接在界面配置网络——需迁移和界面，当前没有需求。
2. **退避状态**：(选定) 从现有 `sync_runs` 推算连续失败次数，不加状态表；(否) 内存计数——重启即丢失，正是今天反复重启的场景。
3. **失败分类存储**：(选定) `sync_runs` 新增 `error_code` 列（迁移 v9），`error_message` 保留脱敏原文供排障；(否) 从文本反解析——脆弱。

## 设计

- `supplier.New`：`transport.Proxy` 由 `CC_MANAGER_SUPPLIER_PROXY` 决定（空=直连，非法 URL 让 `New` 返回错误），不再继承 `HTTP(S)_PROXY`。
- `supplier.QueryMetrics` 非 2xx 返回 `*MetricsHTTPError{Status, Summary}`，实现 `errs.Upstream`：401/403→`PLATFORM_AUTH_FAILED`，404→新码 `PLATFORM_QUERY_DENIED`（“平台拒绝查询这些账号的数据”，提示“请联系供应商核实 OpenAPI 令牌的查询权限”），429/5xx→`PLATFORM_UNAVAILABLE`，其余→`PLATFORM_RESPONSE_INVALID`。网络错误在应用侧归为 `PLATFORM_UNAVAILABLE`。
- 退避：对每个连接，统计 `updated_at` 之后、自最近一次 `succeeded` 起连续非成功的 `platform-usage` 运行数 n；n≥1 时下次尝试时间 = 最近一次开始时间 + min(2·2^(n−1), 60) 分钟；未到时间则跳过本轮（不写运行记录、不打日志）。修改平台连接即重置。
- 仪表盘 `platform` 增加：`sync_status`（`ok`/`failing`/`never`）、`error_code`、`error_message`（注册表文案）、`error_hint`、`consecutive_failures`、`next_attempt_at`；`last_sync_*` 改为只看 `platform-usage`。多连接时显示连续失败最多的那个。
- 前端：`DashboardView` 的“最近同步”行显示中文状态标签（正常/同步异常/尚未同步）；异常时第二行“文案 · 连续失败 N 次 · 下次重试 HH:MM”，第三行提示。

## 留给实现

- 退避常量放 `worker.go` 旁；日志只在实际尝试且失败时写。
- 旧记录 `error_code` 为空时，仪表盘按 `error_message` 含 `HTTP 404` 回退为 `PLATFORM_QUERY_DENIED`，含 `proxyconnect`/`dial tcp` 回退为 `PLATFORM_UNAVAILABLE`，否则 `INTERNAL_ERROR`。
