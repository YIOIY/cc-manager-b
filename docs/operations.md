# 运维手册

适用于当前实现（Go 单二进制 + SQLite + 内嵌前端）。设计目标见 [总体方案](architecture.md) 9.3。

## 1. 安装

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

### 1.5 Docker 持续交付（推荐的生产方式）

推送到 `main` 后，GitHub Actions 依次运行后端、前端、端到端测试，构建镜像并推送到 GHCR（`ghcr.io/<owner>/cc-manager:sha-<12 位提交号>` 与 `:main`），然后等待你在 GitHub 上批准 `production` 部署；批准后通过 SSH 让服务器执行部署脚本。服务器不再需要源码或手工上传发布包。

```
push main ─▶ 测试 ─▶ 构建并推送镜像 ─▶ 等待批准 ─▶ SSH: deploy <tag>
                                                     │
服务器 docker-deploy.sh: 拉取镜像 ─▶ 在线备份 SQLite ─▶ 停旧服务 ─▶ 启动新镜像
                         ─▶ /healthz 返回 ok 且 version == tag ? 记录完成 : 回滚
回滚 = 停新容器 + 从本次备份恢复数据库 + 启动上一个镜像（首次切换时启动原 systemd 服务）
```

#### 一次性准备

1. **代码仓库**：在 GitHub 建私有仓库，`git remote add origin …` 后推送 `main`。
2. **部署密钥**（在自己电脑上生成，私钥只放进 GitHub）：
   ```bash
   ssh-keygen -t ed25519 -N '' -C cc-manager-ci -f ./ci_deploy
   ```
3. **服务器**（Ubuntu，保留现有 `/etc/cc-manager/cc-manager.env` 与 `/var/lib/cc-manager`）：
   ```bash
   sudo apt-get install -y docker.io docker-compose-v2 curl python3
   # 从仓库拷贝 deploy/production 目录与 ci_deploy.pub 到服务器后：
   sudo ./deploy/production/install-docker.sh ghcr.io/<owner>/cc-manager ./ci_deploy.pub
   ```
   脚本把 `compose.yml`、`docker-deploy.sh`、`backup.sh`、`ci-entry.sh` 安装到 `/opt/cc-manager/docker`（root 所有），创建 `cc-deploy` 用户：它的 SSH 密钥被绑定为只能执行 `ci-entry.sh`，sudo 只允许执行 `docker-deploy.sh`。脚本**不会**切换流量。

   安装前它会检查环境文件和代理池（只报告键名，不打印值）：
   - **环境文件里含 `$` 的值必须用单引号包住**，例如 `CC_MANAGER_ADMIN_PASSWORD_HASH='$argon2id$v=19$…'`。Docker Compose 会展开未加引号的 `$`，哈希会被改坏。存在这种值时安装和部署都会拒绝继续。
   - 如果 `CC_MANAGER_SUPPLIER_PROXY` 或代理池里的代理指向 `127.0.0.1`/`localhost`，会给出警告：容器里的回环地址是容器自己，切换后要改成 `host.docker.internal`（compose 已把它映射到宿主机），并让宿主机上的代理监听 Docker 网桥地址（如 `172.17.0.1`）或 `0.0.0.0`（配合防火墙）。
4. **GitHub 配置**：Settings → Environments → 新建 `production`，Required reviewers 选自己，添加 secrets：

   | Secret | 内容 |
   | --- | --- |
   | `DEPLOY_HOST` | 服务器地址 |
   | `DEPLOY_PORT` | SSH 端口（可选，默认 22） |
   | `DEPLOY_USER` | `cc-deploy` |
   | `DEPLOY_SSH_KEY` | `ci_deploy` 私钥全文 |
   | `DEPLOY_KNOWN_HOSTS` | `ssh-keyscan -t ed25519 <服务器地址>` 的输出 |

   GHCR 镜像默认私有，无需额外配置：部署时用该次任务自带的临时令牌拉取，用完即登出，服务器上不保存长期凭证。

#### 首次切换

第一次批准部署时，脚本发现还没有 Docker 记录，会先备份数据库、停止 systemd 的 `cc-manager` 服务、把 `/var/lib/cc-manager` 交给容器用户（UID 10001）、启动容器。健康检查通过后禁用 systemd 服务；失败则交回目录所有权、重新启动 systemd 服务，线上保持原状。

**回滚时数据库何时恢复**：只有新版本已经执行了数据库迁移（`schema_migrations` 版本变高）才会用部署前的备份覆盖数据库，因为旧版本无法在新结构上运行；这种情况下新版本运行期间的写入会丢失，请在任务中心核对 `unknown` 状态的交付。结构没变时保留数据库原样（包括新版本已经提交的账号），日志写明 `database kept`。nginx 与 Cloudflare Tunnel 不需要改动（容器仍监听 `127.0.0.1:18080`）。旧的 `/opt/cc-manager/releases` 保留，可在必要时手工回到 systemd。

#### 日常

- **上线**：合并到 `main` → Actions 页面批准 `deploy`。
- **回退到旧版本**：Actions → CI → Run workflow，`tag` 填旧标签（如 `sha-0123456789ab`），批准即可。数据库若已被新版本迁移，回退旧镜像前请先用该次部署前的备份恢复（见第 4 节），因为迁移只能向前。
- **部署记录**：`/opt/cc-manager/docker/state/deploys.log`（时间、标签、上一标签、备份目录）；当前标签在 `state/current-tag`。
- **日志**：`docker logs -f cc-manager`（单文件 20 MB，保留 5 个）。
- **备份**：每次部署前自动生成 `/var/backups/cc-manager/<时间>-pre-<tag>/`；定期备份仍按第 4 节。
- **失败详情**：`/opt/cc-manager/docker/state/last-deploy.log`（含容器最后 80 行日志；Actions 页面只显示步骤摘要，不会输出应用日志）。
- **更新服务器上的部署脚本**：脚本由管理员安装，CI 无法改写；仓库里改了 `deploy/production/*.sh` 或 `compose.yml` 后，在服务器上重新运行一次 `install-docker.sh`。
- **不要再运行 `install.sh`**：它会重新启用 systemd 服务，开机后与容器争抢 18080 端口。如需退回 systemd，先 `sudo docker rm -f cc-manager`，把 `/var/lib/cc-manager` 交还 `cc-manager` 用户（`chown -R cc-manager:cc-manager`），再 `systemctl enable --now cc-manager`。
- **镜像存储**：每次推送 `main` 都会在 GHCR 留下一个 `sha-*` 镜像。服务器只保留最近 3 个（`KEEP_IMAGES`）；GHCR 上的旧版本在 GitHub → Packages → cc-manager → Package settings 中按需删除，至少保留当前和上一个标签以便回退。
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

## 6.1 指标

`GET /metrics` 输出 Prometheus 文本格式，可直接被 Prometheus / Grafana Agent 抓取（服务只监听回环地址，抓取器需部署在同机或经 nginx 加 Basic Auth 转发）。指标族：`cc_manager_http_requests_total{class}`、`cc_manager_http_request_duration_seconds_{sum,count}`、`cc_manager_http_inflight`、`cc_manager_sessions`、`cc_manager_uptime_seconds`、`cc_manager_worker_last_tick_seconds{worker}`、`cc_manager_jobs{state}`、`cc_manager_sync_runs{kind,state}`、`cc_manager_accounts{archived}`、`cc_manager_proxies{enabled}`、`cc_manager_users{enabled}`。建议告警：`time() - cc_manager_worker_last_tick_seconds > 300`（后台循环停转）、`cc_manager_jobs{state="failed"}` 持续增长、5xx 占比。

## 7. 安全基线

- 服务只监听回环地址，由 nginx/Cloudflare 提供 TLS 与公网入口；`CC_MANAGER_TRUST_PROXY=1` 仅在该拓扑下开启。
- systemd 单元已启用 `ProtectSystem=strict`、`SystemCallFilter=@system-service`、`CapabilityBoundingSet=` 等限制；不要以 root 运行。
- 登录接口按 IP + 用户名指数退避；登录后的写请求按用户令牌桶限速（`CC_MANAGER_WRITE_RATE_PER_MINUTE`，默认 120/分钟，突发 30），防止单个用户通过代理检测、导入等接口打满供应商。两者与会话一样都在进程内存中，因此服务目前只支持单实例部署，重启后会话全部失效。
- 仓库不提交 `.env`、`*.pem`、数据库文件和部署验收记录；真实配置只存在于目标机器。
