# Docker 化持续交付 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** main 推送 → 测试 → 推送 GHCR 镜像 → 批准后自动部署、失败回滚。
**Spec:** `docs/superpowers/specs/2026-11-11-docker-cicd-design.md`

## Global Constraints
- 脚本用 bash + coreutils，在 Ubuntu 运行；为本地验证提供路径与开关的环境变量覆盖（`DEPLOY_ROOT`、`ENV_FILE`、`DATA_DIR`、`BACKUP_ROOT`、`APP_PORT`、`IMAGE`、`PULL`、`SYSTEMD_UNIT`、`CHOWN_DATA`），默认值对应生产路径。
- 不在镜像、CI 日志或仓库中出现密钥。

## Review Focus
1. 首次部署（无上一个标签）健康检查失败时，必须恢复数据库并重新启动 systemd 服务。
2. 回滚时数据库恢复必须在新容器停掉之后、旧服务启动之前，且清掉 `-wal`/`-shm`。
3. 拉取失败或备份失败时不得停掉正在运行的旧服务。
4. 两次部署并发时第二次必须拒绝。
5. CI 部署令牌只经标准输入传递，不出现在命令行参数或日志里。

### Task 1: 镜像与生产 compose
- [ ] `deploy/Dockerfile`：`adduser -u 10001`、`ARG VERSION`、OCI labels（source/revision/version）。
- [ ] `deploy/production/compose.yml`：`image: ${IMAGE}:${TAG}`，env_file 为 `${ENV_FILE}`，覆盖 LISTEN/DB/WEB_DIR，绑定 `${DATA_DIR}:/data`，端口 `127.0.0.1:${APP_PORT}:8080`，只读根文件系统、tmpfs、去权限、日志轮转、健康检查。
- [ ] 验证：本地 `docker build --build-arg VERSION=localA`，`docker compose config` 通过。

### Task 2: 部署脚本
- [ ] `deploy/production/docker-deploy.sh`（按 spec 设计）与 `install-docker.sh`。
- [ ] 本地模拟：A 成功部署 → 写入数据 → B（版本号不符）失败 → 自动回滚到 A 且数据库为部署 B 之前的备份 → 并发锁拒绝 → 拉取失败不影响运行中的 A。

### Task 3: CI
- [ ] `ci.yml`：image 任务在 main/手动触发时登录 GHCR 并推送；新增 deploy 任务（environment: production，concurrency，SSH 通过 secrets，令牌走标准输入）；`workflow_dispatch` 支持指定标签。
- [ ] 验证：YAML 语法检查；若有 actionlint 则运行。

### Task 4: 文档、审查、收尾
- [ ] `docs/operations.md` 安装/升级章节改为 Docker 流程；README 指向；列出 GitHub 端需要配置的 secrets 与 environment。
- [ ] 独立审查并修复确认的问题。
