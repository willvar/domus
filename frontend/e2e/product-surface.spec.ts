import { expect, test } from '@playwright/test'
import { apiBaseURL, e2eCredentials, login, websocketActionCount } from './helpers'

test('桌面与移动端共用唯一文件管理界面，退役产品面不再出现', async ({ page }) => {
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  const loginStory = page.locator('.login-story')
  await expect(loginStory).toBeVisible()
  await expect(loginStory).toContainText(/让文件管理回归简单|File management, kept simple/)
  await expect(loginStory).not.toContainText(/OSS|DOFS|对象存储|object storage|直传|encrypt/i)

  await login(page, e2eCredentials())

  await expect(page).toHaveURL(/\/files(?:\?.*)?$/)
  await expect(page.locator('.file-shell')).toBeVisible()
  await expect(page.locator('.file-sidebar')).toBeVisible()
  await expect(page.locator('.mobile-bottom-nav')).toBeHidden()
  await expect(page.locator('.desktop, .plasma-window, .xterm')).toHaveCount(0)
  await expect(page.locator('.file-shell')).not.toContainText(/共享|Share|终端|Terminal|转码|Transcode/)
  await expect(page.locator('.file-sidebar')).not.toContainText(/OSS|DOFS|对象存储|object storage|直传|encrypt/i)
  await expect(page.locator('.file-statusbar')).not.toContainText(/OSS|DOFS|对象存储|object storage|直传|encrypt/i)

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

  const taskCenterButton = page.getByRole('button', { name: /任务中心|Task center/ })
  await expect(taskCenterButton.locator('.activity-center-icon')).toBeVisible()
  await taskCenterButton.click()
  const taskCenterDrawer = page.locator('.mobile-activity-drawer')
  await expect(taskCenterDrawer).toBeVisible()
  await expect(taskCenterDrawer).toContainText(/任务中心|Task center/)
  await expect.poll(async () => {
    const box = await taskCenterDrawer.boundingBox()
    return box ? Math.round(box.y + box.height) : 0
  }).toBe(844)
  const taskCenterBox = await taskCenterDrawer.boundingBox()
  const mobileHeaderBox = await page.locator('.file-header').boundingBox()
  expect(taskCenterBox).not.toBeNull()
  expect(mobileHeaderBox).not.toBeNull()
  expect(taskCenterBox!.x).toBeCloseTo(0, 1)
  expect(taskCenterBox!.width).toBeCloseTo(390, 1)
  expect(taskCenterBox!.height).toBeLessThanOrEqual(844 * 0.72 + 1)
  expect(taskCenterBox!.y).toBeGreaterThan(mobileHeaderBox!.y + mobileHeaderBox!.height)
  await page.keyboard.press('Escape')
  await expect(taskCenterDrawer).toBeHidden()

  await page.locator('.mobile-bottom-nav').getByRole('button', { name: /回收站|Trash/ }).click()
  await expect(page.locator('.content-heading h1')).toHaveText(/回收站|Trash/)
  await page.locator('.mobile-bottom-nav').getByRole('button', { name: /我的文件|My Files/ }).click()
  await expect(page.locator('.content-heading h1')).toHaveText(/我的文件|My Files/)
  await expect(page).toHaveURL(/\/files(?:\?.*)?$/)

  expect(await websocketActionCount(page, 'session.open')).toBe(0)
  expect(await websocketActionCount(page, 'workspace.event')).toBe(0)
})
