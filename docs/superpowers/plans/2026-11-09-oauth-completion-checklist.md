# 授权后补齐清单 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 授权成功后把拿不到的字段变成一张可跳转、实时打勾的清单，并在提交时拦截缺项。

**Architecture:** 纯前端。清单逻辑放在无 Vue 依赖的 `account-form.ts`（可用 node 单测）；`ClaudeOAuthPanel` 增加紧凑"已授权"态；新组件 `AccountCompletionChecklist.vue` 渲染清单；`AccountFormDialog` 负责字段锚点、待补充/已自动获取标记、提交拦截。

**Tech Stack:** Vue 3 `<script setup>` + TypeScript、Element Plus（按需）、lucide-vue-next、Vitest + @vue/test-utils、Playwright。

**Spec:** `docs/superpowers/specs/2026-11-09-oauth-completion-checklist-design.md`

## Global Constraints

- 不改后端、接口、`auth_json` 字段与校验规则；不生成或默认填充设备标识、密码、套餐起止日期。
- 颜色用 `web/src/styles/base.css` 里的 token（`--accent`、`--muted`、`--line` 等）；待补充色新增 `--pending: #c98a1a` 与 `--pending-soft: #fdf6e8` 到 `:root`。
- 仓库尚无基线提交（用户待审），本计划**不执行 git commit**；每个任务以测试通过为完成标志。
- 命令都在 `web/` 下运行：`npm test -- --reporter=dot`、`npm run lint`、`npm run build`、`npx playwright test e2e/claude-oauth.spec.ts`。

## Review Focus

1. 编辑已有账号且保留已保存凭证时，设备标识与代理不应被算作缺项（Task 1 测试覆盖）。
2. 已选代理池记录时 `proxy_url` 不是缺项；清掉代理后又变回缺项（Task 1）。
3. 清单项"去填写"的锚点必须存在于 DOM，否则静默失败（Task 4 组件测试断言 `getElementById` 命中）。
4. 提交拦截不能挡住所有字段齐全但凭证格式错误的情况——那仍走 `buildOnboardPayload` 抛错显示（Task 4 测试）。
5. 令牌到期描述在 `expired` 非法或已过期时不能抛错（Task 1）。

---

### Task 1: 清单模型 `completionChecklist` / `firstMissingAnchor` / `describeTokenExpiry`

**Files:**
- Modify: `web/src/model/account-form.ts`（文件末尾追加）
- Test: `web/tests/account-form.test.mjs`

**Interfaces:**
- Produces:
  ```ts
  export const fieldAnchors = {
    account: 'field-account', password: 'field-password', plan: 'field-plan',
    registerTime: 'field-register-time', planStart: 'field-plan-start', planEnd: 'field-plan-end',
    connection: 'field-connection', proxy: 'field-proxy', deviceIds: 'field-device-ids',
    credentials: 'field-credentials',
  } as const
  export interface ChecklistItem { key: string; label: string; hint: string; done: boolean; anchor: string }
  export function completionChecklist(edit: AccountForm, names?: { connection?: string; proxy?: string }): ChecklistItem[]
  export function firstMissingAnchor(edit: AccountForm): string | null
  export function describeTokenExpiry(expired: unknown, now?: Date): string
  ```
- 清单固定项顺序：`password`（交付密码 / "Claude 授权不返回密码"）、`plan_dates`（套餐开始 / 结束日期 / "按账单或供应商约定确认"，两者都有才算 done）、`device_ids`（设备标识 claude_device_ids / "供应商必填，从有效凭证中获取，不要随机生成"）、`connection`（平台连接 / 已选时 hint 为 `已选 ${names.connection}`，否则 "选择要提交的供应商平台"）、`proxy`（交付代理 / 已选时 `已选 ${names.proxy}`，否则 "选择代理池记录或填写 proxy_url"）。
- 条件项只在未完成时插入到列表最前：`account`（账号邮箱 / "授权未返回邮箱，请填写登录账号"）、`plan`（套餐 / "授权未识别套餐，请手动选择"）、`register_time`（注册日期 / "授权未返回注册时间"）。
- `keepsSavedCredential(edit)` 为真时 `device_ids`、`proxy` 视为 done；`password_configured` 为真时 `password` done。
- `firstMissingAnchor`：先返回清单第一个未完成项的 anchor；清单全完成但 `missingAccountFields` 仍非空（凭证字段缺）时返回 `fieldAnchors.credentials`；否则 `null`。
- `describeTokenExpiry`：非法时间返回 `''`；已过期返回 `令牌已过期`；<1 小时 `令牌 N 分钟后到期`；<48 小时 `令牌 N 小时后到期`；否则 `令牌 N 天后到期`。

- [ ] **Step 1: 写失败测试**（追加到 `account-form.test.mjs`，补 import）

```js
test('completion checklist lists what OAuth cannot provide and tracks progress', () => {
  const afterOAuth = { account: 'a@b.co', plan: 'claude/cc-pro', register_time: '2025-01-01', access_token: 'a', refresh_token: 'r', credential_email: 'a@b.co', account_uuid: 'u', organization_uuid: 'o', organization_name: 'n', credential_type: 'claude', last_refresh: '2026-11-01T00:00:00Z', expired: '2026-12-01T00:00:00Z', verification_status: 'not_required' }
  const items = completionChecklist(afterOAuth, { connection: '平台 A' })
  assert.deepEqual(items.map((i) => i.key), ['password', 'plan_dates', 'device_ids', 'connection', 'proxy'])
  assert.deepEqual(items.map((i) => i.done), [false, false, false, false, false])
  assert.equal(firstMissingAnchor(afterOAuth), fieldAnchors.password)
  const progressed = { ...afterOAuth, password: 'p', plan_start_time: '2026-01-01', plan_end_time: '2026-02-01', proxy_id: 'px', platform_connection_id: 'c' }
  const next = completionChecklist(progressed, { connection: '平台 A', proxy: '印度代理' })
  assert.deepEqual(next.filter((i) => !i.done).map((i) => i.key), ['device_ids'])
  assert.equal(next.find((i) => i.key === 'proxy').hint, '已选 印度代理')
  assert.equal(firstMissingAnchor(progressed), fieldAnchors.deviceIds)
  assert.equal(firstMissingAnchor({ ...progressed, claude_device_ids_text: 'd1' }), null)
})
test('conditional checklist items appear only while missing; saved credentials count as done', () => {
  const bare = completionChecklist({})
  assert.deepEqual(bare.slice(0, 3).map((i) => i.key), ['account', 'plan', 'register_time'])
  const editing = completionChecklist({ id: 'x', credentials_configured: true, password_configured: true, account: 'a@b.co', plan: 'p', register_time: '2025-01-01', platform_connection_id: 'c' })
  assert.deepEqual(editing.filter((i) => !i.done).map((i) => i.key), ['plan_dates'])
  // credential fields missing but checklist complete → anchor to the credential grid
  assert.equal(firstMissingAnchor({ account: 'a@b.co', password: 'p', plan: 'p', register_location: 'US', register_time: '2025-01-01', plan_start_time: '2026-01-01', plan_end_time: '2026-02-01', platform_connection_id: 'c', proxy_id: 'px', claude_device_ids_text: 'd' }), fieldAnchors.credentials)
})
test('token expiry is described safely', () => {
  const now = new Date('2026-11-09T12:00:00Z')
  assert.equal(describeTokenExpiry('2026-11-09T20:00:00Z', now), '令牌 8 小时后到期')
  assert.equal(describeTokenExpiry('2026-11-09T12:30:00Z', now), '令牌 30 分钟后到期')
  assert.equal(describeTokenExpiry('2026-11-12T12:00:00Z', now), '令牌 3 天后到期')
  assert.equal(describeTokenExpiry('2026-11-09T11:00:00Z', now), '令牌已过期')
  assert.equal(describeTokenExpiry('nope', now), '')
  assert.equal(describeTokenExpiry(undefined, now), '')
})
```

- [ ] **Step 2: 运行确认失败** — `npm test -- --reporter=dot tests/account-form.test.mjs`，期望报 `completionChecklist is not a function`。
- [ ] **Step 3: 在 `account-form.ts` 末尾实现 Interfaces 中的四个导出**，复用 `hasValue`、`parseDeviceIds`、`keepsSavedCredential`、`missingAccountFields`。
- [ ] **Step 4: 运行确认通过** — 同上命令，全部 PASS。

### Task 2: `ClaudeOAuthPanel` 紧凑"已授权"态

**Files:**
- Modify: `web/src/components/ClaudeOAuthPanel.vue`
- Test: `web/tests/claude-oauth-panel.test.ts`

**Interfaces:**
- Produces: 新 prop `summary?: { email: string; organization?: string; plan?: string; expiry?: string } | null`。
- 模板分支顺序：`v-if="pending"`（现有链接态） → `v-else-if="summary"`（新紧凑态，class `oauth-done`） → `v-else`（生成链接按钮）。紧凑态内容：`CheckCircle2` 图标、`<strong>已授权</strong>`、邮箱（class `oauth-done-email`）、一行 meta 用 ` · ` 连接非空的 organization / plan / expiry（class `oauth-done-meta`）、`el-button` 文本 `重新授权`（click → `start`）。紧凑态下隐藏 `.oauth-intro`、`.oauth-steps`、`.oauth-network-hint`。
- 紧凑态下 `error` 仍显示在顶部。

- [ ] **Step 1: 写失败测试**

```ts
test('a summary collapses the panel into an authorized card until re-authorization starts', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: pending }))
  const wrapper = mount(ClaudeOAuthPanel, {
    props: { summary: { email: 'demo@example.com', organization: 'Demo Org', plan: 'Max 20x', expiry: '令牌 8 小时后到期' } },
    global: { stubs },
  })
  expect(wrapper.find('.oauth-done').exists()).toBe(true)
  expect(wrapper.get('.oauth-done-email').text()).toBe('demo@example.com')
  expect(wrapper.get('.oauth-done-meta').text()).toBe('Demo Org · Max 20x · 令牌 8 小时后到期')
  expect(wrapper.find('.oauth-steps').exists()).toBe(false)
  await wrapper.get('button').trigger('click') // 重新授权
  await flushPromises()
  expect(wrapper.find('.oauth-done').exists()).toBe(false)
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  wrapper.unmount()
})
```

- [ ] **Step 2: 运行确认失败** — `npm test -- --reporter=dot tests/claude-oauth-panel.test.ts`。
- [ ] **Step 3: 实现**：加 prop、模板分支、`.oauth-done*` 样式（绿色勾 `color: var(--accent-dark)`，邮箱 15px/600，meta 12px `var(--muted)`，按钮靠右）。
- [ ] **Step 4: 运行确认通过**，并确认原有 3 个用例仍过。

### Task 3: `AccountCompletionChecklist.vue`

**Files:**
- Create: `web/src/components/AccountCompletionChecklist.vue`
- Test: `web/tests/account-completion-checklist.test.ts`

**Interfaces:**
- Props: `items: ChecklistItem[]`、`warnings: string[]`。Emits: `jump: [anchor: string]`。Slot: `device`（仅在 `device_ids` 项未完成时渲染于该项下方）。
- 结构：根 `.completion-card`；头部 `.completion-head`：`remaining > 0` 时 `还差 {{ remaining }} 项才能上号`，否则 `资料已齐全，可以上号`（根加 `is-ready`）；`warnings` 逐条 `el-alert type="warning"`；`<ul class="completion-list">`，每项 `<li class="completion-item" :class="{ 'is-done': item.done }">`，内含状态图标（done → `CheckCircle2`，否则 `Circle`）、`.completion-label`、`.completion-hint`、未完成时 `el-button text` 文本 `去填写`（click → emit jump）。

- [ ] **Step 1: 写失败测试**

```ts
import { mount } from '@vue/test-utils'
import AccountCompletionChecklist from '../src/components/AccountCompletionChecklist.vue'
const stubs = { 'el-button': { template: '<button @click="$emit(\'click\')"><slot /></button>' }, 'el-alert': { template: '<div class="alert">{{ title }}</div>', props: ['title'] } }
const items = [
  { key: 'password', label: '交付密码', hint: 'Claude 授权不返回密码', done: false, anchor: 'field-password' },
  { key: 'device_ids', label: '设备标识', hint: '供应商必填', done: false, anchor: 'field-device-ids' },
  { key: 'connection', label: '平台连接', hint: '已选 平台 A', done: true, anchor: 'field-connection' },
]
test('shows remaining count, jumps to a field, and hosts the device input while it is missing', async () => {
  const wrapper = mount(AccountCompletionChecklist, { props: { items, warnings: ['资料读取失败'] }, global: { stubs }, slots: { device: '<textarea id="field-device-ids" />' } })
  expect(wrapper.get('.completion-head').text()).toContain('还差 2 项')
  expect(wrapper.findAll('.completion-item.is-done')).toHaveLength(1)
  expect(wrapper.find('#field-device-ids').exists()).toBe(true)
  expect(wrapper.get('.alert').text()).toBe('资料读取失败')
  await wrapper.findAll('button')[0]!.trigger('click')
  expect(wrapper.emitted('jump')?.[0]).toEqual(['field-password'])
  await wrapper.setProps({ items: items.map((i) => ({ ...i, done: true })) })
  expect(wrapper.get('.completion-head').text()).toContain('资料已齐全')
  expect(wrapper.find('#field-device-ids').exists()).toBe(false)
  expect(wrapper.classes()).toContain('is-ready')
})
```

- [ ] **Step 2: 运行确认失败**（模块不存在）。
- [ ] **Step 3: 实现组件**。样式：卡片 `border: 1px solid var(--pending)`、背景 `var(--pending-soft)`、圆角 14px；`is-ready` 时边框 `var(--line-tint)` 背景 `var(--tint-50)`；未完成项图标色 `var(--pending)`，完成项整行 `color: var(--muted)` 且 label 带删除线。
- [ ] **Step 4: 运行确认通过**。

### Task 4: `AccountFormDialog` 接入清单、字段标记、提交拦截

**Files:**
- Modify: `web/src/components/AccountFormDialog.vue`
- Modify: `web/src/styles/base.css`（`:root` 加 `--pending`、`--pending-soft`）
- Test: `web/tests/account-form-dialog.test.ts`（新建）

**Interfaces:**
- Consumes: Task 1 的 `completionChecklist`、`firstMissingAnchor`、`describeTokenExpiry`、`fieldAnchors`；Task 2 的 `summary` prop；Task 3 组件。
- 状态：`completionMode = ref(false)`（`fillFromOAuth` / `fillFromPaste` 成功后置 true，对话框打开时重置 false）；`oauthSummary` 由字符串改为 `ref<{ email; organization?; plan?; expiry? } | null>`，在 `fillFromOAuth` 里用 `result.auth_json` + `plans` 的 label + `describeTokenExpiry(result.auth_json.expired)` 组装；`autoAnchors = ref<Set<string>>()`：授权返回 email → `fieldAnchors.account`，plan → `fieldAnchors.plan`，register_time → `fieldAnchors.registerTime`。
- 计算：`checklist = computed(() => completionChecklist(edit.value, { connection: 所选连接 name||base_url, proxy: 所选代理 name||host }))`；`pendingAnchors = computed(() => new Set(checklist 未完成项 anchor))`；`fieldClass(anchor) => ({ 'is-pending': pendingAnchors.has(anchor), 'is-auto': autoAnchors.has(anchor) && !pendingAnchors.has(anchor) })`。
- 模板：
  - 每个相关 `el-form-item` 加 `:id="fieldAnchors.xxx"` 与 `:class="fieldClass(fieldAnchors.xxx)"`：账号、密码、套餐、注册日期、套餐开始、套餐结束、平台连接、代理选择；凭证折叠区 `<details>` 加 `:id="fieldAnchors.credentials"`。
  - `ClaudeOAuthPanel` 传 `:summary="oauthSummary"`。
  - 删除原 `oauth-summary` 两个 `el-alert`；在面板下方 `v-if="completionMode"` 渲染 `<AccountCompletionChecklist :items="checklist" :warnings="oauthWarnings" @jump="jumpTo">`，`#device` 插槽放设备标识 `el-form-item`（`:id="fieldAnchors.deviceIds"`，textarea，原 hint）；`credentialInputs` 里设备项在 `completionMode` 时从 `visibleCredentialInputs` 过滤掉。
  - `credential-manual` summary 文案：completionMode 时 `查看 / 修改全部凭证字段`；默认不再自动展开（`credentialDetailsOpen` 保持 false）。
  - 底部 `account-completeness` 仅 `v-if="!completionMode && missingFields.length"`；页脚 `account-footer-note` 替换为 `.account-footer-status`：`missingFields.length ? '还差 N 项' : '资料已完整'`，带对应图标。
- `jumpTo(anchor)`：`document.getElementById(anchor)`，`scrollIntoView({ behavior: 'smooth', block: 'center' })`，加 class `is-flash` 1200ms 后移除，`querySelector('input,textarea')?.focus()`。
- `save()`：开头若 `missingFields.value.length`，取 `firstMissingAnchor(edit.value)`，若为 `fieldAnchors.credentials` 先 `credentialDetailsOpen = true`，`await nextTick()` 后 `jumpTo`，设置 `error = '还有 N 项资料未填写，已定位到第一项'`，return（不发请求）。
- 样式：`.el-form-item.is-pending` 左边 3px `var(--pending)` 边框 + 左内边距 10px，label `::after` 内容 `待补充`（11px、`var(--pending)`、`var(--pending-soft)` 底、圆角 999px、左边距 6px）；`.is-auto` 的 label `::after` 内容 `已自动获取`（`var(--muted)` / `var(--tint-50)`）；`.is-flash` 用 1.2s 的 `box-shadow` 关键帧动画 `0 0 0 4px var(--pending-soft)`。

- [ ] **Step 1: 写失败测试**（stub 方式沿用 `account-delivery-dialog.test.ts`：`el-dialog`/`el-form`/`el-form-item`（渲染 `<div :id="$attrs.id" :class="$attrs.class"><label>{{label}}</label><slot /></div>`）/`el-input`/`el-select`/`el-option`/`el-date-picker`/`el-alert`/`el-button`/`el-input-number`，`ClaudeOAuthPanel` stub 为 `<button class="fake-oauth" @click="$emit('authorized', fixture)">`，`AccountCompletionChecklist` 不 stub）

```ts
const fixture = { auth_json: { access_token: 'a', refresh_token: 'r', email: 'fixture@example.com', account_uuid: 'u', organization_uuid: 'o', organization_name: 'Org', expired: '2099-01-01T00:00:00Z', last_refresh: '2026-11-09T00:00:00Z', type: 'claude', verification_status: 'not_required' }, plan: 'claude/cc-max-5x', register_time: '2025-02-01', warnings: ['资料读取失败'] }
test('authorization opens completion mode with a checklist, marks fields, and submit stops at the first gap', async () => {
  const wrapper = mount(AccountFormDialog, { attachTo: document.body, props: { modelValue: true, account: null, proxies: [], connections: [{ id: 'c', name: '平台 A', base_url: 'https://a', enabled: true }] }, global: { stubs } })
  await flushPromises()
  expect(wrapper.find('.completion-card').exists()).toBe(false)
  await wrapper.get('.fake-oauth').trigger('click')
  await flushPromises()
  expect(wrapper.get('.completion-head').text()).toContain('还差 4 项') // password, dates, device, proxy（connection 已自动选唯一启用项）
  expect(wrapper.get('.completion-card .alert').text()).toBe('资料读取失败')
  expect(wrapper.get('#field-password').classes()).toContain('is-pending')
  expect(wrapper.get('#field-account').classes()).toContain('is-auto')
  expect(wrapper.get('#field-plan').classes()).toContain('is-auto')
  expect(wrapper.find('.completion-card #field-device-ids').exists()).toBe(true)
  for (const a of ['field-password', 'field-plan-start', 'field-plan-end', 'field-proxy', 'field-device-ids', 'field-connection', 'field-credentials']) expect(document.getElementById(a), a).not.toBeNull()
  await wrapper.get('[data-submit]').trigger('click') // 主按钮 stub 加 data-submit 以便定位
  await flushPromises()
  expect(fetchMock).not.toHaveBeenCalled()
  expect(wrapper.get('#field-password').classes()).toContain('is-flash')
  expect(wrapper.text()).toContain('已定位到第一项')
  wrapper.unmount()
})
test('a complete form still surfaces credential format errors instead of silently blocking', async () => { /* 填满所有字段但 expired 非法 → 点提交 → 文案包含 "RFC3339"，fetch 未调用 */ })
```

- [ ] **Step 2: 运行确认失败** — `npm test -- --reporter=dot tests/account-form-dialog.test.ts`。
- [ ] **Step 3: 实现**（按 Interfaces；主按钮加 `data-submit` 属性；`scrollIntoView` 调用前判断存在以兼容 jsdom：`el.scrollIntoView?.(...)`）。
- [ ] **Step 4: 运行确认通过**；再跑全量 `npm test -- --reporter=dot`、`npm run lint`、`npm run format:check`、`npm run build`。

### Task 5: Playwright 用例与截图回归

**Files:**
- Modify: `web/e2e/claude-oauth.spec.ts`

- [ ] **Step 1: 更新断言**：把两处 `授权成功，已自动填入可获取的账号资料与凭证。` 改为 `dialog.locator('.oauth-done-email')` 文本为 `fixture@example.com`（关闭重开后 `.oauth-done` 数量为 0）；新增 `await expect(dialog.locator('.completion-head')).toContainText('还差')`、`await expect(dialog.locator('.completion-item:not(.is-done)').first()).toContainText('交付密码')`、`#field-password` 带 `is-pending`；"查看全部凭证字段"按钮名改为 `查看 / 修改全部凭证字段`；填写密码后 `.completion-item.is-done` 数量 +1。
- [ ] **Step 2: 运行** `npx playwright test e2e/claude-oauth.spec.ts`，期望 PASS，并查看 `oauth-filled.png` 确认清单与标记可见。
- [ ] **Step 3: 全量** `npx playwright test`，全部 PASS。
