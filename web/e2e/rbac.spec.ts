import { expect, test } from '@playwright/test'
import { ADMIN_PASSWORD, api, login, logout, navItems } from './helpers'

const viewer = { username: 'viewer-e2e', password: 'viewer-password-123' }

test.describe('权限控制页面可见性', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await login(page, 'admin', ADMIN_PASSWORD)
    const users = await api<{ username: string }[]>(page, 'GET', '/users')
    if (!users.some((u) => u.username === viewer.username)) {
      await api(page, 'POST', '/users', { ...viewer, role_ids: ['role-viewer'] })
    }
    await page.close()
  })

  test('管理员能看到全部导航与用户页', async ({ page }) => {
    await login(page, 'admin', ADMIN_PASSWORD)
    await expect(navItems(page).filter({ hasText: '用户与权限' })).toBeVisible()
    await page.goto('/users')
    await expect(page.getByRole('tab', { name: /角色/ })).toBeVisible()
    await logout(page)
  })

  test('只读用户看不到用户页，深链接被重定向，按钮被隐藏', async ({ page }) => {
    await login(page, viewer.username, viewer.password)
    const items = navItems(page)
    await expect(items.filter({ hasText: '账号管理' })).toBeVisible()
    await expect(items.filter({ hasText: '用户与权限' })).toHaveCount(0)
    await page.goto('/users')
    await expect(page).not.toHaveURL(/\/users/)
    await page.goto('/accounts')
    await expect(page.getByRole('heading', { name: '账号管理' })).toBeVisible()
    await expect(page.getByRole('button', { name: /新建账号/ })).toHaveCount(0)
    await expect(page.getByRole('button', { name: /JSON 导入/ })).toHaveCount(0)
    // The API enforces the same rule independently of the UI.
    const res = await page.request.get('/api/v1/users')
    expect(res.status()).toBe(403)
  })
})
