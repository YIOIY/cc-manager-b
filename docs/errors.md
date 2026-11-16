# 错误码参考

本文件由 `make generate` 生成，请勿手改；来源是 `internal/errs/registry.go`。

| Code | 分类 | HTTP | 可重试 | 动作 | 文案 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| `ACCEPTED_PENDING_VERIFICATION` | upstream | — | 否 | check_platform | 平台已接收，等待平台核验 | 非错误，统一文案 |
| `ACCOUNT_DATES_REQUIRED` | validation | — | 否 | fix_input | 缺少注册日期或套餐日期 | 预检 |
| `ACCOUNT_DELIVERED` | conflict | 409 | 否 | none | 平台已接收该账号 | 删除保护：已上号不可删；结果待确认可删本地；允许调用方自定义文案 |
| `ACCOUNT_EMAIL_INVALID` | validation | — | 否 | fix_input | 账号邮箱格式不正确 | 预检 / 导入 |
| `ACCOUNT_EXISTS` | conflict | 409 | 否 | none | 账号已存在 |  |
| `ACCOUNT_IN_PROGRESS` | conflict | 409 | 否 | wait | 账号正在上号中 | 删除保护；允许调用方自定义文案 |
| `ACCOUNT_LINK_CHANGED` | conflict | — | 否 | none | 账号与平台的关联已变化 | 账单同步项 |
| `ACCOUNT_LINK_MISSING` | validation | 422 | 否 | fix_input | 部分账号未关联该平台或已归档 | 账单同步范围 |
| `ACCOUNT_REQUIRED` | validation | 400 | 否 | fix_input | 缺少账号 | HTTP 层阶段 2 并入 fields；预检保留 |
| `ACCOUNT_WHITESPACE` | validation | 422 | 否 | fix_input | 账号不能包含空格 | 导入预览行问题 |
| `ALREADY_LINKED` | conflict | 409 | 否 | none | 账号已在该平台上号 |  |
| `ARCHIVED` | conflict | 409 | 否 | none | 账号已归档 |  |
| `AUTH_INVALID` | auth | 401 | 否 | fix_input | 账号或密码不正确 | 登录失败；不区分用户不存在与密码错误 |
| `AUTH_JSON_INVALID` | validation | 422 | 否 | fix_input | Claude 凭证格式不正确 | 导入预览行问题 |
| `AUTH_RATE_LIMITED` | rate_limit | 429 | 否 | wait | 登录尝试过于频繁 | 登录节流 |
| `AUTH_REQUIRED` | auth | 401 | 否 | login | 尚未登录或登录已失效 | 无会话、会话被清理或服务重启；USER_DISABLED 情形阶段 2 拆出 |
| `BILL_ACCESS_DENIED` | upstream | 502 | 否 | contact_admin | 平台拒绝访问 | 账单查询 401/403 |
| `BILL_ACCOUNT_AMBIGUOUS` | validation | 422 | 否 | check_platform | 同一交付编号关联了多个本地账号 | 账单查询 |
| `BILL_ACCOUNT_INVALID` | validation | 422 | 否 | fix_input | 交付编号必须为正整数且不能重复 | 账单查询 |
| `BILL_ACCOUNT_LIMIT` | validation | 422 | 否 | fix_input | 请选择 1 至 100 个已上号账号 | 账单查询 |
| `BILL_ACCOUNT_NOT_FOUND` | upstream | 502 | 否 | check_platform | 平台未找到所选交付编号，或当前令牌无权访问 | 账单查询 404 |
| `BILL_ACCOUNT_UNLINKED` | validation | 422 | 否 | fix_input | 部分交付编号未关联到所选平台 | 账单查询 |
| `BILL_COST_INVALID` | upstream | — | 否 | check_platform | 平台返回的费用数值不正确 | 账单同步项；来自 supplier 包 |
| `BILL_ID_MISMATCH` | upstream | — | 否 | check_platform | 平台返回的账号编号与请求不一致 | 账单同步项；来自 supplier 包 |
| `BILL_INTERNAL_ERROR` | internal | 500 | 是 | retry | 账单处理失败 |  |
| `BILL_QUERY_TIMEOUT` | upstream | 504 | 是 | retry | 账单查询已超时或取消 | 账单查询超时 |
| `BILL_RANGE_EXPIRED` | validation | 422 | 否 | fix_input | 开始日期不得早于平台业务日期的 90 天前 | 账单查询窗口 |
| `BILL_RANGE_INVALID` | validation | 422 | 否 | fix_input | 账单日期范围不正确 | HTTP 与同步项共用 |
| `BILL_RANGE_MISMATCH` | upstream | — | 否 | check_platform | 平台返回的账单日期范围与请求不一致 | 账单同步项；来自 supplier 包 |
| `BILL_RATE_LIMITED` | rate_limit | 429 | 否 | wait | 平台查询过于频繁 | 账单查询 429 |
| `BILL_RESPONSE_ERROR` | upstream | 502 | 是 | retry | 平台连接失败或账单响应格式异常 | 账单查询响应异常 |
| `BILL_RESPONSE_INVALID` | upstream | — | 否 | check_platform | 平台返回的账单数据无法识别 | 账单同步项；来自 supplier 包 |
| `BILL_ROW_INVALID` | upstream | — | 否 | check_platform | 平台返回的账单行格式不正确 | 账单同步项；来自 supplier 包 |
| `BILL_UPSTREAM_ERROR` | upstream | 502 | 是 | retry | 平台账单查询失败 | 账单查询 5xx |
| `BILL_WINDOW_INVALID` | validation | 422 | 否 | fix_input | 账单日期不在可查询窗口内 | HTTP 与同步项共用 |
| `CONNECTION_CHANGED` | conflict | — | 否 | retry | 平台连接配置已变化 | 账单同步项：任务执行期间连接被修改 |
| `CONNECTION_DISABLED` | validation | 422 | 否 | fix_input | 平台连接已停用或未配置令牌 |  |
| `CONNECTION_EXISTS` | conflict | 409 | 否 | none | 平台连接已存在 | 唯一约束冲突 |
| `CONNECTION_INVALID` | validation | 422 | 否 | fix_input | 平台连接配置无效 |  |
| `CONNECTION_IN_USE` | conflict | 409 | 否 | none | 平台连接仍有关联账号、任务或历史账单 |  |
| `CONNECTION_UNREADABLE` | config | — | 否 | contact_admin | 平台令牌无法读取 | 异步结果：令牌解密失败（密钥更换或数据损坏） |
| `CREDENTIALS_INCOMPLETE` | validation | — | 否 | fix_input | Claude 凭证字段不完整 | 预检：每个有问题的凭证字段一条，field 为路径，message 为字段文案 |
| `CREDENTIALS_REQUIRED` | validation | 422 | 否 | fix_input | 缺少 Claude 凭证 | 上号前必须有凭证 |
| `CREDENTIAL_UNREADABLE` | config | — | 否 | fix_input | 已保存的凭证无法读取 | 阶段 2 并入 SECRET_UNREADABLE |
| `CSRF_FAILED` | auth | 403 | 否 | retry | 页面会话已过期 | 缺少或不匹配的 X-CSRF-Token |
| `DELIVERY_STATE_UNAVAILABLE` | validation | — | 否 | fix_input | 无法读取交付状态 | 预检 |
| `ENCRYPTION_KEY_REQUIRED` | config | 503 | 否 | contact_admin | 服务尚未配置凭证加密密钥 | HTTP 与任务共用；允许调用方自定义文案 |
| `IDEMPOTENCY_CONFLICT` | conflict | 409 | 否 | retry | 该重复提交保护键已用于其他内容 | 同 key 不同载荷；允许调用方自定义文案 |
| `IMPORT_EXPIRED` | conflict | 409 | 否 | retry | 导入预览已过期 |  |
| `INTERNAL_ERROR` | internal | 500 | 是 | retry | 服务处理请求时出错 | 未预期的服务端错误，原因只记录在日志；message 可说明失败的是哪一步；允许调用方自定义文案 |
| `JOB_PAYLOAD_INVALID` | internal | — | 否 | none | 任务参数无法读取 |  |
| `LAST_ADMIN` | conflict | 422 | 否 | none | 至少保留一个启用的管理员 |  |
| `LOCAL_LINK_SAVE_FAILED` | storage | — | 是 | check_platform | 平台已接收，但本地关联保存失败 |  |
| `METADATA_UNREADABLE` | config | — | 否 | fix_input | 补充信息无法读取 |  |
| `METHOD_NOT_ALLOWED` | validation | 405 | 否 | none | 不支持的请求方式 | 路由存在但方法不匹配 |
| `NETWORK` | internal | — | 是 | retry | 无法连接服务 | 仅前端产生 |
| `NOT_FOUND` | not_found | 404 | 否 | none | 资源不存在或已被删除 | details.resource 指明资源类型；允许调用方自定义文案 |
| `NO_LINKED_ACCOUNTS` | validation | 422 | 否 | none | 所选范围没有可同步的关联账号 | 账单同步范围 |
| `NO_RETRY_ITEMS` | validation | 422 | 否 | none | 没有仍在可查窗口内的失败项 | 账单重试 |
| `OAUTH_BUSY` | rate_limit | 429 | 否 | wait | 授权会话较多 | 待授权链接数达到上限 |
| `OAUTH_EXCHANGE_FAILED` | upstream | 502 | 是 | reauthorize | Claude 授权服务未能完成兑换 | 文案由客户端给出；不含上游响应；允许调用方自定义文案 |
| `OAUTH_PROXY_INVALID` | validation | 422 | 否 | fix_input | 授权使用的代理不可用 | ；允许调用方自定义文案 |
| `OAUTH_RESULT_INVALID` | validation | 422 | 否 | fix_input | 授权结果无法识别 | 本地解析错误，文案由解析器给出；允许调用方自定义文案 |
| `OAUTH_SESSION_EXPIRED` | not_found | 404 | 否 | reauthorize | 授权链接已失效 | 链接过期、被替换、已使用或不属于当前会话 |
| `OAUTH_START_FAILED` | internal | 500 | 是 | retry | 无法生成授权链接 | 随机数生成失败 |
| `OPTIONAL_METADATA_INVALID` | validation | 422 | 否 | fix_input | 补充信息格式不正确 | 余额、标签、支付方式等选填项 |
| `ORIGIN_FAILED` | auth | 403 | 否 | none | 请求来源与当前站点不一致 | Origin 头与 Host 不匹配 |
| `PASSWORD_INVALID` | auth | 403 | 否 | fix_input | 当前密码不正确 | 修改密码时旧密码校验 |
| `PASSWORD_REQUIRED` | validation | 422 | 否 | fix_input | 缺少密码 | HTTP 层阶段 2 并入 fields；预检保留 |
| `PASSWORD_UNREADABLE` | config | — | 否 | fix_input | 已保存的密码无法读取 | 阶段 2 并入 SECRET_UNREADABLE |
| `PAYLOAD_TOO_LARGE` | validation | 413 | 否 | fix_input | 提交的内容超过大小限制 | 请求体超过上限 |
| `PERMISSION_DENIED` | permission | 403 | 否 | contact_admin | 当前账号没有执行此操作的权限 | details.permission 为所需权限；阶段 1 保留自定义文案；允许调用方自定义文案 |
| `PLAN_DATE_RANGE_INVALID` | validation | — | 否 | fix_input | 套餐开始日期不能晚于结束日期 | 预检 |
| `PLAN_END_TIME_INVALID` | validation | — | 否 | fix_input | 套餐结束日期格式不正确 | 预检 |
| `PLAN_INVALID` | validation | 422 | 否 | fix_input | 套餐不在允许范围内 | 导入预览行问题 |
| `PLAN_REQUIRED` | validation | — | 否 | fix_input | 缺少有效套餐 | 预检 |
| `PLAN_START_TIME_INVALID` | validation | — | 否 | fix_input | 套餐开始日期格式不正确 | 预检 |
| `PLATFORM_AUTH_FAILED` | upstream | 502 | 否 | contact_admin | 平台拒绝了本次请求的身份验证 | 上游 401/403；允许调用方自定义文案 |
| `PLATFORM_CONNECTION_REQUIRED` | validation | 422 | 否 | fix_input | 缺少平台连接 | HTTP 层阶段 2 并入 fields；预检保留 |
| `PLATFORM_QUERY_DENIED` | upstream | — | 否 | contact_admin | 平台拒绝查询这些账号的数据 | 指标/账单查询返回 404“账号不存在或无权访问”；本地无法修复 |
| `PLATFORM_REJECTED` | upstream | — | 是 | fix_input | 平台拒绝了本次上号 | details.upstream_status；message 可附平台摘要；允许调用方自定义文案 |
| `PLATFORM_RESPONSE_INVALID` | upstream | 502 | 是 | retry | 平台返回的内容无法识别 | 上游响应解析失败；允许调用方自定义文案 |
| `PLATFORM_UNAVAILABLE` | upstream | 502 | 是 | retry | 平台暂时无法访问 | 上游 5xx/429/网络失败；允许调用方自定义文案 |
| `PREFLIGHT_BLOCKED` | validation | 422 | 否 | fix_input | 资料或凭证未完善 | 预检有 error 项时拒绝提交 |
| `PROXY_CHECK_FAILED` | internal | 500 | 是 | retry | 代理检测记录保存失败 |  |
| `PROXY_EXISTS` | conflict | 409 | 否 | none | 代理已存在 | 唯一约束冲突 |
| `PROXY_INVALID` | validation | 422 | 否 | fix_input | 代理配置不可用 | 代理不存在、已停用或字段无效；HTTP 与任务共用；允许调用方自定义文案 |
| `PROXY_IN_USE` | conflict | 409 | 否 | none | 仍有账号绑定该代理 | ；允许调用方自定义文案 |
| `PROXY_PROBE_FAILED` | upstream | — | 是 | retry | 历史任务的本机代理检测失败 | 旧任务码 |
| `PROXY_REQUIRED` | validation | — | 否 | fix_input | 缺少可用代理 | 预检 |
| `PROXY_SETTINGS_INVALID` | validation | 422 | 否 | fix_input | 代理网络设置不正确 | 代理池前置代理设置；允许调用方自定义文案 |
| `PROXY_TIMEOUT` | upstream | — | 是 | fix_input | 代理检测超时或已取消 | 代理检测阶段码 |
| `QUERY_INVALID` | validation | 422 | 否 | fix_input | 查询参数不正确 | fields 列出每个有问题的查询参数；允许调用方自定义文案 |
| `QUERY_SCOPE_TOO_LARGE` | validation | 422 | 否 | fix_input | 单次最多选择 10000 个账号 | 账单查询范围 |
| `RATE_LIMITED` | rate_limit | 429 | 否 | wait | 操作过于频繁 | details.retry_after_seconds 与 Retry-After 头一致 |
| `REGISTER_DATE_RANGE_INVALID` | validation | — | 否 | fix_input | 注册日期不能晚于套餐开始日期 | 预检 |
| `REGISTER_LOCATION_INVALID` | validation | — | 否 | fix_input | 注册地区需为美国（US） | 预检 |
| `REGISTER_TIME_INVALID` | validation | — | 否 | fix_input | 注册日期格式不正确 | 预检 |
| `REQUEST_MALFORMED` | validation | 400 | 否 | fix_input | 请求内容格式不正确 | 请求体无法解析为对象或含有不支持的字段；允许调用方自定义文案 |
| `RESPONSE_INVALID` | internal | — | 是 | retry | 服务返回的内容无法识别 | 仅前端产生 |
| `RETRY_EXHAUSTED` | upstream | — | 否 | check_platform | 多次重试后仍未成功 | 账单同步项 |
| `ROLE_EXISTS` | conflict | 409 | 否 | none | 角色名称已存在 | 唯一约束冲突 |
| `ROLE_IMMUTABLE` | conflict | 422 | 否 | none | 内置角色不能修改或删除 | ；允许调用方自定义文案 |
| `ROLE_IN_USE` | conflict | 409 | 否 | none | 仍有用户绑定此角色 |  |
| `RUN_ACTIVE` | conflict | 409 | 否 | wait | 已有账单同步任务在进行 | 账单同步并发保护 |
| `SECRET_UNREADABLE` | config | 503 | 否 | contact_admin | 加密保存的信息无法读取或写入 | details.secret 指明是哪类秘密：account_password、account_credentials、connection_token、proxy_password、import；允许调用方自定义文案 |
| `SELF_DELETE` | conflict | 422 | 否 | none | 不能删除自己 |  |
| `SELF_DEMOTION` | conflict | 422 | 否 | none | 不能移除自己的管理员角色 |  |
| `SELF_DISABLE` | conflict | 422 | 否 | none | 不能停用自己 |  |
| `STORAGE_UNAVAILABLE` | storage | 503 | 是 | retry | 数据存储暂时不可用 | 数据库忙、锁定、只读或磁盘已满；message 可说明失败的是哪一步；允许调用方自定义文案 |
| `SUBMIT_IN_PROGRESS` | conflict | 409 | 否 | wait | 当前上号任务仍在执行 |  |
| `SUBMIT_OUTCOME_UNKNOWN` | conflict | 409 | 否 | check_platform | 上次上号结果待确认 | 请求发出后未收到可判定的结果；允许调用方自定义文案 |
| `SYNC_IN_BACKOFF` | conflict | 409 | 否 | wait | 同步正在退避等待中 | details.next_attempt_at 为下次自动尝试时间 |
| `SYNC_SCOPE_TOO_LARGE` | validation | 422 | 否 | fix_input | 同步范围过大 | 账单同步范围 |
| `TARGET_AUTH_FAILED` | upstream | — | 是 | fix_input | 目标代理认证失败 | 代理检测阶段码 |
| `TARGET_CONNECT_DENIED` | upstream | — | 是 | fix_input | 目标代理拒绝连接 | 代理检测阶段码 |
| `TARGET_CONNECT_FAILED` | upstream | — | 是 | fix_input | 无法连接目标代理 | 代理检测阶段码 |
| `TARGET_HTTP_ERROR` | upstream | — | 是 | fix_input | 经目标代理访问检测网站返回错误状态 | 代理检测阶段码 |
| `TARGET_REQUEST_FAILED` | upstream | — | 是 | fix_input | 经目标代理访问检测网站失败 | 代理检测阶段码 |
| `TARGET_RESPONSE_FAILED` | upstream | — | 是 | fix_input | 检测网站响应读取失败 | 代理检测阶段码 |
| `TIMEOUT` | internal | — | 是 | retry | 请求超时 | 仅前端产生 |
| `TIMEZONE_UNUSABLE` | config | — | 否 | contact_admin | 平台连接的时区无法使用 | 账单同步项：连接时区无效 |
| `UNSUPPORTED_JOB` | internal | — | 否 | none | 不支持的任务类型 |  |
| `UPSTREAM_AUTH_FAILED` | upstream | — | 是 | fix_input | 前置代理认证失败 | 代理检测阶段码 |
| `UPSTREAM_CONNECT_DENIED` | upstream | — | 是 | fix_input | 前置代理拒绝连接目标代理 | 代理检测阶段码 |
| `UPSTREAM_CONNECT_FAILED` | upstream | — | 是 | fix_input | 无法连接前置代理 | 代理检测阶段码 |
| `UPSTREAM_HTTP_401` | upstream | — | 否 | contact_admin | 平台拒绝了本次请求的身份验证 | 账单同步项：上游 401 |
| `UPSTREAM_HTTP_403` | upstream | — | 否 | contact_admin | 平台拒绝访问 | 账单同步项：上游 403 |
| `UPSTREAM_ID_INVALID` | validation | 422 | 否 | check_platform | 账号缺少有效的供应商账号编号 | 账单同步 / 查询 |
| `UPSTREAM_TLS_FAILED` | upstream | — | 是 | fix_input | 前置代理安全握手失败 | 代理检测阶段码 |
| `UPSTREAM_TUNNEL_FAILED` | upstream | — | 是 | fix_input | 前置代理无法建立到目标代理的隧道 | 代理检测阶段码 |
| `UPSTREAM_UNAVAILABLE` | upstream | — | 是 | retry | 平台暂时无法访问 | 账单同步项 |
| `USER_DISABLED` | auth | 401 | 否 | login | 当前账号已停用或不存在 | 会话有效但用户行已停用或删除 |
| `USER_EXISTS` | conflict | 409 | 否 | none | 账号已存在 | 唯一约束冲突 |
| `USER_INVALID` | validation | 422 | 否 | fix_input | 目标用户不可用 | 资源分配：目标用户不存在或已停用；允许调用方自定义文案 |
| `VALIDATION_FAILED` | validation | 422 | 否 | fix_input | 提交的资料不符合要求 | 字段级问题在 fields 中逐项列出；允许调用方自定义文案 |
| `VERSION_CONFLICT` | conflict | 409 | 否 | retry | 资料已被其他操作更新 | 乐观锁冲突；HTTP 与任务共用 |
| `WEB_BILL_RANGE_MISMATCH` | upstream | 502 | 是 | retry | 平台返回的账单范围与请求不符 | /bill/query 的 range 与请求不一致，该批次丢弃 |
| `WEB_ENDPOINT_MISSING` | upstream | 502 | 否 | contact_admin | 网页后台接口不存在 | 网页后台读取接口返回 404 |
| `WEB_FORBIDDEN` | upstream | 502 | 否 | contact_admin | 网页后台拒绝读取 | 网页后台读取接口返回 403 |
| `WEB_LOGIN_FAILED` | upstream | 502 | 否 | contact_admin | 网页后台登录失败 | 供应商网页后台 pubkey/login 返回 401 或两次登录后仍 401 |
| `WEB_NOT_CONFIGURED` | validation | 422 | 否 | fix_input | 该平台连接未配置网页登录 | 连接缺少 web_username/web_password |
| `WEB_RATE_LIMITED` | upstream | 502 | 是 | wait | 网页后台限流 | 网页后台返回 429 |
| `WEB_SUSPENDED` | upstream | 502 | 否 | fix_input | 网页通道已暂停：登录连续失败 | 连续 3 次 WEB_LOGIN_FAILED 后暂停；重新保存凭证或测试登录成功即恢复 |
| `WINDOW_EXPIRED` | upstream | — | 否 | none | 日期已超出平台可查窗口 | 账单同步项 |
| `WORKER_RESTART_UNKNOWN` | upstream | — | 否 | check_platform | 服务重启前的上号结果待确认 | 进程中断时正在运行的任务 |

HTTP 为 — 的条目只出现在异步结果（任务、账单同步、代理检测、预检）或前端本地错误中。
