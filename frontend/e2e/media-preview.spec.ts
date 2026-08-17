import { expect, test, type Page } from '@playwright/test'
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
  thumbnail_url?: string
  thumbnail_dek?: string
}

interface TaskInfo {
  type: string
  name: string
  status: string
}

function createSinglePagePDF(marker: string): Buffer {
  const escapedMarker = marker.replaceAll('\\', '\\\\').replaceAll('(', '\\(').replaceAll(')', '\\)')
  const stream = `BT\n/F1 24 Tf\n40 120 Td\n(${escapedMarker}) Tj\nET`
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 200] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    `<< /Length ${Buffer.byteLength(stream, 'ascii')} >>\nstream\n${stream}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]

  let pdf = '%PDF-1.4\n'
  const offsets = [0]
  objects.forEach((object, index) => {
    offsets.push(Buffer.byteLength(pdf, 'ascii'))
    pdf += `${index + 1} 0 obj\n${object}\nendobj\n`
  })
  const xrefOffset = Buffer.byteLength(pdf, 'ascii')
  pdf += `xref\n0 ${objects.length + 1}\n`
  pdf += '0000000000 65535 f \n'
  for (const offset of offsets.slice(1)) {
    pdf += `${String(offset).padStart(10, '0')} 00000 n \n`
  }
  pdf += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xrefOffset}\n%%EOF\n`
  return Buffer.from(pdf, 'ascii')
}

async function listFiles(page: Page, path: string): Promise<FileInfo[]> {
  const response = await page.request.get(`${apiBaseURL}/file/`, { params: { path } })
  expect(response.ok(), `listing ${path} failed with HTTP ${response.status()}`).toBeTruthy()
  const body = await response.json() as { files?: FileInfo[] }
  return Array.isArray(body.files) ? body.files : []
}

test('PDF 上传后由 workspace 生成缩略图并可解密预览原件', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
  const marker = `Domus PDF preview E2E ${suffix}`
  const fileName = `domus-e2e-preview-${suffix}.pdf`
  const pdf = createSinglePagePDF(marker)
  const homePath = `/home/${credentials.username}/`
  const filePath = `${homePath}${fileName}`
  const derivedPath = `${homePath}.user/derived/`
  let authenticated = false
  let deleted = false
  let generatedPreviewPath = ''

  try {
    await login(page, credentials)
    authenticated = true
    await openHomeDirectory(page, credentials.username)
    await waitForServiceWorker(page)

    const derivedBefore = new Set((await listFiles(page, derivedPath)).map(file => file.path))
    await uploadFromToolbar(page, {
      name: fileName,
      mimeType: 'application/pdf',
      buffer: pdf,
    })

    const fileItem = page.locator('.file-item').filter({ hasText: fileName })
    await expect(fileItem).toBeVisible({ timeout: 30_000 })
    await expect(fileItem.locator('.file-status-badge')).toHaveCount(0)

    await expect
      .poll(async () => {
        const response = await page.request.get(`${apiBaseURL}/task/`)
        if (!response.ok()) return `HTTP ${response.status()}`
        const tasks = await response.json() as TaskInfo[]
        return tasks.find(task => task.type === 'preview' && task.name === fileName)?.status || 'missing'
      }, {
        timeout: 120_000,
        message: `server preview task for ${fileName} did not complete`,
      })
      .toBe('completed')

    await expect
      .poll(async () => {
        const source = (await listFiles(page, homePath)).find(file => file.name === fileName)
        return Boolean(source?.status === 'ready' && source.thumbnail_url && source.thumbnail_dek)
      }, {
        timeout: 30_000,
        message: `thumbnail metadata for ${fileName} was not published`,
      })
      .toBe(true)

    const derivedAfter = await listFiles(page, derivedPath)
    const generatedPreviews = derivedAfter.filter(file =>
      !derivedBefore.has(file.path) && file.name.endsWith('.preview.jpg'),
    )
    expect(generatedPreviews, 'expected exactly one new server-derived preview').toHaveLength(1)
    generatedPreviewPath = generatedPreviews[0].path

    const thumbnail = fileItem.locator('img.file-thumbnail')
    await expect(thumbnail).toBeVisible({ timeout: 30_000 })
    await expect
      .poll(() => thumbnail.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0 && image.naturalHeight > 0), {
        timeout: 30_000,
        message: 'decrypted server-generated thumbnail did not render',
      })
      .toBe(true)

    const thumbnailURL = await thumbnail.getAttribute('src')
    expect(thumbnailURL).toMatch(/^\/__decrypt__\//)
    await expect
      .poll(() => page.evaluate(async (url) => {
        const response = await fetch(url)
        const bytes = new Uint8Array(await response.arrayBuffer())
        return response.ok && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff
      }, thumbnailURL!), {
        timeout: 20_000,
        message: 'thumbnail decrypt endpoint did not return JPEG bytes',
      })
      .toBe(true)

    await fileItem.dblclick()
    const viewer = page.locator('.plasma-window').filter({
      has: page.locator('.plasma-titlebar-title', { hasText: fileName }),
    })
    await expect(viewer).toBeVisible()
    const pdfFrame = viewer.locator('iframe.viewer-pdf')
    await expect(pdfFrame).toBeVisible()
    const decryptURL = await pdfFrame.getAttribute('src')
    expect(decryptURL).toMatch(/^\/__decrypt__\//)

    await expect
      .poll(() => page.evaluate(async ({ url, expectedMarker }) => {
        const response = await fetch(url)
        const bytes = new Uint8Array(await response.arrayBuffer())
        const text = new TextDecoder('latin1').decode(bytes)
        return {
          ok: response.ok,
          magic: text.slice(0, 4),
          marker: text.includes(expectedMarker),
        }
      }, { url: decryptURL!, expectedMarker: marker }), {
        timeout: 20_000,
        message: 'PDF decrypt endpoint did not return the uploaded plaintext document',
      })
      .toEqual({ ok: true, magic: '%PDF', marker: true })

    await expect
      .poll(() => page.evaluate(async () => {
        const cache = await caches.open('domus-decrypt')
        return (await cache.keys()).length
      }), {
        timeout: 20_000,
        message: 'decrypted PDF was not committed to the bounded plaintext cache',
      })
      .toBeGreaterThan(0)

    await expect
      .poll(() => page.evaluate(async (url) => {
        const response = await fetch(url, { headers: { Range: 'bytes=0-63' } })
        const bytes = new Uint8Array(await response.arrayBuffer())
        return {
          status: response.status,
          contentRange: response.headers.get('Content-Range'),
          contentLength: response.headers.get('Content-Length'),
          bodyLength: bytes.length,
          magic: new TextDecoder('latin1').decode(bytes.slice(0, 4)),
        }
      }, decryptURL!), {
        timeout: 20_000,
        message: 'cached PDF range response was malformed or unresponsive',
      })
      .toEqual({
        status: 206,
        contentRange: `bytes 0-63/${pdf.length}`,
        contentLength: '64',
        bodyLength: 64,
        magic: '%PDF',
      })

    const stabilityMS = Number.parseInt(process.env.DOMUS_E2E_PREVIEW_STABILITY_MS || '2000', 10)
    if (Number.isFinite(stabilityMS) && stabilityMS > 0) {
      const deadline = Date.now() + stabilityMS
      while (Date.now() < deadline) {
        await page.waitForTimeout(Math.min(500, deadline - Date.now()))
        const probe = await page.evaluate(() => ({
          readyState: document.readyState,
          historyLength: window.history.length,
        }))
        expect(probe.readyState).toBe('complete')
        expect(probe.historyLength).toBeLessThan(200)
      }
    }

    await permanentlyDelete(page, filePath)
    deleted = true
    await expect
      .poll(async () => (await listFiles(page, derivedPath)).some(file => file.path === generatedPreviewPath), {
        timeout: 30_000,
        message: 'derived preview remained after permanently deleting its source',
      })
      .toBe(false)
  } finally {
    if (authenticated && !deleted) await permanentlyDelete(page, filePath)
  }
})
