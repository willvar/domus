import { execFileSync } from 'node:child_process'
import { expect, test, type Locator, type Page } from '@playwright/test'
import {
  apiBaseURL,
  e2eCredentials,
  login,
  openHomeDirectory,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

interface FileInfo {
  name: string
  path: string
  status?: string
  content_type?: string
  thumbnail_url?: string
  thumbnail_dek?: string
}

interface FileAccess {
  url: string
  size: number
  name: string
  content_type: string
  chunk_size: number
  dek: string
}

function mediaFixture(arguments_: string[]): Buffer {
  const image = process.env.DOMUS_E2E_WORKSPACE_IMAGE || 'domus-workspace:0.1.0'
  return execFileSync('docker', [
    'run', '--rm', '--network', 'none', image,
    'ffmpeg', '-nostdin', '-hide_banner', '-loglevel', 'error',
    ...arguments_,
    'pipe:1',
  ], { maxBuffer: 4 * 1024 * 1024 })
}

function probeMedia(contents: Buffer): Array<{ codec_type: string; codec_name: string }> {
  const image = process.env.DOMUS_E2E_WORKSPACE_IMAGE || 'domus-workspace:0.1.0'
  const output = execFileSync('docker', [
    'run', '--rm', '--network', 'none', '-i', image,
    'ffprobe', '-v', 'error', '-show_entries', 'stream=codec_type,codec_name', '-of', 'json', 'pipe:0',
  ], { input: contents, maxBuffer: 1024 * 1024 })
  return (JSON.parse(output.toString('utf8')) as {
    streams?: Array<{ codec_type: string; codec_name: string }>
  }).streams || []
}

function decodeMedia(contents: Buffer): void {
  const image = process.env.DOMUS_E2E_WORKSPACE_IMAGE || 'domus-workspace:0.1.0'
  execFileSync('docker', [
    'run', '--rm', '--network', 'none', '-i', image,
    'ffmpeg', '-nostdin', '-hide_banner', '-loglevel', 'error', '-xerror',
    '-i', 'pipe:0', '-map', '0', '-f', 'null', '-',
  ], { input: contents, maxBuffer: 4 * 1024 * 1024 })
}

async function listFiles(page: Page, path: string): Promise<FileInfo[]> {
  const response = await page.request.get(`${apiBaseURL}/file/`, { params: { path } })
  expect(response.ok(), `listing ${path} failed with HTTP ${response.status()}`).toBeTruthy()
  const body = await response.json() as { files?: FileInfo[] }
  return Array.isArray(body.files) ? body.files : []
}

async function waitForTask(page: Page, taskID: string, type: string, name: string): Promise<void> {
  await expect
    .poll(async () => {
      const response = await page.request.get(`${apiBaseURL}/task/`)
      if (!response.ok()) return `HTTP ${response.status()}`
      const tasks = await response.json() as Array<{ task_id: string; type: string; name: string; status: string }>
      return tasks.find(task => task.task_id === taskID && task.type === type && task.name === name)?.status || 'missing'
    }, {
      timeout: 120_000,
      message: `${type} task ${taskID} for ${name} did not complete`,
    })
    .toBe('completed')
}

function itemNamed(page: Page, name: string): Locator {
  return page.locator('.file-item').filter({ hasText: name })
}

function viewerNamed(page: Page, name: string): Locator {
  return page.locator('.plasma-window').filter({
    has: page.locator('.plasma-titlebar-title', { hasText: name }),
  })
}

async function expectDecryptedMagic(page: Page, url: string, offset: number, expected: number[]): Promise<void> {
  await expect
    .poll(() => page.evaluate(async ({ source, start, length }) => {
      const response = await fetch(source, { headers: { Range: `bytes=${start}-${start + length - 1}` } })
      return Array.from(new Uint8Array(await response.arrayBuffer()))
    }, { source: url, start: offset, length: expected.length }), {
      timeout: 20_000,
      message: `decrypted media did not expose expected bytes at offset ${offset}`,
    })
    .toEqual(expected)
}

async function decryptedBytes(page: Page, url: string): Promise<Buffer> {
  const bytes = await page.evaluate(async (source) => {
    const response = await fetch(source)
    if (!response.ok) throw new Error(`decrypt fetch failed with HTTP ${response.status}`)
    return Array.from(new Uint8Array(await response.arrayBuffer()))
  }, url)
  return Buffer.from(bytes)
}

async function fetchUncachedFile(page: Page, path: string): Promise<Buffer> {
  const response = await page.request.get(`${apiBaseURL}/file/access`, { params: { path } })
  expect(response.ok(), `accessing ${path} failed with HTTP ${response.status()}`).toBeTruthy()
  const access = await response.json() as FileAccess
  const bytes = await page.evaluate(async (metadata) => {
    const controller = navigator.serviceWorker.controller
    if (!controller) throw new Error('service worker does not control the E2E page')
    const id = crypto.randomUUID()
    controller.postMessage({
      type: 'register',
      id,
      // Deliberately omit contentHash: this establishes the authoritative
      // plaintext before exercising the Range accumulator and Cache API.
      metadata: {
        url: metadata.url,
        size: metadata.size,
        chunkSize: metadata.chunk_size,
        contentType: metadata.content_type,
        filename: metadata.name,
        dek: metadata.dek,
      },
    })
    await new Promise<void>((resolve) => {
      const channel = new MessageChannel()
      channel.port1.onmessage = () => resolve()
      controller.postMessage({ type: 'flush' }, [channel.port2])
    })
    try {
      const plaintext = await fetch(`/__decrypt__/${id}`)
      if (!plaintext.ok) throw new Error(`decrypt fetch failed with HTTP ${plaintext.status}`)
      return Array.from(new Uint8Array(await plaintext.arrayBuffer()))
    } finally {
      controller.postMessage({ type: 'unregister', id })
    }
  }, access)
  return Buffer.from(bytes)
}

async function closeViewer(viewer: Locator): Promise<void> {
  await viewer.locator('.plasma-btn-close').click()
  await expect(viewer).toBeHidden()
}

test('图片、音频、视频可本地解密预览，视频可在 Workspace 转码', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const imageName = `e2e-image-${suffix}.png`
  const audioName = `e2e-audio-${suffix}.wav`
  const videoName = `e2e-video-${suffix}.webm`
  const homePath = `/home/${credentials.username}/`
  const image = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=',
    'base64',
  )
  const audio = mediaFixture([
    '-f', 'lavfi', '-i', 'sine=frequency=660:duration=0.5',
    '-c:a', 'pcm_s16le', '-f', 'wav',
  ])
  const video = mediaFixture([
    '-f', 'lavfi', '-i', 'testsrc=size=64x64:rate=10:duration=1.5',
    '-f', 'lavfi', '-i', 'sine=frequency=440:duration=1.5',
    '-shortest', '-c:v', 'libvpx', '-deadline', 'realtime', '-cpu-used', '8', '-b:v', '100k',
    '-pix_fmt', 'yuv420p', '-c:a', 'libopus', '-b:a', '48k', '-f', 'webm',
  ])
  const cleanupPaths = [
    `${homePath}${imageName}`,
    `${homePath}${audioName}`,
    `${homePath}${videoName}`,
  ]
  let authenticated = false

  try {
    await login(page, credentials)
    authenticated = true
    await openHomeDirectory(page, credentials.username)
    await waitForServiceWorker(page)

    await uploadFromToolbar(page, { name: imageName, mimeType: 'image/png', buffer: image })
    const imageItem = itemNamed(page, imageName)
    await expect(imageItem.locator('img.file-thumbnail')).toBeVisible({ timeout: 30_000 })
    await imageItem.dblclick()
    const imageViewer = viewerNamed(page, imageName)
    const imageElement = imageViewer.locator('img.viewer-image')
    await expect
      .poll(() => imageElement.evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth > 0))
      .toBe(true)
    await expectDecryptedMagic(page, (await imageElement.getAttribute('src'))!, 0, [0x89, 0x50, 0x4e, 0x47])
    await closeViewer(imageViewer)

    await uploadFromToolbar(page, { name: audioName, mimeType: 'audio/wav', buffer: audio })
    const audioItem = itemNamed(page, audioName)
    await expect(audioItem).toBeVisible()
    await audioItem.dblclick()
    const audioViewer = viewerNamed(page, audioName)
    const audioElement = audioViewer.locator('audio.audio-player')
    await expect
      .poll(() => audioElement.evaluate((element: HTMLAudioElement) => {
        if (element.error) return `error-${element.error.code}`
        return element.readyState >= 1 && Number.isFinite(element.duration) && element.duration > 0 ? 'ready' : 'loading'
      }))
      .toBe('ready')
    await expectDecryptedMagic(page, (await audioElement.getAttribute('src'))!, 0, [0x52, 0x49, 0x46, 0x46])
    await closeViewer(audioViewer)

    await uploadFromToolbar(page, { name: videoName, mimeType: 'video/webm', buffer: video })
    const videoItem = itemNamed(page, videoName)
    await expect(videoItem.locator('img.file-thumbnail')).toBeVisible({ timeout: 30_000 })
    await videoItem.dblclick()
    const videoViewer = viewerNamed(page, videoName)
    const videoElement = videoViewer.locator('video.viewer-video')
    await expect
      .poll(() => videoElement.evaluate((element: HTMLVideoElement) => {
        if (element.error) return `error-${element.error.code}`
        return element.readyState >= 1 && Number.isFinite(element.duration) && element.duration > 0 ? 'ready' : 'loading'
      }))
      .toBe('ready')
    await expectDecryptedMagic(page, (await videoElement.getAttribute('src'))!, 0, [0x1a, 0x45, 0xdf, 0xa3])
    await closeViewer(videoViewer)

    const videoRecord = (await listFiles(page, homePath)).find(file => file.name === videoName)
    expect(videoRecord?.content_type).toBe('video/webm')

    const transcodeResponse = await page.request.post(`${apiBaseURL}/file/transcode`, {
      data: { path: `${homePath}${videoName}`, profile: 'video-720p' },
    })
    const transcodeText = await transcodeResponse.text()
    expect(transcodeResponse.status(), transcodeText).toBe(202)
    const transcode = JSON.parse(transcodeText) as { task_id: string; output_path: string }
    cleanupPaths.push(transcode.output_path)
    const outputName = transcode.output_path.split('/').pop()!
    await waitForTask(page, transcode.task_id, 'transcode', outputName)
    await expect
      .poll(async () => (await listFiles(page, homePath)).find(file => file.path === transcode.output_path)?.status || 'missing')
      .toBe('ready')

    const outputItem = itemNamed(page, outputName)
    await expect(outputItem).toBeVisible({ timeout: 30_000 })

    const pristineOutput = await fetchUncachedFile(page, transcode.output_path)
    expect(pristineOutput.subarray(4, 8).toString('ascii')).toBe('ftyp')
    expect(probeMedia(pristineOutput)).toEqual(expect.arrayContaining([
      expect.objectContaining({ codec_type: 'video', codec_name: 'h264' }),
      expect.objectContaining({ codec_type: 'audio', codec_name: 'aac' }),
    ]))
    decodeMedia(pristineOutput)

    await outputItem.dblclick()
    const outputViewer = viewerNamed(page, outputName)
    const outputVideo = outputViewer.locator('video.viewer-video')
    await expect(outputVideo).toBeVisible()
    await expect
      .poll(() => outputVideo.evaluate((element: HTMLVideoElement) => {
        if (element.error) return `error-${element.error.code}`
        return element.readyState >= 1 && Number.isFinite(element.duration) && element.duration > 0 ? 'ready' : 'loading'
      }))
      .toBe('ready')
    const outputURL = (await outputVideo.getAttribute('src'))!
    const outputBytes = await decryptedBytes(page, outputURL)
    expect(outputBytes.equals(pristineOutput), 'Range-first media access changed cached plaintext bytes').toBe(true)
    decodeMedia(outputBytes)
  } finally {
    if (authenticated) {
      for (const path of cleanupPaths.reverse()) await permanentlyDelete(page, path)
    }
  }
})
