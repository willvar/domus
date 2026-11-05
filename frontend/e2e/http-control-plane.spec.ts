import { expect, test } from '@playwright/test'
import {
  apiBaseURL,
  login,
  openFilesRoot,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

test('real HTTP JSON mutations preserve SSE copying and encoded paths', async ({ page }) => {
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const sourceName = `e2e-http-${suffix} 中文.txt`
  const sourcePath = `/${sourceName}`
  const directoryPath = `/e2e-http-copy-${suffix}/`
  const copiedName = 'copied 中文.txt'
  const copiedPath = directoryPath + copiedName
  const contents = `HTTP migration roundtrip ${suffix}\n客户端加密与复制内容保持一致。\n`
  let authenticated = false
  try {
    await login(page)
    authenticated = true
    await openFilesRoot(page)
    await uploadFromToolbar(page, { name: sourceName, mimeType: 'text/plain', buffer: Buffer.from(contents) })
    const mkdir = await page.request.post(`${apiBaseURL}/file/mkdir`, { data: { path: directoryPath } })
    expect(mkdir.ok()).toBeTruthy()
    const copy = await page.request.post(`${apiBaseURL}/file/copy`, {
      headers: { Accept: 'text/event-stream', 'Accept-Encoding': 'gzip' },
      data: { src_path: sourcePath, dst_path: copiedPath, is_dir: false },
    })
    expect(copy.status()).toBe(200)
    expect(copy.headers()['content-type']).toContain('text/event-stream')
    expect(copy.headers()['content-encoding']).toBeUndefined()
    const events = (await copy.text()).split('\n').filter(line => line.startsWith('data: ')).map(line => JSON.parse(line.slice(6)))
    expect(events.some(event => event.error)).toBeFalsy()
    expect(events.at(-1)).toEqual({ done: true })
    const listing = await page.request.get(`${apiBaseURL}/file/`, { params: { path: directoryPath } })
    expect(listing.ok()).toBeTruthy()
    expect((await listing.json()).files.map((file: { path: string }) => file.path)).toEqual([copiedPath])
    await page.goto(`/files?path=${encodeURIComponent(directoryPath)}`)
    const item = page.locator('.file-item').filter({ has: page.locator('.file-name').getByText(copiedName, { exact: true }) })
    await expect(item).toBeVisible()
    await waitForServiceWorker(page)
    await item.dblclick()
    await expect(page.locator('.text-preview')).toHaveText(contents)
  } finally {
    if (authenticated) {
      await permanentlyDelete(page, sourcePath)
      await permanentlyDelete(page, directoryPath)
    }
  }
})
