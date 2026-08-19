import { expect, test } from '@playwright/test'
import { apiBaseURL, e2eCredentials, login, websocketActionCount } from './helpers'

test('桌面与移动端共用唯一文件管理界面，退役产品面不再出现', async ({ page }) => {
  await login(page, e2eCredentials())

  await expect(page).toHaveURL(/\/files(?:\?.*)?$/)
  await expect(page.locator('.file-shell')).toBeVisible()
  await expect(page.locator('.file-sidebar')).toBeVisible()
  await expect(page.locator('.mobile-bottom-nav')).toBeHidden()
  await expect(page.locator('.desktop, .plasma-window, .xterm')).toHaveCount(0)
  await expect(page.locator('.file-shell')).not.toContainText(/共享|Share|终端|Terminal|转码|Transcode/)

  for (const response of [
    await page.request.get(`${apiBaseURL}/file/shared`),
    await page.request.post(`${apiBaseURL}/file/share`, { data: {} }),
    await page.request.post(`${apiBaseURL}/file/transcode`, { data: {} }),
    await page.request.get(`${apiBaseURL}/workspace/`),
  ]) {
    expect(response.status()).toBe(404)
  }

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('.file-shell')).toBeVisible()
  await expect(page.locator('.file-sidebar')).toBeHidden()
  await expect(page.locator('.mobile-bottom-nav')).toBeVisible()
  await page.locator('.mobile-bottom-nav').getByRole('button', { name: /回收站|Trash/ }).click()
  await expect(page.locator('.content-heading h1')).toHaveText(/回收站|Trash/)
  await page.locator('.mobile-bottom-nav').getByRole('button', { name: /我的文件|My Files/ }).click()
  await expect(page.locator('.content-heading h1')).toHaveText(/我的文件|My Files/)
  await expect(page).toHaveURL(/\/files(?:\?.*)?$/)

  expect(await websocketActionCount(page, 'session.open')).toBe(0)
  expect(await websocketActionCount(page, 'workspace.event')).toBe(0)
})
