# OAuth 可靠性改造验收矩阵（2026-11-23）

范围：当前工作树未提交变更，基线 `9da8e73`。核心计划：`docs/superpowers/plans/2026-11-22-oauth-reliability.md`；设计：`docs/superpowers/specs/2026-11-22-oauth-reliability-design.md`。

## 13 项对照

| #   | 目标                                   | 结论               | 主要证据                                                                                                                                                                                                              |
| --- | -------------------------------------- | ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | 账号级串行锁覆盖 start→exchange→submit | 已实现             | `internal/app/oauth_write_guard.go`；`oauthPending.lease`；`TestOAuthLifecycleStartFreezesWritesUntilCancel`                                                                                                          |
| 2   | 全局 OAuth 并发闸门，排队而非立刻失败  | 已实现，管理员可调 | `internal/app/oauth_gate.go`；`oauth_dispatch_settings.go`；默认 3 并发/30 秒排队                                                                                                                                     |
| 3   | 新号伪 accountID / 邮箱别名冷却        | 已实现             | `oauthKeys`（`id:`、`mail:`、`owner:`）用于 429 归因；exchange 成功后仅对 `id:`/`mail:` seed（`oauthIdentityKeys`），owner 不受影响；`claude_oauth_cooldown_test.go`、`oauth_regression_test.go`                      |
| 4   | 前台单飞 pending                       | 已实现             | `selectPreviousPending` 精确匹配；`refresh=false` 复用；前端显式「作废并重新获取」；`AccountFormDialog.vue` / `ClaudeOAuthPanel.vue`                                                                                  |
| 5   | 失败保留现场                           | 已实现             | exchange 中 `result.Auth != nil` 缓存；`OAUTH_RESULT_INVALID` 只清粘贴串；`oauth_session_expired` 才清链接                                                                                                            |
| 6   | 全量 Retry-After，冷却保留 pending     | 已实现             | `oauthAccountCooldown.extend` 保大不保小；`TestOAuthLifecycleFullRetryAfter`；前端倒计时                                                                                                                              |
| 7   | 五档错误分档                           | 已实现             | `errs/registry.go`：`OAUTH_IDENTITY_REJECTED`、`OAUTH_RETRYABLE`、`OAUTH_RESPONSE_INVALID`、`OAUTH_SUPPLIER_REJECTED` 等；`OAUTH_EXCHANGE_FAILED` 改 `fix_input`；前端按 code/action 分流                             |
| 8   | 探测真实端点并分状态                   | 已实现             | `claudeoauth.Reachable` + `ReachBusy`；真实端点对 GET 返回 JSON 403，带 JSON 体的 401/403/405 视为可达，非 JSON 的 401/403 视为被拦截；429 在 start 阶段给 `retry_after_seconds`；stage/耗时观测在 `oauth_metrics.go` |
| 9   | 授权到提交同出口、同 client            | 已实现             | `captureOAuthNetwork` 固定 fingerprint；`OAUTH_PROXY_CHANGED` / `PROXY_BINDING_CHANGED` 拒绝漂移；worker 显式解析一次 legacy proxy 后持久化                                                                           |
| 10  | 设备 ID 持久化优先                     | 已实现             | `credential_invariants.go`：真实值 → 已保存值 → 生成一次并保存                                                                                                                                                        |
| 11  | 凭证新鲜度与在途任务保护               | 已实现             | `applyCredentialInvariants` + `CREDENTIAL_STALE` 409；start 冻结写、拒绝在途 submit job                                                                                                                               |
| 12  | 最小指纹与 refresh_token 传递          | 已实现（B 档口径） | `oauth_fingerprint.go` 默认 `claude-cli/1.0.118–125` 池；transport 统一 UA/Accept-Language/Accept；Token/profile/roles 统一 `Authorization: Bearer access_token`，不伪造 refresh_token 参数                           |
| 13  | 429 / 重授权 / 探测 / 兑换观测         | 部分实现           | `cc_manager_oauth_rate_limit_total`、`reauthorization_total`、stage×class 直方图；P95 目前由 Prometheus 对 histogram bucket 近似，无独立 P95 函数                                                                     |

## 关键行为清单

- 租约生命周期：start 建立账号租约，cancel/expiry/成功 submit 释放；submit 成功幂等重放不重复推送。
- 锁顺序：router account-write guard 先于 oauth 内部锁，避免普通写与 OAuth lease 互锁。
- stale credential：提交前在账号锁内比较时间戳，`last_refresh` 为主序（本次更晚即接受），相同或缺失时才比较 `expired`；旧快照 409 `CREDENTIAL_STALE`，`details.field/saved/incoming` 给出两侧时间。
- 单出口绑定：direct 也是绑定（空 `proxy_url`），新号首次兑换成功把实际 `proxy_id` 写回账号。
- 预热：手动 1–3 秒、批量 5–15 秒随机延迟（迁移 v19 默认值即此范围，v21 修正早期库中的 120/0/0/300/900），`not_before_at` 存在既有 job payload；等待发生在任务仍为 queued 时，重启不会把预热中的任务记为结果未知。
- 管理配置：`OAuthSettingsPanel.vue` 在代理列表页，`oauth.settings.view` 可看、`oauth.settings` 可改。
- 秘密：日志/审计仅含账号 ID、session id、stage，不含 token/code/proxy 密码/邮箱正文。

## 复核结果（代码审查）

发现并确认的既有偏差：

1. profile/roles 401/403 保留令牌返回身份拒绝错误（对齐 registry 语义，保留现场）。
2. 新号代理绑定成功后写回；已有账号绑定固定，换代理需显式 PATCH。
3. 前端 exchange 超时 95 秒（后端排队 30 秒 + 上游 60 秒）；上游调用与浏览器连接解耦，结果缓存在会话内可空 `result` 取回。
4. 新号再授权靠 `accounts.proxy_id` 写回绑定，无需新增字段。
5. P95 依赖 histogram 桶近似，不是独立指标函数。

## 验收命令

- `npm run format:check`：通过。
- `npm run lint`：0 错误，11 个既有无关 warning。
- `npm test -- --run`：37 文件 / 250 测试通过。
- `npm run build`：通过（Element Plus 大 chunk 既有提示）。
- `go build ./...`、`go vet ./cmd/... ./internal/...`：通过。
- `go test ./internal/errs -run TestGeneratedArtifactsAreCurrent -count=1`：通过。
- `go test ./internal/claudeoauth -count=1`：通过（26.9s）。
- `go test -race -count=1 ./cmd/... ./internal/...`：通过；`internal/app 285.427s`、`internal/claudeoauth 25.354s`，全部 ok。日志：`.oauth-work/final-go-race.log`。

## 边界与未验收项

- 模拟/单测不证明真实 Claude 授权或供应商接收；真实链路需用户账号另测。
- 多副本部署仅预留 `OAuthStorageBackend` 抽象；pending 冷却仍进程内存，不跨实例。
- 浏览器登录的网络不由服务端控制，只保证服务端 OAuth 调用同出口。
- P95 汇总需外部 Prometheus/查询层从 histogram 计算。

## 工件与恢复

- 统一差异：`.oauth-work/changes.patch`（SHA256 `80354c7973f4910782b2688f18b373493d47379a1f3af3f3712d0f12ab63c7c5`，3108 行，不含 node_modules/dist/工作账本）。
- 变更文件清单：`.oauth-work/changed-files.txt`（63 个源码/测试/文档路径）。
- 最终 SHA256：`.oauth-work/final-sha256.json`（SHA256 `12a0acc1b4d40802e1d1a029cf5d04be058956b7d86c36e04c57129f7c16f4ab`，63 个文件）。
- 完整 race 测试日志：`.oauth-work/final-go-race.log`。
- 基线对照：`.oauth-baseline/sha256.json` + `.oauth-baseline/files/`（281 个文件）。

恢复方法：当前所有改动未提交。需要回退单个文件时，用 `git diff -- <path>` 查看修改，修改过的已跟踪文件可用 `git checkout -- <path>` 回到基线；新增文件按 `.oauth-work/changed-files.txt` 中的未跟踪项删除。要整体回退并保留诊断工件时，先复制 `.oauth-work/` 与 `.oauth-baseline/` 到工作树之外，再按清单清理未跟踪文件并对已跟踪文件执行 `git checkout -- .`。不建议直接丢弃工作树，因为基线快照只是校验证据，不是完整备份。
