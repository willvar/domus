import { expect, test, type Locator, type Page, type Request } from '@playwright/test'
import {
  apiBaseURL,
  e2eCredentials,
  login,
  openFilesRoot,
  permanentlyDelete,
  uploadFromToolbar,
  waitForServiceWorker,
} from './helpers'

function itemNamed(page: Page, name: string): Locator {
  return page.locator('.file-item').filter({
    has: page.locator('.file-name').getByText(name, { exact: true }),
  })
}

test('Markdown 可编辑、加密写回并在重新打开后保留修改', async ({ page }) => {
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`
  const name = `e2e-edit-${suffix}.md`
  const path = `/${name}`
  const initialMarker = `DOMUS_EDIT_INITIAL_${suffix}`
  const savedMarker = `DOMUS_EDIT_SAVED_${suffix}`
  const unsavedMarker = `DOMUS_EDIT_UNSAVED_${suffix}`
  const initialContent = Buffer.from(`# Initial heading\n\n${initialMarker}\n`)
  const sections = Array.from({ length: 80 }, (_, index) => [
    `## Section ${index + 1}`,
    '',
    `Paragraph ${index + 1}: ${savedMarker} — synchronized Markdown scrolling must follow source lines, even when rendered blocks have different heights.`,
  ].join('\n')).join('\n\n')
  const savedContent = `# Edited heading\n\n${savedMarker}\n\n${sections}\n`
  let authenticated = false

  try {
    await login(page, e2eCredentials())
    authenticated = true
    await openFilesRoot(page)
    await uploadFromToolbar(page, {
      name,
      mimeType: 'text/markdown',
      buffer: initialContent,
    })

    await waitForServiceWorker(page)
    await itemNamed(page, name).dblclick()
    await expect(page).toHaveURL(/\/preview\?/)
    await expect(page.locator('.markdown-body')).toContainText(initialMarker)

    await page.getByTestId('preview-edit').click()
    const editor = page.getByTestId('text-editor')
    await expect(editor).toBeVisible()
    await expect(page.locator('.edit-panes')).toHaveClass(/edit-panes--split/)

    const code = editor.locator('.cm-content')
    await code.click()
    await page.keyboard.press('Control+A')
    await page.keyboard.insertText(savedContent)
    await expect(page.getByTestId('preview-save')).toBeEnabled()
    await expect(page.locator('.edit-render')).toContainText(savedMarker)

    const sourceScroller = editor.locator('.cm-scroller')
    const renderedPreview = page.getByTestId('markdown-editor-preview')
    await expect.poll(() => renderedPreview.evaluate(element => element.scrollHeight > element.clientHeight)).toBe(true)

    await sourceScroller.evaluate((element) => {
      element.scrollTop = element.scrollHeight - element.clientHeight
      element.dispatchEvent(new Event('scroll'))
    })
    await expect.poll(() => renderedPreview.evaluate((element) => {
      const maxScroll = element.scrollHeight - element.clientHeight
      return maxScroll > 0 ? element.scrollTop / maxScroll : 0
    })).toBeGreaterThan(0.9)

    await page.waitForTimeout(80)
    await renderedPreview.evaluate((element) => {
      element.scrollTop = (element.scrollHeight - element.clientHeight) * 0.3
      element.dispatchEvent(new Event('scroll'))
    })
    await expect.poll(() => sourceScroller.evaluate((element) => {
      const maxScroll = element.scrollHeight - element.clientHeight
      return maxScroll > 0 ? element.scrollTop / maxScroll : 0
    })).toBeGreaterThan(0.15)
    await expect.poll(() => sourceScroller.evaluate((element) => {
      const maxScroll = element.scrollHeight - element.clientHeight
      return maxScroll > 0 ? element.scrollTop / maxScroll : 0
    })).toBeLessThan(0.5)

    const apiRequestBodies: Buffer[] = []
    const objectWrites: Buffer[] = []
    const observeWrite = (request: Request): void => {
      const url = new URL(request.url())
      const body = request.postDataBuffer()
      if (url.origin === apiBaseURL && body) apiRequestBodies.push(body)
      if (request.method() === 'PUT' && url.searchParams.has('partNumber') && body) objectWrites.push(body)
    }
    page.on('request', observeWrite)
    const completion = page.waitForResponse((response) => {
      const url = new URL(response.url())
      if (url.origin !== apiBaseURL || url.pathname !== '/file/upload' || response.request().method() !== 'POST') {
        return false
      }
      try {
        const body = response.request().postDataJSON() as { upload_id?: unknown; encrypted_size?: unknown }
        return typeof body.upload_id === 'string' && typeof body.encrypted_size === 'number'
      } catch {
        return false
      }
    })

    try {
      await page.keyboard.press('Control+S')
      const response = await completion
      expect(response.ok(), `edit save failed with HTTP ${response.status()}`).toBeTruthy()
      await expect(page.locator('.edit-workspace')).toBeHidden()
      await expect(page.locator('.n-message')).toContainText(/文件已保存|File saved/)
    } finally {
      page.off('request', observeWrite)
    }

    const plaintext = Buffer.from(savedContent)
    expect(objectWrites.length, 'editing did not write an encrypted object from the browser').toBeGreaterThan(0)
    expect(apiRequestBodies.every(body => !body.includes(plaintext)), 'plaintext reached the Domus API').toBeTruthy()
    expect(objectWrites.every(body => !body.includes(plaintext)), 'plaintext reached object storage').toBeTruthy()

    await page.reload({ waitUntil: 'domcontentloaded' })
    await expect(page.locator('.markdown-body')).toContainText(savedMarker)
    await expect(page.locator('.markdown-body')).not.toContainText(initialMarker)

    // Navigating away with a dirty buffer must require an explicit decision.
    await page.getByTestId('preview-edit').click()
    await editor.locator('.cm-content').click()
    await page.keyboard.press('Control+End')
    await page.keyboard.insertText(`\n${unsavedMarker}`)
    await page.locator('.back-button').click()
    const discardDialog = page.locator('.domus-confirm-dialog')
    await expect(discardDialog).toBeVisible()
    await discardDialog.getByRole('button', { name: /取消|Cancel/ }).click()
    await expect(page).toHaveURL(/\/preview\?/)
    await expect(page.getByTestId('preview-save')).toBeEnabled()
    await page.locator('.preview-actions').getByRole('button', { name: /取消|Cancel/ }).click()
    await expect(page.locator('.markdown-body')).not.toContainText(unsavedMarker)
  } finally {
    if (authenticated) await permanentlyDelete(page, path)
  }
})
