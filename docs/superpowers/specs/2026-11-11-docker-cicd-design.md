# Docker 化持续交付（简短设计）

日期：2026-11-11。状态：已确认（用户“开始改造”）。

## 目的

上线不再依赖本机打包和手动 SFTP 上传源码或发布包：推送到 main 后由 GitHub Actions 测试、构建镜像并推送到 GHCR，经人工批准后自动登录服务器拉取镜像、备份、切换、健康检查，失败自动回滚。

## 约束

- 加密密钥和管理员凭证只在服务器 `/etc/cc-manager/cc-manager.env`，绝不进入镜像或 CI。
- 数据库迁移只能向前：回滚必须连同数据库一起恢复到部署前备份。
- 服务器现有 nginx + Cloudflare Tunnel 不变；应用仍只监听 `127.0.0.1:18080`。
- CI 的部署密钥只能触发部署，不能改写服务器上的部署脚本（脚本由管理员一次性安装）。

## 方案取舍

- 镜像仓库：(选定) GHCR——仓库就在 GitHub，服务器在海外拉取无障碍，可用每次任务自带的临时令牌拉取私有镜像；(否) 国内镜像仓库——服务器不在国内，徒增凭证管理。
- 部署触发：(选定) CI 经 SSH 调用服务器脚本，由手动运行工作流触发（原定 `production` 环境人工批准，但免费计划的私有仓库不支持 Required reviewers，2026-11-11 改为手动触发）；(否) 服务器自动轮询更新（Watchtower）——无法在切换前备份、无法按数据库回滚。
- 脚本更新：(选定) 管理员一次性安装到 `/opt/cc-manager/docker/`，CI 只传标签；(否) CI 每次上传脚本——等于把 root 权限交给 CI 密钥。

## 设计

- **镜像**：`deploy/Dockerfile` 固定运行用户 UID/GID 10001、写入版本号（提交短哈希）与 OCI 标签；`/healthz` 返回该版本号。
- **服务器布局**：`/opt/cc-manager/docker/{compose.yml,docker-deploy.sh,backup.sh,state/}`；数据库仍在 `/var/lib/cc-manager`（绑定挂载到容器 `/data`），环境文件仍在 `/etc/cc-manager/cc-manager.env`。
- **部署脚本** `docker-deploy.sh <tag> [registry-user]`（标准输入可带拉取令牌）：加锁 → 登录并拉取镜像 → 在线备份数据库 → 停掉旧服务（旧容器或首次时的 systemd）→ 以新标签启动 → 轮询 `/healthz` 直到 `ok` 且版本号等于标签 → 成功则记录当前/上一个标签、首次时禁用 systemd、清理旧镜像（保留 3 个）；失败则停新容器、从备份恢复数据库、启动上一个标签（首次则重新启动 systemd）。
- **CI**：PR 只测试并构建镜像不推送；main 推送或手动触发时推送 `sha-<12位>` 与 `main` 两个标签，再进入需要批准的 `deploy` 任务，通过 SSH 执行 `sudo /opt/cc-manager/docker/docker-deploy.sh`。手动触发可指定任意旧标签用于回退。
- **一次性迁移**：`deploy/production/install-docker.sh` 安装脚本与 compose、建立部署用户与只允许执行部署脚本的 sudo 规则；不自动切换，首次切换由第一次部署完成。

## 不做

不改应用代码（会话仍在内存，部署后需重新登录）；不做多实例或零停机；不引入 Kubernetes。
