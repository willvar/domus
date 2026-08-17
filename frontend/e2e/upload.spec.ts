import { expect, test } from '@playwright/test'
import {
  e2eCredentials,
  login,
  openHomeDirectory,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

test('上传文件后可从列表打开并解密读回原文', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
  const fileName = `domus-e2e-upload-${suffix}.txt`
  const filePath = `/home/${credentials.username}/${fileName}`
  const content = `Domus browser upload E2E ${suffix}\n客户端加密、OSS 上传、解密读回。\n`
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openHomeDirectory(page, credentials.username)

    await uploadFromToolbar(page, {
      name: fileName,
      mimeType: 'text/plain',
      buffer: Buffer.from(content),
    })

    const fileItem = page.locator('.file-item').filter({ hasText: fileName })
    await expect(fileItem).toBeVisible({ timeout: 30_000 })
    await expect(fileItem.locator('.file-status-badge')).toHaveCount(0)

    await waitForServiceWorker(page)

    await fileItem.dblclick()
    const viewer = page.locator('.plasma-window').filter({
      has: page.locator('.plasma-titlebar-title', { hasText: fileName }),
    })
    await expect(viewer).toBeVisible()
    await expect(viewer.locator('.cm-content')).toContainText(`Domus browser upload E2E ${suffix}`)
    await expect(viewer.locator('.cm-content')).toContainText('客户端加密、OSS 上传、解密读回。')
  } finally {
    if (authenticated) await permanentlyDelete(page, filePath)
  }
})
