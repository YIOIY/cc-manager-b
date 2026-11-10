# 供应商平台对接说明

> 本文记录已读取的平台文档事实。未执行真实上传或查询。与之不同，本项目的 `/api/v1` 接口见 [本地 API 契约](api-contract.md)。

## 1. 依据与边界

核对日期：2026-11-04。

- [供应商页面与 OpenAPI 文档入口](https://120.233.254.152:8000/#/supplier)：点击“API接口文档”。
- [供应商常见问题自查指引](https://120.233.254.152:8000/#/faq)。

基地址为 `https://120.233.254.152:8000`。`/#/supplier` 是前端路由，不是 API 路径的一部分。未来实现应由管理员配置平台连接，不在业务代码中写死地址。

本平台是账号交付和管理入口。本项目同样只实现上号管理，不承担模型推理流量转发。

## 2. 鉴权

平台 OpenAPI 文档要求：

```http
Authorization: Bearer <管理员签发的 OpenAPI Token>
Content-Type: application/json
```

三种凭证必须区分：

| 类型 | 用途 | 本项目处理方式 |
| --- | --- | --- |
| 平台网页登录会话 | 人工登录供应商网页 | 不抓取浏览器 Cookie，不作为后端 OpenAPI 凭证 |
| 平台 OpenAPI Token | 上传账号及查询数据 | 管理员在本地后台配置，后端加密保存 |
| Claude OAuth 凭证 | 被交付账号的授权 | 按账号加密保存，组装到 `auth_json` |

令牌由平台管理员签发；网页已登录不等于本地上号器拥有可用的 OpenAPI Token。

## 3. Claude 账号上传

```http
POST /supplier-account/api/openapi/claude/accounts
```

此接口专用于 Claude，无需顶层 `provider`、`model`、`account_type` 或通用代理参数；**`auth_json.proxy_url` 仍然必填**。

### 3.1 请求字段

| 字段 | 类型 | 必填 | 文档约束 |
| --- | --- | --- | --- |
| `plan` | string | 是 | 已授权套餐；当前列出 `claude/cc-max-20x`、`claude/cc-max-5x`、`claude/cc-pro` |
| `account` | string | 是 | 登录 ID / 邮箱，不含空格；同一账号不可重复上号 |
| `password` | string | 是 | 账号密码 |
| `auth_json` | object 或 JSON string | 是 | 本项目统一使用 object 传输，见下表 |
| `register_location` | string | 是 | 当前可选 `US` |
| `register_time` | string | 是 | `YYYY-MM-DD` |
| `plan_start_time` | string | 是 | `YYYY-MM-DD` |
| `plan_end_time` | string | 是 | `YYYY-MM-DD` |
| `payment_method` | string | 否 | 实体/虚拟信用卡、PayPal、ApplePay、GooglePay、第三方代付 |
| `register_device` | string | 否 | `Mac`、`Windows`、`Linux` |
| `balance` | number 或 string | 否 | 剩余套餐百分比 0–100，不带 `%` |
| `recharge_time` | string | 否 | `YYYY-MM-DD` |
| `warranty_period` | number 或 string | 否 | 文档可选 `1`、`7`、`30` 天 |
| `tags` | string[] | 否 | 文档可选“速刷”“慢蒸”，两者互斥；仅上号时可设置，上号后不支持修改 |
| `remark` | string | 否 | 备注 |

支付方式文档列出 `physical_credit_card`、`virtual_credit_card`、`paypal`、`apple_pay`、`google_pay`；`third_party_payment` 的说明文本带有多余引号，实际值需联调确认。确认前不预选该值，允许不填该可选项。

质保期的表单显示和 API 文档措辞存在差异，第一版按 API 的数值保存，同时保留平台文档标签；真实业务含义在联调前确认。

### 3.2 `auth_json` 完整字段

| 字段 | 类型/要求 | 本地处理 |
| --- | --- | --- |
| `id_token` | 必须有字段，允许空字符串 | 不虚构内容 |
| `access_token` | 非空 | 秘密，不回显原值 |
| `refresh_token` | 非空 | 秘密，不回显原值 |
| `last_refresh` | 非空 RFC3339 时间 | 保存时刻与时区偏移；不以本地保存时间冒充刷新时间 |
| `email` | 非空 | 与账号身份核对 |
| `account_uuid` | 非空 | 来自实际授权信息 |
| `organization_uuid` | 非空 | 来自实际组织信息 |
| `organization_name` | 非空 | 不推测或生成占位名称 |
| `claude_device_ids` | 数组，必填且非空 | 具体元素格式和生成规则未在文档解释 |
| `type` | 非空 | 允许值未公开确认，不假定为某个 OAuth 类型 |
| `expired` | 非空 RFC3339 时间 | 作为实际凭证过期时间，不与套餐结束日混用 |
| `proxy_url` | 非空 URL | `http://`、`https://`、`socks5://`、`socks5h://` |
| `verification_status` | 非空 | 允许值和来源未公开确认，不能默认填“已验证” |

平台 Claude 表单还明确说明：整段凭据原样下发下游，额外字段会保留。因此本地应保留未知字段，不能使用仅包含固定 13 字段的序列化方式覆盖原 JSON。

后端必须验证字段存在、类型与格式，但这些验证不能证明平台一定接受凭证，也不能证明账号当前可调用。

### 3.3 成功与失败

成功响应文档示例：

```json
{
  "id": 12345,
  "alias": "claude-a1b2c3",
  "msg": "提交成功"
}
```

- `id` 是本系统后续查询使用的平台交付记录 ID，必须持久保存。
- 同一账号重复、缺字段、格式错误：文档说明返回 HTTP 400。
- 未获授权 Claude 厂商：文档说明返回 HTTP 403；套餐自身是否获授权也需要实际确认。
- 常见错误结构为 `{ "detail": "错误描述" }`；仍需兼容代理层返回非 JSON 错误。
- HTTP 成功只代表接收，账号随后进入同步、验证流程。
- 自查指引称通常约 5 分钟内验证；这不是本地可以保证的硬性 SLA。
- 上传超时并不代表未创建，禁止把上传 POST 当作可任意重试的幂等操作。

## 4. 实时累计数据

```http
POST /supplier-account/api/openapi/metrics/query
```

| 字段 | 类型 | 约束 |
| --- | --- | --- |
| `account_ids` | number[] | 单次最多 100 个，使用上传响应的 `id` |
| `begin_time` | string | `YYYY-MM-DD HH:mm:ss`，平台文档称“本地时区” |
| `end_time` | string | 同上，不晚于当前时间；时间跨度最多 7 天 |
| `aggregate` | bool | 默认 `true`；本项目逐账号查询必须显式传 `false` |

本地连接配置必须存放 `platform_timezone`。初始建议 `Asia/Shanghai`，但需与平台确认，不能把客户端或服务器系统时区自动当作平台时区。

响应文档示例：

```json
{
  "accounts": [
    {
      "id": 12345,
      "aid": 98765,
      "account_alias": "claude-a1b2c3",
      "status": "online",
      "requests": 1234,
      "cost": 0,
      "success_rate": 99.8,
      "prompt_tokens": 100000,
      "completion_tokens": 50000
    }
  ]
}
```

关键语义：

- `id` 与 `aid` 不同：前者是交付系统 ID，后者是平台账号 ID。不能互换。
- `aggregate=true` 时只返回汇总项，`id=0`、`aid=0`，账号别名与状态为空。
- 字段无数据时可能为 `null`；不能直接转换为 0 或“正常”。
- **平台明确表示 `cost` 字段已停用且恒为 0，本地不将它写入金额账本。**
- 成功率为查询窗口内的平均值；不能把多批、多个窗口的平均值再简单平均。
- 文档没有承诺成功率的请求权重算法；跨批汇总只能标记“估算”或不展示，不冒充平台精确值。
- 页面标注实时监控约延迟 2 分钟，必须显示观察时间与数据新鲜度。
- 没有证据表明请求 Token 可以换算成 Claude 订阅剩余额度百分比。

状态值按原文保留：

| 值 | 本地显示建议 |
| --- | --- |
| `online` | 在线 |
| `store` | 平台状态：store（业务含义待确认） |
| `offline` | 离线 |
| `banned` | 封禁 |
| `wait_check` | 待验证 |
| `check_fail` | 验证失败 |
| `arrearage` | 欠费 |
| `fault` | 故障 |
| `suspended` | 已暂停 |
| 未知新值 / null | 未知，并保留原值供诊断 |

`online` 说明观察到的当时状态，不构成永久可用保证。查询响应没有失败原因字段，不能假设一定能自动拉取平台验证失败的详细原因。

## 5. 离线账单

```http
POST /supplier-account/api/openapi/bill/query
```

| 字段 | 类型 | 约束 |
| --- | --- | --- |
| `account_ids` | number[] | 最多 100 个，使用上传响应的 `id` |
| `start_ds` | string | `YYYYMMDD` 或 `YYYY-MM-DD`，不得早于 90 天前 |
| `end_ds` | string | 同上，不早于开始日 |

响应文档示例：

```json
{
  "list": [
    {
      "id": 12345,
      "request_count": 1234,
      "prompt_tokens": 100000,
      "completion_tokens": 50000,
      "cache_read_input_tokens": 3000,
      "cache_write_input_tokens": 1000,
      "cost": 12.34
    }
  ],
  "range": { "start_ds": 20260901, "end_ds": 20260910 }
}
```

- `cost` 单位为人民币元、保留两位小数。本地使用精确十进制解析并转换为整数分。
- 页面标注约延迟 2 小时，不能把当天尚未到齐的账单显示为最终结果。
- 此接口返回指定日期范围的**汇总**；本地如需日趋势，必须逐日查询并存储，不能把一个区间总额分摊成伪造日账单。
- 区间重查结果可以修订；同一账号同一天的结果采用版本化覆盖，不累加重复采样。
- “消费金额”不等于供应商收益、结算金额或剩余订阅百分比，本文不设计未经确认的收益计算。
- 超过上游可查窗口的本地已保存日数据可以继续展示，但需要标记其来源和最后同步时间。

### 本地实现口径（2026-11-05）

本地按单日、最多 100 个账号请求，并校验实际 range 与请求完全一致。兼容八位数字、八位字符串、带横线字符串日期。计数必须显式存在且为非负整数，cost 使用精确分值；单行异常不影响同批合法账号。返回缺少某账号时记为 no_data，已有历史值保留；未确认其代表零用量。

连接时区采用明确的 IANA 名称，以业务日期执行今天减 90 日的边界；禁止未来同步日期是本地规则。历史查询不受上游窗口限制。已通过本地 HTTPS 模拟供应商验收，尚未执行真实供应商账单调用；不以模拟结果声称实际平台边界和限流已获验证。`metrics/query-keys` 按用户要求继续不接入。

## 6. 代理与凭证生命周期

已确认：Claude 凭证中要求 `proxy_url`；平台将原 JSON 下发下游。

2026-11-09 本地新增浏览器 OAuth 授权录入，细节见 [授权录入说明](claude-oauth-onboarding.md)。它减少令牌和身份字段的手动填写，不改变本页供应商必填契约；密码、套餐有效期、设备标识仍须真实资料。选中代理池记录时，后端先解析其地址，再校验完整凭证，不要求浏览器重复提交代理密码。

尚未确认：

1. 平台下游是否始终使用该代理、何时读取代理、是否缓存代理配置。
2. 上号后谁刷新 OAuth Token，刷新后的凭证是否能回传供应商。
3. 更新凭证、换代理、暂停、删除账号的公开 API。
4. 同一个账号是否会映射多个 `aid`，以及 OpenAPI 的状态聚合方式。FAQ 提到可能出现两个 ID，不能据此自行推导映射规则。

因此第一版只能保证“正确组装并提交代理信息”，不能承诺“换本地代理即改变远端请求出口”。已提交账号本地换代理后必须标记待同步，并指引在平台完成受支持的更新流程。

## 7. 当前不应假定存在的 API

- OpenAPI 账号列表、按邮箱查找账号。
- 更新既有账号的凭证、代理、套餐、标签。
- 远端删除、下号、停用、重启账号。
- 获取平台签发 OpenAPI Token 的接口。
- Claude 订阅官方 5h/7d 剩余额度接口。
- `type` / `verification_status` / `claude_device_ids` 的生成接口。

平台还列出按 API Key 查询指标的接口，但本项目首版以账号 ID 对接，暂不纳入实现范围。

## 8. 联调前必须确认

| 编号 | 问题 | 影响范围 | 未确认时的处理 |
| --- | --- | --- | --- |
| Q1 | 一份可成功上传的 Claude 凭证样本及字段规范 | 自动授权、凭证校验 | 仅支持真实完整凭证的导入/填写，不生成占位值 |
| Q2 | Token 刷新责任与上传后更新路径 | 长期维护 | 自动刷新能力关闭；过期风险预警，不宣称远端已更新 |
| Q3 | 远端代理变更和读取策略 | 固定 IP、故障切换 | 本地变更标记待同步，禁止自动声称切换完成 |
| Q4 | OpenAPI 时区、限流、成功率算法 | 数据窗口和调度 | 时区显式配置；保守并发；不伪造聚合成功率 |
| Q5 | 已提交但响应超时时如何确认记录 | 安全重试 | 标记结果未知，人工在平台核对后关联已有 ID |
| Q6 | 实际套餐授权、可选项枚举及当前资源接收规则 | 首次交付 | 使用预检与少量获准账号联调，不把页面选项当成接收承诺 |

以上确认不阻塞本地表单、代理池、Mock 对接和任务框架开发，但阻塞对应的真实自动化能力验收。
