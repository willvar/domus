import { createCipheriv } from 'node:crypto'
import { createServer, type Server } from 'node:http'
import { expect, test, type Page } from '@playwright/test'

const key = Buffer.alloc(32, 37)
const fixtures = new Map<string, { plain: Buffer; wire: Buffer; contentType: string }>()
let server: Server
let port: number

function fixture(name: string, content: string, contentType: string) {
  const plain = Buffer.from(content)
  const chunks = [Buffer.from([1, 0, 1, 0, 0])]
  for (let offset = 0; offset < plain.length; offset += 65536) {
    const ordinal = Buffer.alloc(8)
    ordinal.writeBigUInt64BE(BigInt(offset / 65536))
    const iv = Buffer.alloc(12)
    ordinal.copy(iv, 4)
    const cipher = createCipheriv('aes-256-gcm', key, iv)
    cipher.setAAD(ordinal)
    chunks.push(iv, cipher.update(plain.subarray(offset, offset + 65536)), cipher.final(), cipher.getAuthTag())
  }
  fixtures.set(name, { plain, wire: Buffer.concat(chunks), contentType })
}

function pdf() {
  const stream = 'BT /F1 24 Tf 40 700 Td (Domus document preview) Tj ET'
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]
  let content = '%PDF-1.4\n'
  const offsets = [0]
  for (const [index, object] of objects.entries()) {
    offsets.push(content.length)
    content += `${index + 1} 0 obj\n${object}\nendobj\n`
  }
  const xref = content.length
  content += `xref\n0 6\n0000000000 65535 f \n${offsets.slice(1).map(offset => `${String(offset).padStart(10, '0')} 00000 n \n`).join('')}`
  return content + `trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
}

test.beforeAll(async () => {
  fixture('fixture.pdf', pdf(), 'application/pdf')
  const html = `<!doctype html><html><head><style>h1 { color: rgb(20, 100, 180); }</style></head><body>
    <h1>Rendered before editing</h1><script>document.body.dataset.scriptRan = 'yes'; parent.document.body.dataset.scriptRan = 'yes';</script></body></html>`
  fixture('fixture.html', html, 'text/html')
  fixture('fixture.htm', html, 'text/html')
  fixture('fixture.md', `# Wide Markdown\n\nA rendered document, not source code.\n\n\`\`\`text\n${'wide-code-'.repeat(400)}\n\`\`\`\n\n| First | Second |\n| --- | --- |\n| ${'W'.repeat(2000)} | Value |\n`, 'text/markdown')
  for (const name of ['fixture.log', 'fixture.txt', 'fixture.json', 'fixture.yaml', 'fixture.ts', 'fixture.css']) {
    fixture(name, 'Document preview fixture\n'+ 'long source line '.repeat(400), 'text/plain')
  }
  fixture('fixture.csv', 'Name,Value\nPreview,Wide layout\n', 'text/csv')
  fixture('fixture.ipynb', JSON.stringify({ cells: [{ cell_type: 'code', source: ['print("Wide layout")'] }] }), 'application/json')
  server = createServer((request, response) => {
    const data = fixtures.get(request.url!.slice(1))
    if (!data) { response.writeHead(404).end(); return }
    const range = /^bytes=(\d+)-(\d+)$/.exec(request.headers.range || '')
    const start = range ? Number(range[1]) : 0
    const end = range ? Math.min(Number(range[2]), data.wire.length - 1) : data.wire.length - 1
    response.writeHead(range ? 206 : 200, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Expose-Headers': 'Content-Range',
      'Content-Length': end - start + 1,
      ...(range ? { 'Content-Range': `bytes ${start}-${end}/${data.wire.length}` } : {}),
    })
    response.end(data.wire.subarray(start, end + 1))
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  port = (server.address() as { port: number }).port
})

test.afterAll(async () => {
  server?.closeAllConnections()
  if (server) await new Promise<void>(resolve => server.close(() => resolve()))
})

async function openPreview(page: Page, name: string) {
  await page.routeWebSocket('**/ws', socket => socket.onMessage(message => {
    const request = JSON.parse(String(message))
    if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
  }))
  await page.route('**/user', route => route.fulfill({ json: { username: 'document-fixture', role: 'user' } }))
  await page.route('**/file/?*', route => route.fulfill({ json: { files: [{
    name, path: `/${name}`, inode: 1, size: fixtures.get(name)!.plain.length, is_dir: false,
    created_at: '2026-01-01T00:00:00Z', last_modified: '2026-01-01T00:00:00Z', status: 'ready',
  }] } }))
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => route.fulfill({ json: {} }))
  await page.route('**/file/access?*', route => {
    const data = fixtures.get(name)!
    return route.fulfill({ json: {
      name, path: `/${name}`, inode: 1, generation: 1, size: data.plain.length,
      url: `http://127.0.0.1:${port}/${name}`, content_type: data.contentType,
      chunk_size: 65536, dek: key.toString('hex'),
    } })
  })
  await page.goto('/')
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready
    if (!navigator.serviceWorker.controller) await new Promise<void>(resolve => {
      navigator.serviceWorker.addEventListener('controllerchange', () => resolve(), { once: true })
    })
  })
  await page.locator('.file-item').filter({ has: page.locator('.file-name').getByText(name, { exact: true }) }).dblclick()
}

test('PDF fills widescreen and resized viewports without covering the mobile bar', async ({ page }) => {
  await page.setViewportSize({ width: 2560, height: 1080 })
  await openPreview(page, 'fixture.pdf')
  const frame = page.locator('iframe.preview-pdf')
  await expect(frame).toBeVisible()
  await expect(frame).toHaveAttribute('src', /#view=Fit&navpanes=0$/)
  for (const size of [{ width: 2560, height: 1080 }, { width: 1280, height: 800 }, { width: 844, height: 390 }, { width: 700, height: 844 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(size)
    await expect.poll(async () => Math.round((await frame.boundingBox())!.width)).toBe(size.width - (size.width < 768 ? 20 : 24))
    await expect.poll(async () => {
      const canvasBox = (await page.locator('.preview-canvas').boundingBox())!
      const barBox = size.width < 768 ? (await page.locator('.preview-mobile-bar').boundingBox())! : null
      return barBox ? Math.abs(canvasBox.y + canvasBox.height - barBox.y) < 1 : true
    }).toBeTruthy()
    const box = (await frame.boundingBox())!
    const header = (await page.locator('.preview-header').boundingBox())!
    const bottom = size.width < 768 ? (await page.locator('.preview-mobile-bar').boundingBox())!.y : size.height
    expect(box.y).toBeGreaterThanOrEqual(header.y + header.height)
    expect(box.y + box.height).toBeLessThanOrEqual(bottom + 1)
    expect(box.height).toBeGreaterThan(bottom - header.height - 30)
  }
  const url = (await frame.getAttribute('src'))!
  const bytes = await page.evaluate(async url => new TextDecoder().decode(await (await fetch(url)).arrayBuffer()), url)
  expect(bytes).toBe(fixtures.get('fixture.pdf')!.plain.toString())
})

for (const name of ['fixture.log', 'fixture.txt', 'fixture.json', 'fixture.yaml', 'fixture.ts', 'fixture.css', 'fixture.csv', 'fixture.ipynb']) {
  test(`${name} shares the full-width document layout`, async ({ page }) => {
    await page.setViewportSize({ width: 2560, height: 1080 })
    await openPreview(page, name)
    const document = page.locator('.text-preview, .table-preview, .notebook-preview')
    await expect(document).toBeVisible()
    expect((await document.boundingBox())!.width).toBe(2536)
    expect(await page.locator('.preview-canvas').evaluate(element => element.scrollWidth <= element.clientWidth)).toBeTruthy()
    if (!name.endsWith('.ipynb')) {
      await page.getByTestId('preview-edit').click()
      await expect(page.getByTestId('text-editor')).toBeVisible()
      expect((await page.locator('.edit-workspace').boundingBox())!.width).toBe(2528)
    }
  })
}

for (const name of ['fixture.html', 'fixture.htm']) {
  test(`${name} opens rendered and sandboxed, with source editing still available`, async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1080 })
    await openPreview(page, name)
    const frame = page.locator('iframe.preview-html')
    await expect(frame).toBeVisible()
    await expect(frame).toHaveAttribute('sandbox', '')
    const heading = frame.contentFrame().getByRole('heading', { name: 'Rendered before editing' })
    await expect(heading).toBeVisible()
    await expect(heading).toHaveCSS('color', 'rgb(20, 100, 180)')
    await expect(frame.contentFrame().locator('body')).not.toHaveAttribute('data-script-ran', 'yes')
    await expect(page.locator('body')).not.toHaveAttribute('data-script-ran', 'yes')
    await expect(page.locator('.text-preview, .edit-workspace')).toHaveCount(0)
    expect((await frame.boundingBox())!.width).toBe(1896)
    await page.getByTestId('preview-edit').click()
    await expect(page.getByTestId('text-editor')).toBeVisible()
    await expect(page.locator('.edit-panes')).toHaveClass(/edit-panes--split/)
    await expect(page.locator('iframe.edit-html-preview').contentFrame().getByRole('heading')).toHaveText('Rendered before editing')
    await page.locator('.preview-actions').getByRole('button', { name: /取消|Cancel/ }).click()
    await expect(heading).toBeVisible()
    await expect(page.locator('.edit-workspace')).toHaveCount(0)
  })
}

test('Markdown uses the available landscape width while wide code and tables scroll locally', async ({ page }) => {
  await page.setViewportSize({ width: 2560, height: 1080 })
  await openPreview(page, 'fixture.md')
  const article = page.locator('.document-preview')
  await expect(article.getByRole('heading', { name: 'Wide Markdown' })).toBeVisible()
  for (const size of [{ width: 2560, height: 1080 }, { width: 1280, height: 800 }, { width: 844, height: 390 }, { width: 700, height: 844 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(size)
    await expect.poll(async () => Math.round((await article.boundingBox())!.width)).toBe(size.width - (size.width < 768 ? 20 : 24))
    const canvas = page.locator('.preview-canvas')
    expect(await canvas.evaluate(element => element.scrollWidth <= element.clientWidth)).toBeTruthy()
    for (const selector of ['pre', 'table']) {
      expect(await article.locator(selector).evaluate(element => element.scrollWidth > element.clientWidth)).toBeTruthy()
    }
  }
})
