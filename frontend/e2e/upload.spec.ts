import { expect, test } from '@playwright/test'
import {
  e2eCredentials,
  login,
  openFilesRoot,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

test('上传文件后可从列表打开并解密读回原文', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
  const fileName = `domus-e2e-upload-${suffix}.txt`
  const filePath = `/${fileName}`
  const content = `Domus browser upload E2E ${suffix}\n客户端加密、OSS 上传、解密读回。\n`
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openFilesRoot(page)

    await uploadFromToolbar(page, {
      name: fileName,
      mimeType: 'text/plain',
      buffer: Buffer.from(content),
    })

    const fileItem = page.locator('.file-item').filter({ has: page.locator('.file-name').getByText(fileName, { exact: true }) })
    await expect(fileItem).toBeVisible({ timeout: 30_000 })

    await waitForServiceWorker(page)

    await fileItem.dblclick()
    await expect(page).toHaveURL(/\/preview\?/)
    await expect(page.locator('.preview-title')).toContainText(fileName)
    await expect(page.locator('.text-preview')).toContainText(`Domus browser upload E2E ${suffix}`)
    await expect(page.locator('.text-preview')).toContainText('客户端加密、OSS 上传、解密读回。')

    await page.locator('.back-button').click()
    await expect(page.locator('.file-shell')).toBeVisible()
    await page.locator('.global-search input').fill(`upload-${suffix}`)
    await expect(page.locator('.content-heading')).toContainText(/搜索结果|Search Results/)
    await expect(page.locator('.file-item').filter({ has: page.locator('.file-name').getByText(fileName, { exact: true }) })).toBeVisible()
  } finally {
    if (authenticated) await permanentlyDelete(page, filePath)
  }
})
