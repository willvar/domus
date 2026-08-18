import { readFileSync } from 'node:fs'
import { expect, test, type Page } from '@playwright/test'
import {
  e2eCredentials,
  login,
  openHomeDirectory,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

async function downloadItem(page: Page, fileName: string): Promise<Buffer> {
  await waitForServiceWorker(page)
  const item = page.locator('.file-item').filter({ hasText: fileName })
  await expect(item).toBeVisible()
  const downloadPromise = page.waitForEvent('download')
  await item.dblclick()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toBe(fileName)
  const path = await download.path()
  expect(path).not.toBeNull()
  return readFileSync(path!)
}

test('0B 与跨分片大文件均由浏览器加密直传并完整下载', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const emptyName = `e2e-empty-${suffix}.bin`
  const largeName = `e2e-large-${suffix}.bin`
  const homePath = `/home/${credentials.username}/`
  const large = Buffer.allocUnsafe(9 * 1024 * 1024 + 37)
  for (let index = 0; index < large.length; index++) large[index] = (index * 31 + 17) & 0xff
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openHomeDirectory(page, credentials.username)

    const emptyUpload = await uploadFromToolbar(page, {
      name: emptyName,
      mimeType: 'application/octet-stream',
      buffer: Buffer.alloc(0),
    })
    expect(emptyUpload.multipartPartCount).toBe(1)
    expect(await downloadItem(page, emptyName)).toHaveLength(0)

    const largeUpload = await uploadFromToolbar(page, {
      name: largeName,
      mimeType: 'application/octet-stream',
      buffer: large,
    })
    expect(largeUpload.multipartPartCount).toBeGreaterThan(1)
    const downloaded = await downloadItem(page, largeName)
    expect(downloaded.equals(large)).toBeTruthy()
  } finally {
    if (authenticated) {
      await permanentlyDelete(page, `${homePath}${emptyName}`)
      await permanentlyDelete(page, `${homePath}${largeName}`)
    }
  }
})
