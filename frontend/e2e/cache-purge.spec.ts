import { expect, test, type Page } from '@playwright/test'
import { createServer, type Server } from 'node:http'
import { readFile } from 'node:fs/promises'
import { dirname, extname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { waitForServiceWorker } from './helpers'

const purgeLabel = /清除缓存并重载|Clear caches & reload/
const errorLabel = /未能清除缓存或重载|Could not clear caches or reload/
const waitLabel = /请先完成或取消上传|Finish or cancel uploads/
let productionServer: Server | undefined
let productionURL = ''

test.beforeAll(async () => {
  if (process.env.DOMUS_E2E_PRODUCTION !== '1') return
  const root = join(dirname(fileURLToPath(import.meta.url)), '../dist')
  await readFile(join(root, 'index.html')) // Run npm run build before production verification.
  productionServer = createServer((request, response) => {
    const path = new URL(request.url!, 'http://localhost').pathname
    const extension = extname(path)
    const types: Record<string, string> = { '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.html': 'text/html' }
    const contentType = types[extension || '.html'] || 'application/octet-stream'
    void readFile(join(root, extension ? path : 'index.html')).then(bytes => {
      response.writeHead(200, { 'Content-Type': contentType })
      response.end(bytes)
    }).catch(() => { response.writeHead(404); response.end() })
  })
  await new Promise<void>(resolve => productionServer!.listen(0, '127.0.0.1', resolve))
  productionURL = `http://127.0.0.1:${(productionServer.address() as { port: number }).port}`
})

test.afterAll(async () => {
  productionServer?.closeAllConnections()
  if (productionServer) await new Promise<void>(resolve => productionServer!.close(() => resolve()))
})

async function visit(page: Page, path = '/files') {
  await page.goto(productionURL ? productionURL + path : path)
}

test.beforeEach(async ({ page, context, baseURL }) => {
  await context.addCookies([{ url: productionURL || baseURL!, name: 'domus_session', value: 'cache-fixture', httpOnly: true }])
  await page.routeWebSocket('**/ws', socket => socket.onMessage(message => {
    const request = JSON.parse(String(message))
    if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
  }))
  await page.route('**/user', route => route.fulfill({ json: { username: 'cache-fixture', role: 'user' } }))
  await page.route('**/file/?*', route => route.fulfill({ json: { files: [] } }))
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => route.fulfill({ json: {} }))
})

async function openAccount(page: Page, mobile = false) {
  const button = page.locator('.account-popover:visible').getByRole('button', { name: purgeLabel, exact: true })
  if (!mobile) {
    // A popover can still be visible during its leave transition after a
    // dialog click. Close it fully before toggling the account trigger.
    await page.locator('.file-header').click({ position: { x: 2, y: 2 } })
    await expect(page.locator('.account-popover:visible')).toHaveCount(0)
  }
  if (!await button.isVisible()) await page.locator(mobile ? '.mobile-account' : '.sidebar-account').click()
  await expect(button).toBeVisible()
  return button
}

async function bootId(page: Page) {
  return page.evaluate(() => new Promise<string>(resolve => {
    const { port1, port2 } = new MessageChannel()
    port1.onmessage = event => { port1.close(); resolve(event.data) }
    navigator.serviceWorker.controller!.postMessage({ type: 'boot-id' }, [port2])
  }))
}

async function seedCaches(page: Page) {
  await page.evaluate(async () => {
    for (const name of ['domus-decrypt-v2', 'domus-cache-fixture', 'zephyr-decrypt', 'unrelated-fixture']) {
      await (await caches.open(name)).put('/__cache_fixture__', new Response('cached fixture'))
    }
    document.documentElement.dataset.cacheFixture = 'original'
  })
}

for (const width of [1280, 390]) {
  test(`cache recovery clears only app caches and keeps persistent state (${width}px)`, async ({ page, context }, testInfo) => {
    const mobile = width < 768
    await page.setViewportSize({ width, height: 844 })
    await visit(page, '/files?path=%2F')
    await waitForServiceWorker(page)
    const previousBoot = await bootId(page)

    // A more-specific worker registration must not be removed with Domus.
    await page.evaluate(() => navigator.serviceWorker.register('/sw.js', { scope: '/unrelated-fixture/' }).then(() => {}))
    await expect.poll(() => page.evaluate(async () =>
      (await navigator.serviceWorker.getRegistration('/unrelated-fixture/'))?.active?.state
    )).toBe('activated')
    await seedCaches(page)
    await page.evaluate(async () => {
      localStorage.setItem('domus_playback_quality:cache-fixture', '480p')
      sessionStorage.setItem('cache-fixture', 'session kept')
      await new Promise<void>((resolve, reject) => {
        const request = indexedDB.open('domus', 1)
        request.onsuccess = () => {
          const db = request.result
          const transaction = db.transaction('pending_ops', 'readwrite')
          transaction.objectStore('pending_ops').put({
            id: 'cache-fixture', username: 'other-fixture', schemaVersion: 2,
            createdAt: 1, lastAttempt: null, lastError: null, apiUrl: '/file/mkdir', apiMethod: 'post',
          })
          transaction.oncomplete = () => { db.close(); resolve() }
          transaction.onerror = () => { db.close(); reject(transaction.error) }
        }
        request.onerror = () => reject(request.error)
      })
      const handle = await (await navigator.storage.getDirectory()).getFileHandle('cache-fixture.cipher', { create: true })
      const writer = await handle.createWritable()
      await writer.write('staged fixture')
      await writer.close()
    })

    let entryReads = 0
    await page.route(page.url(), route => {
      if (!route.request().isNavigationRequest()) entryReads++
      return route.continue()
    })
    let button = await openAccount(page, mobile)
    await expect(button).toBeEnabled()
    const actions = page.locator('.account-popover:visible .account-actions')
    for (const action of await actions.getByRole('button').all()) {
      await expect(action.locator('.n-button__icon svg')).toBeVisible()
    }
    const positions = await actions.locator('.n-button__content').evaluateAll(elements =>
      elements.map(element => element.getBoundingClientRect().left)
    )
    expect(Math.max(...positions) - Math.min(...positions)).toBeLessThanOrEqual(1)
    await page.locator('.account-popover:visible').screenshot({ path: testInfo.outputPath('account-menu.png') })
    await expect(page.getByText(/清除缓存并重载（开发）|Purge Caches & Reload \(dev\)/, { exact: true })).toHaveCount(0)
    await button.click()
    const dialog = page.locator('.domus-confirm-dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: /取消|Cancel/, exact: true }).click()
    expect(await page.evaluate(() => caches.keys())).toHaveLength(4)
    expect(await bootId(page)).toBe(previousBoot)
    expect(entryReads).toBe(0)

    button = await openAccount(page, mobile)
    await button.click()
    await expect(dialog).toBeVisible()
    const reloaded = page.waitForEvent('framenavigated', frame => frame === page.mainFrame())
    await dialog.getByRole('button', { name: purgeLabel, exact: true }).click()
    await reloaded
    await expect(page.locator('.file-shell')).toBeVisible()
    await waitForServiceWorker(page)
    expect(entryReads).toBe(1)
    expect(await bootId(page)).not.toBe(previousBoot)
    expect(await page.evaluate(() => document.documentElement.dataset.cacheFixture)).toBeUndefined()
    expect(await page.evaluate(() => caches.keys())).toEqual(['unrelated-fixture'])
    expect(await page.evaluate(async () =>
      (await navigator.serviceWorker.getRegistration('/unrelated-fixture/'))?.active?.state
    )).toBe('activated')
    expect((await context.cookies()).find(cookie => cookie.name === 'domus_session')?.value).toBe('cache-fixture')
    const retained = await page.evaluate(async () => {
      const pending = await new Promise<unknown>((resolve, reject) => {
        const request = indexedDB.open('domus', 1)
        request.onsuccess = () => {
          const db = request.result
          const read = db.transaction('pending_ops').objectStore('pending_ops').get('cache-fixture')
          read.onsuccess = () => { db.close(); resolve(read.result) }
          read.onerror = () => { db.close(); reject(read.error) }
        }
        request.onerror = () => reject(request.error)
      })
      const handle = await (await navigator.storage.getDirectory()).getFileHandle('cache-fixture.cipher')
      return {
        quality: localStorage.getItem('domus_playback_quality:cache-fixture'),
        session: sessionStorage.getItem('cache-fixture'),
        pending,
        staged: await (await handle.getFile()).text(),
      }
    })
    expect(retained).toMatchObject({
      quality: '480p', session: 'session kept', staged: 'staged fixture', pending: { id: 'cache-fixture' },
    })
  })
}

for (const failure of ['entry', 'cache']) {
  test(`cache recovery reports ${failure} failure without silently reloading`, async ({ page }) => {
    await visit(page)
    await waitForServiceWorker(page)
    const previousBoot = await bootId(page)
    await seedCaches(page)
    if (failure === 'entry') {
      await page.route(page.url(), route => route.request().isNavigationRequest()
        ? route.continue()
        : route.fulfill({ status: 503, body: 'fixture unavailable' }))
    } else {
      await page.evaluate(() => {
        const remove = caches.delete.bind(caches)
        caches.delete = async name => {
          if (name === 'domus-cache-fixture') throw new Error('fixture deletion failed')
          return remove(name)
        }
      })
    }
    const button = await openAccount(page)
    await button.click()
    await page.locator('.domus-confirm-dialog').getByRole('button', { name: purgeLabel, exact: true }).click()
    await expect(page.locator('.n-message').filter({ hasText: errorLabel })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.dataset.cacheFixture)).toBe('original')
    expect(await page.evaluate(() => caches.keys())).toContain('unrelated-fixture')
    if (failure === 'entry') {
      expect(await page.evaluate(() => caches.keys())).toHaveLength(4)
      expect(await bootId(page)).toBe(previousBoot)
      expect(await page.evaluate(async () => Boolean(await navigator.serviceWorker.getRegistration('/')))).toBe(true)
    }
    await expect(await openAccount(page)).toBeEnabled()
  })
}

for (const timing of ['confirmation', 'entry refresh']) {
  test(`an upload started during ${timing} blocks cache recovery until it finishes`, async ({ page }) => {
    let release!: () => void
    const pending = new Promise<void>(resolve => { release = resolve })
    let initializations = 0
    await page.route('**/file/upload', async route => {
      if (route.request().postDataJSON().names) return route.fulfill({ json: { conflicts: [] } })
      initializations++
      await pending
      return route.fulfill({ status: 400, json: { error: 'fixture upload failed' } })
    })
    await visit(page)
    await waitForServiceWorker(page)
    await seedCaches(page)
    let entryReads = 0
    let releaseEntry!: () => void
    const entryPending = new Promise<void>(resolve => { releaseEntry = resolve })
    await page.route(page.url(), async route => {
      if (!route.request().isNavigationRequest()) {
        entryReads++
        if (timing === 'entry refresh') await entryPending
      }
      return route.continue()
    })
    const button = await openAccount(page)
    await button.click()
    const dialog = page.locator('.domus-confirm-dialog')
    await expect(dialog).toBeVisible()
    if (timing === 'entry refresh') {
      await dialog.getByRole('button', { name: purgeLabel, exact: true }).click()
      await expect.poll(() => entryReads).toBe(1)
    }
    await page.locator('.upload-input').setInputFiles({ name: 'fixture.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('fixture') })
    await expect.poll(() => initializations).toBe(1)
    if (timing === 'confirmation') await dialog.getByRole('button', { name: purgeLabel, exact: true }).click()
    releaseEntry()
    await expect(page.locator('.n-message').filter({ hasText: waitLabel })).toBeVisible()
    await expect(await openAccount(page)).toBeDisabled()
    expect(await page.evaluate(() => document.documentElement.dataset.cacheFixture)).toBe('original')
    expect(await page.evaluate(() => caches.keys())).toHaveLength(4)
    expect(entryReads).toBe(timing === 'confirmation' ? 0 : 1)
    release()
    await expect(await openAccount(page)).toBeEnabled()
  })
}
