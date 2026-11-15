package errs

var registry = map[string]Def{}

func register(defs ...Def) {
	for _, d := range defs {
		if _, dup := registry[d.Code]; dup {
			panic("errs: duplicate code " + d.Code)
		}
		registry[d.Code] = d
	}
}

// Registered codes. Phase 1 keeps every code the application already uses;
// phase 2 merges the ones whose Doc says so.
func init() {
	register(
		Def{Code: "AUTH_REQUIRED", Category: CategoryAuth, Status: 401, Action: ActionLogin, Message: "尚未登录或登录已失效", Hint: "请重新登录", Doc: "无会话、会话被清理或服务重启；USER_DISABLED 情形阶段 2 拆出"},
		Def{Code: "AUTH_INVALID", Category: CategoryAuth, Status: 401, Action: ActionFixInput, Message: "账号或密码不正确", Doc: "登录失败；不区分用户不存在与密码错误"},
		Def{Code: "AUTH_RATE_LIMITED", Category: CategoryRateLimit, Status: 429, Action: ActionWait, Message: "登录尝试过于频繁", Hint: "请稍后再试", Doc: "登录节流"},
		Def{Code: "CSRF_FAILED", Category: CategoryAuth, Status: 403, Action: ActionRetry, Message: "页面会话已过期", Hint: "请刷新页面后重试", Doc: "缺少或不匹配的 X-CSRF-Token"},
		Def{Code: "ORIGIN_FAILED", Category: CategoryAuth, Status: 403, Action: ActionNone, Message: "请求来源与当前站点不一致", Doc: "Origin 头与 Host 不匹配"},
		Def{Code: "PERMISSION_DENIED", Category: CategoryPermission, Status: 403, Action: ActionContactAdmin, Message: "当前账号没有执行此操作的权限", Hint: "如需此权限请联系管理员", Doc: "details.permission 为所需权限；阶段 1 保留自定义文案", AllowCustomMessage: true},
		Def{Code: "PAYLOAD_TOO_LARGE", Category: CategoryValidation, Status: 413, Action: ActionFixInput, Message: "提交的内容超过大小限制", Hint: "请减少单次提交的数据量", Doc: "请求体超过上限"},
		Def{Code: "METHOD_NOT_ALLOWED", Category: CategoryValidation, Status: 405, Action: ActionNone, Message: "不支持的请求方式", Doc: "路由存在但方法不匹配"},
		Def{Code: "QUERY_SCOPE_TOO_LARGE", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "单次最多选择 10000 个账号", Hint: "请缩小选择范围", Doc: "账单查询范围"},
		Def{Code: "VALIDATION_FAILED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "提交的资料不符合要求", Hint: "请修改标出的字段后重新提交", Doc: "字段级问题在 fields 中逐项列出", AllowCustomMessage: true},
		Def{Code: "PASSWORD_REQUIRED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "缺少密码", Doc: "HTTP 层阶段 2 并入 fields；预检保留"},
		Def{Code: "PASSWORD_INVALID", Category: CategoryAuth, Status: 403, Action: ActionFixInput, Message: "当前密码不正确", Doc: "修改密码时旧密码校验"},
		Def{Code: "ACCOUNT_REQUIRED", Category: CategoryValidation, Status: 400, Action: ActionFixInput, Message: "缺少账号", Doc: "HTTP 层阶段 2 并入 fields；预检保留"},
		Def{Code: "ACCOUNT_WHITESPACE", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "账号不能包含空格", Doc: "导入预览行问题"},
		Def{Code: "ACCOUNT_EMAIL_INVALID", Category: CategoryValidation, Action: ActionFixInput, Message: "账号邮箱格式不正确", Doc: "预检 / 导入"},
		Def{Code: "PLAN_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "套餐不在允许范围内", Doc: "导入预览行问题"},
		Def{Code: "PLAN_REQUIRED", Category: CategoryValidation, Action: ActionFixInput, Message: "缺少有效套餐", Doc: "预检"},
		Def{Code: "AUTH_JSON_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "Claude 凭证格式不正确", Doc: "导入预览行问题"},
		Def{Code: "CREDENTIALS_INCOMPLETE", Category: CategoryValidation, Action: ActionFixInput, Message: "Claude 凭证字段不完整", Hint: "请补充或修正标出的凭证字段", Doc: "预检：每个有问题的凭证字段一条，field 为路径，message 为字段文案"},
		Def{Code: "CREDENTIALS_REQUIRED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "缺少 Claude 凭证", Hint: "请先完成授权或粘贴已有凭证", Doc: "上号前必须有凭证"},
		Def{Code: "PLATFORM_CONNECTION_REQUIRED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "缺少平台连接", Hint: "请选择要提交的供应商平台", Doc: "HTTP 层阶段 2 并入 fields；预检保留"},
		Def{Code: "PREFLIGHT_BLOCKED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "资料或凭证未完善", Hint: "请按预检提示补充后再提交", Doc: "预检有 error 项时拒绝提交"},
		Def{Code: "PROXY_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "代理配置不可用", Hint: "请检查代理协议、主机、端口与认证信息", Doc: "代理不存在、已停用或字段无效；HTTP 与任务共用", AllowCustomMessage: true},
		Def{Code: "PROXY_SETTINGS_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "代理网络设置不正确", Doc: "代理池前置代理设置", AllowCustomMessage: true},
		Def{Code: "OPTIONAL_METADATA_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "补充信息格式不正确", Doc: "余额、标签、支付方式等选填项"},
		Def{Code: "OAUTH_RESULT_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "授权结果无法识别", Hint: "请粘贴 Claude 授权页给出的授权码或完整回调地址", Doc: "本地解析错误，文案由解析器给出", AllowCustomMessage: true},
		Def{Code: "OAUTH_PROXY_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "授权使用的代理不可用", Hint: "请更换代理后重新生成授权链接", Doc: "—", AllowCustomMessage: true},
		Def{Code: "CONNECTION_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "平台连接配置无效", Hint: "请检查平台地址和令牌", Doc: "—"},
		Def{Code: "CONNECTION_DISABLED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "平台连接已停用或未配置令牌", Hint: "请在平台设置中启用并填写令牌", Doc: "—"},
		Def{Code: "ACCOUNT_LINK_MISSING", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "部分账号未关联该平台或已归档", Hint: "请检查选择范围", Doc: "账单同步范围"},
		Def{Code: "NO_LINKED_ACCOUNTS", Category: CategoryValidation, Status: 422, Action: ActionNone, Message: "所选范围没有可同步的关联账号", Doc: "账单同步范围"},
		Def{Code: "NO_RETRY_ITEMS", Category: CategoryValidation, Status: 422, Action: ActionNone, Message: "没有仍在可查窗口内的失败项", Doc: "账单重试"},
		Def{Code: "SYNC_SCOPE_TOO_LARGE", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "同步范围过大", Hint: "请缩小账号或日期范围", Doc: "账单同步范围"},
		Def{Code: "UPSTREAM_ID_INVALID", Category: CategoryValidation, Status: 422, Action: ActionCheckPlatform, Message: "账号缺少有效的供应商账号编号", Hint: "请先在平台核实该账号", Doc: "账单同步 / 查询"},
		Def{Code: "BILL_ACCOUNT_AMBIGUOUS", Category: CategoryValidation, Status: 422, Action: ActionCheckPlatform, Message: "同一交付编号关联了多个本地账号", Hint: "请先核实账号关联", Doc: "账单查询"},
		Def{Code: "BILL_ACCOUNT_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "交付编号必须为正整数且不能重复", Doc: "账单查询"},
		Def{Code: "BILL_ACCOUNT_LIMIT", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "请选择 1 至 100 个已上号账号", Doc: "账单查询"},
		Def{Code: "BILL_ACCOUNT_UNLINKED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "部分交付编号未关联到所选平台", Hint: "请选择该平台已上号的账号", Doc: "账单查询"},
		Def{Code: "BILL_RANGE_EXPIRED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "开始日期不得早于平台业务日期的 90 天前", Doc: "账单查询窗口"},
		Def{Code: "BILL_RANGE_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "账单日期范围不正确", Doc: "HTTP 与同步项共用"},
		Def{Code: "BILL_WINDOW_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "账单日期不在可查询窗口内", Doc: "HTTP 与同步项共用"},
		Def{Code: "NOT_FOUND", Category: CategoryNotFound, Status: 404, Action: ActionNone, Message: "资源不存在或已被删除", Doc: "details.resource 指明资源类型", AllowCustomMessage: true},
		Def{Code: "OAUTH_SESSION_EXPIRED", Category: CategoryNotFound, Status: 404, Action: ActionReauthorize, Message: "授权链接已失效", Hint: "请重新获取授权链接", Doc: "链接过期、被替换、已使用或不属于当前会话"},
		Def{Code: "IMPORT_EXPIRED", Category: CategoryConflict, Status: 409, Action: ActionRetry, Message: "导入预览已过期", Hint: "请重新上传文件生成预览", Doc: "—"},
		Def{Code: "VERSION_CONFLICT", Category: CategoryConflict, Status: 409, Action: ActionRetry, Message: "资料已被其他操作更新", Hint: "请刷新后重新提交", Doc: "乐观锁冲突；HTTP 与任务共用"},
		Def{Code: "ACCOUNT_EXISTS", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "账号已存在", Hint: "同一账号不能重复创建", Doc: "—"},
		Def{Code: "IDEMPOTENCY_CONFLICT", Category: CategoryConflict, Status: 409, Action: ActionRetry, Message: "该重复提交保护键已用于其他内容", Hint: "请刷新页面后重新提交", Doc: "同 key 不同载荷", AllowCustomMessage: true},
		Def{Code: "ARCHIVED", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "账号已归档", Hint: "请先恢复账号", Doc: "—"},
		Def{Code: "ACCOUNT_DELIVERED", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "平台已接收或可能已接收该账号", Hint: "如需下架请改用归档", Doc: "删除保护", AllowCustomMessage: true},
		Def{Code: "ACCOUNT_IN_PROGRESS", Category: CategoryConflict, Status: 409, Action: ActionWait, Message: "账号正在上号中", Hint: "请等待结果后再操作", Doc: "删除保护", AllowCustomMessage: true},
		Def{Code: "PROXY_IN_USE", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "仍有账号绑定该代理", Hint: "请先在账号中解绑或归档账号", Doc: "—", AllowCustomMessage: true},
		Def{Code: "CONNECTION_IN_USE", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "平台连接仍有关联账号、任务或历史账单", Hint: "请保留连接并停用", Doc: "—"},
		Def{Code: "ROLE_IN_USE", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "仍有用户绑定此角色", Hint: "请先解除绑定", Doc: "—"},
		Def{Code: "ROLE_IMMUTABLE", Category: CategoryConflict, Status: 422, Action: ActionNone, Message: "内置角色不能修改或删除", Doc: "—", AllowCustomMessage: true},
		Def{Code: "LAST_ADMIN", Category: CategoryConflict, Status: 422, Action: ActionNone, Message: "至少保留一个启用的管理员", Doc: "—"},
		Def{Code: "SELF_DELETE", Category: CategoryConflict, Status: 422, Action: ActionNone, Message: "不能删除自己", Doc: "—"},
		Def{Code: "SELF_DEMOTION", Category: CategoryConflict, Status: 422, Action: ActionNone, Message: "不能移除自己的管理员角色", Doc: "—"},
		Def{Code: "SELF_DISABLE", Category: CategoryConflict, Status: 422, Action: ActionNone, Message: "不能停用自己", Doc: "—"},
		Def{Code: "RATE_LIMITED", Category: CategoryRateLimit, Status: 429, Action: ActionWait, Message: "操作过于频繁", Hint: "请稍后再试", Doc: "details.retry_after_seconds 与 Retry-After 头一致"},
		Def{Code: "OAUTH_BUSY", Category: CategoryRateLimit, Status: 429, Action: ActionWait, Message: "授权会话较多", Hint: "请稍后重试", Doc: "待授权链接数达到上限"},
		Def{Code: "OAUTH_EXCHANGE_FAILED", Category: CategoryUpstream, Status: 502, Retryable: true, Action: ActionReauthorize, Message: "Claude 授权服务未能完成兑换", Hint: "请重新生成授权链接后再试", Doc: "文案由客户端给出；不含上游响应", AllowCustomMessage: true},
		Def{Code: "ENCRYPTION_KEY_REQUIRED", Category: CategoryConfig, Status: 503, Action: ActionContactAdmin, Message: "服务尚未配置凭证加密密钥", Hint: "请联系管理员完成配置", Doc: "HTTP 与任务共用", AllowCustomMessage: true},
		Def{Code: "OAUTH_START_FAILED", Category: CategoryInternal, Status: 500, Retryable: true, Action: ActionRetry, Message: "无法生成授权链接", Hint: "请稍后重试", Doc: "随机数生成失败"},
		Def{Code: "PROXY_CHECK_FAILED", Category: CategoryInternal, Status: 500, Retryable: true, Action: ActionRetry, Message: "代理检测记录保存失败", Doc: "—"},
		Def{Code: "BILL_INTERNAL_ERROR", Category: CategoryInternal, Status: 500, Retryable: true, Action: ActionRetry, Message: "账单处理失败", Hint: "请稍后重试；若持续出现，请把请求编号提供给管理员", Doc: "—"},
		Def{Code: "BILL_ACCESS_DENIED", Category: CategoryUpstream, Status: 502, Action: ActionContactAdmin, Message: "平台拒绝访问", Hint: "请检查平台设置中的令牌及账号权限", Doc: "账单查询 401/403"},
		Def{Code: "BILL_ACCOUNT_NOT_FOUND", Category: CategoryUpstream, Status: 502, Action: ActionCheckPlatform, Message: "平台未找到所选交付编号，或当前令牌无权访问", Hint: "请核对账号关联和平台令牌", Doc: "账单查询 404"},
		Def{Code: "BILL_RATE_LIMITED", Category: CategoryRateLimit, Status: 429, Action: ActionWait, Message: "平台查询过于频繁", Hint: "请稍后重试", Doc: "账单查询 429"},
		Def{Code: "BILL_UPSTREAM_ERROR", Category: CategoryUpstream, Status: 502, Retryable: true, Action: ActionRetry, Message: "平台账单查询失败", Hint: "请检查查询范围或稍后重试", Doc: "账单查询 5xx"},
		Def{Code: "BILL_QUERY_TIMEOUT", Category: CategoryUpstream, Status: 504, Retryable: true, Action: ActionRetry, Message: "账单查询已超时或取消", Hint: "请稍后重试", Doc: "账单查询超时"},
		Def{Code: "BILL_RESPONSE_ERROR", Category: CategoryUpstream, Status: 502, Retryable: true, Action: ActionRetry, Message: "平台连接失败或账单响应格式异常", Hint: "请稍后重试", Doc: "账单查询响应异常"},
		Def{Code: "RUN_ACTIVE", Category: CategoryConflict, Status: 409, Action: ActionWait, Message: "已有账单同步任务在进行", Hint: "请等待当前任务结束", Doc: "账单同步并发保护"},
		Def{Code: "INTERNAL_ERROR", Category: CategoryInternal, Status: 500, Retryable: true, Action: ActionRetry, Message: "服务处理请求时出错", Hint: "请稍后重试；若持续出现，请把请求编号提供给管理员", Doc: "未预期的服务端错误，原因只记录在日志；message 可说明失败的是哪一步", AllowCustomMessage: true},
		Def{Code: "STORAGE_UNAVAILABLE", Category: CategoryStorage, Status: 503, Retryable: true, Action: ActionRetry, Message: "数据存储暂时不可用", Hint: "请稍后重试；若持续出现，请把请求编号提供给管理员", Doc: "数据库忙、锁定、只读或磁盘已满；message 可说明失败的是哪一步", AllowCustomMessage: true},
		Def{Code: "SECRET_UNREADABLE", Category: CategoryConfig, Status: 503, Action: ActionContactAdmin, Message: "加密保存的信息无法读取或写入", Hint: "请检查服务的加密密钥配置；若密钥已更换，请重新填写相关信息", Doc: "details.secret 指明是哪类秘密：account_password、account_credentials、connection_token、proxy_password、import", AllowCustomMessage: true},
		Def{Code: "REQUEST_MALFORMED", Category: CategoryValidation, Status: 400, Action: ActionFixInput, Message: "请求内容格式不正确", Doc: "请求体无法解析为对象或含有不支持的字段", AllowCustomMessage: true},
		Def{Code: "QUERY_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "查询参数不正确", Hint: "请检查分页、排序或筛选条件", Doc: "fields 列出每个有问题的查询参数", AllowCustomMessage: true},
		Def{Code: "USER_DISABLED", Category: CategoryAuth, Status: 401, Action: ActionLogin, Message: "当前账号已停用或不存在", Hint: "请联系管理员或重新登录", Doc: "会话有效但用户行已停用或删除"},
		Def{Code: "USER_INVALID", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "目标用户不可用", Hint: "请选择仍启用的用户", Doc: "资源分配：目标用户不存在或已停用", AllowCustomMessage: true},
		Def{Code: "PROXY_EXISTS", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "代理已存在", Doc: "唯一约束冲突"},
		Def{Code: "CONNECTION_EXISTS", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "平台连接已存在", Doc: "唯一约束冲突"},
		Def{Code: "ROLE_EXISTS", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "角色名称已存在", Doc: "唯一约束冲突"},
		Def{Code: "USER_EXISTS", Category: CategoryConflict, Status: 409, Action: ActionNone, Message: "账号已存在", Doc: "唯一约束冲突"},
		Def{Code: "ACCOUNT_DATES_REQUIRED", Category: CategoryValidation, Action: ActionFixInput, Message: "缺少注册日期或套餐日期", Doc: "预检"},
		Def{Code: "REGISTER_LOCATION_INVALID", Category: CategoryValidation, Action: ActionFixInput, Message: "注册地区需为美国（US）", Doc: "预检"},
		Def{Code: "REGISTER_TIME_INVALID", Category: CategoryValidation, Action: ActionFixInput, Message: "注册日期格式不正确", Doc: "预检"},
		Def{Code: "PLAN_START_TIME_INVALID", Category: CategoryValidation, Action: ActionFixInput, Message: "套餐开始日期格式不正确", Doc: "预检"},
		Def{Code: "PLAN_END_TIME_INVALID", Category: CategoryValidation, Action: ActionFixInput, Message: "套餐结束日期格式不正确", Doc: "预检"},
		Def{Code: "PLAN_DATE_RANGE_INVALID", Category: CategoryValidation, Action: ActionFixInput, Message: "套餐开始日期不能晚于结束日期", Doc: "预检"},
		Def{Code: "REGISTER_DATE_RANGE_INVALID", Category: CategoryValidation, Action: ActionFixInput, Message: "注册日期不能晚于套餐开始日期", Doc: "预检"},
		Def{Code: "PROXY_REQUIRED", Category: CategoryValidation, Action: ActionFixInput, Message: "缺少可用代理", Doc: "预检"},
		Def{Code: "DELIVERY_STATE_UNAVAILABLE", Category: CategoryValidation, Action: ActionFixInput, Message: "无法读取交付状态", Doc: "预检"},
		Def{Code: "PLATFORM_AUTH_FAILED", Category: CategoryUpstream, Status: 502, Action: ActionContactAdmin, Message: "平台拒绝了本次请求的身份验证", Hint: "请检查平台设置中的令牌及权限", Doc: "上游 401/403", AllowCustomMessage: true},
		Def{Code: "PLATFORM_RESPONSE_INVALID", Category: CategoryUpstream, Status: 502, Retryable: true, Action: ActionRetry, Message: "平台返回的内容无法识别", Hint: "请稍后重试", Doc: "上游响应解析失败", AllowCustomMessage: true},
		Def{Code: "PLATFORM_UNAVAILABLE", Category: CategoryUpstream, Status: 502, Retryable: true, Action: ActionRetry, Message: "平台暂时无法访问", Hint: "请稍后重试", Doc: "上游 5xx/429/网络失败", AllowCustomMessage: true},
		Def{Code: "PLATFORM_QUERY_DENIED", Category: CategoryUpstream, Action: ActionContactAdmin, Message: "平台拒绝查询这些账号的数据", Hint: "请联系供应商核实 OpenAPI 令牌的查询权限", Doc: "指标/账单查询返回 404“账号不存在或无权访问”；本地无法修复"},
		Def{Code: "PLATFORM_REJECTED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "平台拒绝了本次上号", Hint: "请核对账号资料及平台配置后重试", Doc: "details.upstream_status；message 可附平台摘要", AllowCustomMessage: true},
		Def{Code: "SUBMIT_OUTCOME_UNKNOWN", Status: 409, Category: CategoryConflict, Action: ActionCheckPlatform, Message: "上次上号结果待确认", Hint: "请先在平台核实，再决定是否重试", Doc: "请求发出后未收到可判定的结果", AllowCustomMessage: true},
		Def{Code: "WORKER_RESTART_UNKNOWN", Category: CategoryUpstream, Action: ActionCheckPlatform, Message: "服务重启前的上号结果待确认", Hint: "请先在平台核实", Doc: "进程中断时正在运行的任务"},
		Def{Code: "WEB_LOGIN_FAILED", Category: CategoryUpstream, Status: 502, Action: ActionContactAdmin, Message: "网页后台登录失败", Hint: "请检查平台连接里的网页登录账号和密码", Doc: "供应商网页后台 pubkey/login 返回 401 或两次登录后仍 401"},
		Def{Code: "WEB_FORBIDDEN", Category: CategoryUpstream, Status: 502, Action: ActionContactAdmin, Message: "网页后台拒绝读取", Hint: "请确认该后台账号有查看权限", Doc: "网页后台读取接口返回 403"},
		Def{Code: "WEB_ENDPOINT_MISSING", Category: CategoryUpstream, Status: 502, Action: ActionContactAdmin, Message: "网页后台接口不存在", Hint: "平台可能已改版，请联系维护者", Doc: "网页后台读取接口返回 404"},
		Def{Code: "WEB_RATE_LIMITED", Category: CategoryUpstream, Status: 502, Retryable: true, Action: ActionWait, Message: "网页后台限流", Hint: "稍后自动重试", Doc: "网页后台返回 429"},
		Def{Code: "WEB_BILL_RANGE_MISMATCH", Category: CategoryUpstream, Status: 502, Retryable: true, Action: ActionRetry, Message: "平台返回的账单范围与请求不符", Hint: "稍后自动重试", Doc: "/bill/query 的 range 与请求不一致，该批次丢弃"},
		Def{Code: "WEB_NOT_CONFIGURED", Category: CategoryValidation, Status: 422, Action: ActionFixInput, Message: "该平台连接未配置网页登录", Hint: "请在平台连接里填写网页登录账号和密码", Doc: "连接缺少 web_username/web_password"},
		Def{Code: "WEB_SUSPENDED", Category: CategoryUpstream, Status: 502, Action: ActionFixInput, Message: "网页通道已暂停：登录连续失败", Hint: "请检查后台密码后重新保存，或点“测试登录”", Doc: "连续 3 次 WEB_LOGIN_FAILED 后暂停；重新保存凭证或测试登录成功即恢复"},
		Def{Code: "SYNC_IN_BACKOFF", Category: CategoryConflict, Status: 409, Action: ActionWait, Message: "同步正在退避等待中", Hint: "请稍后再试", Doc: "details.next_attempt_at 为下次自动尝试时间"},
		Def{Code: "LOCAL_LINK_SAVE_FAILED", Category: CategoryStorage, Retryable: true, Action: ActionCheckPlatform, Message: "平台已接收，但本地关联保存失败", Hint: "请先核实平台记录，再补录关联", Doc: "—"},
		Def{Code: "ALREADY_LINKED", Status: 409, Category: CategoryConflict, Action: ActionNone, Message: "账号已在该平台上号", Hint: "无需重复提交", Doc: "—"},
		Def{Code: "SUBMIT_IN_PROGRESS", Status: 409, Category: CategoryConflict, Action: ActionWait, Message: "当前上号任务仍在执行", Hint: "请等待结果", Doc: "—"},
		Def{Code: "UNSUPPORTED_JOB", Category: CategoryInternal, Action: ActionNone, Message: "不支持的任务类型", Doc: "—"},
		Def{Code: "JOB_PAYLOAD_INVALID", Category: CategoryInternal, Action: ActionNone, Message: "任务参数无法读取", Doc: "—"},
		Def{Code: "CONNECTION_UNREADABLE", Category: CategoryConfig, Action: ActionContactAdmin, Message: "平台令牌无法读取", Hint: "请在平台设置中重新填写令牌", Doc: "异步结果：令牌解密失败（密钥更换或数据损坏）"},
		Def{Code: "CONNECTION_CHANGED", Category: CategoryConflict, Action: ActionRetry, Message: "平台连接配置已变化", Hint: "请重新发起同步", Doc: "账单同步项：任务执行期间连接被修改"},
		Def{Code: "TIMEZONE_UNUSABLE", Category: CategoryConfig, Action: ActionContactAdmin, Message: "平台连接的时区无法使用", Hint: "请在平台设置中修正时区", Doc: "账单同步项：连接时区无效"},
		Def{Code: "WINDOW_EXPIRED", Category: CategoryUpstream, Action: ActionNone, Message: "日期已超出平台可查窗口", Doc: "账单同步项"},
		Def{Code: "UPSTREAM_HTTP_401", Category: CategoryUpstream, Action: ActionContactAdmin, Message: "平台拒绝了本次请求的身份验证", Hint: "请检查平台设置中的令牌", Doc: "账单同步项：上游 401"},
		Def{Code: "UPSTREAM_HTTP_403", Category: CategoryUpstream, Action: ActionContactAdmin, Message: "平台拒绝访问", Hint: "请检查平台令牌的权限", Doc: "账单同步项：上游 403"},
		Def{Code: "CREDENTIAL_UNREADABLE", Category: CategoryConfig, Action: ActionFixInput, Message: "已保存的凭证无法读取", Hint: "请重新填写凭证", Doc: "阶段 2 并入 SECRET_UNREADABLE"},
		Def{Code: "PASSWORD_UNREADABLE", Category: CategoryConfig, Action: ActionFixInput, Message: "已保存的密码无法读取", Hint: "请重新填写密码", Doc: "阶段 2 并入 SECRET_UNREADABLE"},
		Def{Code: "METADATA_UNREADABLE", Category: CategoryConfig, Action: ActionFixInput, Message: "补充信息无法读取", Hint: "请重新填写补充信息", Doc: "—"},
		Def{Code: "ACCEPTED_PENDING_VERIFICATION", Category: CategoryUpstream, Action: ActionCheckPlatform, Message: "平台已接收，等待平台核验", Doc: "非错误，统一文案"},
		Def{Code: "PROXY_PROBE_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionRetry, Message: "历史任务的本机代理检测失败", Hint: "当前版本可重新提交上号", Doc: "旧任务码"},
		Def{Code: "RETRY_EXHAUSTED", Category: CategoryUpstream, Action: ActionCheckPlatform, Message: "多次重试后仍未成功", Hint: "请核实平台状态后再手动重试", Doc: "账单同步项"},
		Def{Code: "ACCOUNT_LINK_CHANGED", Category: CategoryConflict, Action: ActionNone, Message: "账号与平台的关联已变化", Hint: "请刷新后重新选择", Doc: "账单同步项"},
		Def{Code: "UPSTREAM_UNAVAILABLE", Category: CategoryUpstream, Retryable: true, Action: ActionRetry, Message: "平台暂时无法访问", Hint: "请稍后重试", Doc: "账单同步项"},
		Def{Code: "BILL_RESPONSE_INVALID", Category: CategoryUpstream, Action: ActionCheckPlatform, Message: "平台返回的账单数据无法识别", Doc: "账单同步项；来自 supplier 包"},
		Def{Code: "BILL_RANGE_MISMATCH", Category: CategoryUpstream, Action: ActionCheckPlatform, Message: "平台返回的账单日期范围与请求不一致", Doc: "账单同步项；来自 supplier 包"},
		Def{Code: "BILL_ID_MISMATCH", Category: CategoryUpstream, Action: ActionCheckPlatform, Message: "平台返回的账号编号与请求不一致", Doc: "账单同步项；来自 supplier 包"},
		Def{Code: "BILL_ROW_INVALID", Category: CategoryUpstream, Action: ActionCheckPlatform, Message: "平台返回的账单行格式不正确", Doc: "账单同步项；来自 supplier 包"},
		Def{Code: "BILL_COST_INVALID", Category: CategoryUpstream, Action: ActionCheckPlatform, Message: "平台返回的费用数值不正确", Doc: "账单同步项；来自 supplier 包"},
		Def{Code: "PROXY_TIMEOUT", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "代理检测超时或已取消", Hint: "details.stage 为所处阶段", Doc: "代理检测阶段码"},
		Def{Code: "TARGET_AUTH_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "目标代理认证失败", Hint: "请检查用户名和密码", Doc: "代理检测阶段码"},
		Def{Code: "TARGET_CONNECT_DENIED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "目标代理拒绝连接", Hint: "details.upstream_status 为代理返回的状态", Doc: "代理检测阶段码"},
		Def{Code: "TARGET_CONNECT_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "无法连接目标代理", Hint: "若代理需要境外来源 IP，请配置前置代理", Doc: "代理检测阶段码"},
		Def{Code: "TARGET_HTTP_ERROR", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "经目标代理访问检测网站返回错误状态", Doc: "代理检测阶段码"},
		Def{Code: "TARGET_REQUEST_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "经目标代理访问检测网站失败", Hint: "请检查目标代理是否允许访问外网", Doc: "代理检测阶段码"},
		Def{Code: "TARGET_RESPONSE_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "检测网站响应读取失败", Doc: "代理检测阶段码"},
		Def{Code: "UPSTREAM_AUTH_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "前置代理认证失败", Hint: "请检查前置代理的用户名和密码", Doc: "代理检测阶段码"},
		Def{Code: "UPSTREAM_CONNECT_DENIED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "前置代理拒绝连接目标代理", Hint: "details.upstream_status 为前置代理返回的状态", Doc: "代理检测阶段码"},
		Def{Code: "UPSTREAM_CONNECT_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "无法连接前置代理", Hint: "请检查本机代理是否运行及端口是否正确", Doc: "代理检测阶段码"},
		Def{Code: "UPSTREAM_TLS_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "前置代理安全握手失败", Hint: "请检查证书和协议", Doc: "代理检测阶段码"},
		Def{Code: "UPSTREAM_TUNNEL_FAILED", Category: CategoryUpstream, Retryable: true, Action: ActionFixInput, Message: "前置代理无法建立到目标代理的隧道", Hint: "请检查前置服务、节点与访问规则", Doc: "代理检测阶段码"},
		Def{Code: "NETWORK", Category: CategoryInternal, Retryable: true, Action: ActionRetry, Message: "无法连接服务", Hint: "请检查服务是否运行或网络是否正常", Doc: "仅前端产生"},
		Def{Code: "TIMEOUT", Category: CategoryInternal, Retryable: true, Action: ActionRetry, Message: "请求超时", Hint: "请稍后重试", Doc: "仅前端产生"},
		Def{Code: "RESPONSE_INVALID", Category: CategoryInternal, Retryable: true, Action: ActionRetry, Message: "服务返回的内容无法识别", Hint: "请刷新页面后重试", Doc: "仅前端产生"},
	)
}

// Code constants so callers never spell a code by hand.
const (
	CodeAuthRequired                = "AUTH_REQUIRED"
	CodeAuthInvalid                 = "AUTH_INVALID"
	CodeAuthRateLimited             = "AUTH_RATE_LIMITED"
	CodeCsrfFailed                  = "CSRF_FAILED"
	CodeOriginFailed                = "ORIGIN_FAILED"
	CodePermissionDenied            = "PERMISSION_DENIED"
	CodePayloadTooLarge             = "PAYLOAD_TOO_LARGE"
	CodeMethodNotAllowed            = "METHOD_NOT_ALLOWED"
	CodeQueryScopeTooLarge          = "QUERY_SCOPE_TOO_LARGE"
	CodeValidationFailed            = "VALIDATION_FAILED"
	CodePasswordRequired            = "PASSWORD_REQUIRED"
	CodePasswordInvalid             = "PASSWORD_INVALID"
	CodeAccountRequired             = "ACCOUNT_REQUIRED"
	CodeAccountWhitespace           = "ACCOUNT_WHITESPACE"
	CodeAccountEmailInvalid         = "ACCOUNT_EMAIL_INVALID"
	CodePlanInvalid                 = "PLAN_INVALID"
	CodePlanRequired                = "PLAN_REQUIRED"
	CodeAuthJsonInvalid             = "AUTH_JSON_INVALID"
	CodeCredentialsRequired         = "CREDENTIALS_REQUIRED"
	CodeCredentialsIncomplete       = "CREDENTIALS_INCOMPLETE"
	CodePlatformConnectionRequired  = "PLATFORM_CONNECTION_REQUIRED"
	CodePreflightBlocked            = "PREFLIGHT_BLOCKED"
	CodeProxyInvalid                = "PROXY_INVALID"
	CodeProxySettingsInvalid        = "PROXY_SETTINGS_INVALID"
	CodeOptionalMetadataInvalid     = "OPTIONAL_METADATA_INVALID"
	CodeOauthResultInvalid          = "OAUTH_RESULT_INVALID"
	CodeOauthProxyInvalid           = "OAUTH_PROXY_INVALID"
	CodeConnectionInvalid           = "CONNECTION_INVALID"
	CodeConnectionUnreadable        = "CONNECTION_UNREADABLE"
	CodeConnectionChanged           = "CONNECTION_CHANGED"
	CodeTimezoneUnusable            = "TIMEZONE_UNUSABLE"
	CodeWindowExpired               = "WINDOW_EXPIRED"
	CodeConnectionDisabled          = "CONNECTION_DISABLED"
	CodeAccountLinkMissing          = "ACCOUNT_LINK_MISSING"
	CodeNoLinkedAccounts            = "NO_LINKED_ACCOUNTS"
	CodeNoRetryItems                = "NO_RETRY_ITEMS"
	CodeSyncScopeTooLarge           = "SYNC_SCOPE_TOO_LARGE"
	CodeUpstreamIdInvalid           = "UPSTREAM_ID_INVALID"
	CodeBillAccountAmbiguous        = "BILL_ACCOUNT_AMBIGUOUS"
	CodeBillAccountInvalid          = "BILL_ACCOUNT_INVALID"
	CodeBillAccountLimit            = "BILL_ACCOUNT_LIMIT"
	CodeBillAccountUnlinked         = "BILL_ACCOUNT_UNLINKED"
	CodeBillRangeExpired            = "BILL_RANGE_EXPIRED"
	CodeBillRangeInvalid            = "BILL_RANGE_INVALID"
	CodeBillWindowInvalid           = "BILL_WINDOW_INVALID"
	CodeNotFound                    = "NOT_FOUND"
	CodeOauthSessionExpired         = "OAUTH_SESSION_EXPIRED"
	CodeImportExpired               = "IMPORT_EXPIRED"
	CodeVersionConflict             = "VERSION_CONFLICT"
	CodeAccountExists               = "ACCOUNT_EXISTS"
	CodeIdempotencyConflict         = "IDEMPOTENCY_CONFLICT"
	CodeArchived                    = "ARCHIVED"
	CodeAccountDelivered            = "ACCOUNT_DELIVERED"
	CodeAccountInProgress           = "ACCOUNT_IN_PROGRESS"
	CodeProxyInUse                  = "PROXY_IN_USE"
	CodeConnectionInUse             = "CONNECTION_IN_USE"
	CodeRoleInUse                   = "ROLE_IN_USE"
	CodeRoleImmutable               = "ROLE_IMMUTABLE"
	CodeLastAdmin                   = "LAST_ADMIN"
	CodeSelfDelete                  = "SELF_DELETE"
	CodeSelfDemotion                = "SELF_DEMOTION"
	CodeSelfDisable                 = "SELF_DISABLE"
	CodeRateLimited                 = "RATE_LIMITED"
	CodeOauthBusy                   = "OAUTH_BUSY"
	CodeOauthExchangeFailed         = "OAUTH_EXCHANGE_FAILED"
	CodeEncryptionKeyRequired       = "ENCRYPTION_KEY_REQUIRED"
	CodeOauthStartFailed            = "OAUTH_START_FAILED"
	CodeProxyCheckFailed            = "PROXY_CHECK_FAILED"
	CodeBillInternalError           = "BILL_INTERNAL_ERROR"
	CodeInternalError               = "INTERNAL_ERROR"
	CodeStorageUnavailable          = "STORAGE_UNAVAILABLE"
	CodeSecretUnreadable            = "SECRET_UNREADABLE"
	CodeRequestMalformed            = "REQUEST_MALFORMED"
	CodeQueryInvalid                = "QUERY_INVALID"
	CodeUserDisabled                = "USER_DISABLED"
	CodeUserInvalid                 = "USER_INVALID"
	CodeProxyExists                 = "PROXY_EXISTS"
	CodeConnectionExists            = "CONNECTION_EXISTS"
	CodeRoleExists                  = "ROLE_EXISTS"
	CodeUserExists                  = "USER_EXISTS"
	CodeBillAccessDenied            = "BILL_ACCESS_DENIED"
	CodeBillAccountNotFound         = "BILL_ACCOUNT_NOT_FOUND"
	CodeBillRateLimited             = "BILL_RATE_LIMITED"
	CodeBillUpstreamError           = "BILL_UPSTREAM_ERROR"
	CodeBillQueryTimeout            = "BILL_QUERY_TIMEOUT"
	CodeBillResponseError           = "BILL_RESPONSE_ERROR"
	CodeRunActive                   = "RUN_ACTIVE"
	CodeAccountDatesRequired        = "ACCOUNT_DATES_REQUIRED"
	CodeRegisterLocationInvalid     = "REGISTER_LOCATION_INVALID"
	CodeRegisterTimeInvalid         = "REGISTER_TIME_INVALID"
	CodePlanStartTimeInvalid        = "PLAN_START_TIME_INVALID"
	CodePlanEndTimeInvalid          = "PLAN_END_TIME_INVALID"
	CodePlanDateRangeInvalid        = "PLAN_DATE_RANGE_INVALID"
	CodeRegisterDateRangeInvalid    = "REGISTER_DATE_RANGE_INVALID"
	CodeProxyRequired               = "PROXY_REQUIRED"
	CodeDeliveryStateUnavailable    = "DELIVERY_STATE_UNAVAILABLE"
	CodePlatformRejected            = "PLATFORM_REJECTED"
	CodePlatformQueryDenied         = "PLATFORM_QUERY_DENIED"
	CodePlatformAuthFailed          = "PLATFORM_AUTH_FAILED"
	CodePlatformResponseInvalid     = "PLATFORM_RESPONSE_INVALID"
	CodePlatformUnavailable         = "PLATFORM_UNAVAILABLE"
	CodeSubmitOutcomeUnknown        = "SUBMIT_OUTCOME_UNKNOWN"
	CodeWorkerRestartUnknown        = "WORKER_RESTART_UNKNOWN"
	CodeLocalLinkSaveFailed         = "LOCAL_LINK_SAVE_FAILED"
	CodeWebLoginFailed              = "WEB_LOGIN_FAILED"
	CodeWebForbidden                = "WEB_FORBIDDEN"
	CodeWebEndpointMissing          = "WEB_ENDPOINT_MISSING"
	CodeWebRateLimited              = "WEB_RATE_LIMITED"
	CodeWebBillRangeMismatch        = "WEB_BILL_RANGE_MISMATCH"
	CodeWebNotConfigured            = "WEB_NOT_CONFIGURED"
	CodeWebSuspended                = "WEB_SUSPENDED"
	CodeSyncInBackoff               = "SYNC_IN_BACKOFF"
	CodeAlreadyLinked               = "ALREADY_LINKED"
	CodeSubmitInProgress            = "SUBMIT_IN_PROGRESS"
	CodeUnsupportedJob              = "UNSUPPORTED_JOB"
	CodeJobPayloadInvalid           = "JOB_PAYLOAD_INVALID"
	CodeCredentialUnreadable        = "CREDENTIAL_UNREADABLE"
	CodePasswordUnreadable          = "PASSWORD_UNREADABLE"
	CodeMetadataUnreadable          = "METADATA_UNREADABLE"
	CodeAcceptedPendingVerification = "ACCEPTED_PENDING_VERIFICATION"
	CodeProxyProbeFailed            = "PROXY_PROBE_FAILED"
	CodeRetryExhausted              = "RETRY_EXHAUSTED"
	CodeAccountLinkChanged          = "ACCOUNT_LINK_CHANGED"
	CodeUpstreamUnavailable         = "UPSTREAM_UNAVAILABLE"
	CodeBillResponseInvalid         = "BILL_RESPONSE_INVALID"
	CodeBillRangeMismatch           = "BILL_RANGE_MISMATCH"
	CodeBillIdMismatch              = "BILL_ID_MISMATCH"
	CodeBillRowInvalid              = "BILL_ROW_INVALID"
	CodeBillCostInvalid             = "BILL_COST_INVALID"
	CodeProxyTimeout                = "PROXY_TIMEOUT"
	CodeTargetAuthFailed            = "TARGET_AUTH_FAILED"
	CodeTargetConnectDenied         = "TARGET_CONNECT_DENIED"
	CodeTargetConnectFailed         = "TARGET_CONNECT_FAILED"
	CodeTargetHttpError             = "TARGET_HTTP_ERROR"
	CodeTargetRequestFailed         = "TARGET_REQUEST_FAILED"
	CodeTargetResponseFailed        = "TARGET_RESPONSE_FAILED"
	CodeUpstreamAuthFailed          = "UPSTREAM_AUTH_FAILED"
	CodeUpstreamConnectDenied       = "UPSTREAM_CONNECT_DENIED"
	CodeUpstreamConnectFailed       = "UPSTREAM_CONNECT_FAILED"
	CodeUpstreamTlsFailed           = "UPSTREAM_TLS_FAILED"
	CodeUpstreamTunnelFailed        = "UPSTREAM_TUNNEL_FAILED"
	CodeNetwork                     = "NETWORK"
	CodeTimeout                     = "TIMEOUT"
	CodeResponseInvalid             = "RESPONSE_INVALID"
)
