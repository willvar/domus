import { expect, test } from '@playwright/test'
import {
  e2eCredentials,
  login,
  openHomeDirectory,
  permanentlyDelete,
  restartBackend,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

test('Domus 进程真实重启后 DOFS 文件仍可在新会话解密读取', async ({ page }) => {
  test.skip(process.env.E2E_REUSE_SERVERS === '1', 'cannot restart a caller-owned backend')

  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const fileName = `e2e-restart-${suffix}.txt`
  const filePath = `/home/${credentials.username}/${fileName}`
  const marker = `DOMUS_RESTART_PERSISTENCE_${suffix}`
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openHomeDirectory(page, credentials.username)
    await uploadFromToolbar(page, {
      name: fileName,
      mimeType: 'text/plain',
      buffer: Buffer.from(`${marker}\n`),
    })
    await expect(page.locator('.file-item').filter({ hasText: fileName })).toBeVisible()

    await restartBackend(page)
    await page.context().clearCookies()
    authenticated = false
    await login(page, credentials)
    authenticated = true
    await openHomeDirectory(page, credentials.username)

    const persisted = page.locator('.file-item').filter({ hasText: fileName })
    await expect(persisted).toBeVisible()
    await waitForServiceWorker(page)
    await persisted.dblclick()
    const viewer = page.locator('.plasma-window').filter({
      has: page.locator('.plasma-titlebar-title', { hasText: fileName }),
    })
    await expect(viewer.locator('.cm-content')).toContainText(marker)
  } finally {
    if (authenticated) await permanentlyDelete(page, filePath)
  }
})
