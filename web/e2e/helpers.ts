import { expect, type Page } from '@playwright/test'
import { ADMIN_PASSWORD } from '../playwright.config'

export { ADMIN_PASSWORD }

/** The login view is rendered in place of the shell on any URL while signed out. */
export const loginCard = (page: Page) => page.locator('.login-card')

export async function login(page: Page, username: string, password: string) {
  await page.goto('/')
  await expect(loginCard(page)).toBeVisible()
  await page.getByPlaceholder('输入管理员用户名').fill(username)
  await page.getByPlaceholder('输入登录密码').fill(password)
  await page.getByRole('button', { name: '进入工作台' }).click()
  await expect(page.getByRole('navigation', { name: '主导航' })).toBeVisible()
}

export async function logout(page: Page) {
  await page.getByRole('button', { name: '退出登录' }).click()
  await expect(loginCard(page)).toBeVisible()
}

/** Call the JSON API with the browser's session cookie and a fresh CSRF token. */
export async function api<T = unknown>(page: Page, method: string, path: string, body?: unknown): Promise<T> {
  const session = await page.request.get('/api/v1/auth/session')
  const csrf = (await session.json()).data.csrf_token as string
  const res = await page.request.fetch('/api/v1' + path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    data: body === undefined ? undefined : JSON.stringify(body),
  })
  const json = await res.json()
  if (!res.ok()) throw new Error(`${method} ${path} -> ${res.status()} ${JSON.stringify(json.error)}`)
  return json.data as T
}

export const navItems = (page: Page) => page.getByRole('navigation', { name: '主导航' }).getByRole('button')
