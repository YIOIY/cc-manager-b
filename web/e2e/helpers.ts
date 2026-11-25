import { expect, type Page } from '@playwright/test'
import { ADMIN_PASSWORD } from '../playwright.config'

export { ADMIN_PASSWORD }

/** The login view is rendered in place of the shell on any URL while signed out. */
export const loginCard = (page: Page) => page.locator('.login-card')

/** Captures the debug captcha answer from the page's /auth/captcha response. */
export async function fillLoginCaptcha(page: Page) {
  let answer = ''
  const onResponse = async (response: { url: () => string; ok: () => boolean; json: () => Promise<any> }) => {
    if (!response.url().includes('/api/v1/auth/captcha') || !response.ok()) return
    try {
      const json = await response.json()
      if (json?.data?.answer) answer = String(json.data.answer)
    } catch {
      /* ignore non-JSON */
    }
  }
  page.on('response', onResponse)
  try {
    if (!(await page.getByPlaceholder('输入图形验证码').inputValue())) {
      // Trigger a refresh if the initial response was missed.
      await page.getByRole('button', { name: '点击刷新验证码' }).click()
    }
    await expect.poll(() => answer, { timeout: 10_000 }).not.toBe('')
    await page.getByPlaceholder('输入图形验证码').fill(answer)
  } finally {
    page.off('response', onResponse)
  }
}

export async function login(page: Page, username: string, password: string) {
  let answer = ''
  const onResponse = async (response: { url: () => string; ok: () => boolean; json: () => Promise<any> }) => {
    if (!response.url().includes('/api/v1/auth/captcha') || !response.ok()) return
    try {
      const json = await response.json()
      if (json?.data?.answer) answer = String(json.data.answer)
    } catch {
      /* ignore non-JSON */
    }
  }
  page.on('response', onResponse)
  try {
    await page.goto('/')
    await expect(loginCard(page)).toBeVisible()
    await expect.poll(() => answer, { timeout: 10_000 }).not.toBe('')
    await page.getByPlaceholder('输入管理员账号').fill(username)
    await page.getByPlaceholder('输入登录密码').fill(password)
    await page.getByPlaceholder('输入图形验证码').fill(answer)
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(page.getByRole('navigation', { name: '主导航' })).toBeVisible()
  } finally {
    page.off('response', onResponse)
  }
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
