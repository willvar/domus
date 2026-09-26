import { createCipheriv } from 'node:crypto'
import { execFileSync, spawn } from 'node:child_process'
import { once } from 'node:events'
import { createServer, type Server } from 'node:http'
import { mkdir, mkdtemp, open, readFile, readdir, rm } from 'node:fs/promises'
import { homedir } from 'node:os'
import { join } from 'node:path'
import { chromium, expect, test, type Page, type Browser } from '@playwright/test'

if (process.env.DOMUS_E2E_PLAYBACK_MEMORY === '1') test.use({ trace: 'off', video: 'off' })

const CHUNK = 65536
const ENC = CHUNK + 28
const key = Buffer.alloc(32, 23)
const sourceSize = 8 * 1024 ** 3
let directory: string
let server: Server
let port: number
let served = 0
let maxRange = 0
const files = new Map<string, { size: number; handle: Awaited<ReturnType<typeof open>> }>()
const segments: string[] = []

test.beforeAll(async () => {
  await mkdir(join(homedir(), 'tmp'), { recursive: true })
  directory = await mkdtemp(join(homedir(), 'tmp/domus-playback-'))
  const base = join(directory, 'base.mp4')
  execFileSync('ffmpeg', ['-v', 'error', '-f', 'lavfi', '-i', 'testsrc2=size=320x180:rate=12',
    '-t', '150', '-an', '-c:v', 'libx264', '-preset', 'ultrafast', '-threads', '2', '-g', '24', '-pix_fmt', 'yuv420p', base])
  const data = await readFile(base)
  let moovOffset = 0
  for (let pos = 0; pos < data.length;) {
    const size = data.readUInt32BE(pos)
    if (data.toString('ascii', pos + 4, pos + 8) === 'moov') { moovOffset = pos; break }
    if (!size) throw new Error('Unexpected fixture MP4 box')
    pos += size
  }
  expect(moovOffset).toBeGreaterThan(0)
  // Move only the metadata to the far end of a sparse 8 GiB MP4. The valid
  // sample offsets stay unchanged; opening it must seek, not drain the gap.
  const sparse = await open(join(directory, 'large.mp4'), 'wx+', 0o600)
  await sparse.write(data.subarray(0, moovOffset), 0, moovOffset, 0)
  const moov = data.subarray(moovOffset)
  const gap = sourceSize - data.length
  const free = Buffer.alloc(16)
  free.writeUInt32BE(1)
  free.write('free', 4)
  free.writeBigUInt64BE(BigInt(gap), 8)
  await sparse.write(free, 0, free.length, moovOffset)
  await sparse.write(moov, 0, moov.length, sourceSize - moov.length)
  files.set('large', { size: sourceSize, handle: sparse })

  execFileSync('ffmpeg', ['-v', 'error', '-i', base, '-c', 'copy', '-f', 'hls', '-hls_time', '2', '-hls_list_size', '0',
    '-hls_segment_type', 'fmp4', '-hls_segment_filename', join(directory, 'segment-%03d.m4s'), join(directory, 'index.m3u8')])
  execFileSync('ffmpeg', ['-v', 'error', '-i', base, '-frames:v', '1', '-c:v', 'libwebp', join(directory, 'thumbnail.webp')])
  for (const name of (await readdir(directory)).filter(name => name === 'init.mp4' || name.endsWith('.m4s') || name === 'thumbnail.webp').sort()) {
    const handle = await open(join(directory, name), 'r')
    files.set(name, { size: (await handle.stat()).size, handle })
    if (name.endsWith('.m4s')) segments.push(name)
  }
  // A sparse MP4 free box raises the true artifact size to the byte budget
  // without expensive high-resolution encoding. Media still has two-second
  // GOPs, so Chromium's dependent-frame removal is exercised unchanged.
  for (let index = 0; index < 3; index++) {
    const bytes = await readFile(join(directory, segments[index]))
    const size = 32 * 1024 ** 2
    const free = Buffer.alloc(8)
    free.writeUInt32BE(size - bytes.length)
    free.write('free', 4)
    const handle = await open(join(directory, `budget-${index}`), 'wx+')
    await handle.write(bytes, 0, bytes.length, 0)
    await handle.write(free, 0, free.length, bytes.length)
    await handle.truncate(size)
    files.set(`budget-${index}`, { size, handle })
  }
  server = createServer((request, response) => {
    response.setHeader('Access-Control-Allow-Origin', '*')
    // Thumbnail discovery must work without exposing Content-Range in CORS.
    if (request.url !== '/thumbnail.webp') response.setHeader('Access-Control-Expose-Headers', 'Content-Range')
    const file = files.get(request.url!.slice(1))
    const range = /^bytes=(\d+)-(\d+)$/.exec(request.headers.range || '')
    if (!file) { response.writeHead(400).end(); return }
    const encryptedSize = 5 + file.size + Math.ceil(file.size / CHUNK) * 28
    const start = range ? Number(range[1]) : 0, end = range ? Number(range[2]) : encryptedSize - 1
    if (range) maxRange = Math.max(maxRange, end - start + 1)
    response.writeHead(range ? 206 : 200, {
      ...(range ? { 'Content-Range': `bytes ${start}-${end}/${encryptedSize}` } : {}),
      'Content-Length': end - start + 1,
    })
    void (async () => {
      let pos = start
      while (pos <= end && !response.destroyed) {
        let bytes: Buffer
        if (pos === 0) bytes = Buffer.from([1, 0, 1, 0, 0])
        else {
          const index = Math.floor((pos - 5) / ENC)
          const plain = Buffer.alloc(Math.min(CHUNK, file.size - index * CHUNK))
          await file.handle.read(plain, 0, plain.length, index * CHUNK)
          const iv = Buffer.alloc(12)
          iv.writeBigUInt64BE(BigInt(index), 4)
          const aad = Buffer.alloc(8)
          aad.writeBigUInt64BE(BigInt(index))
          const cipher = createCipheriv('aes-256-gcm', key, iv)
          cipher.setAAD(aad)
          bytes = Buffer.concat([iv, cipher.update(plain), cipher.final(), cipher.getAuthTag()])
        }
        bytes = bytes.subarray(0, end - pos + 1)
        served += bytes.length
        if (!response.write(bytes)) await new Promise<void>(resolve => {
          const done = () => { response.off('drain', done); response.off('close', done); resolve() }
          response.once('drain', done); response.once('close', done)
        })
        pos += bytes.length
      }
      response.end()
    })().catch(() => response.destroy())
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  port = (server.address() as { port: number }).port
})

test.afterAll(async () => {
  server?.closeAllConnections()
  if (server) await new Promise<void>(resolve => server.close(() => resolve()))
  for (const file of files.values()) await file.handle.close()
  if (directory) await rm(directory, { recursive: true, force: true })
})

async function prepare(page: Page) {
  await page.goto('/')
  await page.evaluate(async () => {
    await navigator.serviceWorker.register('/sw.js')
    await navigator.serviceWorker.ready
    if (!navigator.serviceWorker.controller) await new Promise<void>(resolve => navigator.serviceWorker.addEventListener('controllerchange', () => resolve(), { once: true }))
  })
}

async function memory(browser: Browser) {
  const session = await browser.newBrowserCDPSession()
  try {
    const { processInfo } = await session.send('SystemInfo.getProcessInfo')
    let bytes = 0
    for (const process of processInfo) {
      const status = await readFile(`/proc/${process.id}/smaps_rollup`, 'utf8').catch(() => '')
      bytes += Number(status.match(/^Pss:\s+(\d+)/m)?.[1] || 0) * 1024
    }
    return bytes
  } finally { await session.detach() }
}

test('native 8 GiB tail-metadata video stays bounded on open, pause, seeks and close', async ({ page, browser }) => {
  served = maxRange = 0
  await prepare(page)
  const before = await memory(browser)
  await page.evaluate(async ({ port, size, dek }) => {
    const moduleURL = '/src/composables/useServiceWorker.ts'
    const sw = (await import(moduleURL)).useServiceWorker()
    const url = sw.registerDecrypt({ url: `http://127.0.0.1:${port}/large`, size, chunkSize: 65536, contentType: 'video/mp4', dek })
    await sw.flush()
    const video = document.createElement('video')
    video.id = 'resource-video'
    video.preload = 'metadata'
    video.muted = true
    video.controls = true
    document.body.appendChild(video)
    video.src = url
  }, { port, size: sourceSize, dek: key.toString('hex') })
  const video = page.locator('#resource-video')
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThanOrEqual(2)
  expect(await video.evaluate((v: HTMLVideoElement) => v.duration)).toBeCloseTo(150, 0)
  await video.evaluate((v: HTMLVideoElement) => v.play())
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime)).toBeGreaterThan(1)
  await video.evaluate((v: HTMLVideoElement) => v.pause())
  let peak = await memory(browser)
  for (const time of [120, 30, 145, 5]) {
    await video.evaluate((v: HTMLVideoElement, time) => { v.currentTime = time }, time)
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => !v.seeking && v.readyState >= 2)).toBeTruthy()
    peak = Math.max(peak, await memory(browser))
  }
  await page.waitForTimeout(1500)
  const pausedBytes = served
  await page.waitForTimeout(1500)
  expect(served - pausedBytes).toBeLessThanOrEqual(4 * 1024 ** 2)
  expect(served).toBeLessThan(128 * 1024 ** 2)
  expect(maxRange).toBeLessThanOrEqual(4 * 1024 ** 2)
  expect(peak - before).toBeLessThan(384 * 1024 ** 2)
  await video.evaluate(async (v: HTMLVideoElement) => {
    const moduleURL = '/src/composables/useServiceWorker.ts'
    const sw = (await import(moduleURL)).useServiceWorker()
    const url = new URL(v.src).pathname
    v.pause(); v.removeAttribute('src'); v.load(); v.remove()
    sw.unregisterDecrypt(url)
    await sw.flush()
  })
  const closedBytes = served
  await page.waitForTimeout(500)
  expect(served).toBe(closedBytes)
  console.log(JSON.stringify({ native8GiB: true, ciphertextRead: served, peakPSS: peak, baselinePSS: before, maxCipherRange: maxRange }))
})

test('MSE removes played portions of a continuous buffer and closes its media source', async ({ page }) => {
  const artifact = (name: string) => ({ url: `http://127.0.0.1:${port}/${name}`, size: files.get(name)!.size, dek: key.toString('hex'), duration: 2 })
  await page.route('**/file/renditions?*', route => route.fulfill({ json: {
    source_height: 180, renditions: [{ profile: 'original', status: 'ready', codecs: 'avc1.42C00C', duration: 150,
      init: artifact('init.mp4'), segments: segments.map(artifact) }],
  } }))
  await prepare(page)
  await page.evaluate(async () => {
    const moduleURL = '/src/composables/useRenditions.ts'
    const { useRenditions } = await import(moduleURL)
    const video = document.createElement('video')
    video.id = 'mse-video'; video.muted = true
    document.body.appendChild(video)
    const quality = useRenditions({ decryptUrl: () => '/__unexpected_raw_fetch__' })
    ;(window as any).__playbackQuality = quality
    await quality.attach(video, '/fixture.mp4')
  })
  const video = page.locator('#mse-video')
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThanOrEqual(2)
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.buffered.length ? v.buffered.end(v.buffered.length - 1) : 0)).toBeGreaterThanOrEqual(44)
  // Rewinding within one already contiguous range must trim its distant tail.
  await video.evaluate((v: HTMLVideoElement) => { v.currentTime = 35 })
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.buffered.end(v.buffered.length - 1))).toBeGreaterThanOrEqual(78)
  await video.evaluate((v: HTMLVideoElement) => { v.currentTime = 15 })
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.buffered.end(v.buffered.length - 1))).toBeLessThanOrEqual(62)
  await video.evaluate((v: HTMLVideoElement) => { v.currentTime = 40; v.playbackRate = 16; return v.play() })
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime)).toBeGreaterThan(95)
  await video.evaluate((v: HTMLVideoElement) => v.pause())
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime - v.buffered.start(0))).toBeLessThan(34)
  await video.evaluate((v: HTMLVideoElement) => { v.currentTime = 5 })
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => !v.seeking && v.readyState >= 2)).toBeTruthy()
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.buffered.end(v.buffered.length - 1))).toBeLessThanOrEqual(54)
  await page.evaluate(() => { (window as any).__playbackQuality.detach(); delete (window as any).__playbackQuality })
  expect(await video.getAttribute('src')).toBeNull()
  expect(await video.evaluate((v: HTMLVideoElement) => v.readyState)).toBe(0)
})

test('MSE byte-budget and quota eviction preserve the current keyframe segment', async ({ page }) => {
  const artifact = (name: string) => ({ url: `http://127.0.0.1:${port}/${name}`, size: files.get(name)!.size, dek: key.toString('hex'), duration: 2 })
  await page.route('**/file/renditions?*', route => route.fulfill({ json: {
    source_height: 180, renditions: [{ profile: 'original', status: 'ready', codecs: 'avc1.42C00C', duration: 6,
      init: artifact('init.mp4'), segments: [0, 1, 2].map(index => artifact(`budget-${index}`)) }],
  } }))
  await prepare(page)
  await page.evaluate(async () => {
    const url = '/src/composables/useRenditions.ts'
    const { useRenditions } = await import(url)
    const video = document.createElement('video')
    video.id = 'budget-video'; video.muted = true
    document.body.appendChild(video)
    const removals: Array<{ time: number; end: number }> = []
    const remove = SourceBuffer.prototype.remove
    SourceBuffer.prototype.remove = function (start, end) {
      removals.push({ time: video.currentTime, end })
      return remove.call(this, start, end)
    }
    const quality = useRenditions({ decryptUrl: () => '/__unexpected_raw_fetch__' })
    ;(window as any).__budgetPlayback = { quality, removals, quota: false }
    await quality.attach(video, '/budget.mp4')
  })
  const video = page.locator('#budget-video')
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThanOrEqual(2)
  await video.evaluate((v: HTMLVideoElement) => { v.currentTime = 1.5 })
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.buffered.end(v.buffered.length - 1))).toBeGreaterThan(3.9)
  await page.waitForTimeout(750)
  expect(await video.evaluate((v: HTMLVideoElement) => v.buffered.start(0))).toBe(0)

  // Force the tighter quota-recovery path when the next segment is appended.
  await page.evaluate(() => {
    const append = SourceBuffer.prototype.appendBuffer
    SourceBuffer.prototype.appendBuffer = function (bytes) {
      SourceBuffer.prototype.appendBuffer = append
      ;(window as any).__budgetPlayback.quota = true
      throw new DOMException('Test buffer quota', 'QuotaExceededError')
    }
    ;(document.querySelector('#budget-video') as HTMLVideoElement).currentTime = 3.5
  })
  await expect.poll(() => page.evaluate(() => (window as any).__budgetPlayback.quota)).toBeTruthy()
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.buffered.end(v.buffered.length - 1))).toBeGreaterThan(5.9)
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.buffered.start(0))).toBe(2)
  await video.evaluate((v: HTMLVideoElement) => v.play())
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime)).toBeGreaterThan(4.5)
  const removals = await page.evaluate(() => (window as any).__budgetPlayback.removals) as Array<{ time: number; end: number }>
  expect(removals.length).toBeGreaterThan(0)
  for (const removal of removals) expect(removal.end).toBeLessThanOrEqual(Math.floor(removal.time / 2) * 2)
  await page.evaluate(() => { (window as any).__budgetPlayback.quality.detach(); delete (window as any).__budgetPlayback })
})

test('file-list thumbnails decode before playback, after returning, and after a page reload', async ({ page }) => {
  await prepare(page)
  await page.evaluate(() => localStorage.setItem('domus_show_thumbnails', '1'))
  await page.routeWebSocket('**/ws', socket => {
    socket.onMessage(message => {
      const request = JSON.parse(String(message))
      if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
    })
  })
  await page.route('**/user', route => route.fulfill({ json: { username: 'playback-fixture', role: 'user' } }))
  const file = {
    inode: 1, name: 'fixture.mp4', path: '/fixture.mp4', is_dir: false, size: sourceSize,
    created_at: '2026-01-01T00:00:00Z', last_modified: '2026-01-01T00:00:00Z', status: 'ready',
    content_type: 'video/mp4', thumbnail_url: `http://127.0.0.1:${port}/thumbnail.webp`, thumbnail_dek: key.toString('hex'),
  }
  await page.route('**/file/?*', route => route.fulfill({ json: { files: [file] } }))
  await page.route('**/file/access?*', route => {
    if (new URL(route.request().url()).searchParams.get('path') !== file.path) return route.fulfill({ status: 404, json: {} })
    return route.fulfill({ json: { ...file, url: `http://127.0.0.1:${port}/large`, dek: key.toString('hex'), chunk_size: CHUNK, generation: 1 } })
  })
  await page.route('**/file/renditions?*', route => route.fulfill({ json: { source_height: 180, renditions: [] } }))
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => route.fulfill({ json: {} }))
  await page.reload()

  const item = page.locator('.file-item').filter({ has: page.locator('.file-name').getByText(file.name, { exact: true }) })
  const thumbnail = item.locator('img.file-thumbnail')
  const assertThumbnail = async () => {
    await expect(thumbnail).toBeVisible()
    await expect(thumbnail).toHaveAttribute('src', /^\/__decrypt__\//)
    await expect.poll(() => thumbnail.evaluate((image: HTMLImageElement) => [image.naturalWidth, image.naturalHeight])).toEqual([320, 180])
    const bytes = await thumbnail.evaluate(async (image: HTMLImageElement) => {
      const response = await fetch(image.src)
      return [...new Uint8Array(await response.arrayBuffer())]
    })
    expect(Buffer.from(bytes)).toEqual(await readFile(join(directory, 'thumbnail.webp')))
  }
  await assertThumbnail()
  await item.dblclick()
  await expect(page).toHaveURL(/\/preview\?/)
  const video = page.locator('video.preview-media')
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThanOrEqual(2)
  await video.evaluate((v: HTMLVideoElement) => { v.muted = true; return v.play() })
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime)).toBeGreaterThan(0.5)
  await page.locator('.back-button').click()
  await expect(page).toHaveURL(/\/files/)
  await assertThumbnail()
  await page.reload()
  await assertThumbnail()
})

test('natural playback reclamation without inspector network recording', async () => {
  test.skip(process.env.DOMUS_E2E_PLAYBACK_MEMORY !== '1', 'Optional memory diagnostic')
  // Playwright enables CDP Network recording, which retains response bodies.
  // Use a separate browser with Runtime/Page only to measure natural cleanup.
  const child = spawn(chromium.executablePath(), [
    '--headless', '--no-sandbox', '--remote-debugging-port=0', `--user-data-dir=${directory}/memory-profile`, '--no-first-run', 'about:blank',
  ], { env: { ...process.env, TMPDIR: directory }, stdio: ['ignore', 'ignore', 'pipe'] })
  let socket: WebSocket | undefined
  try {
    const endpoint = await new Promise<string>((resolve, reject) => {
      let stderr = ''
      child.stderr.on('data', data => {
        stderr += data
        const match = /DevTools listening on (ws:\/\/\S+)/.exec(stderr)
        if (match) resolve(match[1])
      })
      child.once('error', reject)
      child.once('exit', () => reject(new Error('Browser exited before CDP connected')))
    })
    socket = new WebSocket(endpoint)
    await once(socket, 'open')
    let sequence = 0
    const pending = new Map<number, { resolve: (result: any) => void; reject: (reason: Error) => void }>()
    socket.addEventListener('message', event => {
      const message = JSON.parse(String(event.data))
      const call = pending.get(message.id)
      if (!call) return
      pending.delete(message.id)
      if (message.error) call.reject(new Error(message.error.message))
      else call.resolve(message.result)
    })
    socket.addEventListener('close', () => {
      for (const call of pending.values()) call.reject(new Error('Browser CDP closed'))
      pending.clear()
    })
    const send = (method: string, params = {}, sessionId?: string): Promise<any> => new Promise((resolve, reject) => {
      const id = ++sequence
      pending.set(id, { resolve, reject })
      socket!.send(JSON.stringify({ id, method, params, sessionId }))
    })
    const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
    const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
    const evaluate = async (expression: string) => {
      const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, sessionId)
      if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text)
      return result.result.value
    }
    await send('Page.navigate', { url: process.env.DOMUS_E2E_BASE_URL || 'http://127.0.0.1:8089/' }, sessionId)
    await evaluate(`new Promise(resolve => document.readyState === 'complete' ? resolve() : window.addEventListener('load', resolve, { once: true }))`)
    await evaluate(`(async () => {
      await navigator.serviceWorker.ready;
      if (!navigator.serviceWorker.controller) await new Promise(resolve => navigator.serviceWorker.addEventListener('controllerchange', resolve, { once: true }));
      const api = (await import('/src/composables/useApi.ts')).default;
      api.defaults.adapter = async () => ({ data: { source_height: 180, renditions: [] }, status: 200, statusText: 'OK', headers: {}, config: {} });
      window.sw = (await import('/src/composables/useServiceWorker.ts')).useServiceWorker();
      window.makePlayback = (await import('/src/composables/useRenditions.ts')).useRenditions;
    })()`)
    const sample = async (phase: string) => {
      const { processInfo } = await send('SystemInfo.getProcessInfo')
      const processes = await Promise.all(processInfo.map(async (process: { id: number; type: string }) => {
        const status = await readFile(`/proc/${process.id}/smaps_rollup`, 'utf8').catch(() => '')
        return { type: process.type, pss: Number(status.match(/^Pss:\s+(\d+)/m)?.[1] || 0) * 1024 }
      }))
      const pss = processes.reduce((n, p) => n + p.pss, 0)
      console.log(JSON.stringify({ rawCDP: true, phase, pss, processes }))
      return pss
    }
    const baseline = await sample('baseline')
    const closedPSS: number[] = []
    for (let cycle = 1; cycle <= 3; cycle++) {
      await evaluate(`(async () => {
        window.url = sw.registerDecrypt(${JSON.stringify({ url: `http://127.0.0.1:${port}/large`, size: sourceSize, chunkSize: CHUNK, contentType: 'video/mp4', filename: 'large.mp4', dek: key.toString('hex') })});
        await sw.flush();
        window.video = document.createElement('video'); video.muted = true; video.preload = 'metadata'; document.body.appendChild(video);
        window.quality = makePlayback({ decryptUrl: () => url });
        await quality.attach(video, '/large.mp4');
        await new Promise((resolve, reject) => { video.addEventListener('canplay', resolve, { once: true }); video.addEventListener('error', reject, { once: true }); });
        await video.play(); await new Promise(resolve => setTimeout(resolve, 1000)); video.pause();
        for (const time of [120, 30, 145, 5]) {
          const sought = new Promise(resolve => video.addEventListener('seeked', resolve, { once: true }));
          video.currentTime = time; await sought;
        }
      })()`)
      const playing = await sample(`cycle-${cycle}-playing`)
      expect(playing - baseline).toBeLessThan(384 * 1024 ** 2)
      await evaluate(`(async () => { quality.detach(); video.remove(); sw.unregisterDecrypt(url); await sw.flush(); window.quality = window.video = window.url = null; })()`)
      const closedAt = Date.now()
      const closedBytes = served
      for (const seconds of [0, 1, 5, 15, 30]) {
        await new Promise(resolve => setTimeout(resolve, Math.max(0, seconds * 1000 - (Date.now() - closedAt))))
        const pss = await sample(`cycle-${cycle}-closed-${seconds}s`)
        if (seconds === 30) closedPSS.push(pss)
      }
      expect(served - closedBytes).toBeLessThanOrEqual(4 * 1024 ** 2)
    }
    // Allow allocator noise, but reject another video-sized retained window
    // on every reopen. Do not force GC to make the measurement pass.
    expect(closedPSS[2] - closedPSS[1]).toBeLessThan(32 * 1024 ** 2)
  } finally {
    socket?.close()
    if (child.exitCode === null) {
      const exited = once(child, 'exit')
      child.kill('SIGTERM')
      await exited
    }
  }
})
