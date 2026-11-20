import { expect, test } from '@playwright/test'
import { ADMIN_PASSWORD, api, login } from './helpers'
import type { Account } from '../src/types/api'

test('新建上号：页内保存代理、PKCE 授权、补充资料、核对与明确提交', async ({ page }, testInfo) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await login(page, 'admin', ADMIN_PASSWORD)
  await api(page, 'POST', '/platform-connections', {
    name: '向导测试平台',
    base_url: 'https://supplier.example.com',
    token: 'fixture-token',
    timezone: 'UTC',
  })
  await page.goto('/accounts')
  await page.getByRole('button', { name: /新建账号/ }).click()
  const dialog = page.locator('.account-wizard')
  await expect(dialog.getByRole('button', { name: '登录 Claude', exact: true })).toBeDisabled()
  await expect(dialog.locator('#field-password')).toHaveCount(0)
  // This saves a real pool entry through the app API, without probing the example proxy.
  await dialog.getByRole('radio', { name: '填写新代理' }).check()
  await dialog.locator('#onboarding-proxy-address').fill('http://fixture-user:fixture-password@proxy.example.com:8080')
  await dialog.getByRole('button', { name: '保存并使用' }).click()
  await expect(dialog.getByText('新代理已保存并选中。')).toBeVisible()
  await expect(dialog.getByRole('button', { name: '登录 Claude', exact: true })).toBeEnabled()
  await page.screenshot({ path: testInfo.outputPath('wizard-connect.png'), fullPage: true })
  await dialog.getByRole('button', { name: '登录 Claude', exact: true }).click()
  const url = new URL(await dialog.locator('#claude-authorization-url').inputValue())
  expect(url.searchParams.get('code_challenge_method')).toBe('S256')
  await dialog.locator('#claude-authorization-result').fill('fixture#wrong-state')
  await dialog.getByRole('button', { name: '确认授权结果' }).click()
  await expect(dialog.getByText('授权结果与当前链接不匹配，请使用本次生成的链接')).toBeVisible()
  await page.route('**/claude-oauth/sessions/*/exchange', (route) =>
    route.fulfill({
      json: {
        data: {
          auth_json: {
            access_token: 'fixture-access',
            refresh_token: 'fixture-refresh',
            email: 'fixture@example.com',
            account_uuid: 'fixture-account',
            organization_uuid: 'fixture-org',
            organization_name: 'Fixture org',
            expired: '2099-12-01T00:00:00Z',
            last_refresh: '2026-11-09T00:00:00Z',
            type: 'claude',
            verification_status: 'not_required',
          },
          plan: 'claude/cc-max-5x',
          register_time: '2025-02-01',
          warnings: [],
        },
      },
    }),
  )
  await dialog.locator('#claude-authorization-result').fill('fixture#' + url.searchParams.get('state'))
  await dialog.getByRole('button', { name: '确认授权结果' }).click()
  await expect(dialog.locator('.oauth-done-email')).toHaveText('fixture@example.com')
  await expect(dialog.locator('#field-password')).toHaveCount(0)
  await dialog.locator('[data-next]').click()
  await expect(dialog.locator('.wizard-identity')).toContainText('fixture@example.com')
  await expect(dialog.getByPlaceholder('粘贴 access token')).toHaveCount(0)
  await dialog.locator('[data-next]').click()
  await expect(dialog.getByText(/已定位到第一项/)).toBeVisible()
  await dialog.getByPlaceholder('输入账号密码').fill('fixture-delivery-password')
  await dialog.locator('#field-plan-start input').fill('2026-11-01')
  await dialog.locator('#field-plan-start input').press('Tab')
  await dialog.locator('#field-plan-end input').fill('2026-12-01')
  await dialog.locator('#field-plan-end input').press('Tab')
  await dialog.getByPlaceholder('填写设备 ID，多个 ID 用逗号或换行分隔').fill('fixture-device')
  await page.screenshot({ path: testInfo.outputPath('wizard-details.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(dialog.locator('[data-next]')).toBeInViewport()
  const overflow = await dialog.evaluate((el) => el.scrollWidth > el.clientWidth + 1)
  expect(overflow).toBe(false)
  await page.screenshot({ path: testInfo.outputPath('wizard-mobile.png'), fullPage: true })
  await page.setViewportSize({ width: 320, height: 740 })
  await expect(dialog.locator('[data-next]')).toBeInViewport()
  expect(await dialog.evaluate((el) => el.scrollWidth > el.clientWidth + 1)).toBe(false)
  await page.screenshot({ path: testInfo.outputPath('wizard-mobile-320.png'), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })
  let submits = 0
  // Only external OAuth exchange and account submission are simulated.
  // No example credentials are sent to a supplier.
  await page.route('**/api/v1/accounts/onboard', async (route) => {
    submits++
    const payload = route.request().postDataJSON()
    expect(payload.proxy_id).toBeTruthy()
    expect(payload.auth_json.proxy_url).toBe('')
    await route.fulfill({
      status: 202,
      json: { data: { account_id: 'fixture-local', job_id: 'fixture-job', state: 'queued' } },
    })
  })
  await dialog.locator('[data-next]').click()
  await expect(dialog.locator('[data-review]')).toBeVisible()
  expect(submits).toBe(0)
  await expect(dialog.locator('[data-review]')).not.toContainText('fixture-password')
  await expect(dialog.locator('[data-review]')).not.toContainText('fixture-access')
  await page.screenshot({ path: testInfo.outputPath('wizard-review.png'), fullPage: true })
  await dialog.locator('[data-back]').click()
  await expect(dialog.getByPlaceholder('输入账号密码')).toHaveValue('fixture-delivery-password')
  await dialog.locator('[data-next]').click()
  await dialog.locator('[data-submit]').click()
  await expect(dialog).toBeHidden()
  expect(submits).toBe(1)
  await expect(page.getByText('账号已提交，等待平台处理。可在账号卡片查看上号结果。')).toBeVisible()
  await page.getByRole('button', { name: /新建账号/ }).click()
  await expect(dialog.locator('.oauth-done')).toHaveCount(0)
  await expect(dialog.locator('#field-password')).toHaveCount(0)
  expect(errors).toEqual([])
})

test('已有账号重新授权沿用保存资料，设备缺失时有可见补填入口', async ({ page }) => {
  await login(page, 'admin', ADMIN_PASSWORD)
  const connection = await api<{ id: string }>(page, 'POST', '/platform-connections', {
    name: '重授权测试平台',
    base_url: 'https://supplier.example.com',
    token: 'fixture-token',
    timezone: 'UTC',
  })
  const account = await api<{ id: string }>(page, 'POST', '/accounts', {
    account: 'reauth-fixture@example.com',
    plan: 'claude/cc-pro',
    register_location: 'US',
    register_time: '2025-01-01',
    plan_start_time: '2026-11-01',
    plan_end_time: '2026-12-01',
  })
  await api(page, 'PUT', `/accounts/${account.id}/credentials`, {
    auth_json: {
      id_token: '',
      access_token: 'saved-access',
      refresh_token: 'saved-refresh',
      email: 'reauth-fixture@example.com',
      account_uuid: 'fixture-account',
      organization_uuid: 'fixture-org',
      organization_name: 'Fixture org',
      claude_device_ids: ['saved-device'],
      type: 'claude',
      last_refresh: '2026-11-09T00:00:00Z',
      expired: '2099-12-01T00:00:00Z',
      proxy_url: 'http://fixture-user:fixture-password@proxy.example.com:8080',
      verification_status: 'not_required',
    },
  })
  // Simulate a platform link and supplier responses; local credential storage
  // and OAuth link generation use the real app API.
  await page.route(/\/api\/v1\/accounts(?:\?|$)/, async (route) => {
    const response = await route.fetch()
    const envelope = await response.json()
    const rows = (Array.isArray(envelope.data) ? envelope.data : envelope.data.items) as Account[]
    const row = rows.find((item) => item.id === account.id)
    if (row) {
      row.platform_status = 'sync_failed'
      row.onboarding_state = 'linked'
      row.platform_links = [
        { connection_id: connection.id, ingestion_state: 'accepted', connection_name: '重授权测试平台' },
      ] as Account['platform_links']
    }
    await route.fulfill({ response, json: envelope })
  })
  await page.route('**/claude-oauth/sessions/*/exchange', (route) =>
    route.fulfill({
      json: {
        data: {
          auth_json: {
            access_token: 'fresh-access',
            refresh_token: 'fresh-refresh',
            email: 'reauth-fixture@example.com',
            organization_uuid: 'fixture-org',
            last_refresh: '2026-11-20T00:00:00Z',
            expired: '2099-12-01T00:00:00Z',
            type: 'claude',
            verification_status: 'not_required',
          },
          warnings: [],
        },
      },
    }),
  )
  let submissions = 0
  await page.route(`**/accounts/${account.id}/reauthorize`, async (route) => {
    const body = route.request().postDataJSON()
    expect(body.auth_json.access_token).toBe('fresh-access')
    expect(body.auth_json.refresh_token).toBe('fresh-refresh')
    submissions++
    if (submissions === 1) {
      expect(body.auth_json.claude_device_ids).toEqual([])
      await route.fulfill({
        status: 422,
        json: {
          error: {
            code: 'VALIDATION_FAILED',
            message: '请补填设备标识',
            fields: [{ field: 'auth_json.claude_device_ids', code: 'REQUIRED', message: '请补填设备标识' }],
          },
        },
      })
    } else {
      expect(body.auth_json.claude_device_ids).toEqual(['replacement-device'])
      await route.fulfill({ json: { data: { account_id: account.id, platform_pushed: true, upstream_id: 'fixture' } } })
    }
  })
  await page.goto('/accounts')
  const card = page.locator('article').filter({ hasText: 'reauth-fixture@example.com' })
  await card.getByRole('button', { name: '重新授权' }).click()
  const dialog = page.locator('.account-wizard')
  await dialog.locator('[data-reauthorize]').click()
  await expect(dialog.getByRole('button', { name: '登录 Claude', exact: true })).toBeEnabled()
  const startResponse = page.waitForResponse(
    (res) => new URL(res.url()).pathname === '/api/v1/claude-oauth/sessions' && res.request().method() === 'POST',
  )
  await dialog.getByRole('button', { name: '登录 Claude', exact: true }).click()
  const started = await startResponse
  expect(started.status()).toBe(201)
  expect(started.request().postDataJSON().account_id).toBe(account.id)
  expect(await started.text()).not.toContain('fixture-password')
  await dialog.locator('#claude-authorization-result').fill('fixture')
  await dialog.getByRole('button', { name: '确认授权结果' }).click()
  await dialog.locator('[data-next]').click()
  await expect(dialog.locator('[data-review]')).toBeVisible()
  await expect(dialog.locator('[data-review]')).toContainText('沿用已保存或自动生成')
  await dialog.locator('[data-submit]').click()
  const deviceInput = dialog.locator('[data-reauth-devices] textarea').first()
  await expect(deviceInput).toBeVisible()
  await deviceInput.fill('replacement-device')
  await dialog.locator('[data-next]').click()
  await dialog.locator('[data-submit]').click()
  await expect(dialog).toBeHidden()
  expect(submissions).toBe(2)
})
