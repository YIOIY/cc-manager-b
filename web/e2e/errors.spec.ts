import { expect, test } from '@playwright/test'
import { ADMIN_PASSWORD, api, login } from './helpers'

test('分步表单校验失败时保留输入并定位到待修改项', async ({ page }) => {
  await login(page, 'admin', ADMIN_PASSWORD)
  await api(page, 'POST', '/platform-connections', {
    name: '校验测试平台',
    base_url: 'https://errors-supplier.example.com',
    token: 'fixture',
    timezone: 'UTC',
  })
  await page.goto('/accounts')
  await page.getByRole('button', { name: /新建账号/ }).click()
  const dialog = page.locator('.account-wizard')
  await dialog.locator('#field-connection .el-select').click()
  await page.getByRole('option', { name: '校验测试平台', exact: true }).click()
  await dialog.locator('.wizard-import summary').click()
  await dialog.locator('.wizard-import textarea').fill(
    JSON.stringify({
      account: 'errors@example.com',
      password: 'fixture-password',
      plan: 'claude/cc-pro',
      register_time: '2025-01-01',
      plan_start_time: '2026-01-01',
      plan_end_time: '2026-02-01',
      auth_json: {
        access_token: 'a',
        refresh_token: 'r',
        email: 'errors@example.com',
        account_uuid: 'u',
        organization_uuid: 'o',
        organization_name: 'Org',
        claude_device_ids: ['d'],
        expired: 'not-a-date',
        last_refresh: '2026-11-09T00:00:00Z',
        type: 'claude',
        verification_status: 'not_required',
        proxy_url: 'http://proxy.example.com:8080',
      },
    }),
  )
  await dialog.getByRole('button', { name: '解析并填充' }).click()
  await dialog.locator('[data-next]').click()
  await expect(dialog.locator('.error-notice')).toContainText('RFC3339')
  await expect(dialog.locator('.credential-manual')).toHaveAttribute('open')
  await expect(dialog.locator('#credential-field-expired input')).toHaveValue('not-a-date')
  await expect(dialog.locator('[data-review]')).toHaveCount(0)
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('button', { name: '继续填写', exact: true }).click()
  await expect(dialog.getByPlaceholder('输入账号密码')).toHaveValue('fixture-password')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(dialog).toBeHidden()
})

test('存储错误通知展示请求编号且可复制', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, 'admin', ADMIN_PASSWORD)
  await page.goto('/proxies')
  await page.route('**/api/v1/proxies', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: JSON.stringify({
            error: {
              code: 'STORAGE_UNAVAILABLE',
              category: 'storage',
              message: '数据存储暂时不可用',
              hint: '请稍后重试；若持续出现，请把请求编号提供给管理员',
              retryable: true,
              action: 'retry',
            },
            request_id: 'req-e2e-77',
          }),
        })
      : route.continue(),
  )
  await page.getByRole('button', { name: '新增代理' }).first().click()
  const dialog = page.locator('.el-dialog').filter({ hasText: '新增代理' })
  await dialog.getByPlaceholder(/IP 地址或域名/).fill('10.9.9.9')
  await dialog.getByRole('button', { name: '保存代理' }).click()
  const notice = dialog.locator('.error-notice')
  await expect(notice).toContainText('数据存储暂时不可用')
  await expect(notice).toContainText('req-e2e-77')
  await notice.locator('.error-notice-copy').click()
  const clipboard = await page.evaluate(() => navigator.clipboard.readText())
  expect(clipboard).toContain('STORAGE_UNAVAILABLE')
  expect(clipboard).toContain('req-e2e-77')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
})

test('权限不足提示统一文案', async ({ page, request }) => {
  await login(page, 'admin', ADMIN_PASSWORD)
  // 通过已登录页面上下文创建只读用户（若已存在则忽略冲突）
  await page.goto('/users')
  const csrf = await page.evaluate(async () => {
    const r = await fetch('/api/v1/auth/session')
    const j = await r.json()
    return j.data.csrf_token as string
  })
  await page.evaluate(
    async ({ csrf }) => {
      const roles = await (await fetch('/api/v1/roles')).json()
      const viewer = roles.data.find((r: { name: string }) => r.name === '只读')
      await fetch('/api/v1/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
        body: JSON.stringify({ username: 'e2e-ro', password: 'e2e-readonly-123456', role_ids: [viewer.id] }),
      })
    },
    { csrf },
  )
  await page.goto('/')
  await page.getByRole('button', { name: '退出登录' }).click()
  await login(page, 'e2e-ro', 'e2e-readonly-123456')
  const res = await page.evaluate(async () => {
    const session = await (await fetch('/api/v1/auth/session')).json()
    const r = await fetch('/api/v1/proxies', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.data.csrf_token },
      body: JSON.stringify({ scheme: 'http', host: '1.2.3.4', port: 8080 }),
    })
    return r.json()
  })
  expect(res.error.code).toBe('PERMISSION_DENIED')
  expect(res.error.message).toBe('当前账号没有执行此操作的权限')
  void request
})

test('422 信封在真实表单上标红具体字段', async ({ page }) => {
  await login(page, 'admin', ADMIN_PASSWORD)
  await page.goto('/proxies')
  await page.route('**/api/v1/proxies', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({
          status: 422,
          contentType: 'application/json',
          body: JSON.stringify({
            error: {
              code: 'VALIDATION_FAILED',
              category: 'validation',
              message: '有 2 项资料不符合要求',
              action: 'fix_input',
              fields: [
                { field: 'host', code: 'INVALID_FORMAT', message: '主机地址无法解析' },
                { field: 'port', code: 'OUT_OF_RANGE', message: '端口必须在 1 到 65535 之间' },
              ],
            },
            request_id: 'req-e2e-422',
          }),
        })
      : route.continue(),
  )
  await page.getByRole('button', { name: '新增代理' }).first().click()
  const dialog = page.locator('.el-dialog').filter({ hasText: '新增代理' })
  await dialog.getByPlaceholder(/IP 地址或域名/).fill('bad host')
  await dialog.getByRole('button', { name: '保存代理' }).click()
  // 真实 el-form-item 渲染内联错误，两个字段同时标红
  await expect(dialog.locator('.el-form-item__error')).toHaveCount(2)
  await expect(dialog.locator('.el-form-item__error').filter({ hasText: '主机地址无法解析' })).toHaveCount(1)
  await expect(dialog.locator('.error-notice')).toContainText('有 2 项资料不符合要求')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
})
