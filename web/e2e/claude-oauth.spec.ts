import { expect, test } from '@playwright/test'
import { ADMIN_PASSWORD, api, login } from './helpers'

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
