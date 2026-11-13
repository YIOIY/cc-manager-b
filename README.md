# CC Manager

面向 Claude Code Max 账号的独立上号与运营管理工具。

目标：通过 Web 页面填写或导入账号，管理授权凭证和代理池，向供应商平台提交账号，并查看逐账号状态、Token 用量和金额消耗。

## 项目状态

**当前为开发中的原型，真实供应商联调尚未完成。** 已有 Go/SQLite API、Vue 管理界面和供应商 OpenAPI 客户端，已实现账号表单、JSON 导入、代理检测、任务执行、指标/账单采集、供应商网页后台只读同步（账号状态、用量、按模型账单、自动建号）、审计日志、登录限速与优雅关闭。公网部署前请先阅读 [运维手册](docs/operations.md) 的安全章节。

第一版聚焦已有 Claude 账号的管理与交付，不包含模型 API 网关、自动注册账号、购买订阅或支付。

“新建并上号”支持浏览器 OAuth 授权：选择代理 → 生成 Claude 授权链接 → 登录并粘贴授权码或回调地址 → 自动填入可获取的凭证与资料。密码、套餐起止日期及供应商设备标识仍需补充。授权流程已通过本地模拟测试，真实账号与供应商接收仍待联调，详见 [授权录入说明](docs/claude-oauth-onboarding.md)。

## 文档

从 [文档导航](docs/README.md) 开始。常用入口：

| 文档 | 内容 |
| --- | --- |
| [总体与后端方案](docs/architecture.md) | 边界、技术选型、服务模块、数据模型、任务与同步 |
| [前端详细方案](docs/frontend-design.md) | 页面、表单、交互、状态、组件 |
| [本地 API 契约](docs/api-contract.md) | `/api/v1` 接口、类型、权限、幂等和错误约定 |
| [供应商平台对接说明](docs/platform-contract.md) | 已核实的上游接口、字段、限制 |
| [运维手册](docs/operations.md) | Docker 发布（1.5）、安装、升级、配置项、备份恢复、密钥与密码、日志 |
| [优化项清单](docs/optimization-backlog.md) | 已完成与待办的工程优化 |
| [变更记录](CHANGELOG.md) | 版本变更 |

## 本地运行

需要 Go 1.26、Node.js 24。

```bash
make web-install web-build
```

```bash
export CC_MANAGER_ADMIN_PASSWORD='at-least-12-characters'
export CC_MANAGER_ENCRYPTION_KEY="$(openssl rand -base64 32)"
go run ./cmd/cc-manager
```

默认监听 `127.0.0.1:8080`，浏览器打开 <http://127.0.0.1:8080>。数据库默认写入当前目录的 `cc-manager.db`，可用 `CC_MANAGER_DB` 修改。前端开发时在 `web` 目录运行 `npm run dev`，Vite 会把 `/api` 代理到 `VITE_API_TARGET`（默认 `http://127.0.0.1:8080`）。

常用命令：

```bash
make test
```

```bash
make lint
```

```bash
make release
```

端到端测试（先 `make build`，再用真实二进制跑 Playwright；首次需 `cd web && npx playwright install chromium`）：

```bash
make web-e2e
```

## 配置项

全部通过环境变量提供，启动时一次性解析并校验：

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `CC_MANAGER_LISTEN` | 监听地址 | `127.0.0.1:8080` |
| `CC_MANAGER_DB` | SQLite 文件路径 | `cc-manager.db` |
| `CC_MANAGER_WEB_DIR` | 前端构建产物目录 | `web/dist` |
| `CC_MANAGER_ADMIN_USER` | **首个**管理员用户名，仅在用户表为空时用于创建 | `admin` |
| `CC_MANAGER_ADMIN_PASSWORD_HASH` | 首个管理员密码的 argon2id 哈希，由 `cc-manager hash-password` 生成（推荐） | 无 |
| `CC_MANAGER_ADMIN_PASSWORD` | 首个管理员明文密码，至少 12 位；设置了哈希时忽略。用户表建立后不再读取，忘记密码用 `cc-manager reset-password <用户名>` | 无 |
| `CC_MANAGER_ENCRYPTION_KEY` | 32 字节 base64 主密钥（当前写入密钥）；缺失时拒绝写入任何凭证 | 无 |
| `CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS` | 逗号分隔的旧密钥，用于读取尚未轮换的密文；轮换完成后可移除 | 无 |
| `CC_MANAGER_TRUST_PROXY` | 为 `1` 时信任 `X-Forwarded-Proto/Host/For`，用于反向代理后标记 Secure Cookie 与限速 | `0` |
| `CC_MANAGER_WRITE_RATE_PER_MINUTE` | 每个登录用户每分钟允许的写请求数（POST/PUT/PATCH/DELETE），突发额度为其四分之一（至少 5）；超限返回 `429 RATE_LIMITED`，`0` 关闭 | `120` |
| `CC_MANAGER_LOG_LEVEL` | `debug` / `info` / `warn` / `error` | `info` |
| `CC_MANAGER_BILL_MAX_ITEMS` | 单次账单同步任务的账号日上限 | `10000` |
| `CC_MANAGER_BILL_INTERVAL_MS` | 账单请求之间的最小间隔 | `1000` |
| `CC_MANAGER_SUPPLIER_TLS_PINS` | `{"host:port":"sha256-hex"}`，对特定供应商端点固定证书 | 无 |
| `CC_MANAGER_SUPPLIER_PROXY` | 访问供应商（上号、账单、指标）使用的代理，`http://`、`https://`、`socks5://` 或 `socks5h://` 加主机端口。供应商请求**不再读取** `HTTP_PROXY` / `HTTPS_PROXY`；格式错误时服务拒绝启动 | 无（直连） |
| `CC_MANAGER_SUPPLIER_INSECURE_TLS` | 为 `1` 时跳过供应商证书校验（仅本地联调） | `0` |

## 用户与权限（RBAC）

- **权限目录**按模块细分到动作，共 34 项，例如 `accounts.create`、`accounts.onboard`、`accounts.credentials`、`proxies.test`、`bills.query`、`connections.update`、`users.reset_password`、`roles.manage`。后端路由对每个接口校验对应权限，无权限返回 `403 PERMISSION_DENIED`；前端只根据会话里的权限列表隐藏页面和按钮。
- **角色**保存在数据库，由任意权限组合而成，可随时新建、改权限、删除；内置三个预设：`管理员`（超级角色，权限不可改，自动拥有以后新增的权限）、`运营`、`只读`（后两者可按需调整权限）。
- **页面可见性**由每个模块的 `*.view` 权限决定：没有该权限的用户在导航里看不到页面，直接访问会被跳转，接口也返回 403。角色编辑器顶部用开关逐页控制，关闭页面会一并移除页内动作。
- **用户**可绑定多个角色，有效权限为并集；角色改动对下一次请求立即生效，停用或重置密码会立刻吊销会话。
- **防越权**：非管理员不能授予管理员角色，不能创建或修改含有自己没有的权限的角色，不能修改/停用/删除管理员用户；系统始终保留至少一个启用的管理员；不能停用、降级或删除自己。
- 首个管理员在用户表为空时由环境变量创建；忘记密码用 `cc-manager reset-password <用户名>`。

## 安全模型

- 登录会话保存在进程内，重启后失效；认证会话 12 小时，匿名会话 30 分钟，总量有上限并定期清理。
- 登录失败按「来源 IP + 用户名」指数退避，响应 `429` 与 `Retry-After`；失败与成功都写审计日志。
- 所有写请求校验 Origin 与 CSRF Token；API 响应 `Cache-Control: no-store`，并附带基础安全响应头与请求 ID。
- 凭证、密码、Token 使用 AES-GCM 加密，AAD 绑定资源 ID，已保存凭证的查询接口不回传原文；本次 OAuth 新获取的凭证仅回填发起授权的当前表单，等待用户提交；密文带密钥 ID 前缀（`k1:<kid>:…`），`cc-manager rotate-key` 可在不停机以外的时间窗口重加密全部数据（见运维手册）。
- 请求体有大小上限；数据库唯一约束冲突返回 `409`，其余存储错误返回 `500` 并记录带 `request_id` 的日志。
- 收到 `SIGTERM` 后停止接收新任务、等待进行中的供应商请求结束、执行 WAL checkpoint 再退出。

## 代理池检测网络

在代理池的“网络设置”中选择直连或前置代理。直连模式直接拨号池中代理，TUN/VPN 是否接管由系统路由决定；不会自动读取 macOS 系统代理或叠加环境变量代理。前置代理模式的链路为“本地应用 → 前置代理 → 池中代理 → 检测网站”，前置地址可配置为本机客户端，例如 `http://127.0.0.1:26800`。需要境外来源 IP 时，请确保本机客户端对目标代理的连接选择境外节点。

目标代理和前置代理均支持 HTTP、HTTPS、SOCKS5、SOCKS5H。HTTPS 保留证书验证。前置认证密码加密保存，接口不回显，更新时密码留空保留原值，勾选清除才移除；网络设置支持版本冲突检查。

本机检测先通过目标代理访问 `https://example.com/`，仅 2xx 算通过，不跟随重定向；然后通过同一路径请求 `https://api.ipify.org?format=json` 获取目标出口 IP。总超时可配置为 5–60 秒（默认 20 秒）。检测记录包含连接模式、耗时、出口 IP、失败阶段和错误码，每个代理保留最近 100 次。

本机检测是手动诊断，不阻断账号创建及供应商上号。供应商收到的是账号绑定的真实 `auth_json.proxy_url`；本机前置代理不会写入账号凭证，也不会提交给供应商。

## 账单查询与同步

已实现连接、多账号、业务日期筛选，服务端分页与全结果合计，覆盖统计、持久补采任务、按日/100 账号拆批、失败重试、去重和历史值保护。`metrics/query-keys` 不接入。详细接口与验证方法见 [账单实施记录](docs/bill-query-implementation-report.md)。
