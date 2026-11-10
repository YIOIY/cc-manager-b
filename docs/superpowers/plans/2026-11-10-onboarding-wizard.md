# 新建上号向导实施计划

> 按已批准方案在当前会话顺序执行，使用测试驱动验证。当前仓库文件均未被 Git 跟踪，保留原始文件快照，不创建空工作树或提交无关文件。

**目标：** 将新建及完善账号改为连接、补资料、确认三步，支持页内创建代理。

**架构：** useAccountOnboarding 持有唯一表单及步骤状态，AccountFormDialog 负责呈现；OnboardingProxyPicker 负责选择和创建代理；ClaudeOAuthPanel 保持既有 PKCE 流程；所有提交复用原接口与错误映射。

**技术：** Vue 3、TypeScript、Element Plus、Vitest、Playwright、既有 Go API。

**方案：** docs/onboarding-ux-proposal-2026-11-10.md。

**约束：** 不加入 localhost 回调；自动分配不可用；日期不推算；不生成设备 ID；凭证不写入浏览器存储；保留现有 API 字段错误和幂等请求能力。

- [x] 保存 web/src、web/tests、web/e2e、docs 原文件及 SHA256，运行原版测试。
- [x] 为三步切换、缺项阻断、确认才提交、返回保留资料、账号和代理变更、字段错误回跳建立失败测试。
- [x] 新建 OnboardingProxyPicker.vue：消费 ProxyOption[] 与 modelValue，输出 update:modelValue、created、busy、dirty、pending；新代理显式保存并使用，API 返回 ID 后绑定，失败保留输入，检查 proxies.create 权限。
- [x] AccountFormDialog.vue：step 1/2/3 共用 edit；第一步选择平台代理及授权/导入；第二步只展开待补资料，技术字段折叠；第三步脱敏核对。保留现有错误详情及请求 ID。
- [x] ClaudeOAuthPanel.vue：收敛登录说明、明确打开授权页入口及成功摘要；保持链接生成、解析与兑换协议。
- [x] AccountCompletionChecklist.vue：支持紧凑缺项反馈；正常输入区不重复铺开清单。
- [x] 验证导入与换号不混用旧密码、日期、设备和凭证；代理变化使既有授权失效并提示；未提交退出提醒；异步创建代理不回填关闭后的窗口。
- [x] 更新 ListView.vue 的代理刷新和提交文案；保留任务进度入口。
- [x] 更新旧界面测试为新步骤语义，补充浏览器端完整流程及小屏核验。
- [x] 运行前端全部测试、类型检查、lint、构建及端到端检查。生成差异、验证记录与可在额外副本验证的恢复脚本。

原版：2026-11-10，Vitest 17 个文件、73 项测试通过。原始快照与实际命令输出：/tmp/cc-manager-onboarding-20261110/。
