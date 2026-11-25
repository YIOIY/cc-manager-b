import { expect, test } from '@playwright/test'
import { ADMIN_PASSWORD, login, loginCard, logout } from './helpers'

test.describe('登录', () => {
  test('错误密码停留在登录卡片并提示', async ({ page }) => {
    let answer = ''
    page.on('response', async (response) => {
      if (!response.url().includes('/api/v1/auth/captcha') || !response.ok()) return
      try {
        const json = await response.json()
        if (json?.data?.answer) answer = String(json.data.answer)
      } catch {
        /* ignore */
      }
    })
    await page.goto('/')
    await expect.poll(() => answer).not.toBe('')
    await page.getByPlaceholder('输入管理员账号').fill('admin')
    await page.getByPlaceholder('输入登录密码').fill('definitely-wrong-password')
    await page.getByPlaceholder('输入图形验证码').fill(answer)
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(loginCard(page).locator('.el-alert')).toBeVisible()
    await expect(page.getByRole('navigation', { name: '主导航' })).toHaveCount(0)
  })

  test('错误验证码停留在登录卡片并提示', async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('.login-captcha-image img')).toBeVisible()
    await page.getByPlaceholder('输入管理员账号').fill('admin')
    await page.getByPlaceholder('输入登录密码').fill(ADMIN_PASSWORD)
    await page.getByPlaceholder('输入图形验证码').fill('XXXX')
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(loginCard(page).locator('.el-alert')).toBeVisible()
    await expect(page.getByRole('navigation', { name: '主导航' })).toHaveCount(0)
  })

  test('正确密码进入仪表盘，退出后受保护页面跳回登录', async ({ page }) => {
    await login(page, 'admin', ADMIN_PASSWORD)
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByRole('heading', { name: '仪表盘' }).first()).toBeVisible()
    await logout(page)
    await page.goto('/accounts')
    await expect(loginCard(page)).toBeVisible()
    await expect(page.getByRole('navigation', { name: '主导航' })).toHaveCount(0)
  })
})
