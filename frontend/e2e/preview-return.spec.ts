import { createCipheriv } from 'node:crypto'
import { createServer, type Server } from 'node:http'
import { expect, test, type Page, type WebSocketRoute } from '@playwright/test'
import { uploadFromToolbar } from './helpers'

const key = Buffer.alloc(32, 23)
const count = 120
let server: Server
let objectOrigin: string
let encrypted: Buffer
let thumbnailRequests = 0
let thumbnailGate: Promise<void> | undefined
let heldThumbnails = 0
let thumbnailQuery = ''
const requestedThumbnails = new Set<string>()
let cancelledThumbnailRequests = 0

test.beforeAll(async () => {
  server = createServer(async (request, response) => {
    response.setHeader('Access-Control-Allow-Origin', '*')
    response.setHeader('Access-Control-Allow-Methods', 'GET, PUT, OPTIONS')
    response.setHeader('Access-Control-Allow-Headers', 'Content-Type')
    if (request.method === 'OPTIONS') { response.writeHead(204).end(); return }
    if (request.method === 'PUT') {
      request.resume()
      request.on('end', () => response.writeHead(200).end())
      return
    }
    if (request.url?.startsWith('/thumbnail/')) {
      thumbnailRequests++
      requestedThumbnails.add(new URL(request.url, objectOrigin).pathname)
      response.on('close', () => { if (!response.writableEnded) cancelledThumbnailRequests++ })
      thumbnailQuery = new URL(request.url, objectOrigin).search
      const gate = thumbnailGate
      thumbnailGate = undefined
      if (gate) { heldThumbnails++; await gate }
      if (request.url.startsWith('/thumbnail/failure')) { response.writeHead(503).end(); return }
    }
    const range = /^bytes=(\d+)-(\d+)$/.exec(request.headers.range || '')
    const start = range ? Number(range[1]) : 0
    const end = range ? Number(range[2]) : encrypted.length - 1
    response.writeHead(range ? 206 : 200, {
      'Content-Length': end - start + 1,
      ...(range ? { 'Content-Range': `bytes ${start}-${end}/${encrypted.length}` } : {}),
    }).end(encrypted.subarray(start, end + 1))
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  objectOrigin = `http://127.0.0.1:${(server.address() as { port: number }).port}`
})

test.afterAll(async () => {
  server?.closeAllConnections()
  if (server) await new Promise<void>(resolve => server.close(() => resolve()))
})

async function prepare(page: Page) {
  requestedThumbnails.clear()
  await page.addInitScript(() => {
    localStorage.setItem('domus_locale', 'en')
    localStorage.setItem('domus_show_thumbnails', '1')
  })
  let directorySocket: WebSocketRoute | undefined
  await page.routeWebSocket('**/ws', socket => {
    directorySocket = socket
    socket.onMessage(message => {
      const request = JSON.parse(String(message))
      if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
    })
  })
  let authenticated = false
  let directoryRequests = 0
  const directoryPaths: string[] = []
  let accessRequests = 0
  let audits = 0
  let size = 0
  let directoryVersion = 0
  let directoryDelay = 0
  let directoryGate: Promise<void> | undefined
  let otherDirectoriesEmpty = false
  const files = Array.from({ length: count }, (_, index) => ({
    inode: index + 1, generation: 1, name: `image-${String(index).padStart(3, '0')}.webp`,
    path: `/album/image-${String(index).padStart(3, '0')}.webp`, is_dir: false,
    size: 0, content_type: 'image/webp', status: 'ready',
    created_at: '2026-01-01T00:00:00Z', last_modified: '2026-01-01T00:00:00Z',
    thumbnail_url: `${objectOrigin}/thumbnail/${index}`, thumbnail_dek: key.toString('hex'),
  }))
  function signedFiles(entries: Array<{ thumbnail_url?: string }>) {
    return entries.map(file => {
      const source = file.thumbnail_url ? new URL(file.thumbnail_url) : null
      source?.searchParams.set('signature', String(directoryVersion))
      return { ...file, thumbnail_url: source?.href || '' }
    })
  }
  // API interception disables HTTP caching. Returning must reuse the actual
  // image nodes rather than rely on a warmed browser image cache.
  await page.route('**/user', route => route.fulfill(authenticated
    ? { json: { username: 'preview-fixture', role: 'user' } }
    : { status: 401, json: { error: 'unauthorized' } }))
  await page.route('**/auth', route => route.fulfill({ json: { smtp_enabled: false } }))
  await page.route('**/file/?*', async route => {
    directoryRequests++
    const path = new URL(route.request().url()).searchParams.get('path')
    directoryPaths.push(path || '/')
    const snapshot = signedFiles(otherDirectoriesEmpty && path !== '/album/' ? [] : files)
    const gate = directoryGate
    directoryGate = undefined
    if (gate) await gate
    if (directoryDelay) await new Promise(resolve => setTimeout(resolve, directoryDelay))
    return route.fulfill({ json: { files: snapshot } })
  })
  await page.route('**/file/search?*', route => route.fulfill({ json: { results: files } }))
  await page.route('**/file/access?*', route => {
    accessRequests++
    const file = files.find(item => item.path === new URL(route.request().url()).searchParams.get('path'))!
    return route.fulfill({ json: { ...file, size, url: `${objectOrigin}/source`, dek: key.toString('hex'), chunk_size: 65536, generation: 1 } })
  })
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => { audits++; return route.fulfill({ json: {} }) })
  await page.route('**/oauth/grants', route => route.fulfill({ json: [] }))
  await page.goto('/files?path=/album/')
  const base64 = await page.evaluate(async () => {
    await navigator.serviceWorker.register('/sw.js')
    await navigator.serviceWorker.ready
    if (!navigator.serviceWorker.controller) await new Promise<void>(resolve => navigator.serviceWorker.addEventListener('controllerchange', () => resolve(), { once: true }))
    const canvas = document.createElement('canvas')
    canvas.width = 320; canvas.height = 180
    const context = canvas.getContext('2d')!
    context.fillStyle = '#398ccd'; context.fillRect(0, 0, 320, 180)
    return canvas.toDataURL('image/webp').split(',')[1]
  })
  const bytes = Buffer.from(base64, 'base64')
  size = bytes.length
  const iv = Buffer.alloc(12, 42)
  const cipher = createCipheriv('aes-256-gcm', key, iv)
  cipher.setAAD(Buffer.alloc(8))
  const header = Buffer.from([1, 0, 1, 0, 0])
  encrypted = Buffer.concat([header, iv, cipher.update(bytes), cipher.final(), cipher.getAuthTag()])
  authenticated = true
  await page.reload()
  await thumbnailsReady(page)
  return {
    directoryRequests: (path?: string) => path ? directoryPaths.filter(value => value === path).length : directoryRequests,
    accessRequests: () => accessRequests, audits: () => audits,
    files,
    signedFiles,
    delayDirectory(ms: number) { directoryDelay = ms },
    emptyOtherDirectories() { otherDirectoriesEmpty = true },
    holdNextDirectory() {
      let release!: () => void
      directoryGate = new Promise<void>(resolve => { release = resolve })
      return release
    },
    directoryChanged(path = '/album/') {
      directoryVersion++
      directorySocket!.send(JSON.stringify({ event: 'dir.changed', data: { path } }))
    },
    trashChanged() {
      directoryVersion++
      directorySocket!.send(JSON.stringify({ event: 'trash.changed', data: {} }))
    },
  }
}

async function prepareMutations(page: Page) {
  const fixture = await prepare(page)
  let inode = 9000
  const makeFile = (name: string, isDir = false) => ({
    ...fixture.files[0], inode: ++inode, name, path: `/album/${name}${isDir ? '/' : ''}`,
    is_dir: isDir, size: 12, content_type: 'text/plain', thumbnail_url: '', thumbnail_dek: '',
  })
  for (const name of ['zz-operation.txt', 'zz-permanent.txt', 'zz-partial-ok.txt', 'zz-partial-fail.txt']) fixture.files.push(makeFile(name))
  const trash = fixture.files.slice(0, count).map(file => ({
    ...file, trash_id: `retained-${file.inode}`, original_path: file.path, relative_path: '/', status: 'deleted',
  }))
  trash.push({ ...makeFile('zz-trash.txt'), trash_id: 'trash-target', original_path: '/album/zz-trash.txt', relative_path: '/', status: 'deleted' })
  const failedDeletes = new Set<string>()
  let failedTrashRead = false
  const removeFile = (path: string) => {
    const index = fixture.files.findIndex(file => file.path === path)
    return index < 0 ? undefined : fixture.files.splice(index, 1)[0]
  }
  const changed = () => { fixture.directoryChanged(); fixture.trashChanged() }
  await page.route('**/file/mkdir', async route => {
    const path = route.request().postDataJSON().path.replace(/\/$/, '')
    fixture.files.push({ ...makeFile(path.split('/').pop(), true), path: path + '/' })
    fixture.directoryChanged()
    await route.fulfill({ json: { ok: true } })
  })
  await page.route('**/file/rename', async route => {
    const body = route.request().postDataJSON()
    const file = fixture.files.find(file => file.path === body.old_path)!
    file.path = body.new_path
    file.name = body.new_path.replace(/\/$/, '').split('/').pop()
    fixture.directoryChanged()
    await route.fulfill({ json: { ok: true } })
  })
  for (const operation of ['copy', 'move']) await page.route(`**/file/${operation}`, async route => {
    const body = route.request().postDataJSON()
    const source = operation === 'move' ? removeFile(body.src_path) : fixture.files.find(file => file.path === body.src_path)
    fixture.files.push({ ...(source || makeFile(body.dst_path.split('/').pop())), path: body.dst_path })
    fixture.directoryChanged()
    await route.fulfill(operation === 'copy'
      ? { contentType: 'text/event-stream', body: 'data: {"done":true}\n\n' }
      : { json: { ok: true } })
  })
  await page.route('**/file/delete?*', async route => {
    removeFile(new URL(route.request().url()).searchParams.get('path')!)
    fixture.directoryChanged()
    await route.fulfill({ json: { ok: true } })
  })
  await page.route('**/trash/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (request.method() === 'GET') {
      if (failedTrashRead) { await route.fulfill({ status: 503, json: { error: 'fixture_read_failed' } }); return }
      if (url.pathname === '/trash/') { await route.fulfill({ json: { files: fixture.signedFiles(trash) } }); return }
      const id = decodeURIComponent(url.pathname.split('/')[2])
      const entry = trash.find(file => file.trash_id === id)
      await route.fulfill(entry ? { json: { files: [], trash: { id, name: entry.name } } } : { status: 404, json: { error: 'trash_not_found' } })
      return
    }
    if (request.method() === 'POST' && url.pathname === '/trash/') {
      const body = request.postDataJSON()
      if (failedDeletes.has(body.path)) { await route.fulfill({ status: 409, json: { error: 'fixture_delete_failed' } }); return }
      const file = fixture.files.find(file => file.path === body.path)
      expect(body.expected_inode).toBe(file?.inode)
      if (file) trash.push({ ...removeFile(body.path)!, trash_id: `deleted-${file.inode}`, original_path: body.path, relative_path: '/', status: 'deleted' })
    } else if (url.pathname === '/trash/' && request.method() === 'DELETE') {
      trash.length = 0
    } else {
      const id = decodeURIComponent(url.pathname.split('/')[2])
      const index = trash.findIndex(file => file.trash_id === id)
      if (index >= 0) {
        const [entry] = trash.splice(index, 1)
        if (url.pathname.endsWith('/restore')) {
          const { trash_id, original_path, relative_path, ...file } = entry
          fixture.files.push({ ...file, path: original_path, status: 'ready' })
        }
      }
    }
    changed()
    await route.fulfill({ json: { ok: true } })
  })
  await page.route('**/file/search?*', route => {
    const query = new URL(route.request().url()).searchParams.get('query') || ''
    return route.fulfill({ json: { results: fixture.signedFiles(fixture.files.filter(file => file.name.includes(query))) } })
  })
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    await (await import(moduleURL)).useFileSystemStore().refresh()
  })
  await page.waitForLoadState('networkidle')
  return { ...fixture, trash, failDelete(path: string) { failedDeletes.add(path) }, failTrashRead(failed: boolean) { failedTrashRead = failed } }
}

async function watchMutationList(page: Page, exclude: string[] = []) {
  if (await page.locator('.file-item').count() > 78) await page.locator('.file-item').nth(78).scrollIntoViewIfNeeded()
  if (await page.locator('[data-thumbnail-available="true"]').count()) await thumbnailsReady(page)
  return page.locator('.file-surface').evaluate((surface, exclude) => {
    const nodes = [...surface.querySelectorAll<HTMLElement>('.file-item')].filter(node => node.querySelector('img.file-thumbnail') && !exclude.includes(node.dataset.path || ''))
    const images = nodes.flatMap(node => [...node.querySelectorAll('img.file-thumbnail')])
    const probe = (window as any).__mutationList = { nodes: new WeakSet(nodes), images: new WeakSet(images), expected: nodes.length, loads: 0, loading: 0, removed: 0 }
    new MutationObserver(records => {
      for (const record of records) {
        for (const node of record.removedNodes) if (nodes.some(item => node === item || node.contains(item))) probe.removed++
        for (const node of record.addedNodes) if (node instanceof Element && (node.matches('[data-state="loading"]') || node.querySelector('[data-state="loading"]'))) probe.loading++
      }
    }).observe(surface, { childList: true, subtree: true })
    document.addEventListener('load', event => { if (event.target instanceof HTMLImageElement && probe.images.has(event.target)) probe.loads++ }, true)
    return nodes.length
  }, exclude)
}

async function expectStableMutationList(page: Page, retained: number) {
  await page.waitForLoadState('networkidle')
  expect(await page.locator('.file-surface').evaluate(surface => {
    const probe = (window as any).__mutationList
    return { retained: [...surface.querySelectorAll('.file-item')].filter(node => probe.nodes.has(node)).length, loads: probe.loads, loading: probe.loading, removed: probe.removed }
  })).toEqual({ retained, loads: 0, loading: 0, removed: 0 })
  expect(await page.locator('.file-surface').evaluate(surface => surface.scrollTop)).toBeGreaterThan(1000)
}

async function startMutation(page: Page, action: string, args: unknown[] = [], selection?: string[]) {
  await page.evaluate(async ({ action, args, selection }) => {
    const moduleURL = '/src/stores/fileSystem.ts'
    const fs = (await import(moduleURL)).useFileSystemStore()
    if (selection) fs.selectedFiles = selection
    ;(window as any).__mutation = fs[action](...args)
  }, { action, args, selection })
}

async function finishMutation(page: Page) {
  await page.evaluate(() => (window as any).__mutation)
}

async function confirmMutation(page: Page) {
  const dialog = page.locator('.domus-confirm-dialog')
  await expect(dialog).toHaveCount(1)
  await dialog.getByRole('button', { name: 'OK', exact: true }).click()
  await finishMutation(page)
  await expect(dialog).toHaveCount(0)
}

test('refresh policy preserves a failed background snapshot while keeping empty-trash safety guards', async ({ page }) => {
  const fixture = await prepareMutations(page)
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    await (await import(moduleURL)).useFileSystemStore().navigate('trash:///')
  })
  await thumbnailsReady(page)
  const retained = await watchMutationList(page)
  fixture.failTrashRead(true)
  await startMutation(page, 'refresh')
  await finishMutation(page)
  await expectStableMutationList(page, retained)
  const emptyButton = page.getByRole('button', { name: 'Empty Trash', exact: true })
  await expect(emptyButton).toBeDisabled()
  await startMutation(page, 'emptyTrash')
  await finishMutation(page)
  await expect(page.locator('.domus-confirm-dialog')).toHaveCount(0)
  expect(fixture.trash.length).toBe(count + 1)
  fixture.failTrashRead(false)
  await startMutation(page, 'refresh')
  await finishMutation(page)
  await expectStableMutationList(page, retained)
  await expect(emptyButton).toBeEnabled()

  fixture.trash.push({ ...fixture.trash[0], is_dir: true, name: 'nested', trash_id: 'nested-folder', thumbnail_url: '', thumbnail_dek: '' })
  await startMutation(page, 'navigate', ['trash:///nested-folder/'])
  await finishMutation(page)
  fixture.trash.splice(fixture.trash.findIndex(file => file.trash_id === 'nested-folder'), 1)
  fixture.trashChanged()
  await expect.poll(() => page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    return (await import(moduleURL)).useFileSystemStore().currentPath
  })).toBe('trash:///')
  await thumbnailsReady(page)
})

test('refresh policy distinguishes initial navigation from empty-directory updates and ignores stale searches', async ({ page }) => {
  const fixture = await prepareMutations(page)
  fixture.emptyOtherDirectories()
  const release = fixture.holdNextDirectory()
  try {
    await startMutation(page, 'navigate', ['/fresh/'])
    await expect(page.locator('[data-state="loading"]')).toBeVisible()
  } finally { release() }
  await finishMutation(page)
  await expect(page.locator('[data-state="empty"]')).toBeVisible()
  await watchMutationList(page)
  await startMutation(page, 'refresh')
  await finishMutation(page)
  expect(await page.evaluate(() => (window as any).__mutationList.loading)).toBe(0)

  const releaseAlbum = fixture.holdNextDirectory()
  try {
    await startMutation(page, 'navigate', ['/album/'])
    await expect(page.locator('[data-state="loading"]')).toBeVisible()
    fixture.files.push({ ...fixture.files[0], inode: 99999, name: 'zz-during-navigation.txt', path: '/album/zz-during-navigation.txt', thumbnail_url: '', thumbnail_dek: '' })
    await page.evaluate(async () => {
      const moduleURL = '/src/stores/fileSystem.ts'
      const fs = (await import(moduleURL)).useFileSystemStore()
      void fs.refreshDirectory('/album/')
    })
  } finally { releaseAlbum() }
  await finishMutation(page)
  await expect(page.locator('.file-item[data-path="/album/zz-during-navigation.txt"]')).toHaveCount(1)

  let started = false
  let finish!: () => void
  const gate = new Promise<void>(resolve => { finish = resolve })
  await page.route('**/file/search?*', async route => {
    started = true
    await gate
    await route.fulfill({ json: { results: fixture.signedFiles(fixture.files) } })
  })
  try {
    await startMutation(page, 'performSearch', ['slow'])
    await expect.poll(() => started).toBe(true)
    await page.evaluate(async () => {
      const moduleURL = '/src/stores/fileSystem.ts'
      ;(await import(moduleURL)).useFileSystemStore().exitSearch()
    })
  } finally { finish() }
  await finishMutation(page)
  expect(await page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    const fs = (await import(moduleURL)).useFileSystemStore()
    return { mode: fs.searchMode, loading: fs.searchLoading, results: fs.searchResults.length }
  })).toEqual({ mode: false, loading: false, results: 0 })
})

test('directory signing updates preserve completed thumbnail blobs without another object read', async ({ page }) => {
  const fixture = await prepare(page)
  const url = await page.locator('img.file-thumbnail').first().getAttribute('src')
  const before = thumbnailRequests
  fixture.directoryChanged()
  await expect.poll(() => page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    return (await import(moduleURL)).useFileSystemStore().files[0].thumbnail_url
  })).toContain('signature=1')
  await page.waitForLoadState('networkidle')
  expect(await page.locator('img.file-thumbnail').first().getAttribute('src')).toBe(url)
  expect(await page.evaluate(async url => (await fetch(url!)).status, url)).toBe(200)
  expect(thumbnailRequests).toBe(before)
})

test('thumbnail completion is pushed without periodic directory reads', async ({ page }) => {
  const fixture = await prepare(page)
  const first = fixture.files[0]
  const source = first.thumbnail_url
  first.thumbnail_url = ''
  fixture.directoryChanged()
  const item = page.locator('.file-item').first()
  await expect(item.locator('[data-thumbnail-state="pending"]')).toBeVisible()
  await expect(item.locator('img.file-thumbnail')).toHaveCount(0)
  await page.waitForLoadState('networkidle')
  const requests = fixture.directoryRequests()
  await page.waitForTimeout(5500)
  expect(fixture.directoryRequests(), 'missing thumbnails must not trigger periodic directory reads').toBe(requests)
  first.thumbnail_url = source
  fixture.directoryChanged()
  await expect(item.locator('[data-thumbnail-state="ready"]')).toBeVisible()
  expect(requestedThumbnails.has('/thumbnail/119')).toBe(false)
})

test('changed thumbnail generations update only the changed image', async ({ page }) => {
  const fixture = await prepare(page)
  const sources = await page.locator('img.file-thumbnail').evaluateAll(images => images.map(image => image.getAttribute('src')))
  const readsBefore = thumbnailRequests
  await rememberList(page)
  fixture.files[0].generation++
  fixture.files[0].thumbnail_url = `${objectOrigin}/thumbnail/replaced`
  fixture.directoryChanged()
  await expect.poll(() => page.locator('img.file-thumbnail').first().getAttribute('src')).not.toBe(sources[0])
  await page.waitForLoadState('networkidle')
  const updated = await page.locator('img.file-thumbnail').evaluateAll(images => images.map(image => image.getAttribute('src')))
  expect(updated.slice(1)).toEqual(sources.slice(1))
  expect(await page.evaluate(() => (window as any).__previewReturn.loads)).toBe(0)
  expect(thumbnailRequests).toBeGreaterThan(readsBefore)
  fixture.files[0].thumbnail_url += '?versionId=next'
  fixture.directoryChanged()
  await expect.poll(() => page.locator('img.file-thumbnail').first().getAttribute('src')).not.toBe(updated[0])
  await page.waitForLoadState('networkidle')
  expect(await page.locator('img.file-thumbnail').evaluateAll(images => images.slice(1).map(image => image.getAttribute('src')))).toEqual(sources.slice(1))
  expect(await page.evaluate(() => (window as any).__previewReturn.loads)).toBe(0)
})

test('preview return recovers a missed directory hint without rereading cached thumbnails', async ({ page }) => {
  const fixture = await prepare(page)
  await page.locator('.file-item').nth(1).dblclick()
  await expect(page).toHaveURL(/\/preview\?/)
  await expect(page.locator('.preview-image')).toBeVisible()
  const directories = fixture.directoryRequests()
  const thumbnails = thumbnailRequests
  fixture.files.push({
    ...fixture.files[0], inode: count + 1, name: 'zz-late.txt', path: '/album/zz-late.txt',
    content_type: 'text/plain', thumbnail_url: '', thumbnail_dek: '',
  })
  // The saved state changes without delivering its lightweight hint.
  await page.locator('.back-button').click()
  await expect(page.locator('.file-item[data-path="/album/zz-late.txt"]')).toHaveCount(1)
  await page.waitForLoadState('networkidle')
  expect(fixture.directoryRequests()).toBe(directories + 1)
  expect(thumbnailRequests).toBe(thumbnails)
})

test('background updates target all matching tabs and ignore late responses after navigation', async ({ page }) => {
  const fixture = await prepare(page)
  fixture.emptyOtherDirectories()
  const release = fixture.holdNextDirectory()
  const requests = fixture.directoryRequests()
  try {
    fixture.directoryChanged()
    await expect.poll(fixture.directoryRequests).toBeGreaterThan(requests)
    await page.evaluate(async () => {
      const moduleURL = '/src/stores/fileSystem.ts'
      await (await import(moduleURL)).useFileSystemStore().navigate('/other/')
    })
  } finally {
    release()
  }
  await page.waitForLoadState('networkidle')
  await expect(page.locator('.file-item')).toHaveCount(0)
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    const fs = (await import(moduleURL)).useFileSystemStore()
    fs.restoreTabs([{ path: '/album/' }, { path: '/album/' }, { path: '/other/' }], 2)
  })
  await expect.poll(() => page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    const fs = (await import(moduleURL)).useFileSystemStore()
    return fs.tabs.filter((tab: { path: string }) => tab.path === '/album/').map((tab: { files: unknown[] }) => tab.files.length)
  })).toEqual([count, count])
  await page.waitForLoadState('networkidle')
  const before = fixture.directoryRequests('/album/')
  fixture.files.pop()
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    await (await import(moduleURL)).useFileSystemStore().refreshDirectory('/album/')
  })
  expect(fixture.directoryRequests('/album/') - before).toBe(1)
  expect(await page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    const fs = (await import(moduleURL)).useFileSystemStore()
    return { path: fs.currentPath, counts: fs.tabs.filter((tab: { path: string }) => tab.path === '/album/').map((tab: { files: unknown[] }) => tab.files.length) }
  })).toEqual({ path: '/other/', counts: [count - 1, count - 1] })
  await expect(page.locator('.file-item')).toHaveCount(0)
})

async function thumbnailsReady(page: Page) {
  await expect.poll(() => page.locator('.file-surface').evaluate(surface => {
    const bounds = surface.getBoundingClientRect()
    const visible = [...surface.querySelectorAll<HTMLElement>('[data-thumbnail-available="true"]')].filter(element => {
      const rect = element.getBoundingClientRect()
      return rect.bottom > bounds.top && rect.top < bounds.bottom && rect.right > bounds.left && rect.left < bounds.right
    })
    return visible.length > 0 && visible.every(element => {
      const image = element.querySelector('img')
      return image?.complete && image.naturalWidth > 0
    })
  })).toBeTruthy()
}

async function rememberList(page: Page) {
  await thumbnailsReady(page)
  return page.locator('.file-surface').evaluate(surface => {
    const images = [...surface.querySelectorAll<HTMLImageElement>('img.file-thumbnail')]
    const probe = (window as any).__previewReturn = {
      surface: new WeakRef(surface), images: new WeakSet(images), imageCount: images.length, loads: 0,
    }
    document.addEventListener('load', event => {
      if (event.target instanceof HTMLImageElement && probe.images.has(event.target)) probe.loads++
    }, true)
    return surface.scrollTop
  })
}

async function shortcut(page: Page) {
  return page.evaluate(() => {
    const event = new KeyboardEvent('keydown', { key: 'a', ctrlKey: true, bubbles: true, cancelable: true })
    document.body.dispatchEvent(event)
    return event.defaultPrevented
  })
}

async function observeFileMotion(page: Page) {
  await page.addInitScript(() => {
    ;(window as any).__fileMotion = []
    document.addEventListener('transitionrun', event => {
      const item = event.target
      if (!(item instanceof HTMLElement) || !item.classList.contains('file-item')) return
      const phase = item.classList.contains('file-motion-leave-active') ? 'leave'
        : item.classList.contains('file-motion-enter-active') ? 'enter'
        : item.classList.contains('file-motion-move') ? 'move' : null
      if (!phase) return
      const bounds = item.getBoundingClientRect()
      const surface = item.closest<HTMLElement>('.file-surface')!
      const surfaceBounds = surface.getBoundingClientRect()
      const empty = surface.querySelector<HTMLElement>('[data-state="empty"]')
      ;(window as any).__fileMotion.push({
        phase, path: item.dataset.path, property: event.propertyName,
        duration: Math.max(...getComputedStyle(item).transitionDuration.split(',').map(value => Number.parseFloat(value))),
        left: bounds.left - surfaceBounds.left + surface.scrollLeft,
        top: bounds.top - surfaceBounds.top + surface.scrollTop,
        width: bounds.width, height: bounds.height,
        inert: item.inert, hidden: item.getAttribute('aria-hidden'),
        emptyVisible: !!empty && getComputedStyle(empty).display !== 'none',
      })
    }, true)
  })
}

async function refreshMotionFixture(page: Page) {
  await page.evaluate(async () => {
    const moduleURL = '/src/stores/fileSystem.ts'
    await (await import(moduleURL)).useFileSystemStore().refresh()
  })
}

async function settleFileMotion(page: Page) {
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await expect(page.locator('.file-motion-enter-active, .file-motion-leave-active, .file-motion-move')).toHaveCount(0)
}

for (const mode of ['desktop-icons', 'desktop-list', 'mobile-icons', 'mobile-list']) {
  test.describe(`file-motion-${mode}`, () => {
    const mobile = mode.startsWith('mobile')
    test.use({ viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 900 }, isMobile: mobile, hasTouch: mobile })

    test('file and directory entry, batch departure and gap-closing animate without reloading unchanged items', async ({ page }) => {
      await page.emulateMedia({ reducedMotion: 'no-preference' })
      await observeFileMotion(page)
      const fixture = await prepare(page)
      if (mode.endsWith('-list')) await page.locator('button[title="Details"]').click()
      await thumbnailsReady(page)
      await page.waitForLoadState('networkidle')
      expect(await page.evaluate(() => (window as any).__fileMotion.filter((event: { phase: string }) => event.phase !== 'move'))).toEqual([])
      const retained = await watchMutationList(page)
      const template = { ...fixture.files[0], thumbnail_url: '', thumbnail_dek: '' }
      const folder = { ...template, inode: 90001, name: '00-new-folder', path: '/album/00-new-folder/', is_dir: true }
      // Insert within the scrolled viewport too: scroll anchoring correctly
      // cancels the screen-space movement of an insertion above the viewport.
      const file = { ...template, inode: 90002, name: 'image-079z-new.txt', path: '/album/image-079z-new.txt', is_dir: false }
      fixture.files.push(folder, file)
      await refreshMotionFixture(page)
      for (const path of [folder.path, file.path]) {
        await expect.poll(() => page.evaluate(path => (window as any).__fileMotion.some((event: { phase: string; path: string; property: string; duration: number }) => event.phase === 'enter' && event.path === path && event.property === 'opacity' && event.duration > 0 && event.duration <= .2), path)).toBe(true)
      }
      await expect.poll(() => page.evaluate(() => (window as any).__fileMotion.some((event: { phase: string; property: string }) => event.phase === 'move' && event.property === 'transform'))).toBe(true)
      await settleFileMotion(page)
      await expectStableMutationList(page, retained)
      const before = await page.evaluate(paths => {
        const surface = document.querySelector<HTMLElement>('.file-surface')!
        const surfaceBounds = surface.getBoundingClientRect()
        const boxes = paths.map(path => {
          const bounds = surface.querySelector<HTMLElement>(`[data-path="${path}"]`)!.getBoundingClientRect()
          return { path, left: bounds.left - surfaceBounds.left + surface.scrollLeft, top: bounds.top - surfaceBounds.top + surface.scrollTop, width: bounds.width, height: bounds.height }
        })
        ;(window as any).__fileMotion = []
        return boxes
      }, [folder.path, file.path])
      for (let index = fixture.files.length - 1; index >= 0; index--) if ([folder.path, file.path].includes(fixture.files[index].path)) fixture.files.splice(index, 1)
      await refreshMotionFixture(page)
      await expect.poll(() => page.evaluate(() => (window as any).__fileMotion.some((event: { phase: string }) => event.phase === 'leave'))).toBe(true)
      await settleFileMotion(page)
      await expectStableMutationList(page, retained)
      const departed = await page.evaluate(() => (window as any).__fileMotion)
      for (const box of before) {
        const event = departed.find((event: { phase: string; path: string; property: string }) => event.phase === 'leave' && event.path === box.path && event.property === 'opacity')
        expect(event).toBeDefined()
        expect(event.inert).toBe(true)
        expect(event.hidden).toBe('true')
        for (const dimension of ['left', 'top', 'width', 'height'] as const) expect(Math.abs(event[dimension] - box[dimension])).toBeLessThan(2)
      }
      expect(await page.locator('.file-grid, .file-list__body').evaluateAll(groups => groups.every(group => !(group as HTMLElement).style.minHeight))).toBe(true)
      await page.evaluate(() => { (window as any).__fileMotion = [] })
      await refreshMotionFixture(page)
      await settleFileMotion(page)
      await expectStableMutationList(page, retained)
      expect(await page.evaluate(() => (window as any).__fileMotion)).toEqual([])

      fixture.files.length = 0
      await refreshMotionFixture(page)
      await expect(page.locator('.file-item')).toHaveCount(0)
      await expect(page.locator('[data-state="empty"]')).toBeVisible()
      await page.evaluate(() => {
        ;(window as any).__fileMotion = []
        ;(window as any).__emptyGroup = new WeakRef(document.querySelector('.file-grid, .file-list__body')!)
      })
      fixture.files.push(folder)
      await refreshMotionFixture(page)
      await expect.poll(() => page.evaluate(() => (window as any).__fileMotion.some((event: { phase: string }) => event.phase === 'enter'))).toBe(true)
      await settleFileMotion(page)
      expect(await page.evaluate(() => (window as any).__emptyGroup.deref() === document.querySelector('.file-grid, .file-list__body'))).toBe(true)
      await page.evaluate(() => { (window as any).__fileMotion = [] })
      fixture.files.length = 0
      await refreshMotionFixture(page)
      await expect(page.locator('.file-item')).toHaveCount(0)
      await expect(page.locator('[data-state="empty"]')).toBeVisible()
      expect(await page.evaluate(() => (window as any).__fileMotion.filter((event: { phase: string }) => event.phase === 'leave').every((event: { emptyVisible: boolean }) => !event.emptyVisible))).toBe(true)
      expect(await page.evaluate(() => (window as any).__fileMotion.some((event: { phase: string }) => event.phase === 'leave'))).toBe(true)

      fixture.files.push(file)
      await refreshMotionFixture(page)
      fixture.files.length = 0
      await refreshMotionFixture(page)
      fixture.files.push(file)
      await refreshMotionFixture(page)
      await settleFileMotion(page)
      const restored = page.locator(`.file-item[data-path="${file.path}"]`)
      await expect(restored).toHaveCount(1)
      expect(await restored.evaluate(item => (item as HTMLElement).inert)).toBe(false)
      expect(await page.locator('.file-grid, .file-list__body').evaluateAll(groups => groups.every(group => !(group as HTMLElement).style.minHeight))).toBe(true)
    })
  })
}

test('reduced motion updates file and directory membership without entry, leave or move animations', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await observeFileMotion(page)
  const fixture = await prepare(page)
  const template = { ...fixture.files[0], inode: 99001, name: '00-no-motion', path: '/album/00-no-motion/', is_dir: true, thumbnail_url: '', thumbnail_dek: '' }
  for (const list of [false, true]) {
    if (list) await page.locator('button[title="Details"]').click()
    fixture.files.push(template)
    await refreshMotionFixture(page)
    await expect(page.locator(`.file-item[data-path="${template.path}"]`)).toHaveCount(1)
    fixture.files.pop()
    await refreshMotionFixture(page)
    await settleFileMotion(page)
    await expect(page.locator(`.file-item[data-path="${template.path}"]`)).toHaveCount(0)
    expect(await page.locator('.file-item').first().evaluate(item => getComputedStyle(item).transitionProperty)).toBe('none')
  }
  expect(await page.evaluate(() => (window as any).__fileMotion)).toEqual([])
})

for (const mode of ['desktop-icons', 'desktop-list', 'mobile-icons']) {
  test.describe(mode, () => {
    const mobile = mode.startsWith('mobile')
    test.use({ viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 900 }, isMobile: mobile, hasTouch: mobile })

    test('connected applications modal retains the scrolled list and loaded thumbnails', async ({ page }) => {
      const fixture = await prepare(page)
      if (mode.endsWith('-list')) await page.locator('button[title="Details"]').click()
      await thumbnailsReady(page)
      await page.locator('.file-item').nth(78).scrollIntoViewIfNeeded()
      const scrollTop = await rememberList(page)
      const originalURL = page.url()
      const upstream = thumbnailRequests, directories = fixture.directoryRequests()
      await page.locator(mobile ? '.mobile-account' : '.sidebar-account').click()
      await page.locator('.account-actions').getByText('Connected applications', { exact: true }).click()
      const modal = page.locator('.applications-modal')
      await expect(modal).toBeVisible()
      await expect(modal.getByText('No applications have access', { exact: true })).toBeVisible()
      await expect(page).toHaveURL(originalURL)
      await page.keyboard.press('Escape')
      await expect(modal).toHaveCount(0)
      await expect.poll(() => page.locator('.file-surface').evaluate(surface => surface.scrollTop)).toBe(scrollTop)
      expect(await page.locator('.file-surface').evaluate(surface => {
        const probe = (window as any).__previewReturn
        return { same: surface === probe.surface.deref(), retained: [...surface.querySelectorAll('img.file-thumbnail')].filter(image => probe.images.has(image)).length, loads: probe.loads }
      })).toEqual({ same: true, retained: await page.evaluate(() => (window as any).__previewReturn.imageCount), loads: 0 })
      expect(thumbnailRequests).toBe(upstream)
      expect(fixture.directoryRequests()).toBe(directories)
    })

    test('only visible thumbnails load; scrolling and the display switch control reads', async ({ page }) => {
      await prepare(page)
      if (mode.endsWith('-list')) { await page.locator('button[title="Details"]').click(); await thumbnailsReady(page) }
      await page.waitForLoadState('networkidle')
      expect(requestedThumbnails.size).toBeGreaterThan(0)
      expect(requestedThumbnails.size).toBeLessThan(count)
      expect(requestedThumbnails.has('/thumbnail/119')).toBe(false)
      await page.locator('.file-item').nth(119).scrollIntoViewIfNeeded()
      await thumbnailsReady(page)
      expect(requestedThumbnails.has('/thumbnail/119')).toBe(true)
      await page.locator(mobile ? '.mobile-account' : '.sidebar-account').click()
      await page.getByRole('switch', { name: 'Show thumbnails' }).click()
      await page.waitForLoadState('networkidle')
      const before = thumbnailRequests
      await page.keyboard.press('Escape')
      await expect(page.getByRole('switch', { name: 'Show thumbnails' })).not.toBeVisible()
      await page.locator('.file-item').first().scrollIntoViewIfNeeded()
      await page.waitForTimeout(400)
      await expect(page.locator('img.file-thumbnail')).toHaveCount(0)
      expect(thumbnailRequests).toBe(before)
    })

    test('preview return retains scroll, image nodes and loaded thumbnails', async ({ page }) => {
      const counters = await prepare(page)
      if (mode.endsWith('-list')) await page.locator('button[title="Details"]').click()
      await thumbnailsReady(page)
      for (const back of ['button', 'browser']) {
        const item = page.locator('.file-item').nth(78)
        await item.scrollIntoViewIfNeeded()
        const scrollTop = await rememberList(page)
        expect(scrollTop).toBeGreaterThan(1000)
        if (mobile) await item.tap(); else await item.dblclick()
        await expect(page).toHaveURL(/\/preview\?/)
        await expect.poll(() => page.locator('.preview-image').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
        await expect(page.locator('.file-surface')).toHaveCount(0)
        expect(await shortcut(page), 'hidden file-list shortcuts must not run in preview').toBeFalsy()
        const previewURL = await page.locator('.preview-image').getAttribute('src')
        const upstream = thumbnailRequests, directories = counters.directoryRequests(), audits = counters.audits()
        if (back === 'button') await page.locator('.back-button').click(); else await page.goBack()
        await expect(page).toHaveURL(/\/files\?path=/)
        await expect.poll(counters.directoryRequests).toBe(directories + 1)
        await page.waitForLoadState('networkidle')
        await thumbnailsReady(page)
        expect(await page.locator('.file-surface').evaluate(surface => surface.scrollTop)).toBe(scrollTop)
        expect(await page.locator('.file-surface').evaluate(surface => {
          const probe = (window as any).__previewReturn
          return {
            sameSurface: surface === probe.surface.deref(),
            reusedImages: [...surface.querySelectorAll('img.file-thumbnail')].filter(image => probe.images.has(image)).length,
            loads: probe.loads,
          }
        })).toEqual({ sameSurface: true, reusedImages: await page.evaluate(() => (window as any).__previewReturn.imageCount), loads: 0 })
        expect(thumbnailRequests).toBe(upstream)
        expect(counters.directoryRequests()).toBe(directories + 1)
        await expect.poll(counters.audits).toBe(audits + 1)
        expect(await page.evaluate(async url => (await fetch(url!)).status, previewURL)).toBe(404)
        expect(await shortcut(page), 'file-list shortcuts must resume on return').toBeTruthy()
        await page.keyboard.press('Escape')
      }
    })

    test('upload completion updates the directory without hiding the list or reloading existing thumbnails', async ({ page }) => {
      const fixture = await prepare(page)
      if (mode.endsWith('-list')) await page.locator('button[title="Details"]').click()
      await thumbnailsReady(page)
      fixture.delayDirectory(100)
      let uploads = 0
      const reservations = new Map<string, { name: string; size: number }>()
      await page.route('**/file/upload', async route => {
        const body = route.request().postDataJSON()
        if (body.names) { await route.fulfill({ json: { conflicts: [] } }); return }
        if (!body.upload_id) {
          const id = `list-update-${++uploads}`
          reservations.set(id, { name: body.file_name, size: body.file_size })
          await route.fulfill({ json: { upload_id: id, task_id: '', dek: body.dek, part_size: 8388608, total_parts: 1 } })
          return
        }
        const source = reservations.get(body.upload_id)!
        fixture.files.push({
          inode: count + uploads, generation: 1, name: source.name, path: `/album/${source.name}`,
          is_dir: false, size: source.size, content_type: 'text/plain', status: 'ready',
          created_at: '2026-01-01T00:00:00Z', last_modified: '2026-01-01T00:00:00Z',
          thumbnail_url: '', thumbnail_dek: '',
        })
        fixture.directoryChanged()
        await route.fulfill({ json: { ok: true, generation: 1 } })
      })
      await page.route('**/file/upload/presign?*', route => route.fulfill({ json: {
        expires_in: 3600, parts: [{ part_number: 1, presigned_url: `${objectOrigin}/upload/${uploads}?partNumber=1&uploadId=list-update-${uploads}` }],
      } }))
      await page.locator('.file-item').nth(78).scrollIntoViewIfNeeded()
      await thumbnailsReady(page)
      const top = await page.locator('.file-surface').evaluate(surface => {
        const nodes = [...surface.querySelectorAll('.file-item')]
        const images = [...surface.querySelectorAll('img.file-thumbnail')]
        const probe = (window as any).__directoryUpdate = {
          nodes: new WeakSet(nodes), images: new WeakSet(images), imageCount: images.length, removed: 0, loads: 0, loadingStates: 0,
        }
        new MutationObserver(records => {
          for (const record of records) {
            for (const node of record.removedNodes) {
              if (nodes.some(item => node === item || node.contains(item))) probe.removed++
            }
            for (const node of record.addedNodes) {
              if (node instanceof Element && (node.matches('[data-state="loading"]') || node.querySelector('[data-state="loading"]'))) probe.loadingStates++
            }
          }
        }).observe(surface, { childList: true, subtree: true })
        document.addEventListener('load', event => {
          if (event.target instanceof HTMLImageElement && probe.images.has(event.target)) probe.loads++
        }, true)
        return surface.scrollTop
      })
      await page.evaluate(async () => {
        const moduleURL = '/src/stores/fileSystem.ts'
        ;(await import(moduleURL)).useFileSystemStore().selectFile('/album/image-078.webp')
      })
      const upstream = thumbnailRequests
      const directories = fixture.directoryRequests()
      for (const name of ['zz-first.txt', 'zz-second.txt']) {
        await uploadFromToolbar(page, { name, mimeType: 'text/plain', buffer: Buffer.from(`UPLOAD_LIST_STABILITY_${name}\n`) })
        await expect(page.locator(`.file-item[data-path="/album/${name}"]`)).toBeVisible()
        await page.waitForLoadState('networkidle')
        expect(await page.locator('.file-surface').evaluate(surface => {
          const probe = (window as any).__directoryUpdate
          return {
            oldNodes: [...surface.querySelectorAll('.file-item')].filter(node => probe.nodes.has(node)).length,
            oldImages: [...surface.querySelectorAll('img.file-thumbnail')].filter(node => probe.images.has(node)).length,
            removed: probe.removed, loads: probe.loads, loadingStates: probe.loadingStates,
          }
        })).toEqual({ oldNodes: count, oldImages: await page.evaluate(() => (window as any).__directoryUpdate.imageCount), removed: 0, loads: 0, loadingStates: 0 })
        expect(await page.locator('.file-surface').evaluate(surface => surface.scrollTop)).toBe(top)
        await expect(page.locator('.file-item.selected')).toHaveAttribute('data-path', '/album/image-078.webp')
        expect(thumbnailRequests).toBe(upstream)
      }
      expect(fixture.directoryRequests() - directories).toBeLessThanOrEqual(4)
    })

    test('file mutations share a stable refresh policy for mkdir, rename, copy, move, delete, partial failure and offline replay', async ({ page }) => {
      const fixture = await prepareMutations(page)
      if (mode.endsWith('-list')) await page.locator('button[title="Details"]').click()
      await thumbnailsReady(page)
      const retained = await watchMutationList(page)
      await startMutation(page, 'createFolder')
      const prompt = page.locator('.domus-prompt-dialog')
      await prompt.locator('input').fill('zz-new-folder')
      await prompt.getByRole('button', { name: 'OK', exact: true }).click()
      await finishMutation(page)
      await expect(page.locator('.file-item[data-path="/album/zz-new-folder/"]')).toHaveCount(1)
      await expectStableMutationList(page, retained)

      await startMutation(page, 'rename', ['/album/zz-operation.txt', 'zz-renamed.txt', false])
      await finishMutation(page)
      await expect(page.locator('.file-item[data-path="/album/zz-renamed.txt"]')).toHaveCount(1)
      await expectStableMutationList(page, retained)
      for (const mode of ['copy', 'cut']) {
        const name = mode === 'copy' ? 'zz-copy.txt' : 'zz-move.txt'
        await page.evaluate(async ({ mode, name }) => {
          const moduleURL = '/src/stores/fileSystem.ts'
          ;(await import(moduleURL)).useFileSystemStore().clipboard = { mode, items: [{ name, path: `/source/${name}`, is_dir: false }] }
        }, { mode, name })
        await startMutation(page, 'paste')
        await finishMutation(page)
        await expect(page.locator(`.file-item[data-path="/album/${name}"]`)).toHaveCount(1)
        await expectStableMutationList(page, retained)
      }

      for (const [name, permanent] of [['zz-renamed.txt', false], ['zz-permanent.txt', true]] as const) {
        await startMutation(page, 'deleteSelected', [permanent], [`/album/${name}`])
        await confirmMutation(page)
        await expect(page.locator(`.file-item[data-path="/album/${name}"]`)).toHaveCount(0)
        await expectStableMutationList(page, retained)
      }
      fixture.failDelete('/album/zz-partial-fail.txt')
      await startMutation(page, 'deleteSelected', [], ['/album/zz-partial-ok.txt', '/album/zz-partial-fail.txt'])
      await confirmMutation(page)
      await expect(page.locator('.file-item[data-path="/album/zz-partial-ok.txt"]')).toHaveCount(0)
      await expect(page.locator('.file-item[data-path="/album/zz-partial-fail.txt"]')).toHaveCount(1)
      await expectStableMutationList(page, retained)
      expect(await page.evaluate(async () => {
        const moduleURL = '/src/stores/fileSystem.ts'
        return (await import(moduleURL)).useFileSystemStore().selectedFiles
      })).toEqual(['/album/zz-partial-fail.txt'])

      await page.evaluate(async () => {
        const moduleURL = '/src/stores/pendingOps.ts'
        const pending = (await import(moduleURL)).usePendingOpsStore()
        const op = await pending.enqueue({ username: 'preview-fixture', type: 'mkdir', apiMethod: 'post', apiUrl: '/file/mkdir', apiData: { path: '/album/zz-offline' } })
        await pending.retry(op.id)
      })
      await expect(page.locator('.file-item[data-path="/album/zz-offline/"]')).toHaveCount(1)
      await expectStableMutationList(page, retained)
      fixture.files.find(file => file.name === 'zz-copy.txt')!.size = 84
      await startMutation(page, 'refresh')
      await finishMutation(page)
      await expectStableMutationList(page, retained)
      expect(await page.evaluate(async () => {
        const moduleURL = '/src/stores/fileSystem.ts'
        return (await import(moduleURL)).useFileSystemStore().files.find((file: { name: string }) => file.name === 'zz-copy.txt').size
      })).toBe(84)
    })

    test('trash restore and permanent deletion retain other rows and empty-trash refresh never flashes a loading screen', async ({ page }) => {
      const fixture = await prepareMutations(page)
      await page.evaluate(async () => {
        const moduleURL = '/src/stores/fileSystem.ts'
        await (await import(moduleURL)).useFileSystemStore().navigate('trash:///')
      })
      if (mode.endsWith('-list')) await page.locator('button[title="Details"]').click()
      await thumbnailsReady(page)
      const retained = await watchMutationList(page)
      await startMutation(page, 'restoreSelected', [], ['trash:///trash-target'])
      await finishMutation(page)
      await expect(page.locator('.file-item[data-path="trash:///trash-target"]')).toHaveCount(0)
      await expectStableMutationList(page, retained)
      fixture.trash.push({ ...fixture.trash[0], name: 'zz-permanent-trash.txt', inode: 99001, trash_id: 'permanent-target', thumbnail_url: '', thumbnail_dek: '' })
      fixture.trashChanged()
      await expect(page.locator('.file-item[data-path="trash:///permanent-target"]')).toHaveCount(1)
      await startMutation(page, 'deleteSelected', [], ['trash:///permanent-target'])
      await confirmMutation(page)
      await expect(page.locator('.file-item[data-path="trash:///permanent-target"]')).toHaveCount(0)
      await expectStableMutationList(page, retained)
      await startMutation(page, 'emptyTrash')
      await confirmMutation(page)
      await expect(page.locator('.file-item')).toHaveCount(0)
      expect(await page.evaluate(() => (window as any).__mutationList.loading)).toBe(0)
      await expect(page.locator('[data-state="empty"]')).toBeVisible()
    })

    test('same-query search mutations retain unaffected rows instead of rebuilding search results', async ({ page }) => {
      await prepareMutations(page)
      if (mode.endsWith('-list')) await page.locator('button[title="Details"]').click()
      await page.locator('.global-search input').fill('image')
      await expect.poll(() => page.evaluate(async () => {
        const moduleURL = '/src/stores/fileSystem.ts'
        const fs = (await import(moduleURL)).useFileSystemStore()
        return fs.searchMode && !fs.searchLoading && fs.searchResults.length === 120
      })).toBe(true)
      await thumbnailsReady(page)
      await page.waitForLoadState('networkidle')
      const retained = await watchMutationList(page, ['/album/image-118.webp', '/album/image-119.webp'])
      await startMutation(page, 'rename', ['/album/image-118.webp', 'zz-renamed-image.webp', false])
      await finishMutation(page)
      await page.evaluate(async () => {
        const moduleURL = '/src/stores/fileSystem.ts'
        await (await import(moduleURL)).useFileSystemStore().performSearch('image')
      })
      await expectStableMutationList(page, retained)
      const target = page.locator('.file-item[data-path="/album/image-119.webp"]')
      await target.click({ button: 'right' })
      const menu = page.locator('.action-menu')
      await expect(menu).toHaveCount(1)
      await menu.getByRole('menuitem', { name: 'Delete', exact: true }).click()
      await page.locator('.domus-confirm-dialog').getByRole('button', { name: 'OK', exact: true }).click()
      await expect(target).toHaveCount(0)
      await expectStableMutationList(page, retained)
    })
  })
}

test('leaving the viewport cancels a pending thumbnail and failed images keep their file icon', async ({ page }) => {
  const fixture = await prepare(page)
  let release!: () => void
  const held = heldThumbnails, cancelled = cancelledThumbnailRequests
  thumbnailGate = new Promise<void>(resolve => { release = resolve })
  try {
    await page.locator('.file-item').last().scrollIntoViewIfNeeded()
    await expect.poll(() => heldThumbnails).toBeGreaterThan(held)
    await page.locator('.file-item').first().scrollIntoViewIfNeeded()
    await expect.poll(() => cancelledThumbnailRequests).toBeGreaterThan(cancelled)
  } finally { release() }
  fixture.files[0].thumbnail_url = `${objectOrigin}/thumbnail/failure`
  fixture.directoryChanged()
  const item = page.locator('.file-item').first()
  await expect(item.locator('[data-thumbnail-state="failed"]')).toBeVisible()
  await expect(item.locator('img.file-thumbnail')).toHaveCount(0)
  await expect(item.locator('.thumbnail-surface svg')).toBeVisible()
  const reads = thumbnailRequests
  await page.waitForTimeout(500)
  expect(thumbnailRequests).toBe(reads)
  fixture.files[0].thumbnail_url = `${objectOrigin}/thumbnail/recovered`
  fixture.directoryChanged()
  await thumbnailsReady(page)
})

test('renaming preserves extensions by default and extensionless files still preview by MIME', async ({ page }) => {
  const fixture = await prepareMutations(page)
  const original = page.locator('.file-item[data-path="/album/image-000.webp"]')
  await original.click({ button: 'right' })
  await page.locator('.action-menu').getByRole('menuitem', { name: 'Rename', exact: true }).click()
  let prompt = page.locator('.domus-prompt-dialog')
  await expect(prompt.locator('input')).toHaveValue('image-000')
  await prompt.locator('input').fill('renamed')
  await prompt.getByRole('button', { name: 'OK', exact: true }).click()
  await expect(prompt).toHaveCount(0)
  const renamed = page.locator('.file-item[data-path="/album/renamed.webp"]')
  await renamed.click({ button: 'right' })
  await page.locator('.action-menu').getByRole('menuitem', { name: 'Rename', exact: true }).click()
  prompt = page.locator('.domus-prompt-dialog')
  await prompt.getByRole('checkbox').check()
  await prompt.locator('input').fill('extensionless')
  await prompt.getByRole('button', { name: 'OK', exact: true }).click()
  const confirm = page.locator('.domus-confirm-dialog')
  await expect(confirm).toContainText('extension')
  await confirm.getByRole('button', { name: 'OK', exact: true }).click()
  const file = page.locator('.file-item[data-path="/album/extensionless"]')
  await expect(file).toHaveCount(1)
  expect(fixture.files.find(file => file.path === '/album/extensionless')?.content_type).toBe('image/webp')
  await file.dblclick()
  await expect.poll(() => page.locator('.preview-image').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  expect(await page.evaluate(async () => {
    const moduleURL = '/src/utils/fileKind.ts'
    const kinds = await import(moduleURL)
    return { png: await kinds.uploadContentType(new File([new Uint8Array([137,80,78,71,13,10,26,10])], 'unknown')),
      hidden: kinds.splitExtension('.gitignore'), compound: kinds.splitExtension('archive.tar.gz') }
  })).toEqual({ png: 'image/png', hidden: { stem: '.gitignore', suffix: '' }, compound: { stem: 'archive', suffix: '.tar.gz' } })
})

test('preview reloads stay isolated and leaving the workspace releases the cached list', async ({ page }) => {
  const counters = await prepare(page)
  await rememberList(page)
  await page.locator('.file-item').first().dblclick()
  await expect.poll(() => page.locator('.preview-image').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  const accesses = counters.accessRequests()
  await page.evaluate(async () => {
    const url = '/src/composables/usePreviewReload.ts'
    ;(await import(url)).reloadPreviewComponent()
  })
  await expect.poll(counters.accessRequests).toBe(accesses + 1)
  await expect.poll(() => page.locator('.preview-image').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  await page.locator('.back-button').click()
  await thumbnailsReady(page)
  expect(await page.locator('.file-surface').evaluate(surface => surface === (window as any).__previewReturn.surface.deref())).toBeTruthy()
  await page.route('**/oauth/consent', route => route.fulfill({ status: 400, json: { error: 'invalid_request' } }))
  await page.evaluate(async () => {
    const url = '/src/router.ts'
    await (await import(url)).default.push('/authorize')
  })
  await expect(page.locator('.authorization-card')).toBeVisible()
  await page.evaluate(async () => {
    const url = '/src/router.ts'
    await (await import(url)).default.push('/files?path=/album/')
  })
  await thumbnailsReady(page)
  expect(await page.locator('.file-surface').evaluate(surface => surface === (window as any).__previewReturn.surface.deref())).toBeFalsy()
  await rememberList(page)
  await page.evaluate(() => window.dispatchEvent(new Event('auth:expired')))
  await expect(page.locator('.login-card')).toBeVisible()
  await expect(page.locator('.file-surface')).toHaveCount(0)
  await page.evaluate(async () => {
    const url = '/src/stores/auth.ts'
    await (await import(url)).useAuthStore().checkAuth()
  })
  await thumbnailsReady(page)
  expect(await page.locator('.file-surface').evaluate(surface => surface === (window as any).__previewReturn.surface.deref())).toBeFalsy()
})

test('search text and result position survive preview return', async ({ page }) => {
  await prepare(page)
  const search = page.locator('.global-search input')
  await search.fill('image')
  await expect.poll(() => page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    const fs = (await import(url)).useFileSystemStore()
    return fs.searchMode && !fs.searchLoading
  })).toBeTruthy()
  await thumbnailsReady(page)
  const item = page.locator('.file-item').nth(78)
  await item.scrollIntoViewIfNeeded()
  const top = await rememberList(page)
  await item.click()
  await expect(page).toHaveURL(/\/preview\?/)
  await expect.poll(() => page.locator('.preview-image').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(320)
  await page.locator('.back-button').click()
  await expect(search).toHaveValue('image')
  expect(await page.locator('.file-surface').evaluate(surface => surface.scrollTop)).toBe(top)
  expect(await page.locator('.file-surface').evaluate(surface => surface === (window as any).__previewReturn.surface.deref())).toBeTruthy()
})

test('directory navigation still resets position and replaces the previous cached list', async ({ page }) => {
  await prepare(page)
  await page.locator('.file-item').nth(78).scrollIntoViewIfNeeded()
  expect(await rememberList(page)).toBeGreaterThan(1000)
  await page.evaluate(async () => {
    const url = '/src/stores/fileSystem.ts'
    await (await import(url)).useFileSystemStore().navigate('/other/')
  })
  await expect(page).toHaveURL(/path=(?:%2Fother%2F|\/other\/)/)
  await thumbnailsReady(page)
  expect(await page.locator('.file-surface').evaluate(surface => surface.scrollTop)).toBe(0)
  expect(await page.locator('.file-surface').evaluate(surface => surface === (window as any).__previewReturn.surface.deref())).toBeFalsy()
})
