import { expect, test } from '@playwright/test'
import { e2eCredentials, login, websocketActionCount } from './helpers'

test('终端尺寸同步保持有界且桌面持续响应', async ({ page }) => {
  await login(page, e2eCredentials())

  const terminalIcon = page.locator('.desktop-icon').filter({ hasText: /终端|Terminal/ })
  await expect(terminalIcon).toBeVisible()
  await terminalIcon.dblclick()

  const terminalWindow = page.locator('.plasma-window').filter({
    has: page.locator('.plasma-titlebar-title', { hasText: /终端|Terminal/ }),
  })
  await expect(terminalWindow).toBeVisible()
  await expect(terminalWindow.locator('.xterm')).toBeVisible({ timeout: 120_000 })
  await expect.poll(() => websocketActionCount(page, 'session.open'), { timeout: 120_000 }).toBe(1)

  await page.waitForTimeout(2_000)
  const resizeCount = await websocketActionCount(page, 'session.resize')
  await page.waitForTimeout(3_000)
  const settledResizeCount = await websocketActionCount(page, 'session.resize')

  expect(resizeCount).toBeLessThan(20)
  expect(settledResizeCount - resizeCount).toBeLessThanOrEqual(2)
  await expect(page.locator('.desktop')).toBeVisible()
})
