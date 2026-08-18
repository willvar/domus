import { expect, test, type Request, type Response } from '@playwright/test'
import { apiBaseURL, e2eCredentials, login } from './helpers'

test('公开头像由浏览器直读 OSS 密文并本地解密', async ({ page }) => {
  const root = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const username = `avatar-${suffix}`
  const password = `Avatar-${suffix}-pass`
  let userID = ''
  let rootSession = false

  try {
    await login(page, root)
    rootSession = true
    const createResponse = await page.request.post(`${apiBaseURL}/audit/user/`, {
      data: { username, password, role: 'user' },
    })
    const createBody = await createResponse.text()
    expect(createResponse.status(), createBody).toBe(201)
    const created = JSON.parse(createBody) as { id?: unknown }
    expect(typeof created.id).toBe('string')
    userID = created.id as string

    await page.context().clearCookies()
    rootSession = false
    await login(page, { username, password })

    const frontendOrigin = new URL(page.url()).origin
    const directObjectGETs: string[] = []
    const avatarControlResponses: Response[] = []
    const observeRequest = (request: Request): void => {
      const url = new URL(request.url())
      if (request.method() === 'GET' && url.origin !== apiBaseURL && url.origin !== frontendOrigin) {
        directObjectGETs.push(url.toString())
      }
    }
    const observeResponse = (response: Response): void => {
      const url = new URL(response.url())
      if (url.origin === apiBaseURL && url.pathname === `/user/avatar/${username}`) {
        avatarControlResponses.push(response)
      }
    }
    page.on('request', observeRequest)
    page.on('response', observeResponse)

    try {
      const profileIcon = page.locator('.desktop-icon').filter({ hasText: /^(我|Me)$/ })
      await expect(profileIcon).toBeVisible()
      await profileIcon.dblclick()
      const profile = page.locator('.profile-app')
      await expect(profile).toBeVisible()

      const chooserPromise = page.waitForEvent('filechooser')
      await profile.locator('.profile-avatar').click()
      const chooser = await chooserPromise
      await chooser.setFiles({
        name: 'avatar.svg',
        mimeType: 'image/svg+xml',
        buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#32a852"/></svg>'),
      })

      const avatarImage = profile.locator('.profile-avatar-img')
      await expect(avatarImage).toBeVisible({ timeout: 120_000 })
      await expect(avatarImage).toHaveAttribute('src', /^blob:/)

      const descriptorResponse = await page.request.get(`${apiBaseURL}/user/avatar/${username}`)
      expect(descriptorResponse.status()).toBe(200)
      expect(descriptorResponse.headers()['content-type']).toContain('application/json')
      expect(descriptorResponse.headers()['cache-control']).toBe('no-store')
      const descriptor = await descriptorResponse.json() as {
        url?: unknown
        dek?: unknown
        content_type?: unknown
      }
      expect(typeof descriptor.url).toBe('string')
      expect(typeof descriptor.dek).toBe('string')
      const directURL = descriptor.url as string
      expect(new URL(directURL).origin).not.toBe(apiBaseURL)
      expect(descriptor.dek as string).toMatch(/^[0-9a-f]{64}$/)
      expect(descriptor.content_type).toBe('image/webp')
      const directObject = new URL(directURL)
      expect(
        directObjectGETs.some((candidate) => {
          const observed = new URL(candidate)
          return observed.origin === directObject.origin && observed.pathname === directObject.pathname
        }),
        'browser did not fetch the avatar ciphertext object directly from OSS',
      ).toBeTruthy()
      expect(
        avatarControlResponses.every(response => response.headers()['content-type']?.includes('application/json')),
        'Domus proxied an avatar image body instead of returning control metadata',
      ).toBeTruthy()
    } finally {
      page.off('request', observeRequest)
      page.off('response', observeResponse)
    }
  } finally {
    if (userID) {
      if (!rootSession) {
        await page.context().clearCookies()
        await login(page, root)
      }
      const deleteResponse = await page.request.delete(`${apiBaseURL}/audit/user/${userID}`)
      expect(deleteResponse.ok(), `temporary user cleanup failed with HTTP ${deleteResponse.status()}`).toBeTruthy()
    }
  }
})
