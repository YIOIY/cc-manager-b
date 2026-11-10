import { expect, test } from '@playwright/test'
import { ADMIN_PASSWORD, login } from './helpers'

test.describe('本机检测路径向导', () => {
  test('切换为前置代理并保存，状态卡随之更新', async ({ page }) => {
    await login(page, 'admin', ADMIN_PASSWORD)
    await page.goto('/proxies')
    const panel = page.getByRole('region', { name: '代理检测路径' })
    await expect(panel.getByRole('heading', { name: /本机检测路径.*直连/ })).toBeVisible()
    await panel.getByRole('button', { name: /修改路径/ }).click()
    const dialog = page.getByRole('dialog', { name: '修改本机检测路径' })
    await expect(dialog).toBeVisible()
    const save = dialog.getByRole('button', { name: '保存' })
    await expect(save).toBeDisabled()
    await dialog.getByRole('radio', { name: /通过本机前置代理/ }).click()
    await expect(dialog.getByText('2')).toBeVisible()
    await dialog
      .locator('input[placeholder*="http://127.0.0.1"], input[placeholder*="socks5"], input[placeholder*="代理"]')
      .first()
      .fill('http://127.0.0.1:7890')
    await expect(save).toBeEnabled()
    await save.click()
    await expect(dialog).toBeHidden()
    await expect(panel.getByRole('heading', { name: /本机检测路径.*前置代理/ })).toBeVisible()
    await expect(panel.getByText('http://127.0.0.1:7890')).toBeVisible()

    // Switch back to direct so the next run starts clean.
    await panel.getByRole('button', { name: /修改路径/ }).click()
    await dialog.getByRole('radio', { name: /直连/ }).click()
    await dialog.getByRole('button', { name: '保存' }).click()
    await expect(dialog).toBeHidden()
    await expect(panel.getByRole('heading', { name: /本机检测路径.*直连/ })).toBeVisible()
  })
})
