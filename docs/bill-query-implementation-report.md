# 账单查询与同步实施记录

实施：2026-11-05，最终验收跨至 2026-11-06（Asia/Shanghai）。

本次完成 `/supplier-account/api/openapi/bill/query` 的本地查询与同步闭环，`metrics/query-keys` 按要求不接入。真实供应商未调用；以下采集结果来自本地 HTTPS 模拟供应商，不代表真实平台已联调。

## 已落地功能

- 独立账单页面：连接、多账号、日期/快捷项、关键词、归档、修订次数筛选，稳定排序和服务端分页；合计覆盖全部匹配行。
- 覆盖摘要：按账号日说明缺失、历史待复核、无返回、失败、过期和时区变化；不因关键词或修订过滤而误报完整。
- 持久补采任务：显式账号范围，冻结连接版本/时区/账号映射；逐日、每批最多 100 个供应商 ID。支持进度、分页明细、只重试失败账号日、幂等和活动任务去重。
- 完整响应校验：实际 range、重复/未请求 ID、必填计数和精确金额。空缺不写零；错误保留已有历史。合法零值可以保存。
- 账单覆盖写入：不累加重复采样；仅数值改变才增加 revision；代次保护阻止旧响应覆盖新数据。上号后的账单路径复用完整校验和写入规则。
- 独立调度：指标仍每 2 分钟采集；账单每 2 小时重查最近 3 日、每天重查最近 7 日。按连接时区的日历日期校验今天减 90 日的边界；本地历史可查更早日期。
- 事务迁移：旧金额/修订保留，新增验证标记默认为待复核；持久任务租约过期后续跑。提前失败也进入覆盖统计。
- 兼容入口：保留旧账单 GET 数组与原指标同步历史列表；新页面使用专用 POST 查询和 `kind=bill` 任务列表。

实际请求/响应、错误与配置项见 [API 契约](api-contract.md#账单实现契约2026-11-05)。原方案保留为 [设计依据](bill-query-completion-plan.md)，其中类型名示例、游标建议不作为实际 API 签名；实现使用持久任务项恢复，而非单独新增游标表。

## 验证证据

| 验证 | 结果与证据 |
| --- | --- |
| Go 全量测试 | 20 个顶层测试通过，涵盖迁移回滚、批次/版本/代次、幂等、租约恢复、失败统计、分页与鉴权；`.local-run/bill-acceptance/go-tests.log` |
| 竞态检查 | `go test -race ./internal/app ./internal/supplier -count=1 -timeout=90s` 通过；`race-tests.log` |
| 静态检查 | `go vet ./...` 退出 0；`vet.log`（无诊断输出） |
| 前端测试 | 2 项通过：大整数/金额精度与日历边界；`frontend-tests.log` |
| 前端构建 | TypeScript 检查与 Vite 构建通过；`frontend-build.log` |
| 端到端 HTTP | 本地独立服务 + HTTPS 模拟供应商，20 项断言通过；`acceptance.log`、`acceptance-results.json` |
| 页面交互 | 隔离 Chromium 验证筛选、同步弹窗关闭、105/105 进度、合计刷新、明细和窄屏布局；`browser-results.json`、`browser-qa.log` 与截图 |
| 本地重启 | 健康、既有密码登录、静态资源和新任务入口正常；数据库 `integrity_check=ok`；`live-verification.json` |
| 恢复副本 | 原文件 SHA256、数据库复制、旧二进制启动与健康验证；`recovery-verification.json` |

端到端关键场景：105 个账号 × 3 日拆为 6 次上游请求。首轮 309 个账号日更新，3 个单行错误，3 个无返回；只重试 3 个失败账号日，不扩大账号与日期组合。随后完整补齐 315 行，合计 `388710` 分。重复采样数值不变时 revision 仍为 1。上游错误 range 导致任务失败，原合计和行数保持不变。计数 `9007199254740993` 经实际 JSON 与页面显示保持精确。

浏览器验收先使用用户 Chrome 的独立本地标签页；其后台标签页暂停动画影响弹窗可见性断言，随后使用独立前台渲染的无头 Chromium 验证完整关闭与交互。窄屏测试发现日期选择器最小宽度撑开筛选栏，已修复并复验。详细表格在自己的区域内横向滚动。

构建仍有现存的 Vite 配置加载提示、主包超过 500KB 提示；没有影响本次构建成功。当前轮未进行代码拆包优化。

## 本地服务与数据

地址：`http://127.0.0.1:8080`。

继续使用 `.local-run/config.json` 和 `.local-run/dev.db`。原密码与加密密钥保持不变；重启使内存会话失效，需要重新登录。没有切换到项目根目录的旧 `cc-manager.db`，没有把验收账号写入当前本地数据库。

当前本地数据库没有平台连接、账号或平台关联，因此真实账单尚无数据；该状态已经由实际数据库/API 检查确认。供应商网页的浏览器登录与 OpenAPI Bearer Token 是两种鉴权。后续真实采集需要在平台设置中配置有效 OpenAPI 连接，并有已接受的账号关联。

验收使用端口 18080/18443 与独立数据库；仅该隔离进程启用自签证书测试开关。正式本地 8080 进程未启用该开关。验收结束停止测试服务与浏览器，保留测试文件供复核。

## 差异与备份

项目目录没有 Git 元数据，本次使用原文件快照生成差异：

- `.local-run/implementation-backup/source/`：实施前源文件与文档。
- `.local-run/implementation-backup/manifest.json`：原文件 SHA256。
- `.local-run/implementation-backup/database.sqlite`：实施开始前一致性数据库备份。
- `.local-run/implementation-backup/pre-restart.sqlite`：重启前一致性数据库备份。
- `.local-run/implementation-backup/cc-manager.original`：原运行二进制。
- `.local-run/bill-acceptance/changes.json`、`source.diff`：本次新增/修改清单与统一差异。
- `.local-run/bill-acceptance/final-sha256.json`：当前交付文件摘要，不含密码/密钥内容。

## 恢复方法

先生成**额外恢复副本**，不会修改正在运行的项目或覆盖已有目录：

```sh
cd /Users/disaster/GoProject/cc-manager
python3 .local-run/bill-acceptance/recover.py --output /tmp/cc-manager-recovered
```

目标目录必须不存在。脚本复制项目、覆盖实施前源文件、移除本轮新增源文件，校验原始 SHA256，并放入原二进制和重启前数据库。脚本已在额外副本实际执行；原二进制对该数据库可启动并通过健康检查。

需要前端时，在恢复副本的 `web` 中执行 `npm ci`、`npm run build`。启动时沿用原 `.local-run/config.json` 中的管理员密码和加密密钥，明确将 `CC_MANAGER_DB` 指向恢复副本的 `.local-run/dev.db`，监听独立端口。不要把未知或新生成的加密密钥用于现有密文。

确认副本后，如确需回滚当前环境，先停止 `.local-run/server.pid` 指向且命令路径匹配的本地服务，为当前数据库再做一致性备份，再切换到恢复副本启动。回滚数据库会舍弃备份之后的账单和任务记录；保留当前项目及数据库可恢复本轮结果。不在运行中覆盖 SQLite 数据库或仅复制主文件而遗漏 WAL 内容。
