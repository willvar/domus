import { expect, test, type BrowserContext, type Page } from '@playwright/test'
import {
  apiBaseURL,
  e2eCredentials,
  login,
  openFilesRoot,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

interface CreatedUser {
  id: string
  username: string
  password: string
}

async function createUser(rootPage: Page, label: string, suffix: string): Promise<CreatedUser> {
  const username = `iso-${label}-${suffix}`
  const password = `Isolation-${label}-${suffix}-pass`
  const response = await rootPage.request.post(`${apiBaseURL}/audit/user/`, {
    data: { username, password, role: 'user' },
  })
  const body = await response.text()
  expect(response.status(), body).toBe(201)
  const parsed = JSON.parse(body) as { id?: unknown }
  expect(typeof parsed.id).toBe('string')
  return { id: parsed.id as string, username, password }
}

function itemNamed(page: Page, name: string) {
  return page.locator('.file-item').filter({
    has: page.locator('.file-name').getByText(name, { exact: true }),
  })
}

async function expectOwnText(page: Page, fileName: string, marker: string): Promise<void> {
  await waitForServiceWorker(page)
  const item = itemNamed(page, fileName)
  await expect(item).toBeVisible()
  await item.dblclick()
  await expect(page.locator('.text-preview')).toContainText(marker)
  await page.locator('.back-button').click()
  await expect(page.locator('.file-shell')).toBeVisible()
}

test('同名文件、密钥与控制面命名空间在租户间隔离，退役共享接口不可达', async ({ browser, page }) => {
  const root = e2eCredentials()
  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
  const fileName = `same-name-${suffix}.txt`
  const contexts: BrowserContext[] = []
  const users: CreatedUser[] = []

  try {
    await login(page, root)
    const userA = await createUser(page, 'a', suffix)
    users.push(userA)
    const userB = await createUser(page, 'b', suffix)
    users.push(userB)

    const contextA = await browser.newContext()
    contexts.push(contextA)
    const pageA = await contextA.newPage()
    await login(pageA, { username: userA.username, password: userA.password })
    await openFilesRoot(pageA)

    const contextB = await browser.newContext()
    contexts.push(contextB)
    const pageB = await contextB.newPage()
    await login(pageB, { username: userB.username, password: userB.password })
    await openFilesRoot(pageB)

    const markerA = `only-user-a-${suffix}`
    const markerB = `only-user-b-${suffix}`
    const filePath = `/${fileName}`
    await uploadFromToolbar(pageA, { name: fileName, mimeType: 'text/plain', buffer: Buffer.from(`${markerA}\n`) })
    await uploadFromToolbar(pageB, { name: fileName, mimeType: 'text/plain', buffer: Buffer.from(`${markerB}\n`) })

    const accessA = await pageA.request.get(`${apiBaseURL}/file/access`, { params: { path: filePath } })
    const accessB = await pageB.request.get(`${apiBaseURL}/file/access`, { params: { path: filePath } })
    expect(accessA.ok()).toBeTruthy()
    expect(accessB.ok()).toBeTruthy()
    const descriptorA = await accessA.json() as { url: string; dek: string; content_hash?: string }
    const descriptorB = await accessB.json() as { url: string; dek: string; content_hash?: string }
    expect(new URL(descriptorA.url).origin).not.toBe(apiBaseURL)
    expect(new URL(descriptorB.url).origin).not.toBe(apiBaseURL)
    expect(descriptorA.url).not.toBe(descriptorB.url)
    expect(descriptorA.dek).not.toBe(descriptorB.dek)
    expect(descriptorA.content_hash).not.toBe(descriptorB.content_hash)

    expect((await pageA.request.get(`${apiBaseURL}/file/`, { params: { path: '/.domus/' } })).status()).toBe(403)
    expect((await pageB.request.get(`${apiBaseURL}/file/`, { params: { path: '/.domus/' } })).status()).toBe(403)

    await expectOwnText(pageA, fileName, markerA)
    await expectOwnText(pageB, fileName, markerB)

    for (const retired of [
      await pageA.request.get(`${apiBaseURL}/file/shared`),
      await pageA.request.get(`${apiBaseURL}/file/shares`, { params: { path: filePath } }),
      await pageA.request.post(`${apiBaseURL}/file/share`, { data: { path: filePath, target_username: userB.username } }),
      await pageA.request.post(`${apiBaseURL}/file/transcode`, { data: { path: filePath, profile: 'audio-mp3' } }),
      await pageA.request.get(`${apiBaseURL}/workspace/`),
    ]) {
      expect(retired.status()).toBe(404)
    }
    await expect(pageA.locator('.file-shell')).not.toContainText(/共享|Share|终端|Terminal/)
  } finally {
    for (const context of contexts.reverse()) await context.close().catch(() => {})
    for (const user of users.reverse()) {
      const response = await page.request.delete(`${apiBaseURL}/audit/user/${user.id}`)
      expect(response.ok(), `temporary user cleanup failed with HTTP ${response.status()}`).toBeTruthy()
    }
  }
})
