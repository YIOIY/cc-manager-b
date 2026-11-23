# 变更记录

## Unreleased（2026-11-24 OAuth 可靠性修正）

- 探测：代理池出口探测改为把带 JSON 体的 401/403（Claude 令牌端点对 GET 的真实返回）视为可达，只有连接失败、5xx 或非 JSON 响应才报 `OAUTH_PROXY_UNREACHABLE`；探测返回的 429 仍带 `retry_after_seconds`。修复探测缓存并发写入崩溃、阶段停顿随机源与出口闸门切换的数据竞争。
- 冷却：兑换成功后只按账号 ID / 邮箱播种 60 秒冷却，不再把操作者的浏览器会话一起冷却，同一管理员「继续添加下一个」不再被上一个账号的成功限流。
- 兑换：排队（≤30 秒）与访问上游（≤60 秒）分开计时且共用一套取消规则——排队期间浏览器断开即放弃，进入上游后不再随连接取消，结果缓存在会话内；会话已持有令牌（`stage` 为 `profile` / `ready`）时可以空 `result` 取回或续读资料，前端「继续获取资料」按钮即走此路径；缓存令牌在等待中过期时返回 `404 OAUTH_SESSION_EXPIRED` 并终止会话，不再报成 profile 失败。提交前重新读取会话状态，已取消/已过期的会话不再被标记为提交中。
- 预热：改在任务仍为 queued 时等待，重启不会把预热中的任务记为结果未知；账号忙碌重新排队后沿用同一 `not_before_at`，不再二次等待；默认范围手动 1–3 秒、批量 5–15 秒，上限填 0 关闭，迁移 v21 修正早期库中的越界默认值（120/0/0/300/900）。
- 账号写回：重新授权与重复上号的 UPDATE 只覆盖可选资料字段，保留 `_oauth_fp`、`_credential_devices`、`source` 等内部元数据；对话框内为已有账号切换代理时先释放 OAuth 会话再显式 `PATCH` 改绑（带 `expected_version`），被拒绝时选择器回退。
- 路由与观测：OAuth 写保护中间件移到权限校验之后，无权限请求不再占用账号锁；`/metrics` 中 OAuth 指标按族输出各自的 HELP/TYPE 头。
- 文档：`claude-oauth-onboarding.md`、`api-contract.md`、错误码说明与设置面板文案按实际实现对齐（时间预算、冷却键、探测规则、预热范围、`oauth_session_id` 与 409 语义）。
- 凭证新鲜度：以 `last_refresh` 为主序，本次刷新更晚即接受，相同或缺失时才比较 `expired`，导入时写得过远的有效期不再永久阻塞该账号重授权；`CREDENTIAL_STALE` 的文案与 `details.saved/incoming` 给出两侧时间。
- 兑换阶段 401/403 分型：只有 JSON 错误体才视为身份被拒并终止会话；代理/WAF 的非 JSON 拦截页判定为授权码未到达上游，返回 `OAUTH_PROXY_UNREACHABLE`（`oauth_stage=token`）并保留会话。
- 生成链接：同一浏览器会话的活动链接绑定了别的出口或账号时返回 409 `OAUTH_SESSION_CONFLICT`（details 给出该链接），不再静默返回旧链接；前端据此切换为「作废并重新获取」并以 `refresh` 重发。
- `OAUTH_BUSY` 带 `details.oauth_busy_reason`，`retry_after_seconds` 随原因变化（同会话在途 2 秒、同浏览器并发 5 秒、排队预算用尽约 10 秒带抖动、链接满 15 秒、请求已取消 1 秒），`/metrics` 新增 `cc_manager_oauth_busy_total{reason}`。
- 移除无调用方的 `OAuthStorageBackend` 接口草案及其测试。
- 修复：上号调度设置面板按后端契约发送 `version` 与 JSON 字符串形式的 `ua_pool_json`（此前发送 `expected_version` 与数组，保存必然 400/409，UA 池数量显示为字符串长度）。
- 修复：全局/单出口并发在进程启动时从已保存设置加载，不再重启后回退为内置的 3/2。
- 修复：探测阶段除 429 外的任何失败一律归 `OAUTH_PROXY_UNREACHABLE`，不再把非 JSON 401/403、5xx、200 非 JSON 误报为身份被拒、供应商拒绝或响应异常。
- 修复：重新授权在平台已接收、本地版本冲突时返回带 `details.platform_updated=true` 的 409，OAuth 写保护据此结束会话并缓存结果，同一授权不会被第二次推送；前端据此进入核对模式。
- 修复：出口间隔排队者取消时恢复到上一槽位而不是自己的到达时间，后续兑换不再早于配置间隔。
- 修复：显式刷新链接时只在旧会话仍持有租约的情况下接管，否则重新加锁，避免并发取消后新会话失去账号写锁。
- 待授权会话记录的邮箱统一小写去空格；`OAUTH_RESPONSE_INVALID` / `OAUTH_PROXY_UNREACHABLE` 错误码说明按实际行为改写。

## Unreleased（2026-11-09 账号页解耦、代理编辑删除）

- 新建上号表单简化：凭证区块新增「粘贴凭证 JSON 自动填充」，接受 auth_json 对象或导入格式的单条记录（含 account / password），一次填入 13 个凭证字段，并对 `type`、`verification_status`、`last_refresh` 在缺省时自动补齐；填充后即时提示已填项数与仍缺字段。新账号默认注册日期与套餐开始为今天、套餐结束为一个月后（按月末截断）；平台连接在只有一个启用连接时自动选中，否则沿用上次提交使用的连接；凭证邮箱跟随账号邮箱直到手动修改。新增 `applyCredentialJson` / `defaultAccountDates` 纯函数与单测。
- 代理池 UI：「本机检测路径」卡片改用与列表一致的面板样式（同一边框、圆角、阴影与绿色图标），标题旁显示直连/前置代理标签，链路图与认证、超时信息并为一行，高度从 193px 降到 127px；操作列收敛为「检测 · 编辑 · 更多（检测记录 / 停用或启用 / 删除）」，不再换行。
- 修复：在 RBAC 之前（迁移 v4）建库的实例无法新建用户，报「用户保存失败」。原因是旧 `users.role` 列为 NOT NULL 且无默认值，而新建用户不再写该列。迁移 v7 删除这一遗留列（角色已在 `user_roles` 中），基础建表语句同步移除；补充以 v4 表结构为起点的回归测试。
- 账号页不再依赖「平台设置」「代理池」模块的权限：新增 `GET /lookups/platform-connections` 与 `GET /lookups/proxies`（只含 id、名称、地址、启用状态，不含令牌与检测记录），持有 `accounts.view` / `accounts.onboard`（账单页为 `bills.view`）即可调用；账号表单、上号弹窗、平台账单页的下拉选项改用这两个接口。此前只给账号权限的角色打开账号页会因读取 `/platform-connections` 被 403 而整页报错。
- 代理池新增编辑与删除：`PATCH /proxies/{id}`（名称、协议、主机、端口、用户名、密码/清除密码、启用状态，`expected_version` 乐观锁）、`DELETE /proxies/{id}`（连同检测记录删除；仍被未归档账号绑定时返回 `409 PROXY_IN_USE` 并给出数量；已归档账号自动解绑）。新增权限 `proxies.update`、`proxies.delete`，迁移 v6 自动授予内置「运营」角色；「归档代理」改名为「停用代理」。代理列表返回 `username`、`password_configured`；代理弹窗支持编辑态（留空保留密码、清除密码、启用开关）。
- 路由层新增 `requireAny`（任一权限即可）。

## Unreleased（2026-11-08 性能与限流）

- 新建用户表单：密码字段下方实时提示「至少 12 位，还差 N 位」/「长度满足要求」，不再只靠会消失的占位文字说明创建按钮为何不可点。
- 新增 `GET /metrics`（Prometheus 文本格式，与 `/healthz` 一样不鉴权、只含计数）：HTTP 响应按状态类计数与耗时合计、在途请求数、会话数、运行时长、各后台循环最近一次 tick、任务/同步/账号/代理/用户按状态的数量。
- 新增 Playwright 端到端测试（`web/e2e/`，`npm run e2e` / `make web-e2e`）：驱动真实二进制 + 构建产物，覆盖登录失败与成功、只读角色的导航/深链接/按钮隐藏与 API 403、本机检测路径向导的切换与保存。
- 样式令牌：`base.css` 新增 18 个派生颜色变量（surface/tint/line/muted/ink/accent 各档与 danger），组件样式中 188 处重复的十六进制颜色改为引用变量；相差 1～3 个色阶的近似色收敛到同一变量，视觉差异不可辨。
- 移除遗留接口：`GET /usage`（`usage_metrics` 表从未有写入方，始终返回空）、`GET /billing/ledger` 占位、`POST /sync/metrics`、`POST /sync/bills` 与 `POST /platform-usage/sync`（旧版一键同步，前端早已改用 `/platform-usage/bills/sync`）。前端删除对应的「消耗记录」列表配置。
- 后端长函数拆分：`onboardAccount`（222 行）拆为补全默认值 → 校验 → 连接检查 → 代理解析 → 持久化五步，步骤以 `*apiError` 返回，处理器统一用 `respondError` 回应；`createBillRun`（184 行）拆为请求归一化、幂等回放（预检与事务内共用一份）、连接解析、链接收集、规模规划、写入六步。行为与错误码不变，现有测试全部通过。
- 前端「用户与权限」页拆分：`UsersView.vue` 从 1329 行降到 439 行，角色卡片、角色编辑器、用户表单分别成为 `components/users/` 下的独立组件，纯逻辑（页面可见性依赖、预设、授予边界）抽到 `access.ts` 并补 10 个单元/挂载测试。
- Go 依赖升级：`modernc.org/sqlite` 1.36 → 1.62（含 libc、memory），其余间接依赖同步更新；测试全部通过。
- 账号列表改为服务端分页与筛选（`GET /accounts?page=&page_size=&q=&onboarding_state=&plan=&archived=&proxy=&platform_status=&end_from=&end_to=&created_from=&created_to=`），上号状态在 SQL 中派生，与返回字段同一套规则；页内补充信息（平台链接、最近任务、元数据）只查询当前页的账号，不再每次扫描全部交付与任务历史。前端账号页接入 `serverPaged` 模式。
- 新增按用户的写请求令牌桶限速（`CC_MANAGER_WRITE_RATE_PER_MINUTE`，默认 120/分钟、突发 30），超限 `429 RATE_LIMITED` + `Retry-After`；读请求与登录不受影响（登录仍走原有退避）。

## Unreleased（2026-11-08 用户与权限）

- RBAC：权限目录按模块细分到 30 个动作（`accounts.create/update/archive/import/credentials/onboard/usage`、`proxies.*`、`bills.view/query/sync`、`connections.create/update/delete`、`users.*`、`roles.*`）；角色存数据库（迁移 v5，`roles` / `user_roles`），可自定义权限组合；用户可绑定多个角色取并集；内置管理员为超级角色。非管理员不能授予超出自身的权限或管理员角色。新增 `/permissions`、`/roles` 接口与「角色」管理页。
- 路由层对每个接口独立校验，无权限返回 `403 PERMISSION_DENIED`。
- 修复：会话被吊销（停用、重置密码）后跳回登录页时仍使用旧 CSRF 令牌，导致需刷新才能重新登录；现在 401 后自动获取新匿名会话，登录遇 `CSRF_FAILED` 自动重试一次。
- 验收记录见 `docs/acceptance-20261108.md`。
- 页面可见性成为角色编辑器的一等概念：权限目录为每个模块标注对应页面（`GET /permissions` 新增 `page` 字段），编辑器顶部用开关逐页控制可见；关闭页面会自动移除该页内所有动作权限，开启任一动作会自动打开页面可见；角色卡片首行列出可见页面。用户行操作统一为「编辑 + 更多」菜单，不再换行。
- 「用户与权限」页重做：角色卡片按模块显示授予比例；角色编辑器改为左侧模块导航（含计数与预设菜单）+ 右侧开关列表，显示权限标识与说明，无改动时禁用保存；用户表单的角色改为可多选卡片并实时显示合并后的权限数；用户操作收进下拉菜单。
- 修复：应用壳层的 `header` / `aside` 样式会渗入弹窗内同名元素（代理路径弹窗步骤标题被拉伸、角色编辑器导航被渲染成侧栏），已限定为直接子元素。
- 登录改为核对用户表（argon2id）；未知用户也走同等耗时的哈希比较；停用/改角色/重置密码立即吊销会话；每次请求重新读取用户状态。
- 新增 `/users` 管理接口、`/auth/password` 自助改密、`GET /users/roles` 角色说明；`cc-manager reset-password <用户名>` 命令行找回。
- 审计事件 `actor` 改为真实操作用户；系统动作为 `system`。
- 前端：会话携带权限列表，导航与路由按权限过滤，按钮按权限隐藏（新建/导入/上号/检测/修改连接/修改检测路径等）；新增「用户与权限」页面与「修改密码」对话框，侧栏显示当前用户与角色。
- 迁移 v4 建 `users` 表；环境变量中的管理员账号仅用于首次引导。

## Unreleased（2026-11-08 工程优化，第三轮）

- 代理池「本机检测网络」面板重做：页面内只保留一张状态卡（当前链路、连接方式、认证、超时），所有修改收口到一个分步弹窗（连接方式卡片选择 → 前置代理地址/认证 → 超时），地址即时校验、无改动时禁用保存；移除页面内快速表单、"未保存"提示与重复的路径展示。
- 修复：静态 `index.html` 在进程启动时被缓存，前端重新构建后旧进程会引用已删除的资源哈希；改为每次请求读取。

- CSS 迁入组件：`workspace.css`、`account-form.css`、`account-delivery.css` 拆到各自组件（视图与卡片 `scoped`，对话框普通 `<style>`），共用规则收敛为 `styles/shared.css`，`base.css` 只保留令牌、重置、登录、壳层与 Element Plus 全局覆盖；删除 53 条无引用规则。迁移保持原有层叠顺序与优先级（以标签或 EP 类结尾的选择器以 `:global()` 输出）。
- 验证方法：同一源内置新旧两个构建，对 14 个页面/对话框状态逐元素比较排序后的计算样式与几何，确认无差异。

## Unreleased（2026-11-08 工程优化，第二轮）

- 密钥轮换：密文改为 `k1:<key-id>:<base64>`，旧格式自动兼容；新增 `CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS` 与 `cc-manager rotate-key [--check]`，在单事务内重加密全部密文列与导入草稿。
- `runBillJob` 拆分为 claim / prepare / reserve / fetch / persist 五步（`billRun`），重试退避抽为 `billRetryDelay`；账单、指标、worker、查询路径全部传递 context。
- `GET /jobs` 与 `GET /audit-events` 支持 `page/page_size` 及字段筛选（`state/kind/account_id/created_from/created_to/q`、`action/resource_type/resource_id/...`），无分页参数时保持原数组响应；前端任务中心与审计日志改为服务端分页。
- 前端测试迁移到 Vitest（Vite 8 兼容版本）+ Vue Test Utils + happy-dom，新增 `api.ts`（CSRF 头、401、网络错误）与 `AccountDeliveryDialog` 预检/提交组件测试。
- CSS：合并跨文件重复定义的 7 组选择器。
- 新增测试：指标同步成功路径、导入提交原子性、`decimalFen` 边界、密钥环与轮换。
- 修复：`platform_links.alias` 为 NULL 时指标同步整批失败。

## 2026-11-08 工程优化（第一轮）

### 安全与稳定性

- HTTP 服务增加读写/空闲超时与请求头上限；收到 SIGTERM/SIGINT 后优雅关闭，等待后台 worker 与进行中的供应商请求，退出前执行 WAL checkpoint。
- 登录按「IP + 用户名」指数退避限速（429 + Retry-After），成功与失败都写审计日志。
- 会话存储挂到应用实例，匿名会话缩短到 30 分钟，总量上限 10000，后台定期清理。
- 所有 JSON 请求体加大小限制；统一 `decodeJSON` 帮助函数。
- 支持 `CC_MANAGER_ADMIN_PASSWORD_HASH`（argon2id）；新增 `cc-manager hash-password` 子命令。
- `CC_MANAGER_TRUST_PROXY=1` 时按 `X-Forwarded-Proto/Host/For` 判断 Secure Cookie、同源与客户端 IP。
- 统一安全响应头、API `Cache-Control: no-store`、`X-Request-ID`；静态资源 SPA 回退，不再列目录；`/assets` 不可变缓存。
- 二进制内嵌 `time/tzdata`，容器镜像补 `tzdata` 与 `ca-certificates`。

### 正确性

- 数据库错误不再伪装成业务冲突：UNIQUE 冲突返回 409，其余 500 并记录带 request_id 的结构化日志。
- 所有 `rows.Scan` 错误不再静默跳过；新库所有列 `NOT NULL DEFAULT`，旧库通过迁移回填 NULL。
- 创建账号、JSON 导入提交改为单事务；worker 失败路径统一回写 deliveries 状态；进程重启时 `running` 交付记录标为 `unknown`。
- 供应商拒绝原因（脱敏截断）保存到任务 `result_json.message`，任务列表与账号卡片可见。
- PATCH 账号、PUT 平台连接、PUT 凭证支持 `expected_version` 乐观锁；PATCH 校验套餐/日期/代理。
- 平台连接时区在保存时校验为有效 IANA 时区。

### 性能

- SQLite 通过 DSN 固定 `busy_timeout/WAL/foreign_keys/synchronous=NORMAL/_txlock=immediate`，对每个连接生效。
- 补充 jobs、sync_runs、platform_metric_snapshots、platform_bill_daily、deliveries、platform_links 等索引；账号列表改用单次关联查询。
- 定期清理过期导入草稿、幂等键、7 天前的指标同步记录、每代理超过 100 条的检测记录。
- 前端 Element Plus 按需引入（JS 1.09MB → 0.53MB，CSS 430KB → 154KB），vendor 分块；移除 Google Fonts 远程引用。

### 可维护性

- 迁移改为有序版本列表（v1 账单、v2 代理网络、v3 索引/默认值/审计字段），每步独立事务并有测试保证新库与升级库一致。
- 路由改为 Go 1.22 方法模式（`router.go`），handler 拆分；配置统一为 `Config` 启动时解析。
- 套餐白名单、元数据字段、代理协议、账号身份校验、日期校验收敛到 `validation.go`。
- `/audit-events` 返回真实审计事件（登录、账号/代理/连接/导入/上号/设置变更）。
- 删除无调用的 `proxyFingerprint`、`sortStrings`、`mailParse`、`account.onboard` 任务类型。
- 前端引入 vue-router、类型化 API 层（`types/api.ts`）、401 自动登出、请求超时；`App.vue` 拆为 `views/`、`layouts/`、`components/`、`composables/`；表单校验抽为纯函数并有单元测试；筛选条件写入 URL query；轮询改为可见性感知的 setTimeout 链。
- 前端加入 Prettier、ESLint、拆分 tsconfig、`noEmit`，不再把 `.js` 产物写进 `src/`；`package.json` 锁定版本范围并声明 `engines`。
- 新增 Makefile、GitHub Actions CI、`.gitignore`/`.dockerignore`/`.editorconfig`。

### 部署

- Dockerfile 仅复制构建所需目录，增加 `HEALTHCHECK`；compose 使用 `env_file`、`restart`、`read_only`、`cap_drop`、健康检查。
- systemd 单元补齐 `PrivateDevices/RestrictAddressFamilies/SystemCallFilter/MemoryDenyWriteExecute/CapabilityBoundingSet` 等。
- nginx 增加 HSTS/Permissions-Policy，按路径区分缓存头，限速 key 回退到 `$binary_remote_addr`。
- `install.sh` 可重入、参数化域名与端口；`backup.sh` 不再把密钥与数据库放在同一备份目录，并按保留天数清理。
