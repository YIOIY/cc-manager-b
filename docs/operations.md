# 运维手册

适用于当前实现（Go 单二进制 + SQLite + 内嵌前端）。设计目标见 [总体方案](architecture.md) 9.3。

## 1. 安装1

### 1.1 构建发布包

```bash
make release
```

生成 `dist/cc-manager-<version>-<os>-<arch>.tar.gz`，包含二进制、`web/dist`、`deploy/production` 与 `SHA256SUMS`。在目标机器解压到 `/opt/cc-manager/releases/<version>/`。

### 1.2 首次安装（Ubuntu + systemd + nginx）

1. 复制 `deploy/production/cc-manager.env.example` 为任意路径的环境文件，填写：
   - `CC_MANAGER_ENCRYPTION_KEY`：`openssl rand -base64 32`
   - `CC_MANAGER_ADMIN_PASSWORD_HASH`：在本机执行 `cc-manager hash-password`，输入密码后把输出粘贴进去
   - `CC_MANAGER_TRUST_PROXY=1`（有 nginx/Cloudflare 终止 TLS 时）
2. 执行安装脚本：

```bash
sudo SERVER_NAME=cc.example.com APP_PORT=18080 NGINX_PORT=8088 ./deploy/production/install.sh /opt/cc-manager/releases/<version> /path/to/cc-manager.env
```

脚本会创建 `cc-manager` 系统用户、写入 `/etc/cc-manager/cc-manager.env`（0600）、切换 `/opt/cc-manager/current` 软链接、安装 systemd 单元与 nginx 配置、重启服务并检查 `/healthz`。

### 1.3 升级

解压新版本到新的 releases 目录后，再次运行同一条 `install.sh` 命令，**不传环境文件参数**。已存在的环境文件会被保留（加密密钥不能改变），软链接原子切换，服务重启。升级前先执行一次备份。

### 1.4 本地 Docker（开发自测）

```bash
cp deploy/.env.example deploy/.env   # 填写密钥与密码
docker compose -f deploy/docker-compose.yml up -d --build
```

容器只读根文件系统，数据卷挂载在 `/data`，监听 `127.0.0.1:8080`，需自行放置反向代理。

### 1.5 Docker 发布（生产方式）

在自己电脑上运行 `scripts/release.sh`：跑测试、构建 linux/amd64 镜像，推送到 GHCR（`ghcr.io/<owner>/cc-manager:sha-<12 位提交号>`），再把 `:prod` 指向它。服务器上的 `cc-manager-update.timer` 每 2 分钟检查一次 `:prod`，发现新版本就调用部署脚本完成备份、切换、健康检查，失败自动回滚。服务器不需要源码，GitHub Actions 只负责跑测试。

```
本机 release.sh ─▶ make test ─▶ 构建镜像 ─▶ 推送 sha-<提交号> ─▶ :prod 指向它
                                                                 │
服务器 timer（每 2 分钟）─▶ :prod 的版本 ≠ 当前版本 ? ─▶ docker-deploy.sh
docker-deploy.sh: 在线备份 SQLite ─▶ 停旧容器 ─▶ 启动新镜像
                  ─▶ /healthz 返回 ok 且 version == tag ? 记录完成 : 回滚
回滚 = 停新容器 +（仅在新版本做过迁移时）从本次备份恢复数据库 + 启动上一个镜像
```

#### 一次性准备

1. **本机推送权限**（需要 `write:packages`）：
   ```bash
   gh auth refresh -h github.com -s write:packages
   gh auth token | docker login ghcr.io -u <github 用户名> --password-stdin
   ```
2. **服务器拉取凭证**：在 GitHub → Settings → Developer settings → Personal access tokens (classic) 新建令牌，**只勾选 `read:packages`**。GHCR 目前只接受 classic 令牌。它保存在服务器 root 的 Docker 配置里，只能读取镜像。
3. **服务器**（Ubuntu，保留现有 `/etc/cc-manager/cc-manager.env` 与 `/var/lib/cc-manager`）：
   ```bash
   sudo apt-get install -y docker.io docker-compose-v2 curl python3
   # 把仓库的 deploy/production 目录拷到服务器后：
   printf '%s' '<read:packages 令牌>' | sudo ./deploy/production/install-docker.sh ghcr.io/<owner>/cc-manager <github 用户名>
   ```
   脚本把 `compose.yml`、`docker-deploy.sh`、`docker-update.sh`、`backup.sh` 安装到 `/opt/cc-manager/docker`（root 所有），用令牌执行 `docker login`，安装并启动 `cc-manager-update.timer`。如果服务器上还有旧方案的 CI 部署入口（`cc-deploy` 用户、它的 sudo 规则和 sshd 配置块），会一并删除。`:prod` 还不存在时定时器什么都不做。

   安装前它会检查环境文件和代理池（只报告键名，不打印值）：
   - **环境文件里含 `$` 的值必须用单引号包住**，例如 `CC_MANAGER_ADMIN_PASSWORD_HASH='$argon2id$v=19$…'`。Docker Compose 会展开未加引号的 `$`，哈希会被改坏。存在这种值时安装和部署都会拒绝继续。
   - 如果 `CC_MANAGER_SUPPLIER_PROXY` 或代理池里的代理指向 `127.0.0.1`/`localhost`，会给出警告：容器里的回环地址是容器自己，切换后要改成 `host.docker.internal`（compose 已把它映射到宿主机），并让宿主机上的代理监听 Docker 网桥地址（如 `172.17.0.1`）或 `0.0.0.0`（配合防火墙）。

#### 首次切换（从 systemd）

第一次部署时，脚本发现还没有 Docker 记录，会先备份数据库、停止 systemd 的 `cc-manager` 服务、把 `/var/lib/cc-manager` 交给容器用户（UID 10001）、启动容器。健康检查通过后禁用 systemd 服务；失败则交回目录所有权、重新启动 systemd 服务，线上保持原状。nginx 与 Cloudflare Tunnel 不需要改动（容器仍监听 `127.0.0.1:18080`）。

**回滚时数据库何时恢复**：只有新版本已经执行了数据库迁移（`schema_migrations` 版本变高）才会用部署前的备份覆盖数据库，因为旧版本无法在新结构上运行；这种情况下新版本运行期间的写入会丢失，请在任务中心核对 `unknown` 状态的交付。结构没变时保留数据库原样（包括新版本已经提交的账号），日志写明 `database kept`。

#### 日常

- **上线**：提交并推送代码，然后在本机运行 `./scripts/release.sh`。2 分钟内服务器自动更新；在服务器上 `journalctl -u cc-manager-update -f` 可以看到过程。
- **只推镜像不上线**：`./scripts/release.sh --no-prod`，之后用 `--promote` 上线。
- **回退或切到指定版本**：`./scripts/release.sh --promote sha-0123456789ab`。只移动 `:prod`，不重新构建。数据库若已被新版本迁移，回退旧镜像前请先用该次部署前的备份恢复（见第 4 节），因为迁移只能向前。
- **失败的版本不会反复重试**：部署失败并回滚后，该版本记在 `state/failed-tag`，定时器不再尝试，直到 `:prod` 指向别的版本。修复后发布新提交即可；确实要重试同一版本，在服务器上删除 `state/failed-tag`。
- **暂停自动更新**：`sudo systemctl stop cc-manager-update.timer`（恢复用 `start`）。
- **部署记录**：`/opt/cc-manager/docker/state/deploys.log`（时间、标签、上一标签、备份目录）；当前标签在 `state/current-tag`；定时器的判断记录在 `state/update.log`。
- **日志**：`docker logs -f cc-manager`（单文件 20 MB，保留 5 个）。
- **备份**：每次部署前自动生成 `/var/backups/cc-manager/<时间>-pre-<tag>/`；定期备份仍按第 4 节。
- **失败详情**：`/opt/cc-manager/docker/state/last-deploy.log`（含容器最后 80 行日志）。
- **更新服务器上的部署脚本**：仓库里改了 `deploy/production/` 下的脚本、`compose.yml` 或定时器后，把目录拷到服务器重新运行一次 `install-docker.sh`（令牌已保存，不必再传，命令末尾的用户名仍需要）。
- **不要再运行 `install.sh`**：它会重新启用 systemd 服务，开机后与容器争抢 18080 端口。如需退回 systemd，先停定时器和容器（`systemctl disable --now cc-manager-update.timer; docker rm -f cc-manager`），把 `/var/lib/cc-manager` 交还 `cc-manager` 用户（`chown -R cc-manager:cc-manager`），再 `systemctl enable --now cc-manager`。
- **镜像存储**：每次发布都会在 GHCR 留下一个 `sha-*` 镜像。服务器只保留最近 3 个（`KEEP_IMAGES`）；GHCR 上的旧版本在 GitHub → Packages → cc-manager → Package settings 中按需删除，至少保留当前和上一个标签以便回退。
- **注意**：会话保存在内存中，每次部署后所有人需要重新登录。

## 2. 配置项

完整列表见根 README「配置项」。启动时统一校验，错误会直接退出并打印原因。

## 3. 健康检查与日志

- `GET /healthz`：返回 `ok`、`version`、`database`、各后台 worker 的最近心跳；数据库不可用时返回 503。
- 日志为 JSON（stderr），每条 HTTP 日志含 `request_id`、方法、路径、状态码、耗时、来源 IP；5xx 另有 `request failed` 记录。Docker 部署用 `docker logs -f cc-manager` 查看，systemd 部署用 `journalctl -u cc-manager -f`。
- 500 响应头带 `X-Request-ID`，用户反馈时可据此检索日志。

## 4. 备份与恢复

```bash
sudo ./deploy/production/backup.sh
```

- 使用 SQLite 在线备份 API，备份后做两次 `integrity_check`，输出到 `/var/backups/cc-manager/<时间戳>/`。
- **环境文件（含主密钥）不会复制到备份目录。** 请单独把 `/etc/cc-manager/cc-manager.env` 备份到访问受控的位置；没有密钥的数据库无法解密任何凭证。
- `RETENTION_DAYS`（默认 30）控制旧备份清理。

恢复步骤：停止服务 → 用备份的 `cc-manager.db` 覆盖 `/var/lib/cc-manager/cc-manager.db`（删除旧的 `-wal/-shm`）→ 确认环境文件中的密钥与备份时一致 → 启动服务 → 在任务中心核对状态为 `unknown` 的交付并人工核实平台记录。

Docker 部署时的对应命令（`<tag>` 取 `state/current-tag`，若要回到旧版本则用与备份对应的标签）：

```bash
cd /opt/cc-manager/docker
export IMAGE=$(cat image) TAG=$(cat state/current-tag) HOST_TZ=$(cat /etc/timezone 2>/dev/null || echo UTC)
sudo -E docker compose -p cc-manager -f compose.yml down
sudo rm -f /var/lib/cc-manager/cc-manager.db-wal /var/lib/cc-manager/cc-manager.db-shm
sudo cp /var/backups/cc-manager/<备份目录>/cc-manager.db /var/lib/cc-manager/cc-manager.db
sudo chown -R 10001:10001 /var/lib/cc-manager
sudo -E docker compose -p cc-manager -f compose.yml up -d
```

如果换了标签，把新标签写入 `state/current-tag`，下一次部署才会以它为回滚目标。

## 5. 用户、密码与密钥

- 首次启动时用户表为空，用环境变量里的用户名和密码（或哈希）创建第一个管理员；此后环境变量中的密码不再生效。
- 日常管理在「用户与权限」页面完成：「角色」标签页按模块勾选权限新建或调整角色（内置管理员角色不可改）；「用户」标签页新增用户、绑定一个或多个角色、停用、重置密码。任何用户可在侧栏修改自己的密码。角色权限改动对下一次请求即生效，无需重新登录。
- 管理员忘记密码或被误停用时，在服务器上用同一环境文件执行（服务无需停止）：

```bash
cc-manager reset-password admin                        # systemd 部署
sudo docker exec -it cc-manager /app/cc-manager reset-password admin   # Docker 部署
```

  命令从标准输入读取新密码，重置后该用户会被重新启用，所有已有会话失效，并记录 `user.password_reset` 审计事件。
- 密钥轮换（已支持）：
  1. 生成新密钥 `openssl rand -base64 32`。
  2. 在环境文件中把旧值移到 `CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS`，新值写入 `CC_MANAGER_ENCRYPTION_KEY`，重启服务。此时旧密文仍可读取，新写入已使用新密钥。
  3. 执行 `cc-manager rotate-key --check` 查看待重加密行数，再执行 `cc-manager rotate-key`（同一环境文件，服务可继续运行；命令在单个事务内完成，任何一行无法解密都会整体回滚）。Docker 部署时改完环境文件需要重建容器才能读到新值：`sudo -E docker compose -p cc-manager -f /opt/cc-manager/docker/compose.yml up -d --force-recreate`（先按上面 export `IMAGE`、`TAG`），然后用 `sudo docker exec cc-manager /app/cc-manager rotate-key --check` / `rotate-key` 执行。
  4. 确认 `rotate-key --check` 各表为 0 后，删除 `CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS` 并重启。
  - 密文格式为 `k1:<key-id>:<base64>`，旧格式（无前缀）在轮换时自动升级；审计日志记录 `security.key_rotation`。
- 泄露处置：立即更换平台 OpenAPI Token（平台侧）、代理密码，再按上述步骤轮换主密钥。

## 6. 常见异常

| 现象 | 处理 |
| --- | --- |
| 登录返回 429 | 同一 IP+用户名多次失败后的退避，最长 15 分钟；nginx 也有 6 次/分钟限制 |
| 账号卡片长期「上号中」 | 查看任务中心对应任务的结果列；重启后进行中的任务会标为「结果待确认」，需在平台核实 |
| 账单任务 `CONNECTION_CHANGED` | 任务创建后平台连接的时区或版本变化，重新创建任务 |
| `/healthz` 返回 503 | 数据库文件权限或磁盘问题，检查 `StateDirectory` |
| 容器内账单同步报时区错误 | 镜像已含 tzdata 且二进制内嵌 tzdata；检查连接时区是否为合法 IANA 名称 |
| 仪表盘「网页同步」显示 `WEB_LOGIN_FAILED` | 供应商后台密码变了或被平台锁定。到平台连接里重新填写网页登录密码并点「测试登录」 |
| 仪表盘「网页同步」显示 `WEB_SUSPENDED` | 登录连续失败 3 次后系统主动暂停，避免把后台账号刷到锁定。重新保存密码或「测试登录」成功即恢复 |
| 仪表盘「网页同步」显示 `WEB_ENDPOINT_MISSING` | 平台改版，网页接口路径变了；OpenAPI 同步不受影响，联系维护者更新 `internal/supplier/web.go` |
| 账号卡片提示「平台已移除」 | 平台列表里不再有这条交付；本地账号和历史数据保留，到「平台对账」里核对 |

## 6.1 指标

`GET /metrics` 输出 Prometheus 文本格式，可直接被 Prometheus / Grafana Agent 抓取（服务只监听回环地址，抓取器需部署在同机或经 nginx 加 Basic Auth 转发）。指标族：`cc_manager_http_requests_total{class}`、`cc_manager_http_request_duration_seconds_{sum,count}`、`cc_manager_http_inflight`、`cc_manager_sessions`、`cc_manager_uptime_seconds`、`cc_manager_worker_last_tick_seconds{worker}`、`cc_manager_jobs{state}`、`cc_manager_sync_runs{kind,state}`、`cc_manager_accounts{archived}`、`cc_manager_proxies{enabled}`、`cc_manager_users{enabled}`。建议告警：`time() - cc_manager_worker_last_tick_seconds > 300`（后台循环停转）、`cc_manager_jobs{state="failed"}` 持续增长、5xx 占比。

## 6.2 供应商网页只读通道

在「平台连接」里填写供应商网页后台的登录账号和密码后，系统会自动登录后台读取：账号列表与状态（每 10 分钟）、封禁/验证失败原因、每个账号的用量块（5 小时/7 天窗口占比、累计 Token、累计花费）、按模型的每日账单（每 2 小时，最近 7 天）。平台上有、本地没有的账号会自动建成本地账号（标「来自平台」，没有密码和凭证）；同一邮箱换了新交付 ID 时本地账号自动跟到最新的那条，旧记录在「平台对账」里可查。

- 密码用主密钥加密保存在 `platform_connections.web_password_secret`，会话令牌只在内存里（5 小时），重启后重新登录一次。
- 除下面这一项外只调用读取接口，不会修改平台上的任何数据。
- **修改平台资料（2026-11-12 起）**：账号卡片上的「修改平台资料」按钮（需要 `accounts.platform_edit` 权限，默认只有管理员有；运营角色需在「用户与权限」里单独授予）会把套餐、注册/套餐日期、注册地区、余量、支付方式、设备、充值日期、质保期、备注写回供应商平台。弹窗分两步：先改，再逐项对照"平台当前 → 新值"后提交；提交后系统立即重读平台列表核对并刷新快照，再把本地账号同步成同样的值，审计记录 `account.platform_profile_update`。平台记录在提交前被别人改过会提示"重新打开"；网页登录未配置或暂停时按钮不显示。密码、凭证、标签、代理不会被改动。
- 服务启动后 30 秒先跑一次账号同步、60 秒跑一次账单同步，之后按周期进行。整轮失败（登录、列表读不到）时按连接退避（2 分钟翻倍到 60 分钟），个别账号的原因读不到只记为部分成功、不退避；登录连续失败 3 次暂停该连接的网页通道，仪表盘和连接表单会提示。
- 用量快照只在数值变化时记录，保留 90 天；`sync_runs` 里的 `web-accounts`、`web-bills` 记录与指标同步同样保留 7 天。
- 手动触发一次账单同步：账单页「按模型消费」标签里的「立即同步」，或 `POST /api/v1/platform-usage/bills/models/sync`。

## 6.3 汇率（卡片上的美元金额）

账号卡片把平台的人民币花费同时显示成美元：`美元 = 人民币 ÷ 汇率`。这里的汇率是**供应商平台的固定汇率**，不是市场汇率：平台按美元原价计费，再乘固定汇率折成人民币（平台账单页注明「汇率已乘 7.2」「2026年9月汇率由7.3调整为7.2」），平台后台自己的卡片也是按 `cost ÷ 7.2` 显示美元。

- 平台接口不返回汇率，所以汇率保存在每条平台连接上（`platform_connections.usd_cny_rate`，迁移 v12，默认 7.2），在「平台设置 → 修改 → 美元汇率」里维护。平台再调整汇率时改这一个值即可，修改会写审计。
- 系统不访问任何外部汇率服务。2026-11-13 曾短暂上线过「自动取市场参考汇率」的做法（迁移 v11 的 `exchange_rates` 表），口径不对，v12 已移除。
- 汇率只用于显示，不参与账单和对账，金额仍以人民币为准。

## 6.4 用户之声

任何登录用户都可以从页面顶部的「反馈」按钮（或报错提示里的「反馈这个问题」）提交问题和建议，并在「用户之声」页面看到自己提交的内容、状态和回复。数据在 `feedback` 表（迁移 v13）。

- 处理反馈需要权限 `feedback.manage`（「用户与权限」里的「处理用户反馈」），默认只有管理员有。有这项权限的人能看到全部反馈，侧边栏会显示待处理的数量。
- 每条反馈自动附带：所在页面的路径（不含查询参数）、浏览器信息、系统版本号；从报错处提交时还有错误码和请求编号，可以拿请求编号去日志里对应。不会附带表单内容、搜索词、密码或凭证。
- 没有通知：提交人要回到「用户之声」查看回复。提交和处理都会写审计日志。

## 7. 安全基线

- 服务只监听回环地址，由 nginx/Cloudflare 提供 TLS 与公网入口；`CC_MANAGER_TRUST_PROXY=1` 仅在该拓扑下开启。
- systemd 单元已启用 `ProtectSystem=strict`、`SystemCallFilter=@system-service`、`CapabilityBoundingSet=` 等限制；不要以 root 运行。
- 登录接口按 IP + 用户名指数退避；登录后的写请求按用户令牌桶限速（`CC_MANAGER_WRITE_RATE_PER_MINUTE`，默认 120/分钟，突发 30），防止单个用户通过代理检测、导入等接口打满供应商。两者与会话一样都在进程内存中，因此服务目前只支持单实例部署，重启后会话全部失效。
- 仓库不提交 `.env`、`*.pem`、数据库文件和部署验收记录；真实配置只存在于目标机器。
