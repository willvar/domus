import { readFileSync } from 'node:fs'
import { dirname, isAbsolute, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, type Page, type Request } from '@playwright/test'

export const apiBaseURL = (process.env.DOMUS_E2E_API_BASE || 'http://127.0.0.1:8088').replace(/\/$/, '')
const isolatedPages = new WeakSet<Page>()

const e2eDirectory = dirname(fileURLToPath(import.meta.url))
const projectDirectory = resolve(e2eDirectory, '../..')
const defaultPasswordFile = resolve(e2eDirectory, '../../tmp/dev/root-bootstrap-password')

function runtimeControlFile(name: string): string {
  const configured = process.env.DOMUS_E2E_RUNTIME_ROOT?.trim() || 'tmp/dev'
  const runtimeRoot = isAbsolute(configured) ? configured : resolve(projectDirectory, configured)
  return resolve(runtimeRoot, 'run', name)
}

function readPositiveInteger(path: string, description: string): number {
  const value = Number.parseInt(readFileSync(path, 'utf8').trim(), 10)
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`invalid ${description} in ${path}`)
  }
  return value
}

export interface E2ECredentials {
  username: string
  password: string
}

export interface UploadObservation {
  multipartPartCount: number
  objectUploadOrigins: string[]
}

export interface StorageUsage {
  size: number
  count: number
}

export async function getStorageUsage(page: Page): Promise<StorageUsage> {
  const response = await page.request.get(`${apiBaseURL}/user/storage`)
  expect(response.ok(), `storage usage failed with HTTP ${response.status()}`).toBeTruthy()
  return await response.json() as StorageUsage
}

export async function restartBackend(page: Page): Promise<void> {
  const supervisorPIDFile = runtimeControlFile('e2e-backend-supervisor.pid')
  const generationFile = runtimeControlFile('e2e-backend-generation')
  const supervisorPID = readPositiveInteger(supervisorPIDFile, 'E2E backend supervisor PID')
  const previousGeneration = readPositiveInteger(generationFile, 'E2E backend generation')

  process.kill(supervisorPID, 'SIGUSR1')

  await expect
    .poll(() => {
      try {
        return readPositiveInteger(generationFile, 'E2E backend generation')
      } catch {
        return previousGeneration
      }
    }, {
      timeout: 180_000,
      message: 'Domus backend supervisor did not publish a restarted generation',
    })
    .toBeGreaterThan(previousGeneration)

  await expect
    .poll(async () => {
      try {
        return (await page.request.get(`${apiBaseURL}/auth`, { timeout: 2_000 })).status()
      } catch {
        return 0
      }
    }, {
      timeout: 30_000,
      message: 'Domus API did not become ready after the supervised restart',
    })
    .toBe(200)
}

async function observeWebSocketActions(page: Page): Promise<void> {
  if (isolatedPages.has(page)) return
  isolatedPages.add(page)

  await page.addInitScript(() => {
    const send = WebSocket.prototype.send
    const actionCounts: Record<string, number> = {}
    Object.defineProperty(window, '__domusE2EWebSocketActions', {
      configurable: true,
      value: actionCounts,
    })
    WebSocket.prototype.send = function (data): void {
      if (typeof data === 'string') {
        try {
          const message = JSON.parse(data) as { id?: unknown; action?: unknown }
          if (typeof message.action === 'string') {
            actionCounts[message.action] = (actionCounts[message.action] || 0) + 1
          }
        } catch {
          // Ignore non-JSON websocket payloads.
        }
      }
      send.call(this, data)
    }
  })
}

export async function websocketActionCount(page: Page, action: string): Promise<number> {
  return page.evaluate((requestedAction) => {
    const counts = (window as Window & {
      __domusE2EWebSocketActions?: Record<string, number>
    }).__domusE2EWebSocketActions
    return counts?.[requestedAction] || 0
  }, action)
}

export function e2eCredentials(): E2ECredentials {
  const username = process.env.DOMUS_E2E_USERNAME?.trim() || 'root'
  const explicitPassword = process.env.DOMUS_E2E_PASSWORD?.trim()
  if (explicitPassword) return { username, password: explicitPassword }

  const passwordFile = process.env.DOMUS_E2E_PASSWORD_FILE?.trim() || defaultPasswordFile
  let password = ''
  try {
    password = readFileSync(passwordFile, 'utf8').trim()
  } catch (error) {
    throw new Error(
      `Unable to read the E2E password file ${passwordFile}; set DOMUS_E2E_PASSWORD or DOMUS_E2E_PASSWORD_FILE`,
      { cause: error },
    )
  }
  if (!password) throw new Error(`E2E password file is empty: ${passwordFile}`)
  return { username, password }
}

export async function login(page: Page, credentials = e2eCredentials()): Promise<void> {
  await observeWebSocketActions(page)

  await expect
    .poll(
      async () => {
        try {
          return (await page.request.get(`${apiBaseURL}/auth`, { timeout: 2_000 })).status()
        } catch {
          return 0
        }
      },
      { timeout: 120_000, message: `Domus API did not become ready at ${apiBaseURL}` },
    )
    .toBe(200)

  const loginCard = page.locator('.login-card')
  let frontendLoaded = false
  // Chromium can emit ERR_NETWORK_CHANGED while its first context is being
  // attached to the host network. A failed Vite module request leaves a blank
  // document and cannot recover without navigation, so retry only this initial
  // page bootstrap; all application requests below remain single-attempt.
  for (let attempt = 0; attempt < 3 && !frontendLoaded; attempt++) {
    try {
      await page.goto('/', { waitUntil: 'domcontentloaded' })
      await loginCard.waitFor({ state: 'visible', timeout: 5_000 })
      frontendLoaded = true
    } catch {
      if (attempt === 2) throw new Error('Domus frontend did not render the login page after 3 navigations')
    }
  }
  await page.locator('.login-card input[type="text"]:visible').first().fill(credentials.username)
  await page.locator('.login-card input[type="password"]:visible').fill(credentials.password)

  const loginResponsePromise = page.waitForResponse((response) => {
    const url = new URL(response.url())
    return url.origin === apiBaseURL && url.pathname === '/auth' && response.request().method() === 'POST'
  })
  await page.locator('.login-card button[type="submit"]:visible').click()
  const loginResponse = await loginResponsePromise
  expect(loginResponse.ok(), `login failed with HTTP ${loginResponse.status()}`).toBeTruthy()
  await expect(page).toHaveURL(/\/files(?:\?.*)?$/)
}

export async function openFilesRoot(page: Page): Promise<void> {
  await page.goto('/files', { waitUntil: 'domcontentloaded' })
  await expect(page.locator('.file-shell')).toBeVisible()
  await expect(page.locator('.breadcrumbs')).toContainText(/我的文件|My Files/)
  await expect(page.locator('.file-surface')).toBeVisible()
  const response = await page.request.get(`${apiBaseURL}/file/`, { params: { path: '/' } })
  expect(response.ok(), `root listing failed with HTTP ${response.status()}`).toBeTruthy()
}

export async function waitForServiceWorker(page: Page): Promise<void> {
  await expect
    .poll(() => page.evaluate(() => Boolean(navigator.serviceWorker?.controller)), {
      timeout: 20_000,
      message: 'service worker did not take control for client-side decryption',
    })
    .toBe(true)
}

export async function uploadFromToolbar(
  page: Page,
  file: { name: string; mimeType: string; buffer: Buffer; conflictAction?: 'replace' | 'rename' },
): Promise<UploadObservation> {
  const apiControlBodies: string[] = []
  const apiControlBodyBuffers: Buffer[] = []
  const objectUploadRequests: Array<{ url: string; body: Buffer | null }> = []
  const observeControlRequest = (request: Request): void => {
    const url = new URL(request.url())
    if (request.method() === 'PUT' && url.searchParams.has('partNumber') && url.searchParams.has('uploadId')) {
      objectUploadRequests.push({ url: url.toString(), body: request.postDataBuffer() })
    }
    if (url.origin === apiBaseURL) {
      const body = request.postData()
      if (body) apiControlBodies.push(body)
      const bodyBuffer = request.postDataBuffer()
      if (bodyBuffer) apiControlBodyBuffers.push(bodyBuffer)
    }
  }
  page.on('request', observeControlRequest)

  const completionResponsePromise = page.waitForResponse((response) => {
    const url = new URL(response.url())
    if (url.origin !== apiBaseURL || url.pathname !== '/file/upload' || response.request().method() !== 'POST') {
      return false
    }
    try {
      const body = response.request().postDataJSON() as {
        upload_id?: unknown
        content_hash?: unknown
        encrypted_size?: unknown
      }
      return typeof body.upload_id === 'string' &&
        typeof body.content_hash === 'string' &&
        typeof body.encrypted_size === 'number'
    } catch {
      return false
    }
  }, { timeout: 120_000 })

  try {
    const fileChooserPromise = page.waitForEvent('filechooser')
    await page.locator('.primary-actions').getByRole('button', { name: /上传文件|Upload Files/ }).click()
    const fileChooser = await fileChooserPromise
    await fileChooser.setFiles(file)

    if (file.conflictAction) {
      const duplicateSummary = page.locator('.duplicate-dialog__summary').filter({ hasText: file.name })
      await expect(duplicateSummary).toBeVisible()
      const action = file.conflictAction === 'replace' ? /替换|Replace/ : /重命名|Rename/
      await page.getByRole('button', { name: action }).click()
    }

    if (file.buffer.length === 0) {
      const zeroByteDialog = page.locator('.domus-dialog').filter({ hasText: /0B|0.?byte|zero.?byte/i })
      await expect(zeroByteDialog).toBeVisible()
      await zeroByteDialog.locator('.n-dialog__action button').last().click()
    }

    const completionResponse = await completionResponsePromise
    const completionBody = await completionResponse.json().catch(() => ({})) as { error?: unknown }
    const completionError = typeof completionBody.error === 'string' ? ` (${completionBody.error})` : ''
    expect(
      completionResponse.ok(),
      `upload completion failed with HTTP ${completionResponse.status()}${completionError}`,
    ).toBeTruthy()
  } finally {
    page.off('request', observeControlRequest)
  }

  expect(objectUploadRequests.length, 'browser did not issue a multipart PUT to object storage').toBeGreaterThan(0)
  for (const upload of objectUploadRequests) {
    expect(new URL(upload.url).origin, 'object data was PUT to the Domus HTTP origin').not.toBe(apiBaseURL)
    expect(upload.body, 'Playwright could not inspect an object-store PUT body').not.toBeNull()
  }
  if (file.buffer.length > 0) {
    expect(
      apiControlBodyBuffers.every(body => !body.includes(file.buffer)),
      'plaintext file bytes appeared in a Domus HTTP request body',
    ).toBeTruthy()
    expect(
      objectUploadRequests.every(upload => !upload.body?.includes(file.buffer)),
      'plaintext file bytes appeared in an object-store PUT body',
    ).toBeTruthy()
  }

  // Reassemble each observed multipart object and require one to match the
  // exact DOFS v1 encrypted geometry for the source file. This is stronger
  // than merely failing to find plaintext in the request body: it proves the
  // browser actually sent the expected encrypted representation to OSS.
  const multipartObjects = new Map<string, Array<{ partNumber: number; body: Buffer }>>()
  for (const upload of objectUploadRequests) {
    if (!upload.body) continue
    const url = new URL(upload.url)
    const uploadID = url.searchParams.get('uploadId') || url.toString()
    const partNumber = Number.parseInt(url.searchParams.get('partNumber') || '1', 10)
    const parts = multipartObjects.get(uploadID) || []
    parts.push({ partNumber, body: upload.body })
    multipartObjects.set(uploadID, parts)
  }
  const cryptoChunkSize = 65536
  const expectedEncryptedSize = 5 + file.buffer.length + Math.ceil(file.buffer.length / cryptoChunkSize) * (12 + 16)
  const hasExpectedEncryptedObject = [...multipartObjects.values()].some((parts) => {
    const body = Buffer.concat(parts.sort((left, right) => left.partNumber - right.partNumber).map(part => part.body))
    return body.length === expectedEncryptedSize &&
      body[0] === 0x01 &&
      body.readUInt32BE(1) === cryptoChunkSize
  })
  expect(hasExpectedEncryptedObject, 'OSS PUT did not contain a valid DOFS v1 encrypted source object').toBeTruthy()

  // Regression guard for the architecture boundary: textual file content
  // must be encrypted before any network upload and must not be smuggled into
  // an API metadata field (the former search_text behavior did exactly that).
  if (file.mimeType.startsWith('text/')) {
    const fragments = file.buffer.toString('utf8')
      .split(/\r?\n/)
      .map(value => value.trim())
      .filter(value => value.length >= 16)
    for (const fragment of fragments) {
      expect(
        apiControlBodies.every(body => !body.includes(fragment)),
        `plaintext file fragment reached the Domus control plane: ${fragment}`,
      ).toBeTruthy()
    }
  }

  return {
    multipartPartCount: objectUploadRequests.length,
    objectUploadOrigins: objectUploadRequests.map(upload => new URL(upload.url).origin),
  }
}

export async function permanentlyDelete(page: Page, path: string): Promise<void> {
  const response = await page.request.delete(`${apiBaseURL}/file/delete`, {
    params: { path, permanent: 'true' },
  })
  if (response.status() === 404) return
  expect(response.ok(), `cleanup failed with HTTP ${response.status()}`).toBeTruthy()
}
