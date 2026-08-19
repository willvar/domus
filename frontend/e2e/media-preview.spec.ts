import { expect, test, type Page } from '@playwright/test'
import {
  apiBaseURL,
  e2eCredentials,
  login,
  openFilesRoot,
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

test('浏览器上传 PDF 时生成加密缩略图，显示开关默认关闭且不影响原件预览', async ({ page }) => {
  const credentials = e2eCredentials()
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
  const marker = `Domus PDF preview E2E ${suffix}`
  const fileName = `domus-e2e-preview-${suffix}.pdf`
  const pdf = createSinglePagePDF(marker)
  const rootPath = '/'
  const filePath = `${rootPath}${fileName}`
  let authenticated = false
  let deleted = false
  let currentThumbnailURL = ''

  try {
    await login(page, credentials)
    authenticated = true
    await openFilesRoot(page)
    await waitForServiceWorker(page)

    expect(await page.evaluate(() => localStorage.getItem('domus_show_thumbnails'))).toBeNull()
    await uploadFromToolbar(page, {
      name: fileName,
      mimeType: 'application/pdf',
      buffer: pdf,
    })

    const fileItem = page.locator('.file-item').filter({ has: page.locator('.file-name').getByText(fileName, { exact: true }) })
    await expect(fileItem).toBeVisible({ timeout: 30_000 })

    await expect
      .poll(async () => {
        const source = (await listFiles(page, rootPath)).find(file => file.name === fileName)
        return Boolean(source?.status === 'ready' && source.thumbnail_url && source.thumbnail_dek)
      }, {
        timeout: 30_000,
        message: `thumbnail metadata for ${fileName} was not published`,
      })
      .toBe(true)

    const firstSource = (await listFiles(page, rootPath)).find(file => file.name === fileName)
    currentThumbnailURL = firstSource?.thumbnail_url || ''
    expect(currentThumbnailURL, 'source did not expose its browser-generated thumbnail descriptor').not.toBe('')

    const taskResponse = await page.request.get(`${apiBaseURL}/task/`)
    expect(taskResponse.ok()).toBeTruthy()
    const taskList = await taskResponse.json() as TaskInfo[]
    expect(taskList.some(task => task.type === 'preview')).toBe(false)

    // Metadata exists, but the device-local display preference is OFF. This
    // keeps the thumbnail decrypt URL out of the DOM and avoids its OSS GET.
    await expect(fileItem.locator('img.file-thumbnail')).toHaveCount(0)
    await page.locator('.sidebar-account').click()
    const thumbnailSwitch = page.getByRole('switch', { name: /显示缩略图|Show thumbnails/ })
    await expect(thumbnailSwitch).toHaveAttribute('aria-checked', 'false')
    await thumbnailSwitch.click()
    await expect(thumbnailSwitch).toHaveAttribute('aria-checked', 'true')
    expect(await page.evaluate(() => localStorage.getItem('domus_show_thumbnails'))).toBe('1')

    const thumbnail = fileItem.locator('img.file-thumbnail')
    await expect(thumbnail).toBeVisible({ timeout: 30_000 })
    await expect
      .poll(() => thumbnail.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0 && image.naturalHeight > 0), {
        timeout: 30_000,
        message: 'decrypted browser-generated thumbnail did not render',
      })
      .toBe(true)

    const thumbnailURL = await thumbnail.getAttribute('src')
    expect(thumbnailURL).toMatch(/^\/__decrypt__\//)
    await expect
      .poll(() => page.evaluate(async (url) => {
        const response = await fetch(url)
        const bytes = new Uint8Array(await response.arrayBuffer())
        return response.ok &&
          new TextDecoder('latin1').decode(bytes.slice(0, 4)) === 'RIFF' &&
          new TextDecoder('latin1').decode(bytes.slice(8, 12)) === 'WEBP'
      }, thumbnailURL!), {
        timeout: 20_000,
        message: 'thumbnail decrypt endpoint did not return WebP bytes',
      })
      .toBe(true)

    await fileItem.dblclick()
    await expect(page).toHaveURL(/\/preview\?/)
    const pdfFrame = page.locator('iframe.preview-pdf')
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

    // Replacing content keeps the source inode/path but must produce a fresh
    // generation-scoped thumbnail and reclaim the previous thumbnail inode.
    await openFilesRoot(page)
    await uploadFromToolbar(page, {
      name: fileName,
      mimeType: 'application/pdf',
      buffer: createSinglePagePDF(`Domus replacement PDF ${suffix}`),
      conflictAction: 'replace',
    })
    const previousThumbnailURL = currentThumbnailURL
    await expect
      .poll(async () => {
        const source = (await listFiles(page, rootPath)).find(file => file.name === fileName)
        return source?.thumbnail_url || ''
      }, {
        timeout: 30_000,
        message: 'replacement did not rotate the generation-scoped thumbnail',
      })
      .not.toBe(previousThumbnailURL)
    currentThumbnailURL = (await listFiles(page, rootPath)).find(file => file.name === fileName)?.thumbnail_url || ''
    expect(currentThumbnailURL).not.toBe('')
    await expect.poll(async () => (await fetch(previousThumbnailURL)).ok, {
      timeout: 30_000,
      message: 'replaced thumbnail ciphertext remained in object storage',
    }).toBe(false)
    await expect(fileItem.locator('img.file-thumbnail')).toBeVisible()

    await permanentlyDelete(page, filePath)
    deleted = true
    await expect.poll(async () => (await fetch(currentThumbnailURL)).ok, {
      timeout: 30_000,
      message: 'thumbnail ciphertext remained after permanently deleting its source',
    }).toBe(false)
  } finally {
    if (authenticated && !deleted) await permanentlyDelete(page, filePath)
  }
})
