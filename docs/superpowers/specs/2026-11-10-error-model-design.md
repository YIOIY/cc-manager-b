# 错误分类与错误信息统一方案（设计）

日期：2026-11-10。范围：Go 后端全部 HTTP 响应、后台任务 / 交付 / 账单同步 / 代理检测的结果码、`internal/supplier` 与 `internal/claudeoauth` 的错误、前端 `api.ts` 的错误处理与所有错误展示。状态：待确认，尚未实现。

## 1. 现状与问题

盘点结果（`grep` 自 `internal/app`、`internal/supplier`、`web/src`）：

| 事实 | 数据 |
| --- | --- |
| HTTP 错误码散落在字符串字面量里，没有中央定义 | 54 个 code，分布在 `fail` / `apiFail` / `serverError` 共约 190 处调用 |
| 同一个 code 配多种文案 | `VALIDATION_FAILED` 40 处 20 多种文案；`NOT_FOUND` 21 处 8 种；`PERMISSION_DENIED` 5 种；`ENCRYPTION_KEY_REQUIRED` 两种 |
| 同一个含义用不同 code | `ACCOUNT_EXISTS` / `DUPLICATE_ACCOUNT`；`INVALID_JSON` / `MALFORMED_JSON` / `VALIDATION_FAILED("内容不是合法 JSON")` |
| 契约文档承诺但从未实现 | `field_errors`、`retryable`、响应体 `request_id`、`SESSION_EXPIRED`、`FORBIDDEN`、`STORAGE_UNAVAILABLE`、`PLATFORM_AUTH_FAILED`、`PLATFORM_RESPONSE_INVALID`、`UNSUPPORTED_CAPABILITY`、`DELIVERY_EXISTS`、`INVALID_QUERY` |
| 五套互不相通的错误体系 | HTTP 响应 code；任务结果 code（`PLATFORM_REJECTED`、`SUBMIT_OUTCOME_UNKNOWN`、`WORKER_RESTART_UNKNOWN` …）；账单同步 `error_code`（`BILL_*`、`UPSTREAM_UNAVAILABLE`、`RETRY_EXHAUSTED`）；代理检测 `error_code`；预检 check code。`supplier` 包甚至把 code 当 `errors.New` 的 message 用 |
| 前端只有一张手写映射表 | `account-workflow.ts` 的 `issues` 37 条，覆盖不到一半的码；未命中时显示“请核对账号资料与平台配置（CODE）” |
| 前端不按 code 决策 | `errorMessage()` 只透传 message；全项目只有一处按 code 分支（`OAUTH_RESULT_INVALID`）；没有字段级错误定位，表单错误一律整块 alert |
| 500 一律 `DB_ERROR` | `respondError` 兜底、`读取平台指标失败` 等都标成 DB_ERROR，排障会被误导 |
| 排障信息断链 | `X-Request-ID` 只在响应头和日志里，界面从不展示，用户报障时拿不到 |
| 上游信息脱敏不足 | `sanitizeUpstream` 只做截断；`PLATFORM_REJECTED` 的 message 直接是 `err.Error()`，含 HTTP 状态和响应摘要，英文且可能夹带敏感内容 |
| 文案风格不统一 | 有“请…”祈使句、有陈述句、有内部词汇（“JSON 对象”“worker”“DB”）、有英文 |

## 2. 目标 / 非目标 / 反目标

- 目标：所有错误（同步响应、异步结果、检测记录）来自同一张注册表；每个错误有稳定 code、分类、HTTP 状态、是否可重试、建议动作、一句面向用户的中文文案；字段级错误能定位到输入框；界面能给出 request_id 供排障；日志保留完整原因。
- 非目标：不改业务流程和状态机；不改成功响应格式；不引入 i18n（文案只有中文，但结构上为多语言留位）。
- 反目标：为了“完整”堆出上百个 code。code 表达“哪一类问题”，“具体是哪个字段 / 哪个资源”放到结构化字段里。

## 3. 考虑过的方案

1. **只整理文案表**：把 54 个 code 的文案统一、补齐前端映射。改动最小，但五套体系仍然分裂，契约与实现继续两张皮，字段级错误和 request_id 仍然没有。
2. **中央注册表 + 统一信封 + 前端解析层（选定）**：Go 侧一个 `errs` 包定义全部错误，响应信封在现有 `{error:{code,message}}` 上增量扩展（旧字段不变），任务 / 同步 / 检测复用同一张表；前端一个 `errors.ts` 解析信封并驱动展示。兼容现有客户端，可分阶段落地。
3. **全面切到 RFC 9457 `application/problem+json`**：标准化好，但要改所有响应格式、前端信封解析和全部测试，收益主要是“标准”，对这个单前端的内部系统不值。

## 4. 错误模型

### 4.1 注册表条目

Go：`internal/errs`（新包，与 `internal/app` 平级，不依赖任何业务包，这样 `supplier`、`claudeoauth` 也能实现它的接口而不形成循环依赖）。

```go
type Category string // auth | permission | validation | not_found | conflict | rate_limit | upstream | config | storage | internal
type Action   string // none | retry | wait | login | fix_input | contact_admin | check_platform | reauthorize

type Def struct {
    Code      string   // 稳定标识，SCREAMING_SNAKE
    Category  Category
    Status    int      // HTTP 状态；异步结果不用
    Retryable bool
    Action    Action
    Message   string   // 面向用户的默认中文文案，一句话
    Hint      string   // 可选：下一步该做什么
    Doc       string   // 开发者说明，只进文档
}
```

构造方式：`errs.New(def)`、`errs.Newf(def, fields...)`、`.WithMessage(msg)`（仅允许在注册表 `AllowCustomMessage` 的条目上覆盖，如 `NOT_FOUND` 带资源名）、`.WithFields(...)`、`.WithCause(err)`（只进日志）、`.WithDetails(map)`（白名单内的安全结构化信息）。

### 4.2 分类与 HTTP 状态的对应

| category | 含义 | HTTP | 前端默认行为 |
| --- | --- | --- | --- |
| auth | 未登录、会话失效、CSRF / 来源校验 | 401 / 403 | 跳登录；CSRF 失败提示刷新 |
| permission | 已登录但无权限 | 403 | 就地提示，不跳转 |
| validation | 请求格式或字段不满足规则 | 400（语法）/ 422（业务） | 字段级标红 + 顶部汇总 |
| not_found | 资源不存在或不可见 | 404 | 提示并刷新列表 |
| conflict | 版本冲突、重复、状态不允许 | 409 | 提示并建议刷新 / 改用别的操作 |
| rate_limit | 本地限速 | 429 | 显示等待时间（Retry-After） |
| upstream | 供应商平台、Claude 授权、代理网络 | 502 / 504 | 说明是外部问题，给“重试 / 去平台核实” |
| config | 服务端配置缺失（加密密钥、平台 Token） | 503 | 提示联系管理员 |
| storage | 数据库不可用 / 写入失败 | 503 | 提示稍后重试，带 request_id |
| internal | 其他未预期错误 | 500 | 提示带 request_id |

### 4.3 code 命名与收敛规则

- 形式 `主体_问题`，主体用业务名词（ACCOUNT、PROXY、CONNECTION、ROLE、USER、BILL、OAUTH、SESSION），问题用固定动词集合：`REQUIRED`、`INVALID`、`NOT_FOUND`、`EXISTS`、`IN_USE`、`IN_PROGRESS`、`IMMUTABLE`、`EXPIRED`、`REJECTED`、`UNAVAILABLE`、`UNKNOWN_OUTCOME`。
- 通用码保留且只保留一个：`NOT_FOUND`（带 `details.resource`）、`VALIDATION_FAILED`（带 `fields`）、`VERSION_CONFLICT`、`PERMISSION_DENIED`（带 `details.permission`）、`RATE_LIMITED`。
- 合并：`DUPLICATE_ACCOUNT`→`ACCOUNT_EXISTS`；`INVALID_JSON`/`MALFORMED_JSON`→`REQUEST_MALFORMED`（400）；`PAGE_INVALID`/`FILTER_INVALID`→`QUERY_INVALID`（带 `fields`）；`DB_ERROR`→拆成 `STORAGE_UNAVAILABLE`（503，连接 / 锁 / 磁盘）与 `INTERNAL_ERROR`（500，其他）；`ENCRYPTION_FAILED`/`CONNECTION_UNREADABLE`/`CREDENTIAL_UNREADABLE`/`PASSWORD_UNREADABLE`→`SECRET_UNREADABLE`（config，带 `details.secret`）。
- 预检 / 任务里的 `*_REQUIRED` `*_INVALID` 字段类码不再是顶层 code，而是 `VALIDATION_FAILED.fields[].code`，field code 固定为 `REQUIRED`、`INVALID_FORMAT`、`OUT_OF_RANGE`、`UNSUPPORTED_VALUE`、`MISMATCH`，`field` 用 JSON 路径（`auth_json.refresh_token`、`plan_start_time`）。
- 异步结果码单列一组（`Status` 为 0）：`PLATFORM_REJECTED`、`PLATFORM_AUTH_FAILED`、`PLATFORM_RESPONSE_INVALID`、`PLATFORM_UNAVAILABLE`、`SUBMIT_OUTCOME_UNKNOWN`、`LOCAL_LINK_SAVE_FAILED`、`ACCEPTED_PENDING_VERIFICATION`（非错误，但同表登记以统一文案）、`BILL_*` 保留现有 8 个、`PROXY_PROBE_*`。
- 文档里有、实现里没有的码：实现 `PLATFORM_*` 三个；新增 `USER_DISABLED`（现在混在 `AUTH_REQUIRED` 的第二种文案里）；**不做** `SESSION_EXPIRED`，会话存在内存里，“无 cookie”“未知 cookie”“服务重启”无法可靠区分，统一 `AUTH_REQUIRED`；删除 `FORBIDDEN`、`SESSION_EXPIRED`、`UNSUPPORTED_CAPABILITY`、`DELIVERY_EXISTS`（无对应场景）。

目标规模：HTTP 码约 45 个，异步 / 检测结果码约 30 个（含代理探测的 12 个阶段码），field code 5 个。

### 4.4 响应信封

在现有结构上增量扩展，旧字段位置与含义不变：

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "category": "validation",
    "message": "有 2 项资料不符合要求",
    "hint": "请修改标红的字段后重新提交",
    "retryable": false,
    "action": "fix_input",
    "fields": [
      { "field": "plan_end_time", "code": "OUT_OF_RANGE", "message": "套餐结束日期不能早于开始日期" },
      { "field": "auth_json.refresh_token", "code": "REQUIRED", "message": "缺少刷新令牌" }
    ],
    "details": { "resource": "account" }
  },
  "request_id": "8hq88I3c1EYm-1DOxEV8"
}
```

- `request_id` 同时出现在成功与失败的响应体里（契约早已这样写，补齐实现）。
- `details` 只允许注册表声明过的键（`resource`、`permission`、`secret`、`retry_after_seconds`、`upstream_status`、`job_id`），其余一律拒绝写入，防止泄露。
- 429 继续带 `Retry-After` 头，并把秒数复制到 `details.retry_after_seconds`。
- 异步结果不改表结构：`jobs.result_json` 写完整的 `{code, message, retryable, action, details}`；`bill_sync_items.error_code`、`proxy_checks.error_code` 仍只存 code，`message`/`retryable`/`action` 在读取接口里按注册表即时派生（老数据自动获得新文案，无需回填）。`allowed_actions` 是**新增**字段，挂在账号的 `submission_job`、账单同步项和代理检测记录上，由 `action` 加当前状态推导；前端主按钮继续由状态表决定，`allowed_actions` 只控制次要操作（重试 / 去平台核实 / 关联平台 ID）的显示。

### 4.5 上游错误脱敏

`supplier` / `claudeoauth` 返回的错误实现 `errs.Upstream` 接口：`Code()`（映射到 PLATFORM_* / OAUTH_*）、`Status()`（上游 HTTP 状态）、`Summary()`（上游响应里白名单字段：`detail`、`message`、`code`，各截 120 字符，去掉看起来像令牌的片段）。用户看到的是注册表文案 + “平台返回：{Summary}”，原始响应体只进日志并带 request_id / job_id。替换掉现在的 `sanitizeUpstream` 截断。

### 4.6 文案规范

- 一句话，先说发生了什么，再说怎么办；不道歉，不用“抱歉 / 对不起”。
- 用用户的词：账号、代理、平台连接、授权链接；不出现 JSON、DB、worker、token（写“令牌”）、payload。
- `message` 不含变量以外的英文；资源名、字段名用中文标签而不是 JSON 键。
- 同一动作全程同名（“上号”就一直叫上号，不混用“提交”“上传”）。
- 祈使句只放在 `hint` 里（“请重新获取授权链接”），`message` 用陈述句（“授权链接已失效”）。
- 空状态 / 失败态必须给出下一步，`action` 为 `none` 的错误要说明为什么无法操作。

### 4.7 前端

新建 `web/src/errors.ts`，`api.ts` 只负责抛 `ApiError`：

```ts
class ApiError { status; code; category; message; hint?; retryable; action; fields: FieldError[]; details; requestId }
describeError(err): { title: string; detail?: string; requestId?: string; action: Action; fields: FieldError[] }
applyFieldErrors(fields, formMap): Record<formKey, string>   // JSON 路径 → 表单键
```

- 全局处理放在 `api.ts` 的一处：`auth` 跳登录（已有 `onUnauthorized`）、`rate_limit` 统一 toast 带等待秒数、网络 / 超时 / 解析失败生成本地 `NETWORK` / `TIMEOUT` / `RESPONSE_INVALID`（同样登记在前端常量表）。
- 表单（账号、代理、平台连接、用户、角色、导入）接 `applyFieldErrors`，字段下方显示 message，顶部 alert 只显示汇总 `message` + `hint`。
- 新组件 `ErrorNotice.vue`：标题 + 说明 + 可选“复制错误信息”（含 code、request_id、时间），替换现在 20 处手写的 `el-alert type="error"`。
- `account-workflow.ts` 的 `issues` 表删除，改为直接用后端返回的 message / hint；只保留对历史数据（旧 `result_json` 只有 code）的兜底映射。
- 前端 code / category / action 常量由 Go 注册表 `go generate` 生成到 `web/src/generated/error-codes.ts`，杜绝手抄。

### 4.8 保证不回退的机制

- `errs` 包单测：code 唯一、命名符合规则、message 非空且不含禁用词（JSON、DB、worker、token、抱歉）、每个 category 的 Status 在允许集合内。
- AST 扫描测试：`internal/app` 里不允许再出现 `fail(w, status, "字面量"...)`，所有响应必须经 `errs`；`fail`/`apiFail`/`serverError` 收缩为 `errs` 的内部实现。
- `go generate` 产物纳入 CI 比对（生成后 `git diff --exit-code`）。
- `docs/errors.md` 由注册表生成：code、分类、状态、可重试、动作、文案、出现场景；`docs/api-contract.md` 1.2 节改为引用它。

### 4.9 审阅后补充的决定

独立实现者审阅提出 8 个问题，以下 7 个由本设计直接决定：

1. **成功响应不改形状**：预检 `data.checks[]`、导入预览 `rows[].issues[]`、账号列表里的 `submission_job` 保持现有结构，只是每个 code 旁**新增** `message`（和需要时的 `action`）由注册表生成。因此前端 `issues` 映射表可以下线，只保留对历史 `result_json` 的兜底。`fields[]` 只出现在 4xx 错误信封里。
2. **多文案子系统的归属**：Claude 授权的本地解析错误（state 不匹配、格式不对等约 12 条）归一个 `OAUTH_RESULT_INVALID`，在注册表标记 `AllowCustomMessage`，文案仍由解析器给；兑换阶段的上游失败走 `OAUTH_EXCHANGE_FAILED` + `Upstream` 摘要。代理探测的 12 个阶段码（隧道失败、目标拒绝、超时…）各自登记为独立结果码，变量（HTTP 状态、阶段）放 `details`，文案用模板。账单查询的约 20 个请求校验码（时区缺失、范围过大…）并入 `VALIDATION_FAILED.fields` 或 `QUERY_INVALID.fields`。
3. **4.2 的“前端默认行为”列只是文案和建议动作**，不是自动行为。`describeError` 是纯函数；全局自动处理只有三件事：`AUTH_REQUIRED`/`CSRF_FAILED` 跳登录或提示刷新（按 code 判断，`AUTH_INVALID` 登录失败不跳转）、`RATE_LIMITED` 统一提示等待秒数、网络 / 超时本地错误。列表刷新之类由各页面自己决定。
4. **字段路径到中文标签的映射只有一份**，放在 Go 注册表（`FieldLabels`），后端用它生成字段 message，`go generate` 把同一张表输出到前端用于定位表单项。
5. **`Upstream` 接口放在 `internal/errs`**，`supplier`、`claudeoauth` 直接实现；`Code()` 返回的是 `errs` 包导出的常量而不是手写字符串。
6. **不实现 `SESSION_EXPIRED`**，新增 `USER_DISABLED`（见 4.3）。
7. **异步结果派生而非回填**（见 4.4）。

第 8 个问题（校验是收集全部字段错误还是遇错即停）影响用户体验，留给产品方决定，见文末“待拍板”。

## 5. 迁移步骤（兼容，每步可独立发布）

1. **建表不换血**：新增 `errs` 包与 35 + 15 + 5 的注册表；`fail`/`apiFail`/`respondError` 改为内部调用 `errs`，响应自动补 `category`/`retryable`/`action`/`request_id`，code 与 message 暂不变。前端 `ApiError` 读取新字段但不改行为。
2. **收敛调用点**：按 4.3 的合并表替换 190 处调用；预检与 onboard 校验改为输出 `fields`；worker / bill_sync / proxy probe 的结果码改走注册表；`supplier`、`claudeoauth` 实现 `Upstream` 接口。更新对应 Go 测试的期望 code。
3. **前端接入**：`errors.ts`、`ErrorNotice.vue`、六个表单的字段级错误、`issues` 表下线、生成 TS 常量；Playwright 补“字段级报错定位”和“request_id 可复制”两条用例。
4. **文档与护栏**：生成 `docs/errors.md`，改写契约 1.2，加 AST 扫描测试和生成产物比对。

每步都跑 `go test -race ./...`、`npm test`、`npm run build`、Playwright。

## 6. 留给实现

- 注册表的最终清单（在步骤 1 里以表格形式提交审阅后再写代码）。
- `details` 白名单键名的最终确定。
- `ErrorNotice.vue` 的视觉：沿用现有 token，琥珀 = 可修复，红 = 需介入，灰 = 外部 / 等待。
- 历史 `result_json` 只含旧 code 的任务如何展示：建议兜底表保留 6 个月后删除。

## 7. 已拍板

**校验失败时一次报出全部字段问题**（2026-11-10 确认）。六个表单（账号 / 上号、代理、平台连接、用户、角色、导入）的后端校验改为收集 `[]FieldError` 后一次返回；预检与导入预览的 200 载荷也按同样方式列出全部问题。`fields[]` 为空时 `VALIDATION_FAILED` 的 `message` 为通用汇总，非空时为“有 N 项资料不符合要求”。
