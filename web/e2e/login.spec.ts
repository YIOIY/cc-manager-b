import { expect, test } from '@playwright/test'
import { ADMIN_PASSWORD, login, loginCard, logout } from './helpers'

test.describe('登录', () => {
  test('错误密码停留在登录卡片并提示', async ({ page }) => {
    await page.goto('/')
    await page.getByPlaceholder('输入管理员账号').fill('admin')
    await page.getByPlaceholder('输入登录密码').fill('definitely-wrong-password')
    await page.getByRole('button', { name: '进入工作台' }).click()
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
