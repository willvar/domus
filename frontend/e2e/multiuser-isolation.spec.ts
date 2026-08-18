import { expect, test, type BrowserContext, type Page } from '@playwright/test'
import {
  apiBaseURL,
  e2eCredentials,
  login,
  openHomeDirectory,
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

async function expectOwnText(page: Page, fileName: string, marker: string): Promise<void> {
  await waitForServiceWorker(page)
  const item = page.locator('.file-item').filter({ hasText: fileName })
  await expect(item).toBeVisible()
  await item.dblclick()
  const viewer = page.locator('.plasma-window').filter({
    has: page.locator('.plasma-titlebar-title', { hasText: fileName }),
  })
  await expect(viewer.locator('.cm-content')).toContainText(marker)
  await viewer.locator('.plasma-btn-close').click()
  await expect(viewer).toBeHidden()
}

test('两个用户的同名文件、密钥和控制面命名空间彼此隔离', async ({ browser, page }) => {
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
    await openHomeDirectory(pageA, userA.username)

    const contextB = await browser.newContext()
    contexts.push(contextB)
    const pageB = await contextB.newPage()
    await login(pageB, { username: userB.username, password: userB.password })
    await openHomeDirectory(pageB, userB.username)

    const markerA = `only-user-a-${suffix}`
    const markerB = `only-user-b-${suffix}`
    const pathA = `/home/${userA.username}/${fileName}`
    const pathB = `/home/${userB.username}/${fileName}`
    await uploadFromToolbar(pageA, {
      name: fileName,
      mimeType: 'text/plain',
      buffer: Buffer.from(`${markerA}\n`),
    })
    await uploadFromToolbar(pageB, {
      name: fileName,
      mimeType: 'text/plain',
      buffer: Buffer.from(`${markerB}\n`),
    })

    const accessA = await pageA.request.get(`${apiBaseURL}/file/access`, { params: { path: pathA } })
    const accessB = await pageB.request.get(`${apiBaseURL}/file/access`, { params: { path: pathB } })
    expect(accessA.ok()).toBeTruthy()
    expect(accessB.ok()).toBeTruthy()
    const descriptorA = await accessA.json() as { url: string; dek: string; content_hash?: string }
    const descriptorB = await accessB.json() as { url: string; dek: string; content_hash?: string }
    expect(new URL(descriptorA.url).origin).not.toBe(apiBaseURL)
    expect(new URL(descriptorB.url).origin).not.toBe(apiBaseURL)
    expect(descriptorA.url).not.toBe(descriptorB.url)
    expect(descriptorA.dek).not.toBe(descriptorB.dek)
    expect(descriptorA.content_hash).not.toBe(descriptorB.content_hash)

    expect((await pageA.request.get(`${apiBaseURL}/file/access`, { params: { path: pathB } })).status()).toBe(404)
    expect((await pageB.request.get(`${apiBaseURL}/file/access`, { params: { path: pathA } })).status()).toBe(404)

    const crossListA = await pageA.request.get(`${apiBaseURL}/file/`, {
      params: { path: `/home/${userB.username}/` },
    })
    const crossListB = await pageB.request.get(`${apiBaseURL}/file/`, {
      params: { path: `/home/${userA.username}/` },
    })
    expect(((await crossListA.json()) as { files?: unknown[] }).files || []).toHaveLength(0)
    expect(((await crossListB.json()) as { files?: unknown[] }).files || []).toHaveLength(0)

    await expectOwnText(pageA, fileName, markerA)
    await expectOwnText(pageB, fileName, markerB)

    const itemA = pageA.locator('.file-item').filter({ hasText: fileName })
    await itemA.click({ button: 'right' })
    await pageA.locator('.plasma-context-menu .ctx-item').filter({
      hasText: /^(分享文件|Share File)$/,
    }).click()
    const shareDialog = pageA.locator('.breeze-modal-dialog').filter({ hasText: /分享文件|Share File/ })
    await expect(shareDialog).toBeVisible()
    await shareDialog.locator('.share-form input').fill(userB.username)
    await shareDialog.locator('.share-submit button').click()
    const shareCard = shareDialog.locator('.share-card').filter({ hasText: userB.username })
    await expect(shareCard).toBeVisible()

    const ownedResponse = await pageA.request.get(`${apiBaseURL}/file/shares`, { params: { path: pathA } })
    expect(ownedResponse.ok()).toBeTruthy()
    const owned = await ownedResponse.json() as Array<{ id: number; share_id: string }>
    expect(owned).toHaveLength(1)
    const shareID = owned[0].share_id
    expect((await pageA.request.get(`${apiBaseURL}/file/shared/${shareID}`)).status()).toBe(403)
    expect((await pageB.request.get(`${apiBaseURL}/file/shared/${shareID}`)).ok()).toBeTruthy()

    await pageB.locator('.place-item').filter({ hasText: /共享|Shared with me/ }).click()
    const sharedItem = pageB.locator('.file-item').filter({ hasText: fileName })
    await expect(sharedItem).toBeVisible()
    await sharedItem.dblclick()
    const sharedViewer = pageB.locator('.plasma-window').filter({
      has: pageB.locator('.plasma-titlebar-title', { hasText: fileName }),
    })
    await expect(sharedViewer.locator('.cm-content')).toContainText(markerA)
    await sharedViewer.locator('.plasma-btn-close').click()

    await shareCard.getByRole('button', { name: /停止分享|Stop sharing/i }).click()
    const revokeDialog = pageA.locator('.breeze-modal-dialog').filter({
      has: pageA.locator('.breeze-modal-dialog__footer'),
      hasText: /停止.*分享|Stop sharing/i,
    })
    await expect(revokeDialog).toBeVisible()
    await revokeDialog.locator('.breeze-modal-dialog__footer button').last().click()
    await expect(shareCard).toBeHidden()
    await expect(sharedItem).toBeHidden()
    expect((await pageB.request.get(`${apiBaseURL}/file/shared/${shareID}`)).status()).toBe(404)
  } finally {
    for (const context of contexts.reverse()) await context.close().catch(() => {})
    for (const user of users.reverse()) {
      const response = await page.request.delete(`${apiBaseURL}/audit/user/${user.id}`)
      expect(response.ok(), `temporary user cleanup failed with HTTP ${response.status()}`).toBeTruthy()
    }
  }
})
